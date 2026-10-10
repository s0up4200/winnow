package winnow

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// period is one Period: the time from start up to end. Both are midnight in
// the time zone of the scheduler clock.
type period struct {
	every      string
	start, end time.Time
}

// periodOf returns the Period of kind every that holds t, in the time zone
// of t. A week starts on Monday (ISO 8601).
func periodOf(every string, t time.Time) period {
	y, m, d := t.Date()
	p := period{every: every}
	switch every {
	case "daily":
		p.start = time.Date(y, m, d, 0, 0, 0, 0, t.Location())
		p.end = p.start.AddDate(0, 0, 1)
	case "weekly":
		p.start = time.Date(y, m, d-(int(t.Weekday())+6)%7, 0, 0, 0, 0, t.Location())
		p.end = p.start.AddDate(0, 0, 7)
	case "monthly":
		p.start = time.Date(y, m, 1, 0, 0, 0, 0, t.Location())
		p.end = p.start.AddDate(0, 1, 0)
	case "yearly":
		p.start = time.Date(y, 1, 1, 0, 0, 0, 0, t.Location())
		p.end = p.start.AddDate(1, 0, 0)
	}
	return p
}

// prev returns the Period before p.
func (p period) prev() period { return periodOf(p.every, p.start.Add(-time.Nanosecond)) }

// key names p in the store: its first day.
func (p period) key() string { return p.start.Format(time.DateOnly) }

// sendTime returns the time at which winnow sends the Digest message of p.
func (p period) sendTime(d *Digest) time.Time {
	// time.Date keeps the wall clock time on a day with a DST change.
	y, m, day := p.end.Date()
	return time.Date(y, m, day, int(d.sendAt/time.Hour), int(d.sendAt%time.Hour/time.Minute), 0, 0, p.end.Location())
}

// key names d in the store: its name and its Period kind. A change to Every
// thus makes a new Digest, as a new name does. Without the kind, a daily
// and a weekly Period that start on the same Monday share one send state.
func (d *Digest) key() string { return d.Name + "/" + d.Every }

// lastPeriod returns the last Period of d whose send time is at or before
// now.
func lastPeriod(d *Digest, now time.Time) period {
	p := periodOf(d.Every, now).prev()
	if now.Before(p.sendTime(d)) {
		p = p.prev()
	}
	return p
}

// heading returns the heading of a Digest message for p, for example
// "Weekly digest".
func (p period) heading() string {
	return strings.ToUpper(p.every[:1]) + p.every[1:] + " digest"
}

// dates returns the dates of p for the small line over the heading.
func (p period) dates() string {
	switch p.every {
	case "daily":
		return longDay(p.start)
	case "weekly":
		_, week := p.start.ISOWeek()
		return fmt.Sprintf("week %d · %s – %s", week, shortDay(p.start), shortDay(p.end.AddDate(0, 0, -1)))
	case "monthly":
		return p.start.Format("January 2006")
	}
	return p.start.Format("2006")
}

// shortDay returns a date as "5 October".
func shortDay(t time.Time) string { return t.Format("2 January") }

// longDay returns a date as "Monday 5 October".
func longDay(t time.Time) string { return t.Format("Monday 2 January") }

// counts holds the metrics of one repository, or the totals of a Digest.
type counts struct {
	merged, prOpened, prClosed int
	issOpened, issClosed       int
	releases                   int
	tag                        string // the tag of the newest release
	stars, forks, discussions  int
	other                      map[string]int // each other Event, by "<event>.<action>"
}

// add counts e and reports whether it counted e. An Other Event counts only
// with includeOther.
func (c *counts) add(e *Event, includeOther bool) bool {
	merged := e.Merged != nil && *e.Merged
	switch e.NameAction() {
	case "pull_request.closed":
		if merged {
			c.merged++
		} else {
			c.prClosed++
		}
	case "pull_request.opened":
		c.prOpened++
	case "issues.opened":
		c.issOpened++
	case "issues.closed":
		c.issClosed++
	case "release.published":
		c.releases++
		c.tag = e.Tag
	case "star.created":
		c.stars++
	case "fork":
		c.forks++
	case "discussion.created":
		c.discussions++
	default:
		if !includeOther {
			return false
		}
		if c.other == nil {
			c.other = map[string]int{}
		}
		c.other[e.NameAction()]++
	}
	return true
}

// activity orders the repository lines.
func (c *counts) activity() int {
	return c.merged + c.prOpened + c.issOpened + c.issClosed + c.releases
}

// topRepos is the number of repositories that get their own lines in a
// Digest message. The other repositories share one line.
const topRepos = 6

// summarize returns the Digest message for the Events of one Period. from
// is the first-run time of the Digest when p is a partial Period, else the
// zero time. When all repositories have one owner, the message names the
// owner and shows the Icon of the owner from icons. It returns false when d
// includes no Event.
func summarize(d *Digest, p period, from time.Time, events []Event, icons map[string]string) (message, bool) {
	type repo struct {
		name, url, forge string
		counts
	}
	var total counts
	repos := map[string]*repo{}
	n := 0
	for i := range events {
		e := &events[i]
		// The current Rules decide for the whole Period, so that a narrower
		// match also leaves out the Events from before the change. GitHub
		// sends watch.started and star.created for one star, so watch does
		// not count. An unstar does not subtract. The current include_other
		// also decides for the whole Period.
		if !d.Match.match(e) || e.Name == "watch" || e.NameAction() == "star.deleted" {
			continue
		}
		if !total.add(e, bool(d.IncludeOther)) {
			continue
		}
		n++
		if e.Repo == "" {
			continue
		}
		key := e.RepoURL + " " + e.Repo
		r := repos[key]
		if r == nil {
			r = &repo{name: e.Repo, url: e.RepoURL, forge: e.Forge}
			repos[key] = r
		}
		r.add(e, bool(d.IncludeOther))
	}
	if n == 0 {
		return message{}, false
	}

	var lines []string
	line := func(prefix string, parts ...string) {
		if parts = slices.DeleteFunc(parts, func(s string) bool { return s == "" }); len(parts) > 0 {
			lines = append(lines, prefix+strings.Join(parts, " · "))
		}
	}
	line("", tally("PR", []int{total.merged, total.prOpened, total.prClosed}, "merged", "opened", "closed")...)
	line("", append(tally("issue", []int{total.issOpened, total.issClosed}, "opened", "closed"),
		plural(total.releases, "release"), plural(total.stars, "star"), plural(total.forks, "fork"), plural(total.discussions, "discussion"))...)
	other := slices.SortedFunc(maps.Keys(total.other), func(a, b string) int {
		return cmp.Or(total.other[b]-total.other[a], strings.Compare(a, b))
	})
	for i, k := range other {
		other[i] = count(total.other[k], k)
	}
	line("Other: ", other...)
	totals := strings.Join(lines, "\n")

	sorted := slices.SortedFunc(maps.Values(repos), func(a, b *repo) int {
		return cmp.Or(b.activity()-a.activity(), strings.Compare(a.name, b.name))
	})
	// owner is the owner of all repositories, or "" when they have more
	// than one owner.
	var owner string
	for i, r := range sorted {
		o, _, _ := strings.Cut(r.name, "/")
		if i > 0 && !strings.EqualFold(o, owner) {
			owner = ""
			break
		}
		owner = o
	}
	var top, rest []string
	for i, r := range sorted {
		name := r.name
		if owner != "" {
			name = name[len(owner)+1:]
		}
		if i >= topRepos {
			l := link(name, r.url)
			if r.merged > 0 {
				l += " " + strconv.Itoa(r.merged) + " merged"
			}
			rest = append(rest, l)
			continue
		}
		heading := "### " + link(name, r.url)
		if r.tag != "" {
			heading += "  ·  " + link(r.tag, r.url+"/releases/tag/"+url.PathEscape(r.tag))
		}
		if r.forge == "github" {
			heading += "  ·  " + link("Pulse", r.url+"/pulse")
		}
		parts := append([]string{count(r.merged, "merged"), did(r.prOpened, "PR", "opened")},
			tally("issue", []int{r.issOpened, r.issClosed}, "opened", "closed")...)
		parts = slices.DeleteFunc(append(parts, plural(r.stars, "star")), func(s string) bool { return s == "" })
		top = append(top, strings.TrimSpace(heading+"\n"+strings.Join(parts, " · ")))
	}

	footer := "-# " + strconv.Itoa(len(repos)) + " repositories"
	if len(repos) == 1 {
		footer = "-# 1 repository"
	}
	if !from.IsZero() {
		footer += " · from " + longDay(from)
	}
	sub := "-# " + p.dates()
	if owner != "" {
		sub = "-# " + owner + " · " + p.dates()
	}
	heading := "## " + p.heading()
	totals, top, also := fit(maxText-utf8.RuneCountInString(sub+heading+footer), totals, top, rest)
	in := beside(icons[strings.ToLower(owner)], display(sub), display(heading), display(totals))
	in = append(in, component{Type: typeSeparator, Spacing: spacingLarge})
	for _, l := range top {
		in = append(in, display(l))
	}
	in = append(in, component{Type: typeSeparator}, display(also+footer))
	return message{Username: "GitHub", AvatarURL: githubIcon, Components: []component{box(colorStar, in...)}}, true
}

// fit returns the totals, the top repository lines, and the line with the
// links to the other repositories, which fit in room characters together.
// The line ends with a newline, or is empty when no repository is left. fit
// drops whole repositories from the end, and then adds "and N more
// repositories" to the line. When the totals alone are too long, it cuts
// them.
func fit(room int, totals string, top, rest []string) (string, []string, string) {
	all := len(top) + len(rest)
	for shown := all; ; shown-- {
		t, r := top[:min(shown, len(top))], rest[:max(0, shown-len(top))]
		if shown < all {
			r = append(slices.Clip(r), "and "+plural(all-shown, "more repository"))
		}
		also := ""
		if len(r) > 0 {
			also = "-# Also: " + strings.Join(r, " · ") + "\n"
		}
		size := utf8.RuneCountInString(totals + strings.Join(t, "") + also)
		if size <= room {
			return totals, t, also
		}
		if shown == 0 {
			// The totals alone are too long, for example with many other
			// Events.
			return cut(totals, max(1, room-utf8.RuneCountInString(also))), nil, also
		}
	}
}

// did returns "<n> <noun>s <verb>" with n in bold, for example "**2** PRs
// merged", or "" when n is 0.
func did(n int, noun, verb string) string {
	if s := plural(n, noun); s != "" {
		return s + " " + verb
	}
	return ""
}

// tally returns the counts of noun for each verb, for example "**2** PRs
// merged" and "**1** opened". Only the first count that is not 0 names the
// noun. A count of 0 gives no part.
func tally(noun string, ns []int, verbs ...string) []string {
	var parts []string
	for i, n := range ns {
		switch {
		case n == 0:
		case len(parts) == 0:
			parts = append(parts, did(n, noun, verbs[i]))
		default:
			parts = append(parts, count(n, verbs[i]))
		}
	}
	return parts
}

// count returns "<n> <what>" with n in bold, or "" when n is 0.
func count(n int, what string) string {
	if n == 0 {
		return ""
	}
	return "**" + strconv.Itoa(n) + "** " + what
}

// plural returns "<n> <noun>" with the plural of noun when n is not 1, or ""
// when n is 0.
func plural(n int, noun string) string {
	if n != 1 {
		if s, ok := strings.CutSuffix(noun, "y"); ok {
			noun = s + "ies"
		} else {
			noun += "s"
		}
	}
	return count(n, noun)
}

// retryEvery is the wait between two tries of one Digest message, and
// giveUp is the time after the first try at which winnow sets the state
// failed.
const (
	retryEvery = time.Hour
	giveUp     = 24 * time.Hour
)

// tries holds the Digest messages that Discord did not take yet. The
// workers of the outbox write sending; the scheduler step reads it.
type tries struct {
	mu sync.Mutex
	m  map[tryKey]*try
}

type tryKey struct{ digest, period string }

type try struct {
	p           period
	first, next time.Time // the times of the first try and of the next try
	sending     bool      // the message is on the queue or in a send
}

// Run runs the scheduler step now and then each minute, until ctx ends.
// Without the store, it returns at once.
func (s *Server) Run(ctx context.Context) {
	if s.store == nil {
		return
	}
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		s.step(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// step runs each Sweep that is due, and sends each Digest message that is
// due at this time. One goroutine runs the steps, so two Sweeps never
// overlap.
func (s *Server) step(ctx context.Context) {
	now := s.now()
	_ = s.sweepDue(ctx, now, false) // sweep logs each failure
	for i := range s.cfg.Digests {
		if err := s.stepDigest(ctx, &s.cfg.Digests[i], now); err != nil {
			s.log.Error("digest step failed", "digest", s.cfg.Digests[i].Name, "error", err)
		}
	}
}

// stepDigest does the step for d. It tries again each message that Discord
// did not take, and sets the state failed after giveUp. It sends the last
// Period when that Period has no state. Each older Period with no state,
// back to the first-run time, gets the state skipped and a log line.
func (s *Server) stepDigest(ctx context.Context, d *Digest, now time.Time) error {
	first, err := s.store.firstRun(d.key(), now)
	if err != nil {
		return err
	}
	var retry []period
	var failed []string // Period keys
	s.tries.mu.Lock()
	for k, t := range s.tries.m {
		switch {
		case k.digest != d.Name || t.sending:
		case !now.Before(t.first.Add(giveUp)):
			delete(s.tries.m, k)
			failed = append(failed, k.period)
		case !now.Before(t.next):
			retry = append(retry, t.p)
		}
	}
	s.tries.mu.Unlock()
	for _, key := range failed {
		if err := s.store.setState(d.key(), key, "failed"); err != nil {
			return err
		}
		s.log.Error("digest failed", "digest", d.Name, "period", key)
	}
	for _, p := range retry {
		if err := s.sendDigest(ctx, d, p, first, now); err != nil {
			return err
		}
	}

	last := lastPeriod(d, now)
	for p := last; p.end.After(first); p = p.prev() {
		s.tries.mu.Lock()
		_, trying := s.tries.m[tryKey{d.Name, p.key()}]
		s.tries.mu.Unlock()
		if trying {
			return nil
		}
		state, err := s.store.state(d.key(), p.key())
		if err != nil || state != "" {
			return err
		}
		if p.key() == last.key() {
			if err := s.sendDigest(ctx, d, p, first, now); err != nil {
				return err
			}
			continue
		}
		if err := s.store.setState(d.key(), p.key(), "skipped"); err != nil {
			return err
		}
		s.log.Warn("digest skipped", "digest", d.Name, "period", p.key())
	}
	return nil
}

// sendDigest counts the stored Events of p and puts the Digest message on
// the queue of the Sink of d. A Period with no Events gets the state empty
// and no message. Before the first send of a step, it runs each Sweep, so
// that the counts include the Backfills.
func (s *Server) sendDigest(ctx context.Context, d *Digest, p period, first, now time.Time) error {
	if err := s.sweepDue(ctx, now, true); err != nil {
		s.log.Warn("digest can count too few Events", "digest", d.Name, "period", p.key(), "error", err)
	}
	// A partial Period counts from the first-run time, as its footer says.
	// After a change to Every, the store has older Events of the Digest name.
	var from time.Time
	if p.start.Before(first) {
		from = first
	}
	events, err := s.store.events(d.Name, cmp.Or(from, p.start), p.end)
	if err != nil {
		return err
	}
	k := tryKey{d.Name, p.key()}
	msg, ok := summarize(d, p, from, events, s.cfg.Icons)
	if !ok {
		return s.store.setState(d.key(), k.period, "empty")
	}
	s.tries.mu.Lock()
	t := s.tries.m[k]
	if t == nil {
		t = &try{p: p, first: now}
		s.tries.m[k] = t
	}
	t.next, t.sending = now.Add(retryEvery), true
	s.tries.mu.Unlock()
	attrs := []any{"digest", d.Name, "period", k.period}
	s.outbox.send(d.To, msg, entry{attrs: attrs, done: func(err error) {
		if err == nil {
			// Set the state before the try goes, so that no step sends
			// the message again in between. When the state does not save,
			// the try stays in sending, so that no step sends it again
			// until a restart.
			if err := s.store.setState(d.key(), k.period, "sent"); err != nil {
				s.log.Error("digest state not saved", append(attrs, "error", err)...)
				return
			}
			s.log.Info("digest sent", attrs...)
		}
		s.tries.mu.Lock()
		defer s.tries.mu.Unlock()
		if err != nil {
			t.sending = false
		} else {
			delete(s.tries.m, k)
		}
	}})
	return nil
}

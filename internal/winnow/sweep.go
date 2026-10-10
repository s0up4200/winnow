package winnow

import (
	"cmp"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// The Sweep constants. GitHub keeps deliveries for 3 days, so a Sweep looks
// back 3 days, and a handled delivery has no use after 4 days.
const (
	sweepEvery    = 15 * time.Minute
	sweepLookback = 3 * 24 * time.Hour
	sweptKeep     = 4 * 24 * time.Hour
	githubTimeout = 30 * time.Second
)

// sweeper holds the GitHub client and the state in memory of the Sweep of
// one Source. Only the scheduler goroutine uses it.
type sweeper struct {
	source string
	cfg    *Sweep
	client *http.Client
	last   time.Time // the time of the last Sweep that worked
	tried  time.Time // the time of the last Sweep
	err    error     // the error of the last Sweep; not nil while the Sweep fails
}

// delivery is one attempt in the delivery list of a GitHub hook, or the
// detail of one attempt.
type delivery struct {
	ID          int64     `json:"id"`
	GUID        string    `json:"guid"`
	DeliveredAt time.Time `json:"delivered_at"`
	StatusCode  int       `json:"status_code"`
	Request     struct {
		Headers map[string]string `json:"headers"`
		Payload jsontext.Value    `json:"payload"`
	} `json:"request"`
}

// statusError is a GitHub reply that is not 200.
type statusError struct{ status int }

func (e *statusError) Error() string { return fmt.Sprintf("GitHub API status %d", e.status) }

// get sends GET url to the GitHub API and decodes the reply into v. It
// returns the URL of the next page from the Link header, or "".
func (w *sweeper) get(ctx context.Context, url string, v any) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+w.cfg.Token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := w.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", &statusError{resp.StatusCode}
	}
	if err := json.UnmarshalRead(resp.Body, v); err != nil {
		return "", err
	}
	return nextLink(resp.Header.Get("Link")), nil
}

// deliveries returns the URL of the delivery list of the hook.
func (w *sweeper) deliveries() string {
	return fmt.Sprintf("%s/orgs/%s/hooks/%d/deliveries", w.cfg.api, w.cfg.Org, w.cfg.Hook)
}

// nextLink returns the URL with rel="next" in a Link header, or "".
func nextLink(h string) string {
	for l := range strings.SplitSeq(h, ",") {
		url, params, ok := strings.Cut(l, ";")
		if ok && strings.Contains(params, `rel="next"`) {
			return strings.Trim(strings.TrimSpace(url), "<>")
		}
	}
	return ""
}

// missed returns the first attempt of each delivery in the last
// sweepLookback that no attempt delivered and that no attempt got a 4xx
// status for, oldest first. GitHub lists the newest attempts first.
func (w *sweeper) missed(ctx context.Context, now time.Time) ([]delivery, error) {
	first := map[string]delivery{}
	done := map[string]bool{} // a delivery with a 2xx or a 4xx attempt
	for url := w.deliveries() + "?per_page=100"; url != ""; {
		var page []delivery
		next, err := w.get(ctx, url, &page)
		if err != nil {
			return nil, err
		}
		url = next
		for _, d := range page {
			if d.DeliveredAt.Before(now.Add(-sweepLookback)) {
				url = ""
				break
			}
			if d.StatusCode >= 200 && d.StatusCode < 300 || d.StatusCode >= 400 && d.StatusCode < 500 {
				done[d.GUID] = true
			}
			if f, ok := first[d.GUID]; !ok || d.DeliveredAt.Before(f.DeliveredAt) {
				first[d.GUID] = d
			}
		}
	}
	var out []delivery
	for guid, d := range first {
		if !done[guid] {
			out = append(out, d)
		}
	}
	slices.SortFunc(out, func(a, b delivery) int { return cmp.Or(a.DeliveredAt.Compare(b.DeliveredAt), cmp.Compare(a.ID, b.ID)) })
	return out, nil
}

// sweepDue runs the Sweep of each Source that did not run in this step and
// whose last Sweep that worked is sweepEvery or more ago. With all, it runs
// each Sweep that did not run in this step, as before a Digest send. It
// returns an error when a Sweep of this step failed.
func (s *Server) sweepDue(ctx context.Context, now time.Time, all bool) error {
	var errs []error
	for _, w := range s.sweepers {
		if !w.tried.Equal(now) && (all || now.Sub(w.last) >= sweepEvery) {
			w.tried, w.err = now, s.sweep(ctx, w, now)
		}
		if w.tried.Equal(now) {
			errs = append(errs, w.err)
		}
	}
	return errors.Join(errs...)
}

// sweep runs the Sweep of w, logs a failure, and sends an alert when the
// Sweep starts to fail and when it works again.
func (s *Server) sweep(ctx context.Context, w *sweeper, now time.Time) error {
	n, err := s.sweepOnce(ctx, w, now)
	if err != nil {
		// A 401 or a 404 can mean a token, webhook, or access problem.
		log := s.log.Warn
		if se, ok := errors.AsType[*statusError](err); ok && (se.status == http.StatusUnauthorized || se.status == http.StatusNotFound) {
			log = s.log.Error
		}
		log("sweep failed", "source", w.source, "error", err)
		if w.err == nil {
			desc := err.Error()
			if se, ok := errors.AsType[*statusError](err); ok && se.status == http.StatusNotFound {
				desc = "GitHub API status 404. Check the organization and webhook ID, that the token belongs to an org owner with Webhooks read access, and whether an OAuth app created the webhook. See the README Sweeps section."
			}
			s.alert("Sweep of "+w.source+" failed", desc)
		}
	} else {
		w.last = now
		if w.err != nil {
			s.alert("Sweep of "+w.source+" works again", "")
		}
	}
	// A Sweep that fails can handle deliveries before the failure. No later
	// Sweep counts them, so the line comes also after a failure.
	if n > 0 {
		s.log.Info("swept", "source", w.source, "backfills", n)
	}
	return err
}

// sweepOnce stores each missed delivery of w that no Sweep handled before
// as a Backfill. It returns the number of Backfills.
func (s *Server) sweepOnce(ctx context.Context, w *sweeper, now time.Time) (int, error) {
	if err := s.store.forgetSwept(now.Add(-sweptKeep)); err != nil {
		return 0, err
	}
	missed, err := w.missed(ctx, now)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, m := range missed {
		done, err := s.store.swept(m.GUID)
		if err != nil {
			return n, err
		}
		if done {
			continue
		}
		var d delivery
		if _, err := w.get(ctx, fmt.Sprintf("%s/%d", w.deliveries(), m.ID), &d); err != nil {
			return n, err
		}
		// The claim comes after the fetch, so that a failed fetch does
		// not lose the delivery. It is the only guard against a second
		// post, also from another process on the same store.
		ok, err := s.store.claim(m.GUID, now)
		if err != nil {
			return n, err
		}
		if !ok {
			continue
		}
		if s.backfill(w.source, m, &d) {
			n++
		}
	}
	return n, nil
}

// backfill admits the delivery d with the first attempt m as a Backfill.
// It returns false when the delivery is not an Event.
func (s *Server) backfill(source string, m delivery, d *delivery) bool {
	// The payload is parsed JSON, not the signed body, so winnow does not
	// check a signature. Winnow trusts the API reply.
	h := http.Header{}
	for k, v := range d.Request.Headers {
		h.Set(k, v)
	}
	e, err := parseEvent(source, s.cfg.Bots, h, d.Request.Payload)
	if err != nil {
		s.log.Error("backfill not parsed", "source", source, "delivery", m.GUID, "error", err)
		return false
	}
	if e.Name == "ping" {
		return false
	}
	s.admit(e, m.DeliveredAt, true)
	return true
}

// alert sends a message to the alerts Sink. Without one, it does nothing.
func (s *Server) alert(title, desc string) {
	if s.cfg.Alerts == "" {
		return
	}
	in := []component{display("## " + title)}
	if desc != "" {
		in = append(in, display(cut(desc, maxText-utf8.RuneCountInString(in[0].Content))))
	}
	msg := message{Username: "GitHub", AvatarURL: githubIcon, Components: []component{box(colorStar, in...)}}
	s.outbox.send(s.cfg.Alerts, msg, entry{attrs: []any{"alert", title}})
}

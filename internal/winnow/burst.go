package winnow

import (
	"sync"
	"time"
)

// burstWait is how long winnow holds the first delivery of a Burst. GitHub
// sends one delivery for each reviewer or assignee, all within about one
// second, so 5 seconds catch a Burst and delay a card only a little. A later
// delivery does not extend the wait.
const burstWait = 5 * time.Second

// burstKey identifies a Burst: one action, by one sender, on one pull
// request or issue, that one Route sends.
type burstKey struct {
	source, repo, name, action, sender string
	number                             int
	route                              *Route // two Routes can have the same name
}

// burst is a Burst that waits: the first Event, into which the later Events
// merge, and its Route.
type burst struct {
	e     *Event
	route *Route
}

// bursts holds each Burst for its wait, and then gives the merged Event to
// deliver. It keeps the Bursts in memory only, like the Sink queues.
type bursts struct {
	wait    time.Duration
	deliver func(*Event, *Route)
	// mu also covers the calls to deliver, so that flush returns only when
	// no timer sends anymore.
	mu     sync.Mutex
	open   map[burstKey]*burst
	closed bool // flush ran
}

// joinsBurst reports whether e can join a Burst: a review request, an
// assignment, or the reverse of one of them, that names a Subject.
func joinsBurst(e *Event) bool {
	switch e.NameAction() {
	case "pull_request.review_requested", "pull_request.review_request_removed",
		"pull_request.assigned", "pull_request.unassigned", "issues.assigned", "issues.unassigned":
		return len(e.Subjects) > 0
	}
	return false
}

// add puts e in its Burst. The first Event of a Burst starts the wait. A
// later Event gives its Subjects to the first Event. After
// flush, add delivers e at once.
func (b *bursts) add(e *Event, route *Route) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		b.deliver(e, route)
		return
	}
	k := burstKey{source: e.Source, repo: e.Repo, name: e.Name, action: e.Action, sender: e.Sender, number: e.Number, route: route}
	if g, ok := b.open[k]; ok {
		g.e.merge(e)
		return
	}
	b.open[k] = &burst{e: e, route: route}
	time.AfterFunc(b.wait, func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		// A flush can send this Burst first.
		if g, ok := b.open[k]; ok && g.e == e {
			delete(b.open, k)
			b.deliver(g.e, g.route)
		}
	})
}

// flush delivers each Burst that waits, and makes add deliver at once.
func (b *bursts) flush() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
	for _, g := range b.open {
		b.deliver(g.e, g.route)
	}
	clear(b.open)
}

package winnow

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
)

// queueSize is the number of Events that one Sink queue holds.
const queueSize = 100

// entry is one Event on the queue of one Sink. When a Route names several
// Sinks, their workers share the Event, so a worker must not change it.
type entry struct {
	event *Event
	route string
}

// failure tells why a Sink did not deliver an Event. A send function returns
// it as its error. It holds the fields of the failed delivery line.
type failure struct {
	reason   string // queue_full, rejected, retries_exhausted, or shutdown
	attempts int    // the number of sends
	status   int    // the HTTP status of the last reply, or 0 when there was no reply
	detail   string // the error body of the last reply, or the network error
}

func (f *failure) Error() string {
	return fmt.Sprintf("%s after %d attempts: status %d: %s", f.reason, f.attempts, f.status, f.detail)
}

// sink is one Sink: a buffered queue and one worker goroutine that calls send
// for each Event in the queue, in order. The worker knows nothing about
// Discord.
type sink struct {
	name  string
	queue chan entry
	send  func(context.Context, *Event) error
	log   *slog.Logger
}

// enqueue puts en on the queue. It returns false and does not block when the
// queue is full.
func (s *sink) enqueue(en entry) bool {
	select {
	case s.queue <- en:
		return true
	default:
		return false
	}
}

// run is the worker. It sends the Events in the queue. When drain closes, it
// sends the Events left in the queue and stops. When ctx ends, send stops and
// the worker logs each Event left with reason=shutdown. Nothing closes the
// queue, so that a late enqueue cannot panic.
func (s *sink) run(ctx context.Context, drain <-chan struct{}) {
	for {
		select {
		case en := <-s.queue:
			if err := s.send(ctx, en.event); err != nil {
				f, ok := errors.AsType[*failure](err)
				if !ok {
					f = &failure{reason: "rejected", attempts: 1, detail: err.Error()}
				}
				s.logFailure(en, f)
			}
		case <-drain:
			if len(s.queue) == 0 {
				return
			}
		}
	}
}

// logFailure writes the failed delivery line for en.
func (s *sink) logFailure(en entry, f *failure) {
	attrs := append([]any{"reason", f.reason, "sink", s.name, "route", en.route}, en.event.logAttrs()...)
	s.log.Error("delivery failed", append(attrs, "attempts", f.attempts, "status", f.status, "error", f.detail)...)
}

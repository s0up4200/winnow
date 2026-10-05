package winnow

import (
	"errors"
	"fmt"
	"log/slog"
)

// queueSize is the number of Events that one Sink queue holds.
const queueSize = 100

// delivery is one Event on the queue of one Sink. When a Route names several
// Sinks, their workers share the Event, so a worker must not change it.
type delivery struct {
	event *Event
	route string
}

// failure tells why a Sink did not deliver an Event. A send function returns
// it as its error. It holds the fields of the failed delivery line.
type failure struct {
	reason   string // queue_full, rejected, or retries_exhausted
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
	queue chan delivery
	send  func(*Event) error
	log   *slog.Logger
}

func startSink(name string, send func(*Event) error, log *slog.Logger) *sink {
	s := &sink{name: name, queue: make(chan delivery, queueSize), send: send, log: log}
	go s.run()
	return s
}

// enqueue puts d on the queue. It returns false and does not block when the
// queue is full.
func (s *sink) enqueue(d delivery) bool {
	select {
	case s.queue <- d:
		return true
	default:
		return false
	}
}

func (s *sink) run() {
	for d := range s.queue {
		if err := s.send(d.event); err != nil {
			f, ok := errors.AsType[*failure](err)
			if !ok {
				f = &failure{reason: "rejected", attempts: 1, detail: err.Error()}
			}
			s.logFailure(d, f)
		}
	}
}

// logFailure writes the failed delivery line for d.
func (s *sink) logFailure(d delivery, f *failure) {
	attrs := append([]any{"reason", f.reason, "sink", s.name, "route", d.route}, d.event.logAttrs()...)
	s.log.Error("delivery failed", append(attrs, "attempts", f.attempts, "status", f.status, "error", f.detail)...)
}

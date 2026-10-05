package winnow

import "log/slog"

// queueSize is the number of Events that one Sink queue holds.
const queueSize = 100

// delivery is one Event on the queue of one Sink. When a Route names several
// Sinks, their workers share the Event, so a worker must not change it.
type delivery struct {
	event *Event
	route string
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
			s.logFailure(d, "error", err)
		}
	}
}

// logFailure writes the failed delivery line for d, with the extra fields in
// attrs.
func (s *sink) logFailure(d delivery, attrs ...any) {
	attrs = append(attrs, "sink", s.name, "route", d.route)
	s.log.Error("delivery failed", append(attrs, d.event.logAttrs()...)...)
}

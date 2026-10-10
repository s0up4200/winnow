package winnow

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

// queueSize is the number of messages that one Sink queue holds.
const queueSize = 100

// outbox delivers routed Events to their Sinks. It owns one queue and one
// worker for each Sink, and it writes each failed delivery line.
type outbox struct {
	sinks    map[string]*sink
	icons    map[string]string // the Icons of the configuration
	log      *slog.Logger
	failures *prometheus.CounterVec
	wg       sync.WaitGroup     // the Sink workers
	mu       sync.RWMutex       // deliver reads drain and enqueues under the read lock
	drain    chan struct{}      // closed when Shutdown starts
	stop     context.CancelFunc // stops the sends when the drain time ends
}

// sink is one Sink: a buffered queue of rendered messages and the Discord
// sender of its worker.
type sink struct {
	name    string
	pings   pings // the User map and the kinds of Ping that the Sink keeps
	queue   chan entry
	discord *discordSender
}

// entry is one rendered message on the queue of one Sink.
type entry struct {
	msg   message
	route string
	attrs []any // the log fields of the Event
	// done, when set, gets the result of the delivery: nil when Discord
	// took the message, else a *failure. The outbox calls it before the
	// failed delivery line.
	done func(error)
}

// failure tells why a Sink did not deliver an Event. The Discord sender
// returns it as its error, and the outbox makes it for queue_full and
// shutdown. It holds the fields of the failed delivery line.
type failure struct {
	reason   string // queue_full, rejected, retries_exhausted, or shutdown
	attempts int    // the number of sends
	status   int    // the HTTP status of the last reply, or 0 when there was no reply
	detail   string // the error body of the last reply, or the network error
}

func (f *failure) Error() string {
	return fmt.Sprintf("%s after %d attempts: status %d: %s", f.reason, f.attempts, f.status, f.detail)
}

// newOutbox makes one Discord sender, one queue, and one worker for each Sink
// of cfg.
func newOutbox(cfg *Config, log *slog.Logger, failures *prometheus.CounterVec) *outbox {
	ctx, stop := context.WithCancel(context.Background())
	o := &outbox{sinks: map[string]*sink{}, icons: cfg.Icons, log: log, failures: failures, drain: make(chan struct{}), stop: stop}
	for name, sc := range cfg.Sinks {
		sk := &sink{name: name, pings: pings{users: cfg.Users, kinds: sc.kinds}, queue: make(chan entry, queueSize), discord: newDiscordSender(sc)}
		o.sinks[name] = sk
		o.wg.Go(func() { o.run(ctx, sk) })
	}
	return o
}

// deliver renders e for each Sink of route and puts the message on the queue
// of the Sink. The message pings only for the kinds of Ping that the Sink
// keeps. When the drain started or a queue is full, deliver writes a failed
// delivery line for that Sink.
func (o *outbox) deliver(e *Event, route *Route) {
	attrs := e.logAttrs()
	// Under the read lock, the drain cannot start between the check and the
	// enqueue, so a worker that stopped never misses a message.
	o.mu.RLock()
	defer o.mu.RUnlock()
	for _, to := range route.To {
		sk := o.sinks[to]
		o.enqueue(sk, entry{route: route.Name, attrs: attrs}, func() message { return render(e, sk.pings, o.icons) })
	}
}

// send puts the message msg on the queue of the Sink to. en.done gets the
// result.
func (o *outbox) send(to string, msg message, en entry) {
	o.mu.RLock()
	defer o.mu.RUnlock()
	o.enqueue(o.sinks[to], en, func() message { return msg })
}

// enqueue puts en with the message of msg on the queue of sk. It calls msg
// only when the drain did not start. The caller holds the read lock.
func (o *outbox) enqueue(sk *sink, en entry, msg func() message) {
	select {
	case <-o.drain:
		o.fail(sk, en, &failure{reason: "shutdown"})
		return
	default:
	}
	en.msg = msg()
	select {
	case sk.queue <- en:
	default:
		o.fail(sk, en, &failure{reason: "queue_full"})
	}
}

// shutdown keeps the contract of Server.Shutdown.
func (o *outbox) shutdown(ctx context.Context) {
	o.mu.Lock()
	select {
	case <-o.drain:
	default:
		close(o.drain)
	}
	o.mu.Unlock()
	context.AfterFunc(ctx, o.stop)
	o.wg.Wait()
}

// run is the worker of sk. It sends the messages in the queue, in order. When
// drain closes, it sends the messages left in the queue and stops. When ctx
// ends, the sends stop and the worker logs each message left with
// reason=shutdown. Nothing closes the queue, so that a late enqueue cannot
// panic.
func (o *outbox) run(ctx context.Context, sk *sink) {
	for {
		select {
		case en := <-sk.queue:
			err := sk.discord.send(ctx, en.msg)
			if err == nil {
				if en.done != nil {
					en.done(nil)
				}
				continue
			}
			f, ok := errors.AsType[*failure](err)
			if !ok {
				f = &failure{reason: "rejected", attempts: 1, detail: err.Error()}
			}
			o.fail(sk, en, f)
		case <-o.drain:
			if len(sk.queue) == 0 {
				return
			}
		}
	}
}

// fail gives f to en.done and writes the failed delivery line for en on sk.
func (o *outbox) fail(sk *sink, en entry, f *failure) {
	o.failures.WithLabelValues(sk.name, f.reason).Inc()
	if en.done != nil {
		en.done(f)
	}
	attrs := append([]any{"reason", f.reason, "sink", sk.name, "route", en.route}, en.attrs...)
	o.log.Error("delivery failed", append(attrs, "attempts", f.attempts, "status", f.status, "error", f.detail)...)
}

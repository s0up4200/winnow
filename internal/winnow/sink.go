package winnow

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
)

// queueSize is the number of messages that one Sink queue holds.
const queueSize = 100

// outbox delivers routed Events to their Sinks. It owns one queue and one
// worker for each Sink, and it writes each failed delivery line.
type outbox struct {
	sinks map[string]*sink
	log   *slog.Logger
	wg    sync.WaitGroup     // the Sink workers
	mu    sync.RWMutex       // deliver reads drain and enqueues under the read lock
	drain chan struct{}      // closed when Shutdown starts
	stop  context.CancelFunc // stops the sends when the drain time ends
}

// sink is one Sink: a buffered queue of rendered messages and the Discord
// sender of its worker.
type sink struct {
	name    string
	users   map[string]string // the User map when the Sink has mentions: true, else nil
	queue   chan entry
	discord *discordSender
}

// entry is one rendered message on the queue of one Sink.
type entry struct {
	msg   message
	route string
	attrs []any // the log fields of the Event
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
func newOutbox(cfg *Config, log *slog.Logger) *outbox {
	ctx, stop := context.WithCancel(context.Background())
	o := &outbox{sinks: map[string]*sink{}, log: log, drain: make(chan struct{}), stop: stop}
	for name, sc := range cfg.Sinks {
		sk := &sink{name: name, queue: make(chan entry, queueSize), discord: newDiscordSender(sc)}
		if sc.Mentions {
			sk.users = cfg.Users
		}
		o.sinks[name] = sk
		o.wg.Go(func() { o.run(ctx, sk) })
	}
	return o
}

// deliver renders e for each Sink of route and puts the message on the queue
// of the Sink. Only a Sink with mentions: true gets the User map, so only its
// message can ping. When the drain started or a queue is full, deliver writes
// a failed delivery line for that Sink.
func (o *outbox) deliver(e *Event, route *Route) {
	attrs := e.logAttrs()
	// Under the read lock, the drain cannot start between the check and the
	// enqueue, so a worker that stopped never misses a message.
	o.mu.RLock()
	defer o.mu.RUnlock()
	for _, to := range route.To {
		sk := o.sinks[to]
		en := entry{route: route.Name, attrs: attrs}
		select {
		case <-o.drain:
			o.logFailure(sk, en, &failure{reason: "shutdown"})
			continue
		default:
		}
		en.msg = render(e, sk.users)
		select {
		case sk.queue <- en:
		default:
			o.logFailure(sk, en, &failure{reason: "queue_full"})
		}
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
			if err := sk.discord.send(ctx, en.msg); err != nil {
				f, ok := errors.AsType[*failure](err)
				if !ok {
					f = &failure{reason: "rejected", attempts: 1, detail: err.Error()}
				}
				o.logFailure(sk, en, f)
			}
		case <-o.drain:
			if len(sk.queue) == 0 {
				return
			}
		}
	}
}

// logFailure writes the failed delivery line for en on sk.
func (o *outbox) logFailure(sk *sink, en entry, f *failure) {
	attrs := append([]any{"reason", f.reason, "sink", sk.name, "route", en.route}, en.attrs...)
	o.log.Error("delivery failed", append(attrs, "attempts", f.attempts, "status", f.status, "error", f.detail)...)
}

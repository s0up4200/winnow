package winnow

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
)

// maxBody is the largest delivery that winnow reads. GitHub sends at most
// 25 MiB.
const maxBody = 25 << 20

// Server is the HTTP handler of winnow. It owns one worker for each Sink.
type Server struct {
	*http.ServeMux
	cfg   *Config
	sinks map[string]*sink
	log   *slog.Logger
	drain chan struct{}      // closed when Shutdown starts
	stop  context.CancelFunc // stops the sends when the drain time ends
}

// New returns the HTTP handler for cfg and starts one worker for each Sink.
func New(cfg *Config, log *slog.Logger) *Server {
	ctx, stop := context.WithCancel(context.Background())
	s := &Server{ServeMux: http.NewServeMux(), cfg: cfg, sinks: map[string]*sink{}, log: log, drain: make(chan struct{}), stop: stop}
	for name, sc := range cfg.Sinks {
		discord := newDiscordSender(sc.Discord)
		s.sinks[name] = startSink(ctx, s.drain, name, func(ctx context.Context, e *Event) error {
			msg := render(e, cfg.Users)
			if !sc.Mentions { // only a Sink with mentions: true keeps the ping
				msg.Content, msg.AllowedMentions.Users = "", nil
			}
			return discord.send(ctx, msg)
		}, log)
	}
	// The pattern has no method, so that an unknown Source gets 404 before
	// a wrong method gets 405.
	s.HandleFunc("/hook/{source}", s.hook)
	// The health check shows only that the HTTP server runs, not the state
	// of a Sink.
	s.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, "ok")
	})
	return s
}

// Shutdown sends the Events left in the Sink queues and returns when the
// queues are empty. When ctx ends first, the sends stop and each Event that
// is not sent gets a failed delivery line with reason=shutdown. Call it after
// the HTTP server stops, because the workers do not read an Event that comes
// after the drain.
func (s *Server) Shutdown(ctx context.Context) {
	close(s.drain)
	context.AfterFunc(ctx, s.stop)
	for _, sk := range s.sinks {
		<-sk.done
	}
}

// hook receives one delivery. The order of the checks is part of the
// contract: a request to an unknown Source costs nothing, and winnow parses
// no body with a bad signature.
func (s *Server) hook(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("source")
	src, ok := s.cfg.Sources[name]
	if !ok {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != "application/json" {
		http.Error(w, "content type must be application/json", http.StatusUnsupportedMediaType)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			s.log.Warn("body too large", "source", name, "limit", maxBody)
			http.Error(w, "body too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "cannot read body", http.StatusBadRequest)
		return
	}
	if !validSignature(src.Secret, body, r.Header) {
		http.Error(w, "bad signature", http.StatusUnauthorized)
		return
	}
	e, err := parseEvent(name, s.cfg.Bots, r.Header, body)
	if err != nil {
		http.Error(w, "cannot parse delivery: "+err.Error(), http.StatusBadRequest)
		return
	}
	if e.Name == "ping" {
		w.WriteHeader(http.StatusOK)
		return
	}

	route := s.cfg.route(e)
	if route == nil {
		s.logDecision(e, "unmatched", "", []string{})
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if route.Drop {
		s.logDecision(e, "dropped", route.Name, []string{})
		w.WriteHeader(http.StatusNoContent)
		return
	}
	s.logDecision(e, "sent", route.Name, route.To)
	for _, to := range route.To {
		d := delivery{event: e, route: route.Name}
		if !s.sinks[to].enqueue(d) {
			s.sinks[to].logFailure(d, &failure{reason: "queue_full"})
		}
	}
	w.WriteHeader(http.StatusAccepted)
}

// logDecision writes the decision line: what the Routes did with e.
func (s *Server) logDecision(e *Event, outcome, route string, sinks []string) {
	attrs := []any{"outcome", outcome, "route", route, "sinks", sinks, "sender", e.Sender}
	s.log.Info("routed", append(attrs, e.logAttrs()...)...)
}

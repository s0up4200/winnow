package winnow

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"maps"
	"mime"
	"net/http"
	"slices"
	"time"
)

// maxBody is the largest delivery that winnow reads. GitHub sends at most
// 25 MiB.
const maxBody = 25 << 20

// Server is the HTTP handler of winnow. It gives each routed Event to Sink
// delivery, and it stores each Event that a Digest matches.
type Server struct {
	*http.ServeMux
	cfg    *Config
	log    *slog.Logger
	outbox *outbox
	store  *store           // nil when cfg has no Digests and no Sweep
	now    func() time.Time // the clock; tests set a fixed time
	tries  tries
	// sweepers holds the Sweep of each Source with a sweep block, in the
	// order of the Source names.
	sweepers []*sweeper
}

// New returns the HTTP handler for cfg and starts one worker for each Sink.
// When cfg has Digests or a Sweep, New opens the store.
func New(cfg *Config, log *slog.Logger) (*Server, error) {
	s := &Server{ServeMux: http.NewServeMux(), cfg: cfg, log: log, now: time.Now, tries: tries{m: map[tryKey]*try{}}}
	for _, name := range slices.Sorted(maps.Keys(cfg.Sources)) {
		if w := cfg.Sources[name].Sweep; w != nil {
			client := &http.Client{Timeout: githubTimeout, Transport: w.transport}
			s.sweepers = append(s.sweepers, &sweeper{source: name, cfg: w, client: client})
		}
	}
	if cfg.needsStore() {
		st, err := openStore(cfg.Database, log)
		if err != nil {
			return nil, err
		}
		s.store = st
	}
	s.outbox = newOutbox(cfg, log)
	// The pattern has no method, so that an unknown Source gets 404 before
	// a wrong method gets 405.
	s.HandleFunc("/hook/{source}", s.hook)
	// The health check shows only that the HTTP server runs, not the state
	// of a Sink.
	s.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	})
	return s, nil
}

// Shutdown sends the messages left in the Sink queues and returns when the
// queues are empty. When ctx ends first, the sends stop and each message that
// is not sent gets a failed delivery line with reason=shutdown. Call it after
// the HTTP server stops. A handler that still runs after the drain starts
// does not enqueue its message, but logs it with reason=shutdown. A second
// call only waits for the workers.
// Shutdown then closes the store. Stop Run before Shutdown.
func (s *Server) Shutdown(ctx context.Context) {
	s.outbox.shutdown(ctx)
	if s.store != nil {
		if err := s.store.close(); err != nil {
			s.log.Warn("store did not close", "error", err)
		}
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
	// The Route decision does not change whether winnow stores the Event
	// (ADR 0007).
	s.storeEvent(e, s.now())
	// Winnow claims a live delivery, so that a Sweep skips it. GitHub can
	// mark a delivery as failed when the tunnel drops the reply. The
	// delivery ID is the GUID of the delivery.
	if s.store != nil && len(s.sweepers) > 0 && e.Delivery != "" {
		if _, err := s.store.claim(e.Delivery, s.now()); err != nil {
			s.log.Error("store write failed", append(e.logAttrs(), "error", err)...)
		}
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
	s.logDecision(e, "matched", route.Name, route.To)
	s.outbox.deliver(e, route)
	w.WriteHeader(http.StatusAccepted)
}

// storeEvent stores e, with at as the receive time, for each Digest that matches
// it. Live delivery never depends on the store, so a failed write only logs.
func (s *Server) storeEvent(e *Event, at time.Time) {
	var digests []string
	for _, d := range s.cfg.Digests {
		if d.Match.match(e) {
			digests = append(digests, d.Name)
		}
	}
	if s.store != nil && len(digests) > 0 {
		if err := s.store.addEvent(e, at, digests); err != nil {
			s.log.Error("store write failed", append(e.logAttrs(), "error", err)...)
		}
	}
}

// logDecision writes the decision line: what the Routes did with e. extra
// holds more fields for the line.
func (s *Server) logDecision(e *Event, outcome, route string, sinks []string, extra ...any) {
	attrs := []any{"outcome", outcome, "route", route, "sinks", sinks, "sender", e.Sender}
	level := slog.LevelDebug
	if outcome == "matched" {
		level = slog.LevelInfo
	}
	s.log.Log(context.Background(), level, "routed", slices.Concat(attrs, e.logAttrs(), extra)...)
}

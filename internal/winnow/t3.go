package winnow

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// T3 is the configuration of the T3 hook. It sends each Ping of the login
// For from a Trusted sender in Senders to the T3 Code webhook URL of the
// repository in Repos (ADR 0012).
type T3 struct {
	For     string            `yaml:"for"`
	Senders []string          `yaml:"senders"` // the Trusted senders
	Secret  string            `yaml:"secret"`  // signs each request
	Repos   map[string]string `yaml:"repos"`   // owner/repo to the T3 Code webhook URL
	// retryBase is the wait before the first retry, and retryTotal is the
	// longest time that the retries of one request take; 0 means 1 s and
	// 5 min. They are not in the file. Tests set a few milliseconds.
	retryBase, retryTotal time.Duration
}

// repoKey matches a key of t3.repos: owner/repo.
var repoKey = regexp.MustCompile(`^[[:alnum:]_.-]+/[[:alnum:]_.-]+$`)

// load expands the secret and the URLs, and checks each key. An error never
// holds a URL, because the URL holds the token of the T3 Code webhook.
func (t *T3) load() (errs []error) {
	if t.For == "" {
		errs = append(errs, errors.New("t3.for is empty"))
	}
	if len(t.Senders) == 0 {
		errs = append(errs, errors.New("t3.senders is empty"))
	}
	if slices.Contains(t.Senders, "") {
		errs = append(errs, errors.New("t3.senders: a login is empty"))
	}
	var err error
	if t.Secret, err = expand(t.Secret); err != nil {
		errs = append(errs, fmt.Errorf("t3.secret: %w", err))
	} else if t.Secret == "" {
		errs = append(errs, errors.New("t3.secret is empty"))
	}
	if len(t.Repos) == 0 {
		errs = append(errs, errors.New("t3.repos is empty"))
	}
	repos := map[string]bool{}
	for _, key := range slices.Sorted(maps.Keys(t.Repos)) {
		if !repoKey.MatchString(key) {
			errs = append(errs, fmt.Errorf("t3.repos.%s: want owner/repo", key))
		}
		if repos[strings.ToLower(key)] {
			errs = append(errs, fmt.Errorf("t3.repos.%s: another key names the same repository", key))
		}
		repos[strings.ToLower(key)] = true
		v, err := expand(t.Repos[key])
		if err != nil {
			errs = append(errs, fmt.Errorf("t3.repos.%s: %w", key, err))
			continue
		}
		if u, err := url.Parse(v); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			errs = append(errs, fmt.Errorf("t3.repos.%s: want an http or https URL", key))
		}
		t.Repos[key] = v
	}
	return errs
}

// t3Kinds holds the Ping kinds that the T3 hook keeps.
var t3Kinds = pingKinds{"comments": true, "assigned": true}

// t3MaxAge is the oldest Backfill that the T3 hook sends, by the time at
// which the forge sent it.
const t3MaxAge = 24 * time.Hour

// t3MaxWait is the longest wait between two retries of one request.
const t3MaxWait = 30 * time.Second

// t3Request is the JSON body of one T3 hook request. It holds facts only,
// never text that the sender wrote, so that no untrusted text gets into
// the instructions of the agent.
type t3Request struct {
	Forge    string `json:"forge"`
	Repo     string `json:"repo"`
	Number   int    `json:"number"`
	Kind     string `json:"kind"`
	URL      string `json:"url"`
	IssueURL string `json:"issue_url"`
	Sender   string `json:"sender"`
	For      string `json:"for"`
	Delivery string `json:"delivery"`
	Backfill bool   `json:"backfill"`
}

// t3Entry is one request on the queue of the T3 hook.
type t3Entry struct {
	url   string
	body  []byte
	attrs []any // the log fields
}

// t3Hook sends the Pings of one login to T3 Code. It owns one queue and one
// worker, apart from the Sink queues, so that a slow T3 Code blocks nothing
// else.
type t3Hook struct {
	cfg      *T3
	senders  map[string]bool   // lowercase Trusted senders
	repos    map[string]string // lowercase owner/repo to the webhook URL
	teams    map[string][]string
	client   *http.Client
	log      *slog.Logger
	requests *prometheus.CounterVec
	queue    chan t3Entry
	wg       sync.WaitGroup
	mu       sync.RWMutex       // consider reads drain and enqueues under the read lock
	drain    chan struct{}      // closed when the shutdown starts
	stop     context.CancelFunc // stops the sends when the drain time ends
}

// newT3Hook starts the worker of the T3 hook of cfg, or returns nil when cfg
// has no T3 hook.
func newT3Hook(cfg *Config, log *slog.Logger, requests *prometheus.CounterVec) *t3Hook {
	t := cfg.T3
	if t == nil {
		return nil
	}
	ctx, stop := context.WithCancel(context.Background())
	h := &t3Hook{
		cfg:     t,
		senders: map[string]bool{},
		repos:   map[string]string{},
		teams:   cfg.Teams,
		// A redirect is a setup mistake, for example a sign-in page, so it
		// fails like a 4xx.
		client: &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}},
		log:      log,
		requests: requests,
		queue:    make(chan t3Entry, queueSize),
		drain:    make(chan struct{}),
		stop:     stop,
	}
	for _, s := range t.Senders {
		h.senders[strings.ToLower(s)] = true
	}
	for repo, u := range t.Repos {
		h.repos[strings.ToLower(repo)] = u
	}
	h.wg.Go(func() { h.run(ctx) })
	return h
}

// consider queues a request for e when e pings the login of the T3 hook from
// a Trusted sender on a listed repository. at is the time at which the forge
// sent e. A Backfill older than t3MaxAge sends nothing. A nil hook does
// nothing.
func (h *t3Hook) consider(e *Event, at, now time.Time, backfill bool) {
	if h == nil {
		return
	}
	url, ok := h.repos[strings.ToLower(e.Repo)]
	if !ok || !h.senders[strings.ToLower(e.Sender)] || e.SenderBot || strings.EqualFold(e.Sender, h.cfg.For) {
		return
	}
	if backfill && now.Sub(at) >= t3MaxAge {
		return
	}
	kind, logins := pinged(e, h.teams)
	if !t3Kinds[kind] || !slices.Contains(logins(), strings.ToLower(h.cfg.For)) {
		return
	}
	body, err := json.Marshal(t3Request{
		Forge:    e.Forge,
		Repo:     e.Repo,
		Number:   e.Number,
		Kind:     kind,
		URL:      e.URL,
		IssueURL: cmp.Or(e.IssueURL, e.URL),
		Sender:   e.Sender,
		For:      h.cfg.For,
		Delivery: e.Delivery,
		Backfill: backfill,
	})
	if err != nil {
		h.log.Error("t3 hook failed", "repo", e.Repo, "delivery", e.Delivery, "error", err)
		return
	}
	en := t3Entry{url: url, body: body, attrs: []any{"repo", e.Repo, "number", e.Number, "kind", kind, "sender", e.Sender, "delivery", e.Delivery, "backfill", backfill}}
	// Under the read lock, the drain cannot start between the check and the
	// enqueue, so a worker that stopped never misses a request.
	h.mu.RLock()
	defer h.mu.RUnlock()
	select {
	case <-h.drain:
		h.fail(en, &failure{reason: "shutdown"})
		return
	default:
	}
	select {
	case h.queue <- en:
	default:
		h.fail(en, &failure{reason: "queue_full"})
	}
}

// shutdown starts the drain and waits for the worker, like the Sink queues
// in Server.Shutdown.
func (h *t3Hook) shutdown(ctx context.Context) {
	h.mu.Lock()
	select {
	case <-h.drain:
	default:
		close(h.drain)
	}
	h.mu.Unlock()
	context.AfterFunc(ctx, h.stop)
	h.wg.Wait()
}

// run is the worker. It sends the requests in the queue, in order. When
// drain closes, it sends the requests left in the queue and stops. When ctx
// ends, the sends stop and the worker logs each request left with
// reason=shutdown.
func (h *t3Hook) run(ctx context.Context) {
	for {
		select {
		case en := <-h.queue:
			attempts, status, f := h.send(ctx, en)
			if f != nil {
				h.fail(en, f)
				continue
			}
			h.requests.WithLabelValues("sent", "").Inc()
			h.log.Info("t3 hook sent", append(en.attrs, "status", status, "attempts", attempts)...)
		case <-h.drain:
			if len(h.queue) == 0 {
				return
			}
		}
	}
}

// send posts en and retries it after a network error, a 429, or a 5xx, for
// at most retryTotal. It returns the number of attempts and the HTTP status
// of the reply that T3 Code took, or the failure.
func (h *t3Hook) send(ctx context.Context, en t3Entry) (int, int, *failure) {
	base, total := cmp.Or(h.cfg.retryBase, time.Second), cmp.Or(h.cfg.retryTotal, 5*time.Minute)
	start := time.Now()
	last := &failure{} // the last attempt that failed
	for attempt, wait := 1, base; ; attempt, wait = attempt+1, min(wait*2, t3MaxWait) {
		status, err := h.post(ctx, en)
		if err == nil && status/100 == 2 {
			return attempt, status, nil
		}
		if ctx.Err() != nil {
			break
		}
		f := &failure{reason: "rejected", attempts: attempt, status: status}
		if err != nil {
			f.detail = err.Error()
		}
		if err == nil && status != http.StatusTooManyRequests && status < 500 {
			return attempt, status, f
		}
		if time.Since(start)+wait > total {
			f.reason = "retries_exhausted"
			return attempt, status, f
		}
		last = f
		if !sleep(ctx, wait) {
			break
		}
	}
	// ctx ended before T3 Code took the request. A send that ctx cut is not
	// an attempt.
	last.reason = "shutdown"
	return last.attempts, last.status, last
}

// post sends en once with its signature and returns the HTTP status. An
// error never holds the URL.
func (h *t3Hook) post(ctx context.Context, en t3Entry) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, en.url, bytes.NewReader(en.body))
	if err != nil {
		return 0, errors.New("cannot make the request")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Winnow-Signature-256", signature(h.cfg.Secret, en.body))
	resp, err := h.client.Do(req)
	if err != nil {
		return 0, postError(err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	return resp.StatusCode, nil
}

// postError describes a failed send without the URL. The error of the HTTP
// client can quote the URL, also inside a nested error such as a Location
// header that does not parse, so only a network error keeps its text: it
// names the host and the port, not the path.
func postError(err error) error {
	if oe, ok := errors.AsType[*net.OpError](err); ok {
		return oe
	}
	if ne, ok := errors.AsType[net.Error](err); ok && ne.Timeout() {
		return errors.New("timeout")
	}
	return errors.New("request failed")
}

// fail counts f and writes the failed delivery line of the T3 hook.
func (h *t3Hook) fail(en t3Entry, f *failure) {
	h.requests.WithLabelValues("failed", f.reason).Inc()
	attrs := append([]any{"reason", f.reason}, en.attrs...)
	h.log.Error("t3 hook failed", append(attrs, "attempts", f.attempts, "status", f.status, "error", f.detail)...)
}

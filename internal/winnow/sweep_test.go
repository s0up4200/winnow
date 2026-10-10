package winnow

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeGitHub is a fake GitHub API that serves the deliveries of one org
// webhook.
type fakeGitHub struct {
	*httptest.Server
	mu       sync.Mutex
	attempts []fakeAttempt // newest first
	perPage  int
	status   int           // when not 0, each request gets this status
	fail     map[int64]int // the status of the detail request of an attempt ID
	requests []string      // the path and query of each request
}

// fakeAttempt is one delivery attempt that the fake GitHub lists.
type fakeAttempt struct {
	id      int64
	guid    string
	at      time.Time
	status  int // the status_code
	event   string
	payload []byte
}

func newFakeGitHub(t *testing.T) *fakeGitHub {
	g := &fakeGitHub{perPage: 100, fail: map[int64]int{}}
	g.Server = httptest.NewServer(http.HandlerFunc(g.serve))
	t.Cleanup(g.Close)
	return g
}

// add puts an attempt at the top of the list. It sets the ID.
func (g *fakeGitHub) add(a fakeAttempt) {
	g.mu.Lock()
	defer g.mu.Unlock()
	a.id = int64(len(g.attempts) + 1)
	g.attempts = slices.Insert(g.attempts, 0, a)
}

// count returns the number of requests whose path and query contain part.
func (g *fakeGitHub) count(part string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	n := 0
	for _, r := range g.requests {
		if strings.Contains(r, part) {
			n++
		}
	}
	return n
}

func (g *fakeGitHub) serve(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.requests = append(g.requests, r.URL.RequestURI())
	if r.Header.Get("Authorization") != "Bearer test-token" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if g.status != 0 {
		w.WriteHeader(g.status)
		return
	}
	item := func(a fakeAttempt) map[string]any {
		return map[string]any{"id": a.id, "guid": a.guid, "delivered_at": a.at.UTC().Format(time.RFC3339), "status_code": a.status, "event": a.event}
	}
	rest, ok := strings.CutPrefix(r.URL.Path, "/orgs/autobrr/hooks/42/deliveries")
	switch {
	case !ok:
		w.WriteHeader(http.StatusNotFound)
	case rest == "":
		start, _ := strconv.Atoi(r.URL.Query().Get("cursor"))
		end := min(start+g.perPage, len(g.attempts))
		if end < len(g.attempts) {
			w.Header().Set("Link", fmt.Sprintf(`<%s/orgs/autobrr/hooks/42/deliveries?per_page=%d&cursor=%d>; rel="next"`, g.URL, g.perPage, end))
		}
		var page []map[string]any
		for _, a := range g.attempts[start:end] {
			page = append(page, item(a))
		}
		_ = json.MarshalWrite(w, page)
	default:
		id, _ := strconv.ParseInt(strings.TrimPrefix(rest, "/"), 10, 64)
		if st := g.fail[id]; st != 0 {
			w.WriteHeader(st)
			return
		}
		i := slices.IndexFunc(g.attempts, func(a fakeAttempt) bool { return a.id == id })
		if i < 0 {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		a := g.attempts[i]
		d := item(a)
		// GitHub mixes the case of the header names.
		d["request"] = map[string]any{
			"headers": map[string]string{"X-GitHub-Event": a.event, "x-github-delivery": a.guid, "content-type": "application/json"},
			"payload": jsontext.Value(a.payload),
		}
		_ = json.MarshalWrite(w, d)
	}
}

// onlyHost is the transport of the GitHub client in a test. It fails the
// test on a request to a host that is not the fake GitHub.
type onlyHost struct {
	t    *testing.T
	host string
}

func (o onlyHost) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Host != o.host {
		o.t.Errorf("Sweep request to %s, want only the fake GitHub", r.URL)
		return nil, errors.New("not the fake GitHub")
	}
	return http.DefaultTransport.RoundTrip(r)
}

// sweepConfig has one Source with a Sweep, a Route for releases that
// accepts Backfills, a Route for the rest that does not, an alerts Sink,
// and a daily Digest of all Events.
const sweepConfig = `
sources:
  github-autobrr:
    secret: test-secret
    sweep: { token: test-token, org: autobrr, hook: 42 }
sinks:
  releases: { discord: https://discord.example.invalid/api/webhooks/1/token }
  rest: { discord: https://discord.example.invalid/api/webhooks/2/token }
  alerts: { discord: https://discord.example.invalid/api/webhooks/3/token }
  digest: { discord: https://discord.example.invalid/api/webhooks/4/token }
alerts: alerts
routes:
  - name: releases
    backfill: true
    to: [releases]
    match: { event: release }
  - name: rest
    to: [rest]
    match: {}
digests:
  - name: test
    every: daily
    to: digest
    match: {}
`

// missed adds a failed attempt of the fixture testdata/github/<name>.json
// at the time at, with the status code status. It returns the GUID.
func (h *harness) missed(event, name string, at time.Time, status int) string {
	h.t.Helper()
	h.ids++
	guid := fmt.Sprintf("guid-%d", h.ids)
	h.gh.add(fakeAttempt{guid: guid, at: at, status: status, event: event, payload: fixture(h.t, "github/"+name)})
	return guid
}

// sinks returns the sorted Sink names of the requests that the fake Discord
// got, after a stop that empties the queues. Then it starts the server
// again.
func (h *harness) sinks() []string {
	h.t.Helper()
	h.restart(h.config)
	var got []string
	for len(h.discord) > 0 {
		got = append(got, (<-h.discord).Sink)
	}
	slices.Sort(got)
	return got
}

// alertTitles returns the titles of the alerts in reqs.
func alertTitles(t *testing.T, reqs []discordRequest) []string {
	t.Helper()
	var titles []string
	for _, r := range reqs {
		if r.Sink != "alerts" {
			continue
		}
		titles = append(titles, strings.TrimPrefix(displays(decodeMessage(t, r.Body).Components)[0], "## "))
	}
	return titles
}

// drain stops the server, so that the queues are empty, and returns the
// requests that the fake Discord got. Then it starts the server again.
func (h *harness) drain() []discordRequest {
	h.t.Helper()
	h.restart(h.config)
	var got []discordRequest
	for len(h.discord) > 0 {
		got = append(got, <-h.discord)
	}
	return got
}

func TestSweepBackfillRoutes(t *testing.T) {
	h := newHarness(t, sweepConfig)
	h.step(day(10, 1, 11, 0))
	h.missed("release", "release_published", day(10, 1, 12, 0), 502)
	h.missed("star", "star_created", day(10, 1, 12, 1), 0) // a time-out
	h.step(day(10, 1, 12, 15))

	got := h.drain()
	i := slices.IndexFunc(got, func(r discordRequest) bool { return r.Sink == "releases" })
	if len(got) != 1 || i < 0 {
		t.Fatalf("requests = %v, want the release", got)
	}
	// A Backfill looks like a live message.
	live := newHarness(t, sweepConfig)
	live.receive(day(10, 1, 12, 0), live.github("release", "release_published"))
	if want := live.waitDiscord().Body; string(got[i].Body) != string(want) {
		t.Errorf("Backfill message\n%s\nwant the live message\n%s", got[i].Body, want)
	}
	// The star matches the Route rest, which does not accept Backfills. It
	// does not fall through to a later Route.
	var star map[string]any
	for _, l := range h.linesWith("routed") {
		if l["event"] == "star.created" {
			star = l
		}
	}
	if star["outcome"] != "dropped" || star["route"] != "rest" || star["backfill"] != true {
		t.Errorf("star decision = %v, want dropped by rest as a Backfill", star)
	}
}

func TestSweepBackfillCountsInItsPeriod(t *testing.T) {
	h := newHarness(t, sweepConfig)
	h.step(day(9, 30, 0, 0))
	h.step(day(10, 2, 8, 50))
	// The list shows the delivery after the Sweep of 08:50. Only the Sweep
	// before the Digest send at 09:00 finds it.
	h.missed("star", "star_created", day(10, 1, 23, 59), 502)
	h.step(day(10, 2, 9, 0))
	var digests []string
	for _, r := range h.drain() {
		if r.Sink == "digest" {
			digests = append(digests, allText(decodeMessage(t, r.Body)))
		}
	}
	if len(digests) != 1 || !strings.HasPrefix(digests[0], "-# Codertocat · Wednesday 1 October\n") || !strings.Contains(digests[0], "**1** star\n") {
		t.Errorf("digests = %+v, want one for 1 October with one star", digests)
	}
}

func TestSweepSkipsLiveDeliveryWithoutDigests(t *testing.T) {
	config, _, _ := strings.Cut(sweepConfig, "digests:")
	h := newHarness(t, config)
	h.step(day(10, 1, 11, 0))
	at := day(10, 1, 12, 0)
	// GitHub marked this delivery as failed, but winnow received it. No
	// Digest matches it.
	guid := h.missed("release", "release_published", at, 502)
	req := h.github("release", "release_published")
	req.Header.Set("X-GitHub-Delivery", guid)
	h.clock = at
	h.do(req)
	h.waitDiscord()
	h.step(day(10, 1, 12, 15))
	if got := h.sinks(); len(got) != 0 {
		t.Errorf("sinks = %v, want none", got)
	}
}

func TestSweepSkipsHandledDeliveries(t *testing.T) {
	h := newHarness(t, sweepConfig)
	h.step(day(10, 1, 11, 0))
	at := day(10, 1, 12, 0)
	h.missed("release", "release_published", at, 401)
	h.missed("release", "release_published", at, 200)
	guid := h.missed("release", "release_published", at, 502)
	h.gh.add(fakeAttempt{guid: guid, at: at.Add(time.Minute), status: 200, event: "release", payload: fixture(t, "github/release_published")})
	// GitHub marked this delivery as failed, but winnow received it.
	live := h.missed("release", "release_published", at, 502)
	req := h.github("release", "release_published")
	req.Header.Set("X-GitHub-Delivery", live)
	h.clock = at
	h.do(req)
	h.waitDiscord()
	h.step(day(10, 1, 12, 15))

	if got := h.sinks(); len(got) != 0 {
		t.Errorf("sinks = %v, want none", got)
	}
	if n := h.gh.count("/deliveries/"); n != 0 {
		t.Errorf("got %d detail requests, want 0", n)
	}
}

func TestRedeliveryAfterBackfillRoutes(t *testing.T) {
	h := newHarness(t, sweepConfig)
	h.step(day(10, 1, 11, 0))
	guid := h.missed("star", "star_created", day(10, 1, 12, 0), 502)
	// The Route rest does not accept Backfills, so the Sweep posts nothing.
	h.step(day(10, 1, 12, 15))
	// A redelivery from GitHub is how an operator sends that Event.
	req := h.github("star", "star_created")
	req.Header.Set("X-GitHub-Delivery", guid)
	h.clock = day(10, 1, 13, 0)
	if got := h.do(req).Code; got != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", got, http.StatusAccepted)
	}
	h.step(day(10, 2, 9, 0))

	var rest int
	var digests []string
	for _, r := range h.drain() {
		switch r.Sink {
		case "rest":
			rest++
		case "digest":
			digests = append(digests, allText(decodeMessage(t, r.Body)))
		}
	}
	if rest != 1 {
		t.Errorf("got %d messages to rest, want 1", rest)
	}
	if len(digests) != 1 || !strings.Contains(digests[0], "**1** star\n") {
		t.Errorf("digests = %+v, want one with one star", digests)
	}
}

func TestSweepDetailFailureKeepsDelivery(t *testing.T) {
	h := newHarness(t, sweepConfig)
	h.step(day(10, 1, 11, 0))
	h.missed("release", "release_published", day(10, 1, 12, 0), 502)
	h.gh.fail[1] = http.StatusInternalServerError
	h.step(day(10, 1, 12, 15))
	if got := h.linesWith("sweep failed"); len(got) != 1 || got[0]["level"] != "WARN" {
		t.Errorf("sweep failed lines = %v, want one warn line", got)
	}
	delete(h.gh.fail, 1)
	// The next tick tries again.
	h.step(day(10, 1, 12, 16))
	got := h.drain()
	if titles := alertTitles(t, got); !slices.Equal(titles, []string{"Sweep of github-autobrr failed", "Sweep of github-autobrr works again"}) {
		t.Errorf("alerts = %q", titles)
	}
	if !slices.ContainsFunc(got, func(r discordRequest) bool { return r.Sink == "releases" }) {
		t.Error("the release was not posted")
	}
}

func TestSweepPostsOnceAfterRestart(t *testing.T) {
	h := newHarness(t, sweepConfig)
	h.step(day(10, 1, 11, 0))
	h.missed("release", "release_published", day(10, 1, 12, 0), 502)
	h.step(day(10, 1, 12, 15))
	h.restart(h.config) // the restart sweeps at start
	h.step(day(10, 1, 12, 30))
	if got := h.sinks(); !slices.Equal(got, []string{"releases"}) {
		t.Errorf("sinks = %v, want one release", got)
	}
	if n := h.gh.count("/deliveries/"); n != 1 {
		t.Errorf("got %d detail requests, want 1", n)
	}
}

func TestSweepTwoServersPostOnce(t *testing.T) {
	h := newHarness(t, sweepConfig)
	h2 := *h
	h2.start(h.config)
	t.Cleanup(h2.stop)
	for i := range 5 {
		h.missed("release", "release_published", day(10, 1, 12, i), 502)
	}
	var wg sync.WaitGroup
	for _, x := range []*harness{h, &h2} {
		x.clock = day(10, 1, 12, 15)
		wg.Go(func() { x.srv.step(t.Context()) })
	}
	wg.Wait()
	h2.stop()
	n := 0
	for _, s := range h.sinks() {
		if s == "releases" {
			n++
		}
	}
	if n != 5 {
		t.Errorf("got %d releases, want 5", n)
	}
}

func TestSweepFollowsPagesForThreeDays(t *testing.T) {
	h := newHarness(t, sweepConfig)
	h.gh.perPage = 2
	h.missed("release", "release_published", day(10, 1, 0, 0), 502)
	h.missed("release", "release_published", day(10, 1, 1, 0), 502)
	h.missed("release", "release_published", day(10, 3, 12, 0), 502)
	h.missed("release", "release_published", day(10, 4, 1, 0), 502)
	h.missed("release", "release_published", day(10, 4, 2, 0), 502)
	h.step(day(10, 4, 3, 0))
	// The Sweep stops at the delivery of 1 October 01:00 on page 2.
	if got := h.sinks(); !slices.Equal(got, []string{"releases", "releases", "releases"}) {
		t.Errorf("sinks = %v, want 3 releases", got)
	}
	if n := h.gh.count("/deliveries?"); n != 2 {
		t.Errorf("got %d list requests, want 2", n)
	}
}

func TestSweepForgetsAfterFourDays(t *testing.T) {
	h := newHarness(t, sweepConfig)
	// The delivered_at is after the first Sweep, so that the delivery stays
	// in the 3 days that the Sweep reads. Only a forgotten claim then makes
	// a second post.
	h.missed("star", "star_created", day(10, 5, 0, 0), 502)
	h.step(day(10, 1, 0, 0))
	h.step(day(10, 4, 23, 59))
	if n := h.gh.count("/deliveries/"); n != 1 {
		t.Fatalf("got %d detail requests before 4 days, want 1", n)
	}
	h.step(day(10, 5, 0, 30))
	if n := h.gh.count("/deliveries/"); n != 2 {
		t.Errorf("got %d detail requests after 4 days, want 2", n)
	}
}

func TestSweepAlertsOnFailureAndRecovery(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusNotFound} {
		for _, endpoint := range []string{"list", "detail"} {
			t.Run(strconv.Itoa(status)+"/"+endpoint, func(t *testing.T) {
				h := newHarness(t, sweepConfig)
				if endpoint == "detail" {
					h.missed("star", "star_created", day(10, 1, 11, 0), 502)
					h.gh.fail[1] = status
				} else {
					h.gh.status = status
				}
				h.step(day(10, 1, 12, 0))
				h.step(day(10, 1, 12, 1))
				if got := h.linesWith("sweep failed"); len(got) != 2 || got[0]["level"] != "ERROR" || got[1]["level"] != "ERROR" {
					t.Errorf("sweep failed lines = %v, want two error lines", got)
				}
				// A new status during the same failure must not send another alert.
				h.gh.status = http.StatusServiceUnavailable
				h.step(day(10, 1, 12, 2))
				h.gh.status = 0
				clear(h.gh.fail)
				h.step(day(10, 1, 12, 3))
				h.step(day(10, 1, 12, 20))
				got := h.drain()
				titles := alertTitles(t, got)
				// The detail case also sends an alert for the recovered Backfill.
				titles = slices.DeleteFunc(titles, func(title string) bool { return strings.HasPrefix(title, "Sweep of github-autobrr found ") })
				if !slices.Equal(titles, []string{"Sweep of github-autobrr failed", "Sweep of github-autobrr works again"}) {
					t.Errorf("alerts = %q", titles)
				}
				want := "GitHub API status 401"
				if status == http.StatusNotFound {
					want = "GitHub API status 404. Check the organization and webhook ID, that the token belongs to an org owner with Webhooks read access, and whether an OAuth app created the webhook. See the README Sweeps section."
				}
				for _, r := range got {
					if r.Sink != "alerts" {
						continue
					}
					texts := displays(decodeMessage(t, r.Body).Components)
					if texts[0] == "## Sweep of github-autobrr failed" && (len(texts) != 2 || texts[1] != want) {
						t.Errorf("failure text = %q, want %q", texts[1:], want)
					}
				}
			})
		}
	}
}

func TestSweepCountsBackfillsBeforeAFailure(t *testing.T) {
	h := newHarness(t, sweepConfig)
	h.step(day(10, 1, 11, 0))
	h.missed("release", "release_published", day(10, 1, 12, 0), 502)
	h.missed("release", "release_published", day(10, 1, 12, 1), 502)
	h.gh.fail[2] = http.StatusInternalServerError
	h.step(day(10, 1, 12, 15))
	if got := h.linesWith("swept"); len(got) != 1 || got[0]["backfills"] != 1.0 {
		t.Errorf("swept lines = %v, want one with the Backfill before the failure", got)
	}
}

func TestSweepFailureBeforeDigestStillSends(t *testing.T) {
	h := newHarness(t, sweepConfig)
	h.step(day(9, 30, 0, 0))
	h.receive(day(10, 1, 12, 0), h.github("star", "star_created"))
	h.gh.status = http.StatusServiceUnavailable
	h.step(day(10, 2, 9, 0))
	if got := h.linesWith("digest can count too few Events"); len(got) != 1 {
		t.Errorf("got %d warning lines, want 1", len(got))
	}
	if !slices.ContainsFunc(h.drain(), func(r discordRequest) bool { return r.Sink == "digest" }) {
		t.Error("the Digest was not sent")
	}
}

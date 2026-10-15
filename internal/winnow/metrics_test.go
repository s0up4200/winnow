package winnow

import (
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
)

// metrics reads the public endpoint, including its types and labels.
func (h *harness) metrics() map[string]*dto.MetricFamily {
	h.t.Helper()
	rec := h.do(httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		h.t.Fatalf("GET /metrics = %d, want 200", rec.Code)
	}
	p := expfmt.NewTextParser(model.UTF8Validation)
	families, err := p.TextToMetricFamilies(rec.Body)
	if err != nil {
		h.t.Fatal(err)
	}
	for name := range families {
		switch name {
		case "winnow_sink_delivery_failures_total", "winnow_webhook_rejections_total", "winnow_events_total", "winnow_digests_total":
		default:
			h.t.Fatalf("metric family %s, want only the four application counters", name)
		}
	}
	return families
}

// assertCounter checks every series, with zero as the default expectation.
func (h *harness) assertCounter(name string, want map[[2]string]float64) {
	h.t.Helper()
	family := h.metrics()[name]
	if family == nil || family.GetType() != dto.MetricType_COUNTER {
		h.t.Fatalf("%s is not a counter", name)
	}
	first, second := "sink", "reason"
	if name == "winnow_webhook_rejections_total" {
		first, second = "source", "status"
	}
	seen := map[[2]string]bool{}
	for _, m := range family.Metric {
		labels := map[string]string{}
		for _, l := range m.Label {
			labels[l.GetName()] = l.GetValue()
		}
		if len(labels) != 2 || labels[first] == "" || labels[second] == "" {
			h.t.Fatalf("%s labels = %v", name, labels)
		}
		key := [2]string{labels[first], labels[second]}
		seen[key] = true
		if got := m.GetCounter().GetValue(); got != want[key] {
			h.t.Errorf("%s%v = %v, want %v", name, labels, got, want[key])
		}
	}
	for key := range want {
		if !seen[key] {
			h.t.Errorf("%s missing labels %v", name, key)
		}
	}
}

func (h *harness) assertMetrics(sink, reason string, failures float64, source, status string, rejections float64) {
	h.t.Helper()
	h.assertCounter("winnow_sink_delivery_failures_total", map[[2]string]float64{{sink, reason}: failures})
	h.assertCounter("winnow_webhook_rejections_total", map[[2]string]float64{{source, status}: rejections})
}

func TestMetricsStartAtZero(t *testing.T) {
	h := newHarness(t, quiConfig)
	h.assertCounter("winnow_sink_delivery_failures_total", map[[2]string]float64{
		{"qui", "queue_full"}:        0,
		{"qui", "rejected"}:          0,
		{"qui", "retries_exhausted"}: 0,
		{"qui", "shutdown"}:          0,
	})
	h.assertCounter("winnow_webhook_rejections_total", map[[2]string]float64{
		{"github-autobrr", "400"}: 0,
		{"github-autobrr", "401"}: 0,
		{"github-autobrr", "405"}: 0,
		{"github-autobrr", "413"}: 0,
		{"github-autobrr", "415"}: 0,
	})
	for name, want := range map[string]int{"winnow_sink_delivery_failures_total": 4, "winnow_webhook_rejections_total": 5} {
		if got := len(h.metrics()[name].Metric); got != want {
			t.Errorf("%s has %d series, want %d", name, got, want)
		}
	}
}

func TestMetricsTransientErrorThenSuccess(t *testing.T) {
	for _, status := range []int{http.StatusInternalServerError, 0} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			h := newHarness(t, quiConfig)
			h.script(reply{status: status})
			deliverLabel(t, h)
			shutdown(h, time.Second)
			if got := len(h.discord); got != 2 {
				t.Fatalf("Discord sends = %d, want one retry", got)
			}
			h.assertMetrics("qui", "rejected", 0, "github-autobrr", "401", 0)
		})
	}
}

func TestMetricsIsolationAndRestart(t *testing.T) {
	h := newHarness(t, quiConfig)
	other := newHarness(t, quiConfig)
	h.do(httptest.NewRequest(http.MethodGet, "/hook/github-autobrr", nil))
	h.script(reply{status: http.StatusNotFound})
	deliverLabel(t, h)
	h.waitFailure()
	h.assertMetrics("qui", "rejected", 1, "github-autobrr", "405", 1)
	other.assertMetrics("qui", "rejected", 0, "github-autobrr", "405", 0)
	h.restart(quiConfig)
	h.assertMetrics("qui", "rejected", 0, "github-autobrr", "405", 0)
}

func TestMetricsEscapedNamesAndUnknownSources(t *testing.T) {
	name := "a\"b\\c\nd"
	h := newHarness(t, `
sources:
  "a\"b\\c\nd": { secret: test-secret }
sinks:
  "a\"b\\c\nd": { discord: https://discord.example.invalid/api/webhooks/1/token }
routes:
  - match: {}
    to: ["a\"b\\c\nd"]
`)
	h.assertMetrics(name, "queue_full", 0, name, "400", 0)
	for _, path := range []string{"/hook/nope", "/hook/another", "/unrelated", "/healthz", "/metrics"} {
		h.do(httptest.NewRequest(http.MethodGet, path, nil))
	}
	h.assertMetrics(name, "queue_full", 0, name, "400", 0)
	if got := h.metrics(); len(got["winnow_sink_delivery_failures_total"].Metric) != 4 || len(got["winnow_webhook_rejections_total"].Metric) != 5 {
		t.Fatal("requests introduced new metric series")
	}
}

func TestMetricsConcurrentScrapesAndIncrements(t *testing.T) {
	h := newHarness(t, quiConfig)
	h.script(reply{status: http.StatusNotFound})
	deliverLabel(t, h)
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for range 10 {
				h.do(httptest.NewRequest(http.MethodGet, "/hook/github-autobrr", nil))
				h.metrics()
			}
		})
	}
	wg.Wait()
	h.waitFailure()
	h.assertMetrics("qui", "rejected", 1, "github-autobrr", "405", 40)
}

func TestMetricsDigestRetryRetainsFailures(t *testing.T) {
	h := newHarness(t, digestConfig("weekly", "{}"))
	h.step(day(9, 29, 0, 0))
	h.receive(day(10, 1, 12, 0), h.github("star", "star_created"))
	h.assertMetrics("digest", "rejected", 0, "github-autobrr", "401", 0)
	h.script(reply{status: 400})
	h.step(day(10, 6, 9, 0))
	h.waitFailure()
	h.assertMetrics("digest", "rejected", 1, "github-autobrr", "401", 0)
	h.script(reply{status: 400})
	h.step(day(10, 6, 10, 0))
	for deadline := time.Now().Add(2 * time.Second); len(h.linesWith("delivery failed")) < 2; time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("no second failed Digest cycle")
		}
	}
	h.assertMetrics("digest", "rejected", 2, "github-autobrr", "401", 0)
	h.step(day(10, 6, 11, 0))
	shutdown(h, time.Second)
	if got := len(h.linesWith("digest sent")); got != 1 {
		t.Fatalf("successful Digest sends = %d, want 1", got)
	}
	h.assertMetrics("digest", "rejected", 2, "github-autobrr", "401", 0)
}

func TestMetricsBackfillAndSweepAlertFailures(t *testing.T) {
	h := newHarness(t, sweepConfig)
	h.step(day(10, 1, 11, 0))
	h.missed("release", "release_published", day(10, 1, 12, 0), 502)
	h.missed("star", "star_created", day(10, 1, 12, 1), 0)
	h.missed("release", "release_published", day(10, 1, 12, 2), 502)
	h.gh.fail[3] = http.StatusInternalServerError
	// The accepted release and the Sweep failure alert each fail at Discord.
	// The excluded star does not send to the rest Sink.
	h.script(reply{status: 400}, reply{status: 400})
	h.step(day(10, 1, 12, 15))
	shutdown(h, time.Second)
	h.assertCounter("winnow_sink_delivery_failures_total", map[[2]string]float64{
		{"releases", "rejected"}: 1,
		{"alerts", "rejected"}:   1,
	})
	h.assertCounter("winnow_webhook_rejections_total", nil)
}

func TestMetricsSweepAPIFailureIsNotDeliveryFailure(t *testing.T) {
	h := newHarness(t, sweepConfig)
	h.gh.status = http.StatusUnauthorized
	h.step(day(10, 1, 11, 0))
	shutdown(h, time.Second)
	h.assertMetrics("alerts", "rejected", 0, "github-autobrr", "401", 0)
}

func TestMetricsOneFailurePerSink(t *testing.T) {
	h := newHarness(t, `
sources:
  github-autobrr: { secret: test-secret }
sinks:
  a: { discord: https://discord.example.invalid/api/webhooks/1/a }
  b: { discord: https://discord.example.invalid/api/webhooks/2/b }
routes:
  - match: {}
    to: [a, b]
`)
	h.script(reply{status: 400}, reply{status: 400})
	deliverLabel(t, h)
	shutdown(h, time.Second)
	h.assertCounter("winnow_sink_delivery_failures_total", map[[2]string]float64{
		{"a", "rejected"}: 1,
		{"b", "rejected"}: 1,
	})
}

// series returns each series of the counter name, by its labels in the
// form name=value, joined with commas in the order of the label names. A
// counter without series is not on the endpoint.
func (h *harness) series(name string) map[string]float64 {
	h.t.Helper()
	family := h.metrics()[name]
	if family == nil {
		return map[string]float64{}
	}
	if family.GetType() != dto.MetricType_COUNTER {
		h.t.Fatalf("%s is not a counter", name)
	}
	got := map[string]float64{}
	for _, m := range family.Metric {
		var labels []string
		for _, l := range m.Label {
			labels = append(labels, l.GetName()+"="+l.GetValue())
		}
		got[strings.Join(labels, ",")] = m.GetCounter().GetValue()
	}
	return got
}

// eventSeries returns the labels of one winnow_events_total series in the
// form of series. repo is the repository, and the Source is github-autobrr.
func eventSeries(repo, event, action, outcome string, backfill, authorBot bool) string {
	return fmt.Sprintf("action=%s,author_bot=%t,backfill=%t,event=%s,outcome=%s,repo=%s,source=github-autobrr",
		action, authorBot, backfill, event, outcome, repo)
}

func (h *harness) assertSeries(name string, want map[string]float64) {
	h.t.Helper()
	if got := h.series(name); !maps.Equal(got, want) {
		h.t.Errorf("%s = %v, want %v", name, got, want)
	}
}

const eventsConfig = `
sources:
  github-autobrr: { secret: test-secret }
sinks:
  qui: { discord: https://discord.example.invalid/api/webhooks/1/token }
routes:
  - name: noise
    drop: true
    match: { event: release }
  - name: qui
    match: { repo: autobrr/qui }
    to: [qui]
`

// Each admitted live Event counts once, with the Route decision, the forge
// action, and whether the Author is a Bot sender.
func TestMetricsCountEvents(t *testing.T) {
	h := newHarness(t, eventsConfig)
	for _, d := range [][2]string{
		{"label", "label_created"},
		{"issue_comment", "issue_comment_bot"},
		{"pull_request", "pull_request_merged"},
		{"pull_request", "pull_request_closed_unmerged"},
		{"release", "release_published"},
		{"star", "star_created"},
		{"star", "star_created"},
		{"fork", "fork"},
		{"ping", "ping"},
	} {
		h.receive(day(10, 1, 12, 0), h.github(d[0], d[1]))
	}
	h.assertSeries("winnow_events_total", map[string]float64{
		eventSeries("autobrr/qui", "label", "created", "matched", false, false):                    1,
		eventSeries("autobrr/qui", "issue_comment", "created", "matched", false, true):             1,
		eventSeries("autobrr/qui", "pull_request", "merged", "matched", false, false):              1,
		eventSeries("Codertocat/Hello-World", "pull_request", "closed", "unmatched", false, false): 1,
		eventSeries("Codertocat/Hello-World", "release", "published", "dropped", false, false):     1,
		eventSeries("Codertocat/Hello-World", "star", "created", "unmatched", false, false):        2,
		eventSeries("Codertocat/Hello-World", "fork", "", "unmatched", false, false):               1,
	})
}

// A rejected request and a request to an unknown Source create no series.
func TestMetricsRejectedRequestsCountNoEvents(t *testing.T) {
	h := newHarness(t, eventsConfig)
	bad := h.github("star", "star_created")
	bad.Header.Set("X-Hub-Signature-256", "sha256=00")
	h.do(bad)
	h.do(signedDelivery("nope", "star", fixture(t, "github/star_created")))
	h.assertSeries("winnow_events_total", map[string]float64{})
}

// Each delivery of a Burst counts once, although winnow posts one card.
func TestMetricsBurstCountsEachDelivery(t *testing.T) {
	h := newHarness(t, burstConfig)
	for _, login := range []string{"octocat", "hubot", "monalisa"} {
		h.accept("pull_request", reviewer(t, login, ""))
	}
	h.assertSeries("winnow_events_total", map[string]float64{
		eventSeries("Codertocat/Hello-World", "pull_request", "review_requested", "matched", false, false): 3,
	})
}

// A Backfill counts when the Sweep admits it. A Backfill that its Route does
// not accept counts as dropped.
func TestMetricsCountBackfills(t *testing.T) {
	h := newHarness(t, sweepConfig)
	h.step(day(10, 1, 11, 0))
	h.missed("release", "release_published", day(10, 1, 12, 0), 502)
	h.missed("star", "star_created", day(10, 1, 12, 1), 0)
	h.step(day(10, 1, 12, 15))
	h.assertSeries("winnow_events_total", map[string]float64{
		eventSeries("Codertocat/Hello-World", "release", "published", "matched", true, false): 1,
		eventSeries("Codertocat/Hello-World", "star", "created", "dropped", true, false):      1,
	})
}

// digestSeries returns the four winnow_digests_total series of the Digest
// test, with the counts in the order sent, failed, skipped, empty.
func digestSeries(sent, failed, skipped, empty float64) map[string]float64 {
	return map[string]float64{
		"digest=test,state=sent":    sent,
		"digest=test,state=failed":  failed,
		"digest=test,state=skipped": skipped,
		"digest=test,state=empty":   empty,
	}
}

// waitLine waits until the log has a line with the message msg.
func (h *harness) waitLine(msg string) {
	h.t.Helper()
	for deadline := time.Now().Add(2 * time.Second); len(h.linesWith(msg)) == 0; time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			h.t.Fatalf("no %q line", msg)
		}
	}
}

func TestMetricsDigestsStartAtZero(t *testing.T) {
	h := newHarness(t, digestConfig("weekly", "{}"))
	h.assertSeries("winnow_digests_total", digestSeries(0, 0, 0, 0))
	h = newHarness(t, eventsConfig)
	h.assertSeries("winnow_digests_total", map[string]float64{})
}

func TestMetricsDigestSent(t *testing.T) {
	h := newHarness(t, digestConfig("weekly", "{}"))
	h.step(day(9, 29, 0, 0))
	h.receive(day(10, 1, 12, 0), h.github("star", "star_created"))
	h.step(day(10, 6, 9, 0))
	h.waitLine("digest sent")
	h.step(day(10, 6, 10, 0))
	h.assertSeries("winnow_digests_total", digestSeries(1, 0, 0, 0))
}

func TestMetricsDigestEmpty(t *testing.T) {
	h := newHarness(t, digestConfig("weekly", "{event: fork}"))
	h.step(day(9, 29, 0, 0))
	h.receive(day(10, 1, 12, 0), h.github("star", "star_created"))
	h.step(day(10, 6, 9, 0))
	h.step(day(10, 6, 10, 0))
	h.assertSeries("winnow_digests_total", digestSeries(0, 0, 0, 1))
}

// Each Period that winnow missed while it was down counts as skipped.
func TestMetricsDigestSkipped(t *testing.T) {
	h := newHarness(t, digestConfig("weekly", "{}"))
	h.step(day(9, 1, 0, 0))
	h.receive(day(10, 1, 12, 0), h.github("star", "star_created"))
	h.restart(h.config)
	h.step(day(10, 6, 10, 0))
	h.waitLine("digest sent")
	h.assertSeries("winnow_digests_total", digestSeries(1, 0, 4, 0))
}

// A Digest counts once as failed after all retries, and a failed try does
// not count.
func TestMetricsDigestFailed(t *testing.T) {
	h := newHarness(t, digestConfig("weekly", "{}"))
	h.step(day(9, 29, 0, 0))
	h.receive(day(10, 1, 12, 0), h.github("star", "star_created"))
	h.script(reply{status: 400})
	h.step(day(10, 6, 9, 0))
	h.waitFailure()
	h.assertSeries("winnow_digests_total", digestSeries(0, 0, 0, 0))
	h.step(day(10, 7, 9, 0))
	h.step(day(10, 7, 10, 0))
	h.assertSeries("winnow_digests_total", digestSeries(0, 1, 0, 0))
}

package winnow

import (
	"net/http"
	"net/http/httptest"
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
	return families
}

// assertCounter checks every series, with zero as the default expectation.
func (h *harness) assertCounter(name string, want map[[2]string]float64) {
	h.t.Helper()
	families := h.metrics()
	if len(families) != 2 {
		h.t.Fatalf("metric families = %v, want only two application counters", families)
	}
	family := families[name]
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

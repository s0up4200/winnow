package winnow

import (
	"bytes"
	"context"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"
)

// deliverLabel sends the label fixture to the qui Route and checks the reply.
func deliverLabel(t *testing.T, h *harness) {
	t.Helper()
	if got := h.do(signedDelivery("github-autobrr", "label", fixture(t, "github/label_created"))).Code; got != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", got)
	}
}

func TestRejectedReplyIsNotRetried(t *testing.T) {
	h := newHarness(t, quiConfig)
	h.script(reply{status: http.StatusNotFound, body: `{"message": "Unknown Webhook", "code": 10015}`})

	deliverLabel(t, h)

	want := map[string]any{
		"level":    "ERROR",
		"msg":      "delivery failed",
		"reason":   "rejected",
		"sink":     "qui",
		"route":    "qui",
		"source":   "github-autobrr",
		"event":    "label.created",
		"repo":     "autobrr/qui",
		"delivery": testDelivery,
		"attempts": 1.0,
		"status":   404.0,
		"error":    `{"message": "Unknown Webhook", "code": 10015}`,
	}
	if got := h.waitFailure(); !reflect.DeepEqual(got, want) {
		t.Errorf("failed delivery line = %v\nwant %v", got, want)
	}
	if n := len(h.discord); n != 1 {
		t.Errorf("fake Discord got %d requests, want 1", n)
	}
}

func TestServerErrorRetriesRunOut(t *testing.T) {
	h := newHarness(t, quiConfig)
	fail := reply{status: http.StatusInternalServerError, body: strings.Repeat("x", 600)}
	h.script(fail, fail, fail, fail)

	deliverLabel(t, h)

	f := h.waitFailure()
	if f["reason"] != "retries_exhausted" || f["attempts"] != 4.0 || f["status"] != 500.0 {
		t.Errorf("failed delivery line = %v, want retries_exhausted after 4 attempts with status 500", f)
	}
	if got := f["error"]; got != strings.Repeat("x", 500) {
		t.Errorf("error = %v, want the reply body cut to 500 bytes", got)
	}
	if n := len(h.discord); n != 4 {
		t.Fatalf("fake Discord got %d requests, want 4", n)
	}
	prev := h.waitDiscord()
	for i := range 3 {
		r := h.waitDiscord()
		if gap, want := r.At.Sub(prev.At), testRetryBase<<i; gap < want {
			t.Errorf("retry %d after %v, want at least %v", i+1, gap, want)
		}
		prev = r
	}
}

func TestNetworkErrorRetriesRunOut(t *testing.T) {
	h := newHarness(t, quiConfig)
	h.script(reply{}, reply{}, reply{}, reply{})

	deliverLabel(t, h)

	f := h.waitFailure()
	if f["reason"] != "retries_exhausted" || f["attempts"] != 4.0 || f["status"] != 0.0 || f["error"] == "" {
		t.Errorf("failed delivery line = %v, want retries_exhausted after 4 attempts with status 0 and an error", f)
	}
	if logs := h.logs.String(); strings.Contains(logs, "http://") {
		t.Errorf("logs hold the Sink URL:\n%s", logs)
	}
}

func TestRateLimitedMessageIsSentAgain(t *testing.T) {
	h := newHarness(t, quiConfig)
	h.script(reply{
		status: http.StatusTooManyRequests,
		header: map[string]string{"X-RateLimit-Global": "true", "X-RateLimit-Scope": "global"},
		body:   `{"message": "You are being rate limited.", "retry_after": 0.1, "global": true}`,
	})

	deliverLabel(t, h)

	r1, r2 := h.waitDiscord(), h.waitDiscord()
	if !bytes.Equal(r1.Body, r2.Body) {
		t.Errorf("second body = %s, want %s", r2.Body, r1.Body)
	}
	if gap := r2.At.Sub(r1.At); gap < 100*time.Millisecond {
		t.Errorf("sent again after %v, want at least retry_after 100ms", gap)
	}
}

func TestEmptyRateLimitBucketDelaysNextSend(t *testing.T) {
	h := newHarness(t, quiConfig)
	h.script(reply{
		status: http.StatusNoContent,
		header: map[string]string{"X-RateLimit-Remaining": "0", "X-RateLimit-Reset-After": "0.1"},
	})

	deliverLabel(t, h)
	deliverLabel(t, h)

	r1, r2 := h.waitDiscord(), h.waitDiscord()
	if gap := r2.At.Sub(r1.At); gap < 100*time.Millisecond {
		t.Errorf("next send after %v, want at least X-RateLimit-Reset-After 100ms", gap)
	}
}

func TestFullQueueDropsNewEvent(t *testing.T) {
	h := newHarness(t, quiConfig)
	// The worker waits a minute in the first send, so the queue fills.
	h.script(reply{status: http.StatusTooManyRequests, body: `{"retry_after": 60}`})
	deliverLabel(t, h)
	h.waitDiscord()
	for range queueSize {
		deliverLabel(t, h)
	}

	deliverLabel(t, h)

	f := h.waitFailure()
	if f["reason"] != "queue_full" || f["sink"] != "qui" || f["attempts"] != 0.0 || f["status"] != 0.0 {
		t.Errorf("failed delivery line = %v, want queue_full for sink qui with no attempts", f)
	}
}

func TestAttemptLimitCountsRateLimits(t *testing.T) {
	h := newHarness(t, quiConfig)
	limited := reply{status: http.StatusTooManyRequests, body: `{"message": "You are being rate limited.", "retry_after": 0.001, "global": false}`}
	h.script(limited, limited, limited, limited, limited)

	deliverLabel(t, h)

	f := h.waitFailure()
	if f["reason"] != "retries_exhausted" || f["attempts"] != 5.0 || f["status"] != 429.0 {
		t.Errorf("failed delivery line = %v, want retries_exhausted after 5 attempts with status 429", f)
	}
	if n := len(h.discord); n != 5 {
		t.Errorf("fake Discord got %d requests, want 5", n)
	}
}

// shutdown stops the Sink workers of h as winnow does on SIGTERM, with
// timeout as the drain limit. It returns how long the stop took.
func shutdown(h *harness, timeout time.Duration) time.Duration {
	ctx, cancel := context.WithTimeout(h.t.Context(), timeout)
	defer cancel()
	start := time.Now()
	h.handler.(*Server).Shutdown(ctx)
	return time.Since(start)
}

func TestShutdownSendsQueuedEvents(t *testing.T) {
	h := newHarness(t, quiConfig)
	// The empty bucket keeps the next two Events in the queue for 100ms.
	h.script(reply{
		status: http.StatusNoContent,
		header: map[string]string{"X-RateLimit-Remaining": "0", "X-RateLimit-Reset-After": "0.1"},
	})
	for range 3 {
		deliverLabel(t, h)
	}

	shutdown(h, 2*time.Second)

	if n := len(h.discord); n != 3 {
		t.Errorf("fake Discord got %d requests, want 3", n)
	}
	if f := h.linesWith("delivery failed"); len(f) > 0 {
		t.Errorf("failed delivery lines = %v, want none", f)
	}
}

func TestShutdownLogsUnsentEvents(t *testing.T) {
	tests := []struct {
		name  string
		reply reply
		want  []float64 // the attempts of each shutdown line
	}{
		// The first Event waits a minute for its retry. The second is in
		// the queue.
		{"retry wait", reply{status: http.StatusTooManyRequests, body: `{"retry_after": 60}`}, []float64{1, 0}},
		// The first Event is sent. The second waits a minute for the rate
		// limit bucket.
		{"empty bucket", reply{
			status: http.StatusNoContent,
			header: map[string]string{"X-RateLimit-Remaining": "0", "X-RateLimit-Reset-After": "60"},
		}, []float64{0}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, quiConfig)
			h.script(tt.reply)
			deliverLabel(t, h)
			deliverLabel(t, h)
			h.waitDiscord()

			if took := shutdown(h, 100*time.Millisecond); took > time.Second {
				t.Fatalf("shutdown took %v, want about the 100ms drain limit", took)
			}

			lines := h.linesWith("delivery failed")
			if len(lines) != len(tt.want) {
				t.Fatalf("got %d failed delivery lines, want %d: %v", len(lines), len(tt.want), lines)
			}
			for i, f := range lines {
				if f["reason"] != "shutdown" || f["sink"] != "qui" || f["delivery"] != testDelivery || f["attempts"] != tt.want[i] {
					t.Errorf("failed delivery line %d = %v, want reason shutdown after %v attempts", i, f, tt.want[i])
				}
			}
		})
	}
}

// A handler can still run when the HTTP server stop times out. An Event that
// it routes after the workers stopped gets a shutdown line.
func TestEventAfterShutdownIsLogged(t *testing.T) {
	h := newHarness(t, quiConfig)
	shutdown(h, 100*time.Millisecond)

	deliverLabel(t, h)

	f := h.waitFailure()
	if f["reason"] != "shutdown" || f["sink"] != "qui" || f["delivery"] != testDelivery || f["attempts"] != 0.0 {
		t.Errorf("failed delivery line = %v, want reason shutdown with no attempts", f)
	}
}

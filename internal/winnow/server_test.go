package winnow

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

const quiConfig = `
sources:
  github-autobrr: { secret: test-secret }
sinks:
  qui: { discord: https://discord.example.invalid/api/webhooks/1/token }
routes:
  - name: qui
    match: { repo: autobrr/qui }
    to: [qui]
`

func TestHealthz(t *testing.T) {
	h := newHarness(t, quiConfig)

	rec := h.do(httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
		t.Errorf("GET /healthz = %d %q, want 200 \"ok\"", rec.Code, rec.Body)
	}
}

func TestDeliveryBecomesFallbackMessage(t *testing.T) {
	h := newHarness(t, quiConfig)

	rec := h.do(signedDelivery("github-autobrr", "label", fixture(t, "github/label_created")))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", rec.Code)
	}

	got := h.waitDiscord()
	if got.Sink != "qui" {
		t.Errorf("sink = %q, want qui", got.Sink)
	}
	assertJSON(t, got.Body, `{
		`+githubPoster+`,
		"embeds": [{
			"title": "label.created on autobrr/qui by s0up4200",
			"url": "https://github.example.invalid/autobrr/qui"
		}],
		"allowed_mentions": {"parse": []}
	}`)

	want := map[string]any{
		"level":    "INFO",
		"msg":      "routed",
		"outcome":  "sent",
		"route":    "qui",
		"sinks":    []any{"qui"},
		"source":   "github-autobrr",
		"event":    "label.created",
		"repo":     "autobrr/qui",
		"sender":   "s0up4200",
		"delivery": testDelivery,
	}
	if got := h.decision(); !reflect.DeepEqual(got, want) {
		t.Errorf("decision line = %v\nwant %v", got, want)
	}
}

func TestRequestChecks(t *testing.T) {
	label := fixture(t, "github/label_created")
	tests := []struct {
		name string
		req  func() *http.Request
		want int
	}{
		{"unknown source before the signature check", func() *http.Request {
			req := signedDelivery("nope", "label", label)
			req.Method = http.MethodGet
			req.Header.Del("X-Hub-Signature-256")
			return req
		}, http.StatusNotFound},
		{"method other than POST", func() *http.Request {
			req := signedDelivery("github-autobrr", "label", label)
			req.Method = http.MethodGet
			return req
		}, http.StatusMethodNotAllowed},
		{"form content type", func() *http.Request {
			req := signedDelivery("github-autobrr", "label", label)
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			return req
		}, http.StatusUnsupportedMediaType},
		{"json content type with charset", func() *http.Request {
			req := signedDelivery("github-autobrr", "label", label)
			req.Header.Set("Content-Type", "application/json; charset=utf-8")
			return req
		}, http.StatusAccepted},
		{"missing signature", func() *http.Request {
			req := signedDelivery("github-autobrr", "label", label)
			req.Header.Del("X-Hub-Signature-256")
			return req
		}, http.StatusUnauthorized},
		{"signature with another secret", func() *http.Request {
			req := signedDelivery("github-autobrr", "label", label)
			req.Header.Set("X-Hub-Signature-256", sign("other-secret", label))
			return req
		}, http.StatusUnauthorized},
		{"signature prefix with no digest", func() *http.Request {
			req := signedDelivery("github-autobrr", "label", label)
			req.Header.Set("X-Hub-Signature-256", "sha256=")
			return req
		}, http.StatusUnauthorized},
		{"bad signature on a body that does not parse", func() *http.Request {
			req := signedDelivery("github-autobrr", "label", []byte("{"))
			req.Header.Set("X-Hub-Signature-256", sign("other-secret", []byte("{")))
			return req
		}, http.StatusUnauthorized},
		{"signed body that does not parse", func() *http.Request {
			return signedDelivery("github-autobrr", "label", []byte("{"))
		}, http.StatusBadRequest},
		{"missing event header", func() *http.Request {
			req := signedDelivery("github-autobrr", "label", label)
			req.Header.Del("X-GitHub-Event")
			return req
		}, http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, quiConfig)
			if got := h.do(tt.req()).Code; got != tt.want {
				t.Errorf("status = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestBodyOverLimit(t *testing.T) {
	h := newHarness(t, quiConfig)
	body := make([]byte, 25<<20+1)

	if got := h.do(signedDelivery("github-autobrr", "label", body)).Code; got != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", got)
	}
	lines := h.logLines()
	if len(lines) != 1 || lines[0]["source"] != "github-autobrr" {
		t.Errorf("log lines = %v, want one line for source github-autobrr", lines)
	}
}

func TestPingMakesNoEvent(t *testing.T) {
	h := newHarness(t, `
sources:
  github-autobrr: { secret: test-secret }
sinks:
  all: { discord: https://discord.example.invalid/api/webhooks/1/token }
routes:
  - match: {}
    to: [all]
`)
	if got := h.do(signedDelivery("github-autobrr", "ping", fixture(t, "github/ping"))).Code; got != http.StatusOK {
		t.Fatalf("status = %d, want 200", got)
	}
	if lines := h.logLines(); len(lines) != 0 {
		t.Errorf("log lines = %v, want none", lines)
	}
}

func TestUnmatchedEvent(t *testing.T) {
	h := newHarness(t, quiConfig)
	body := bytes.ReplaceAll(fixture(t, "github/label_created"), []byte("autobrr/qui"), []byte("autobrr/autobrr"))

	if got := h.do(signedDelivery("github-autobrr", "label", body)).Code; got != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", got)
	}
	d := h.decision()
	if d["outcome"] != "unmatched" || d["route"] != "" || !reflect.DeepEqual(d["sinks"], []any{}) {
		t.Errorf("decision line = %v, want outcome unmatched with empty route and sinks", d)
	}
}

func TestFirstMatchingRouteWins(t *testing.T) {
	h := newHarness(t, `
sources:
  github-autobrr: { secret: test-secret }
sinks:
  a: { discord: https://discord.example.invalid/api/webhooks/1/a }
  b: { discord: https://discord.example.invalid/api/webhooks/2/b }
routes:
  - name: other
    match: { repo: autobrr/autobrr }
    to: [b]
  - match: { owner: autobrr }
    to: [a]
  - name: also-matches
    match: {}
    to: [b]
`)
	if got := h.do(signedDelivery("github-autobrr", "label", fixture(t, "github/label_created"))).Code; got != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", got)
	}
	if got := h.waitDiscord().Sink; got != "a" {
		t.Errorf("sink = %q, want a", got)
	}
	d := h.decision()
	if d["route"] != "#2" || !reflect.DeepEqual(d["sinks"], []any{"a"}) {
		t.Errorf("decision line = %v, want route #2 to sink a", d)
	}
}

func TestRouteSendsToEachSink(t *testing.T) {
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
	if got := h.do(signedDelivery("github-autobrr", "label", fixture(t, "github/label_created"))).Code; got != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", got)
	}
	got := map[string]bool{h.waitDiscord().Sink: true, h.waitDiscord().Sink: true}
	if !got["a"] || !got["b"] {
		t.Errorf("sinks = %v, want a and b", got)
	}
}

func TestRuleFields(t *testing.T) {
	label := fixture(t, "github/label_created")
	botComment := fixture(t, "github/issue_comment_bot")
	tests := []struct {
		name  string
		match string
		event string
		body  []byte
		want  int
	}{
		{"source", "{source: github-autobrr}", "label", label, http.StatusAccepted},
		{"forge", "{forge: github}", "label", label, http.StatusAccepted},
		{"event and action", "{event: label, action: created}", "label", label, http.StatusAccepted},
		{"other action", "{event: label, action: deleted}", "label", label, http.StatusNoContent},
		{"sender", "{sender: s0up4200}", "label", label, http.StatusAccepted},
		{"human sender is not a bot", "{sender_bot: true}", "label", label, http.StatusNoContent},
		{"Bot type is a bot", "{sender_bot: true}", "issue_comment", botComment, http.StatusAccepted},
		{"login with [bot] is a bot", "{sender_bot: true}", "issue_comment",
			bytes.ReplaceAll(botComment, []byte(`"type": "Bot"`), []byte(`"type": "User"`)), http.StatusAccepted},
		{"comment on a pull request", "{is_pull: true}", "issue_comment", botComment, http.StatusAccepted},
		{"merged pull request", "{merged: true, draft: false}", "pull_request", fixture(t, "github/pull_request_merged"), http.StatusAccepted},
		{"field that the Event does not have", "{merged: false}", "label", label, http.StatusNoContent},
		{"list means any of", "{action: [deleted, created]}", "label", label, http.StatusAccepted},
		{"list with no match", "{action: [deleted, edited]}", "label", label, http.StatusNoContent},
		{"all fields must hold", "{action: created, sender: someone}", "label", label, http.StatusNoContent},
		{"list of matchers means any of", "[{sender: someone}, {action: created}]", "label", label, http.StatusAccepted},
		{"list of matchers with no match", "[{sender: someone}, {action: deleted}]", "label", label, http.StatusNoContent},
		{"not with one matcher", "{not: {sender: s0up4200}}", "label", label, http.StatusNoContent},
		{"not with a list", "{not: [{sender: someone}, {action: created}]}", "label", label, http.StatusNoContent},
		{"not with no match", "{not: [{sender: someone}, {action: deleted}]}", "label", label, http.StatusAccepted},
		{"star is a wildcard", "{repo: autobrr/*, sender: s0up*}", "label", label, http.StatusAccepted},
		{"question mark is literal", `{sender: "s0up420?"}`, "label", label, http.StatusNoContent},
		{"repo matches in lowercase", "{repo: AutoBrr/QUI, owner: AUTOBRR}", "label", label, http.StatusAccepted},
		{"event matches as it is", "{event: LABEL}", "label", label, http.StatusNoContent},
		{"missing field never matches", "{merged: true}", "label", label, http.StatusNoContent},
		{"not on a missing field passes", "{not: {draft: true}}", "label", label, http.StatusAccepted},
		// An optional Rule field belongs to one Event name, also when another
		// payload has the same key.
		{"ref only on push", `{ref: "*"}`, "create",
			bytes.Replace(label, []byte(`"action"`), []byte(`"ref": "main", "action"`), 1), http.StatusNoContent},
		{"draft only on pull_request", "{draft: false}", "pull_request_review", fixture(t, "github/pull_request_review_submitted"), http.StatusNoContent},
		{"bot author", "{author_bot: true}", "pull_request",
			bytes.Replace(fixture(t, "github/pull_request_merged"), []byte(`"login": "s0up4200"`), []byte(`"login": "renovate[bot]"`), 1), http.StatusAccepted},
		{"human author", "{author_bot: true}", "pull_request", fixture(t, "github/pull_request_merged"), http.StatusNoContent},
		{"author_bot only with an author", "{author_bot: false}", "label", label, http.StatusNoContent},
		{"is_pull only on issue_comment", "{is_pull: false}", "issues", fixture(t, "github/issues_opened"), http.StatusNoContent},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, `
sources:
  github-autobrr: { secret: test-secret }
sinks:
  all: { discord: https://discord.example.invalid/api/webhooks/1/token }
routes:
  - match: `+tt.match+`
    to: [all]
`)
			if got := h.do(signedDelivery("github-autobrr", tt.event, tt.body)).Code; got != tt.want {
				t.Errorf("status = %d, want %d", got, tt.want)
			}
		})
	}
}

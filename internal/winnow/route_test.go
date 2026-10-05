package winnow

import (
	"bytes"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

// botCommentConfig holds the bot-comment Route from the sample configuration,
// above a catch-all Route. A login with [bot] has quotes inside a YAML flow
// collection, because [ and ] are flow indicators there.
const botCommentConfig = `
sources:
  github-autobrr: { secret: test-secret }
sinks:
  all: { discord: https://discord.example.invalid/api/webhooks/1/token }
routes:
  - name: bot-comments
    drop: true
    match:
      event: [issue_comment, pull_request_review, pull_request_review_comment]
      sender_bot: true
      not:
        - sender: ["renovate[bot]", renovate]
        - { sender: "dependabot[bot]", repo: autobrr/qui }
  - name: rest
    match: {}
    to: [all]
`

func TestBotCommentRoute(t *testing.T) {
	comment := fixture(t, "github/issue_comment_bot") // renovate[bot] on autobrr/qui
	from := func(login, repo string) []byte {
		b := bytes.ReplaceAll(comment, []byte("renovate[bot]"), []byte(login))
		return bytes.ReplaceAll(b, []byte("autobrr/qui"), []byte(repo))
	}
	tests := []struct {
		name  string
		body  []byte
		route string
		want  int
	}{
		{"renovate is an exception", comment, "rest", http.StatusAccepted},
		{"sender matches in lowercase", from("Renovate[bot]", "autobrr/qui"), "rest", http.StatusAccepted},
		{"other bot is dropped", from("github-actions[bot]", "autobrr/qui"), "bot-comments", http.StatusNoContent},
		{"[bot] is not a character class", from("renovatet", "autobrr/qui"), "bot-comments", http.StatusNoContent},
		{"dependabot on autobrr/qui is an exception", from("dependabot[bot]", "autobrr/qui"), "rest", http.StatusAccepted},
		{"dependabot on another repo is dropped", from("dependabot[bot]", "autobrr/autobrr"), "bot-comments", http.StatusNoContent},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, botCommentConfig)
			if got := h.do(signedDelivery("github-autobrr", "issue_comment", tt.body)).Code; got != tt.want {
				t.Fatalf("status = %d, want %d", got, tt.want)
			}
			if got := h.decision()["route"]; got != tt.route {
				t.Errorf("route = %v, want %s", got, tt.route)
			}
		})
	}
}

func TestDropRoute(t *testing.T) {
	h := newHarness(t, `
sources:
  github-autobrr: { secret: test-secret }
routes:
  - drop: true
    match: {}
`)
	if got := h.do(signedDelivery("github-autobrr", "label", fixture(t, "github/label_created"))).Code; got != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", got)
	}
	d := h.decision()
	if d["outcome"] != "dropped" || d["route"] != "#1" || !reflect.DeepEqual(d["sinks"], []any{}) {
		t.Errorf("decision line = %v, want outcome dropped, route #1, no sinks", d)
	}
}

func TestNotInsideNotStopsLoad(t *testing.T) {
	_, errs, _ := Load([]byte(`
sinks:
  a: { discord: https://discord.example.invalid/1 }
routes:
  - name: nested
    match: { not: { not: { sender: x } } }
    to: [a]
`))
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "route nested") || !strings.Contains(errs[0].Error(), "not") {
		t.Errorf("errors = %v, want one error for route nested about not", errs)
	}
}

func TestUnknownKeyInsideNot(t *testing.T) {
	_, errs, _ := Load([]byte(`
sinks:
  a: { discord: https://discord.example.invalid/1 }
routes:
  - match: [{ not: [{ sendr: x }] }]
    to: [a]
`))
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "sendr") {
		t.Errorf("errors = %v, want one error about sendr", errs)
	}
}

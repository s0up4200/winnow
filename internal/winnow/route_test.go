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
	if d["level"] != "DEBUG" || d["outcome"] != "dropped" || d["route"] != "#1" || !reflect.DeepEqual(d["sinks"], []any{}) {
		t.Errorf("decision line = %v, want DEBUG outcome dropped, route #1, no sinks", d)
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

func TestLoadChecksEachMatcher(t *testing.T) {
	_, errs, warns := Load([]byte(`
sinks:
  a: { discord: https://discord.example.invalid/1 }
routes:
  - name: typos
    match:
      - { event: [push, pull_requests] }
      - { event: "workflow_*", not: { event: isues } }
    to: [a]
  - name: not-only
    match: { not: { sender: x } }
    to: [a]
  - name: all
    match: [{ sender: x }, {}]
    to: [a]
  - name: late
    match: {}
    to: [a]
`))
	wantErrs := []string{"route late: route all before it matches every Event"}
	wantWarns := []string{
		`route typos: event "pull_requests" is not a known Event name`,
		`route typos: event "isues" is not a known Event name`,
	}
	var gotErrs []string
	for _, err := range errs {
		gotErrs = append(gotErrs, err.Error())
	}
	if !reflect.DeepEqual(gotErrs, wantErrs) || !reflect.DeepEqual(warns, wantWarns) {
		t.Errorf("errors = %q, warnings = %q\nwant errors %q, warnings %q", gotErrs, warns, wantErrs, wantWarns)
	}
}

func TestPrivateRuleField(t *testing.T) {
	private := func(b []byte) []byte {
		return bytes.Replace(b, []byte(`"private": false`), []byte(`"private": true`), 1)
	}
	star := fixture(t, "github/star_created") // a public repository
	issue := fixture(t, "forgejo/issues-opened")
	github := func(b []byte) *http.Request { return signedDelivery("github-autobrr", "star", b) }
	forgejo := func(b []byte) *http.Request { return forgejoDelivery("forgejo", "issues", "issues", b) }
	// winnow answers a ping before the Routes, so the ping body goes out as
	// an org-level Event. It has no repository.
	org := func(b []byte) *http.Request { return signedDelivery("github-autobrr", "organization", b) }
	ping := fixture(t, "github/ping")
	tests := []struct {
		name  string
		match string
		send  func([]byte) *http.Request
		body  []byte
		want  int
	}{
		{"public github matches private false", "false", github, star, http.StatusAccepted},
		{"private github fails private false", "false", github, private(star), http.StatusNoContent},
		{"private github matches private true", "true", github, private(star), http.StatusAccepted},
		{"public github fails private true", "true", github, star, http.StatusNoContent},
		{"public forgejo matches private false", "false", forgejo, issue, http.StatusAccepted},
		{"private forgejo fails private false", "false", forgejo, private(issue), http.StatusNoContent},
		{"private forgejo matches private true", "true", forgejo, private(issue), http.StatusAccepted},
		{"public forgejo fails private true", "true", forgejo, issue, http.StatusNoContent},
		{"no repository fails private false", "false", org, ping, http.StatusNoContent},
		{"no repository fails private true", "true", org, ping, http.StatusNoContent},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, `
sources:
  forgejo: { secret: test-secret }
  github-autobrr: { secret: test-secret }
sinks:
  all: { discord: https://discord.example.invalid/api/webhooks/1/token }
routes:
  - match: { private: `+tt.match+` }
    to: [all]
`)
			if got := h.do(tt.send(tt.body)).Code; got != tt.want {
				t.Errorf("status = %d, want %d", got, tt.want)
			}
		})
	}
}

// winnow clears the sender of a Private sponsor, and a sender: Rule must not
// match the cleared sender, also not with "*".
func TestSenderRuleSkipsPrivateSponsor(t *testing.T) {
	tests := []struct {
		name    string
		fixture string
		want    int
	}{
		{"public sponsor matches", "github/sponsorship_created", http.StatusAccepted},
		{"private sponsor does not match", "github/sponsorship_pending_tier_change_private", http.StatusNoContent},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, `
sources:
  github-autobrr: { secret: test-secret }
sinks:
  all: { discord: https://discord.example.invalid/api/webhooks/1/token }
routes:
  - match: { sender: "*" }
    to: [all]
`)
			if got := h.do(signedDelivery("github-autobrr", "sponsorship", fixture(t, tt.fixture))).Code; got != tt.want {
				t.Errorf("status = %d, want %d", got, tt.want)
			}
		})
	}
}

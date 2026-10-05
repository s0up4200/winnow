package winnow

import (
	"bytes"
	"encoding/json/v2"
	"net/http"
	"testing"
)

// mentionConfig sends every Event to two Sinks: ping has mentions: true,
// quiet has no mentions key. The User map key OctoCat is in other case than
// the login octocat in the fixtures, because a forge login is not
// case-sensitive.
const mentionConfig = `
sources:
  github-autobrr: { secret: test-secret }
users:
  OctoCat: "111"
  Codertocat: "222"
sinks:
  ping: { discord: https://discord.example.invalid/api/webhooks/1/token, mentions: true }
  quiet: { discord: https://discord.example.invalid/api/webhooks/2/token }
routes:
  - match: {}
    to: [ping, quiet]
`

func TestMentions(t *testing.T) {
	reviewRequested := func(t *testing.T) []byte { return fixture(t, "github/pull_request_review_requested") }
	// assigned changes the review request into an assignment of login.
	assigned := func(login string) func(*testing.T) []byte {
		return func(t *testing.T) []byte {
			b := bytes.Replace(reviewRequested(t), []byte(`"review_requested"`), []byte(`"assigned"`), 1)
			b = bytes.Replace(b, []byte(`"requested_reviewer"`), []byte(`"assignee"`), 1)
			return bytes.Replace(b, []byte(`"login": "octocat"`), []byte(`"login": "`+login+`"`), 1)
		}
	}
	tests := []struct {
		name  string
		event string
		body  func(*testing.T) []byte
		ping  bool // the ping Sink gets <@111>
	}{
		{"review request pings the reviewer", "pull_request", reviewRequested, true},
		{"assignment pings the assignee in any case", "pull_request", assigned("OCTOCAT"), true},
		{"no ping when the sender assigns itself", "pull_request", assigned("codertocat"), false},
		{"no ping for a user that is not in users", "pull_request", func(t *testing.T) []byte {
			return bytes.Replace(reviewRequested(t), []byte(`"login": "octocat"`), []byte(`"login": "stranger"`), 1)
		}, false},
		{"no ping for another action", "pull_request", func(t *testing.T) []byte {
			return bytes.Replace(reviewRequested(t), []byte(`"review_requested"`), []byte(`"edited"`), 1)
		}, false},
		{"a Fallback message never pings", "pull_request_review_thread", reviewRequested, false},
		// The payload has no alert, so the Renderer gives the Fallback message.
		{"a security Event with no alert never pings", "code_scanning_alert", assigned("octocat"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, mentionConfig)
			if got := h.do(signedDelivery("github-autobrr", tt.event, tt.body(t))).Code; got != http.StatusAccepted {
				t.Fatalf("status = %d, want 202", got)
			}
			got := map[string][]byte{}
			for range 2 {
				r := h.waitDiscord()
				got[r.Sink] = r.Body
			}
			want := `{"allowed_mentions": {"parse": []}}`
			if tt.ping {
				want = `{"content": "<@111>", "allowed_mentions": {"parse": [], "users": ["111"]}}`
			}
			assertMentions(t, got["ping"], want)
			assertMentions(t, got["quiet"], `{"allowed_mentions": {"parse": []}}`)
		})
	}
}

// assertMentions compares the content and allowed_mentions of the message
// body with want. It ignores the embeds.
func assertMentions(t *testing.T, body []byte, want string) {
	t.Helper()
	var msg map[string]any
	if err := json.Unmarshal(body, &msg); err != nil {
		t.Fatal(err)
	}
	delete(msg, "embeds")
	b, _ := json.Marshal(msg)
	assertJSON(t, b, want)
}

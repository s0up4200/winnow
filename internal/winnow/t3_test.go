package winnow

import (
	"encoding/json/v2"
	"net/http"
	"strings"
	"testing"
	"time"
)

// t3Config sends each Event to one Sink, and has a T3 hook for octocat with
// one Trusted sender, codertocat, on the repository of the GitHub fixtures.
// The configuration writes the logins in other case than the fixtures,
// because a forge login is not case-sensitive.
const t3Config = `
sources:
  github-autobrr: { secret: test-secret }
users:
  octocat: "111"
teams:
  codertocat/devs: [octocat]
sinks:
  ping: { discord: https://discord.example.invalid/api/webhooks/1/token, mentions: true }
routes:
  - match: {}
    to: [ping]
t3:
  for: OctoCat
  senders: [CoderTocat]
  secret: t3-secret
  repos:
    codertocat/hello-world: https://t3.example.invalid/hooks/hello
`

// withText keeps the GitHub fixture name and sets the text of its most
// specific object to body.
func withText(t *testing.T, name, body string) []byte {
	t.Helper()
	var p map[string]any
	if err := json.Unmarshal(fixture(t, "github/"+name), &p); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"comment", "review", "issue", "pull_request", "discussion"} {
		if obj, ok := p[key].(map[string]any); ok {
			obj["body"] = body
			if key == "comment" || key == "review" {
				break
			}
		}
	}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// edit changes the fixture body with set, which gets the parsed payload.
func edit(t *testing.T, body []byte, set func(p map[string]any)) []byte {
	t.Helper()
	var p map[string]any
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatal(err)
	}
	set(p)
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// t3Body is the request body for a Ping of octocat from Codertocat on
// Codertocat/Hello-World.
func t3Body(number int, kind, url, issueURL string, backfill bool) string {
	b, err := json.Marshal(map[string]any{
		"forge": "github", "repo": "Codertocat/Hello-World", "number": number, "kind": kind,
		"url": url, "issue_url": issueURL, "sender": "Codertocat", "for": "OctoCat",
		"delivery": testDelivery, "backfill": backfill,
	})
	if err != nil {
		panic(err)
	}
	return string(b)
}

func TestT3HookSendsPings(t *testing.T) {
	const site = "https://example.invalid/Codertocat/Hello-World/"
	tests := []struct {
		name, event string
		body        func(t *testing.T) []byte
		want        string
	}{
		{"comment mention", "issue_comment", func(t *testing.T) []byte {
			return withText(t, "issue_comment_created", "@octocat does this break the parser?")
		}, t3Body(1, "comments", site+"issues/1#issuecomment-492700400", site+"issues/1", false)},
		{"comment on a pull request", "issue_comment", func(t *testing.T) []byte {
			return withText(t, "issue_comment_created_on_pull", "@octocat ok?")
		}, t3Body(1, "comments", site+"pull/1#issuecomment-492700400", site+"pull/1", false)},
		{"review comment", "pull_request_review_comment", func(t *testing.T) []byte {
			return withText(t, "pull_request_review_comment_created", "@octocat ok?")
		}, t3Body(2, "comments", site+"pull/2#discussion_r284312630", site+"pull/2", false)},
		{"discussion comment", "discussion_comment", func(t *testing.T) []byte {
			return withText(t, "discussion_comment_created", "@octocat ok?")
		}, t3Body(4, "comments", site+"discussions/4#discussioncomment-550062", site+"discussions/4", false)},
		{"assignment", "issues", func(t *testing.T) []byte {
			return fixture(t, "github/issues_assigned")
		}, t3Body(1, "assigned", site+"issues/1", site+"issues/1", false)},
		{"review summary", "pull_request_review", func(t *testing.T) []byte {
			return withText(t, "pull_request_review_submitted", "@octocat is this approach OK?")
		}, t3Body(2, "comments", site+"pull/2#pullrequestreview-237895671", site+"pull/2", false)},
		{"new issue description", "issues", func(t *testing.T) []byte {
			return withText(t, "issues_opened", "cc @octocat")
		}, t3Body(1, "comments", site+"issues/1", site+"issues/1", false)},
		{"new pull request description", "pull_request", func(t *testing.T) []byte {
			return withText(t, "pull_request_opened", "cc @octocat")
		}, t3Body(2, "comments", site+"pull/2", site+"pull/2", false)},
		{"new discussion description", "discussion", func(t *testing.T) []byte {
			return withText(t, "discussion_created", "cc @octocat")
		}, t3Body(4, "comments", site+"discussions/4", site+"discussions/4", false)},
		{"team mention", "issue_comment", func(t *testing.T) []byte {
			return withText(t, "issue_comment_created", "cc @Codertocat/devs")
		}, t3Body(1, "comments", site+"issues/1#issuecomment-492700400", site+"issues/1", false)},
		{"repeated mention sends once", "issue_comment", func(t *testing.T) []byte {
			return withText(t, "issue_comment_created", "@octocat @OCTOCAT @codertocat/devs")
		}, t3Body(1, "comments", site+"issues/1#issuecomment-492700400", site+"issues/1", false)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, t3Config)
			if got := h.do(signedDelivery("github-autobrr", tt.event, tt.body(t))).Code; got >= 300 {
				t.Fatalf("status = %d", got)
			}
			got := h.t3Requests()
			if len(got) != 1 {
				t.Fatalf("got %d T3 requests, want 1", len(got))
			}
			r := got[0]
			assertJSON(t, r.Body, tt.want)
			if r.Path != "/hooks/"+testT3Token+"/codertocat/hello-world" {
				t.Errorf("path = %q", r.Path)
			}
			if r.Type != "application/json" {
				t.Errorf("Content-Type = %q", r.Type)
			}
			if want := sign("t3-secret", r.Body); r.Signature != want {
				t.Errorf("signature = %q, want %q", r.Signature, want)
			}
			if lines := h.linesWith("t3 hook sent"); len(lines) != 1 || lines[0]["status"] != float64(http.StatusAccepted) {
				t.Errorf("sent lines = %v, want one with status 202", lines)
			}
		})
	}
}

func TestT3HookSendsNothing(t *testing.T) {
	comment := func(text string) func(t *testing.T) []byte {
		return func(t *testing.T) []byte { return withText(t, "issue_comment_created", text) }
	}
	sender := func(login, typ string) func(t *testing.T) []byte {
		return func(t *testing.T) []byte {
			return edit(t, withText(t, "issue_comment_created", "@octocat ok?"), func(p map[string]any) {
				s := p["sender"].(map[string]any)
				s["login"], s["type"] = login, typ
			})
		}
	}
	tests := []struct {
		name, config, event string
		body                func(t *testing.T) []byte
	}{
		{"sender that is not trusted", strings.Replace(t3Config, "[CoderTocat]", "[someone]", 1), "issue_comment", comment("@octocat ok?")},
		{"Bot sender", strings.Replace(t3Config, "[CoderTocat]", "[CoderTocat, \"helper[bot]\"]", 1), "issue_comment", sender("helper[bot]", "Bot")},
		{"Bot sender from the bot list", strings.Replace(t3Config, "users:", "bots: [codertocat]\nusers:", 1), "issue_comment", comment("@octocat ok?")},
		{"the sender is for", strings.Replace(t3Config, "[CoderTocat]", "[CoderTocat, octocat]", 1), "issue_comment", sender("octocat", "User")},
		{"review request", t3Config, "pull_request", func(t *testing.T) []byte { return fixture(t, "github/pull_request_review_requested") }},
		{"edited comment", t3Config, "issue_comment", func(t *testing.T) []byte {
			return edit(t, withText(t, "issue_comment_created", "@octocat ok?"), func(p map[string]any) { p["action"] = "edited" })
		}},
		{"edited description", t3Config, "issues", func(t *testing.T) []byte {
			return edit(t, withText(t, "issues_opened", "@octocat ok?"), func(p map[string]any) { p["action"] = "edited" })
		}},
		{"repository that is not listed", strings.Replace(t3Config, "codertocat/hello-world:", "codertocat/other:", 1), "issue_comment", comment("@octocat ok?")},
		{"mention in code", t3Config, "issue_comment", comment("`@octocat`\n\n    @octocat")},
		{"mention in a quote", t3Config, "issue_comment", comment("> @octocat ok?")},
		{"mention of another login", t3Config, "issue_comment", comment("@octocatx @someone")},
		{"assignment of another login", t3Config, "issues", func(t *testing.T) []byte {
			return edit(t, fixture(t, "github/issues_assigned"), func(p map[string]any) { p["assignee"].(map[string]any)["login"] = "someone" })
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, tt.config)
			if got := h.do(signedDelivery("github-autobrr", tt.event, tt.body(t))).Code; got >= 300 {
				t.Fatalf("status = %d", got)
			}
			if got := h.t3Requests(); len(got) != 0 {
				t.Errorf("got T3 requests %v, want none", got)
			}
		})
	}
}

// A Route that drops the Event does not stop the T3 hook, and the hook
// works with no Sinks.
func TestT3HookIgnoresRoutes(t *testing.T) {
	h := newHarness(t, `
sources:
  github-autobrr: { secret: test-secret }
routes:
  - match: { repo: "Codertocat/*" }
    drop: true
t3:
  for: octocat
  senders: [codertocat]
  secret: t3-secret
  repos: { Codertocat/Hello-World: https://t3.example.invalid/hooks/hello }
`)
	if got := h.do(signedDelivery("github-autobrr", "issue_comment", withText(t, "issue_comment_created", "@octocat ok?"))).Code; got != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", got)
	}
	if got := h.t3Requests(); len(got) != 1 {
		t.Errorf("got %d T3 requests, want 1", len(got))
	}
}

// t3SweepConfig adds a Sweep to t3Config.
var t3SweepConfig = strings.Replace(t3Config, "github-autobrr: { secret: test-secret }",
	"github-autobrr:\n    secret: test-secret\n    sweep: { token: test-token, org: autobrr, hook: 42 }", 1)

// A Backfill sends when the forge sent it less than 24 hours ago.
func TestT3HookBackfill(t *testing.T) {
	h := newHarness(t, t3SweepConfig)
	h.step(day(10, 1, 11, 0))
	fresh := h.missed("issues", "issues_assigned", day(10, 1, 12, 0), 502)
	h.missed("issues", "issues_assigned", day(9, 30, 12, 30), 502) // 24 hours before the Sweep
	h.step(day(10, 1, 12, 30))
	got := h.t3Requests()
	if len(got) != 1 {
		t.Fatalf("got %d T3 requests, want only the fresh Backfill", len(got))
	}
	const issue = "https://example.invalid/Codertocat/Hello-World/issues/1"
	want := strings.Replace(t3Body(1, "assigned", issue, issue, true), testDelivery, fresh, 1)
	assertJSON(t, got[0].Body, want)
}

func TestT3HookRetries(t *testing.T) {
	h := newHarness(t, t3Config)
	h.scriptT3(reply{status: http.StatusServiceUnavailable}, reply{status: 0}, reply{status: http.StatusTooManyRequests})
	h.do(signedDelivery("github-autobrr", "issue_comment", withText(t, "issue_comment_created", "@octocat ok?")))
	for range 4 {
		h.waitT3()
	}
	got := h.t3Requests()
	if len(got) != 0 {
		t.Errorf("got %d more T3 requests after the success", len(got))
	}
	if lines := h.linesWith("t3 hook sent"); len(lines) != 1 || lines[0]["attempts"] != float64(4) {
		t.Errorf("sent lines = %v, want one after 4 attempts", lines)
	}
	if lines := h.linesWith("t3 hook failed"); len(lines) != 0 {
		t.Errorf("failed lines = %v, want none", lines)
	}
}

// waitT3Failure waits for the failed delivery line of the T3 hook. It fails
// the test if more than one comes.
func (h *harness) waitT3Failure() map[string]any {
	h.t.Helper()
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		switch found := h.linesWith("t3 hook failed"); len(found) {
		case 0:
		case 1:
			return found[0]
		default:
			h.t.Fatalf("got %d failed lines, want 1: %v", len(found), found)
		}
	}
	h.t.Fatal("no failed line of the T3 hook")
	return nil
}

func TestT3HookFailures(t *testing.T) {
	tests := []struct {
		name    string
		replies []reply
		reason  string
		status  float64
		single  bool // T3 Code gets one request only
	}{
		{"server error retries then fails", repeat(reply{status: http.StatusBadGateway}, 100), "retries_exhausted", 502, false},
		{"network error retries then fails", repeat(reply{status: 0}, 100), "retries_exhausted", 0, false},
		{"wrong signature fails at once", []reply{{status: http.StatusUnauthorized}}, "rejected", 401, true},
		{"unknown hook fails at once", []reply{{status: http.StatusNotFound}}, "rejected", 404, true},
		{"redirect with a Location that does not parse", repeat(reply{status: http.StatusFound, header: map[string]string{"Location": "/hooks/" + testT3Token + "/%zz"}}, 100), "retries_exhausted", 0, false},
		{"redirect to a sign-in page fails at once", []reply{{status: http.StatusFound, header: map[string]string{"Location": "/login"}}}, "rejected", 302, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, t3Config)
			h.scriptT3(tt.replies...)
			h.do(signedDelivery("github-autobrr", "issue_comment", withText(t, "issue_comment_created", "@octocat ok?")))
			line := h.waitT3Failure()
			want := map[string]any{"level": "ERROR", "msg": "t3 hook failed", "reason": tt.reason, "repo": "Codertocat/Hello-World",
				"number": float64(1), "kind": "comments", "sender": "Codertocat", "delivery": testDelivery, "status": tt.status, "backfill": false}
			for k, v := range want {
				if line[k] != v {
					t.Errorf("failed line %s = %v, want %v", k, line[k], v)
				}
			}
			if n := len(h.t3); tt.single && n != 1 || !tt.single && n < 2 {
				t.Errorf("T3 Code got %d requests", n)
			}
			if strings.Contains(h.logs.String(), testT3Token) || strings.Contains(h.logs.String(), h.t3Fake.URL) {
				t.Errorf("a log line shows the T3 Code URL:\n%s", h.logs)
			}
			series := map[string]float64{
				"outcome=sent,reason=":                    0,
				"outcome=failed,reason=queue_full":        0,
				"outcome=failed,reason=rejected":          0,
				"outcome=failed,reason=retries_exhausted": 0,
				"outcome=failed,reason=shutdown":          0,
			}
			series["outcome=failed,reason="+tt.reason] = 1
			h.assertSeries("winnow_t3_hook_requests_total", series)
		})
	}
}

// repeat returns n copies of r.
func repeat(r reply, n int) []reply {
	rs := make([]reply, n)
	for i := range rs {
		rs[i] = r
	}
	return rs
}

// The shutdown sends the requests that are still in the queue.
func TestT3HookShutdownDrains(t *testing.T) {
	h := newHarness(t, t3Config)
	h.scriptT3(reply{status: http.StatusServiceUnavailable})
	for range 3 {
		h.do(signedDelivery("github-autobrr", "issue_comment", withText(t, "issue_comment_created", "@octocat ok?")))
	}
	h.srv.Shutdown(t.Context())
	if n := len(h.t3); n != 4 {
		t.Errorf("T3 Code got %d requests, want 4", n)
	}
	if lines := h.linesWith("t3 hook sent"); len(lines) != 3 {
		t.Errorf("sent lines = %d, want 3", len(lines))
	}
}

func TestT3HookMetricSent(t *testing.T) {
	h := newHarness(t, t3Config)
	h.do(signedDelivery("github-autobrr", "issue_comment", withText(t, "issue_comment_created", "@octocat ok?")))
	h.waitT3()
	for deadline := time.Now().Add(2 * time.Second); len(h.linesWith("t3 hook sent")) == 0 && time.Now().Before(deadline); time.Sleep(time.Millisecond) {
	}
	if got := h.series("winnow_t3_hook_requests_total")["outcome=sent,reason="]; got != 1 {
		t.Errorf("sent = %v, want 1", got)
	}
}

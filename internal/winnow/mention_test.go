package winnow

import (
	"bytes"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"slices"
	"strings"
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
// body with want. It ignores the poster and the embeds.
func assertMentions(t *testing.T, body []byte, want string) {
	t.Helper()
	var msg map[string]any
	if err := json.Unmarshal(body, &msg); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"username", "avatar_url", "embeds"} {
		delete(msg, k)
	}
	b, _ := json.Marshal(msg)
	assertJSON(t, b, want)
}

// commentPayload keeps the forge fixture and changes only its comment text.
func commentPayload(t *testing.T, name, body string) []byte {
	t.Helper()
	var p map[string]any
	if err := json.Unmarshal(fixture(t, name), &p); err != nil {
		t.Fatal(err)
	}
	p["comment"].(map[string]any)["body"] = body
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestCommentMentions(t *testing.T) {
	h := newHarness(t, strings.Replace(mentionConfig, "  Codertocat:", `  alias: "111"
  other: "333"
  "999": "444"
  Codertocat:`, 1))
	body := "Before I build, @OCTOCAT @alias @other @octocat @stranger @CODERTOCAT <@999> <@&888> @everyone"
	if got := h.do(signedDelivery("github-autobrr", "issue_comment", commentPayload(t, "github/issue_comment_created", body))).Code; got != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", got)
	}
	for range 2 {
		r := h.waitDiscord()
		want := `{"allowed_mentions": {"parse": []}}`
		if r.Sink == "ping" {
			want = `{"content": "<@111> <@333>", "allowed_mentions": {"parse": [], "users": ["111", "333"]}}`
		}
		assertMentions(t, r.Body, want)
		var msg message
		if err := json.Unmarshal(r.Body, &msg); err != nil {
			t.Fatal(err)
		}
		if msg.Embeds[0].Description != body {
			t.Errorf("comment = %q, want %q", msg.Embeds[0].Description, body)
		}
	}
}

func TestCommentMentionText(t *testing.T) {
	tests := []struct {
		name, body string
	}{
		{"long hanging paragraph", "Paragraph\n" + strings.Repeat("    more\n", 8000) + "    @other"},
		{"long indented code", strings.Repeat("    @octocat\n", 8000) + "\n@other"},
		{"dense mentions", strings.Repeat("@unmapped ", 20000) + "@other"},
		{"indented code", "    @octocat\n\n@other"},
		{"blockquote", "> @octocat\ncontinued @octocat\n\n@other"},
		{"many literal brackets", strings.Repeat("](text ", 8000) + "@other"},
		{"link after literal brackets", strings.Repeat("]( ", 8000) + "[link](/@octocat) @other"},
		{"ordinary list mention", "- @other"},
		{"inline code", "`@octocat` @other"},
		{"backtick fence", "```go\n@octocat\n```\n@other"},
		{"unclosed fence", "@other\n```\n@octocat"},
		{"bare URLs", "https://example.invalid/@octocat ftp://example.invalid/@octocat @other"},
		{"link destination", "[link](/@octocat) [@other](https://example.invalid)"},
		{"HTML attribute", "<span title=\"@octocat\">text</span> @other"},
		{"HTML visible mention", "<span title=\"@octocat\">@other</span>"},
		{"HTML tag in code", "`<span title=\"` ask @other"},
		{"HTML declaration", "<!DOCTYPE @octocat>\n\n@other"},
		{"processing instruction", "<?php @octocat ?>\n\n@other"},
		{"CDATA", "<![CDATA[ @octocat ]]>\n\n@other"},
		{"image alt text", "![@octocat](https://example.invalid/image.png) @other"},
		{"footnote label", "Ask later[^@octocat] @other\n\n[^@octocat]: details"},
		{"hidden comments", "<!-- @octocat\n@octocat -->\n\n@other"},
		{"unclosed hidden comment", "@other\n\n<!-- @octocat"},
		{"escaped mention", "\\@octocat @other"},
		{"login boundaries", "@octocat-long @octocat_extra @octocat.more foo@octocat foo.bar@octocat @octocat/team @@octocat @other"},
		{"self mention", "@CODERTOCAT @other"},
		{"HTML block", "<details>\n@octocat\n</details>\n\n@other"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := strings.Replace(mentionConfig, "  Codertocat:", "  other: \"333\"\n  Codertocat:", 1)
			h := newHarness(t, cfg)
			if got := h.do(signedDelivery("github-autobrr", "issue_comment", commentPayload(t, "github/issue_comment_created", tt.body))).Code; got != http.StatusAccepted {
				t.Fatalf("status = %d, want 202", got)
			}
			for range 2 {
				r := h.waitDiscord()
				want := `{"allowed_mentions": {"parse": []}}`
				if r.Sink == "ping" {
					want = `{"content": "<@333>", "allowed_mentions": {"parse": [], "users": ["333"]}}`
				}
				assertMentions(t, r.Body, want)
			}
		})
	}
}

func TestCommentMentionEvents(t *testing.T) {
	tests := []struct {
		fixture, event, typ, action string
		ping                        bool
	}{
		{"github/issue_comment_created", "issue_comment", "", "created", true},
		{"github/issue_comment_created_on_pull", "issue_comment", "", "created", true},
		{"github/pull_request_review_comment_created", "pull_request_review_comment", "", "created", true},
		{"github/discussion_comment_created", "discussion_comment", "", "created", true},
		{"forgejo/issue_comment-created", "issue_comment", "issue_comment", "created", true},
		{"forgejo/pull_request_comment-created", "issue_comment", "pull_request_comment", "created", true},
		{"github/issue_comment_created", "issue_comment", "", "edited", false},
		{"github/issue_comment_created", "issue_comment", "", "deleted", false},
		{"github/pull_request_review_comment_created", "pull_request_review_comment", "", "edited", false},
		{"github/discussion_comment_created", "discussion_comment", "", "deleted", false},
		{"forgejo/issue_comment-created", "issue_comment", "issue_comment", "edited", false},
		{"forgejo/pull_request_comment-created", "issue_comment", "pull_request_comment", "deleted", false},
		{"github/issues_opened", "issues", "", "opened", false},
		{"github/pull_request_opened", "pull_request", "", "opened", false},
		{"github/discussion_created", "discussion", "", "created", false},
		{"github/pull_request_review_submitted", "pull_request_review", "", "submitted", false},
		{"forgejo/pull_request_review_comment-reviewed", "pull_request_review", "pull_request_review_comment", "reviewed", false},
		{"github/issue_comment_created", "unknown_event", "", "created", false},
	}
	for _, tt := range tests {
		t.Run(tt.fixture+"/"+tt.event+"/"+tt.action, func(t *testing.T) {
			var p map[string]any
			if err := json.Unmarshal(fixture(t, tt.fixture), &p); err != nil {
				t.Fatal(err)
			}
			p["action"] = tt.action
			for _, key := range []string{"comment", "review", "issue", "pull_request", "discussion"} {
				if obj, ok := p[key].(map[string]any); ok {
					obj["body"], obj["content"] = "Ask @octocat", "Ask @octocat"
				}
			}
			body, err := json.Marshal(p)
			if err != nil {
				t.Fatal(err)
			}
			h := newHarness(t, mentionConfig)
			req := signedDelivery("github-autobrr", tt.event, body)
			if tt.typ != "" {
				req = forgejoDelivery("github-autobrr", tt.event, tt.typ, body)
			}
			if got := h.do(req).Code; got != http.StatusAccepted {
				t.Fatalf("status = %d, want 202", got)
			}
			for range 2 {
				r := h.waitDiscord()
				want := `{"allowed_mentions": {"parse": []}}`
				if r.Sink == "ping" && tt.ping {
					want = `{"content": "<@111>", "allowed_mentions": {"parse": [], "users": ["111"]}}`
				}
				assertMentions(t, r.Body, want)
			}
		})
	}
}

func TestCommentMentionBeyondExcerpt(t *testing.T) {
	h := newHarness(t, mentionConfig)
	body := strings.Repeat("ordinary text ", 1000) + "@octocat"
	if got := h.do(signedDelivery("github-autobrr", "issue_comment", commentPayload(t, "github/issue_comment_created", body))).Code; got != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", got)
	}
	for range 2 {
		r := h.waitDiscord()
		want := `{"allowed_mentions": {"parse": []}}`
		if r.Sink == "ping" {
			want = `{"content": "<@111>", "allowed_mentions": {"parse": [], "users": ["111"]}}`
		}
		assertMentions(t, r.Body, want)
		var msg message
		if err := json.Unmarshal(r.Body, &msg); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(msg.Embeds[0].Description, "@octocat") {
			t.Error("excerpt includes the late mention")
		}
	}
}

func TestCommentMentionScanLimit(t *testing.T) {
	h := newHarness(t, mentionConfig)
	body := "@octocat\n" + strings.Repeat("a\n\n", maxMentionScan/3)
	if got := h.do(signedDelivery("github-autobrr", "issue_comment", commentPayload(t, "github/issue_comment_created", body))).Code; got != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", got)
	}
	for range 2 {
		assertMentions(t, h.waitDiscord().Body, `{"allowed_mentions": {"parse": []}}`)
	}
}

func TestCommentMentionLimits(t *testing.T) {
	tests := []struct {
		name                string
		count, digits, want int
	}{
		{"forty repeated mentions", 40, 19, 1},
		{"content limit", 90, 19, 87},
		{"user limit", 101, 3, 100},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var users, body strings.Builder
			var wantIDs []string
			for i := range tt.count {
				id := fmt.Sprintf("%0*d", tt.digits, i+1)
				login := fmt.Sprintf("user%d", i)
				if tt.want == 1 {
					id, login = "1234567890123456789", "oneuser"
				}
				if tt.want != 1 || i == 0 {
					fmt.Fprintf(&users, "  %s: %q\n", login, id)
				}
				fmt.Fprintf(&body, "@%s ", login)
				if i < tt.want {
					wantIDs = append(wantIDs, id)
				}
			}
			h := newHarness(t, strings.Replace(mentionConfig, "users:\n", "users:\n"+users.String(), 1))
			if got := h.do(signedDelivery("github-autobrr", "issue_comment", commentPayload(t, "github/issue_comment_created", body.String()))).Code; got != http.StatusAccepted {
				t.Fatalf("status = %d, want 202", got)
			}
			for range 2 {
				r := h.waitDiscord()
				if r.Sink == "quiet" {
					assertMentions(t, r.Body, `{"allowed_mentions": {"parse": []}}`)
					continue
				}
				var msg message
				if err := json.Unmarshal(r.Body, &msg); err != nil {
					t.Fatal(err)
				}
				if !slices.Equal(msg.AllowedMentions.Users, wantIDs) {
					t.Errorf("allowed users = %v, want %v", msg.AllowedMentions.Users, wantIDs)
				}
				var wantMentions []string
				for _, id := range wantIDs {
					wantMentions = append(wantMentions, "<@"+id+">")
				}
				if want := strings.Join(wantMentions, " "); msg.Content != want {
					t.Errorf("content = %q, want %q", msg.Content, want)
				}
				if len(msg.AllowedMentions.Parse) != 0 || msg.Embeds[0].Description != strings.TrimSpace(body.String()) {
					t.Error("automatic parsing enabled or comment text changed")
				}
			}
		})
	}
}

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

// pingUsers is the User map of the pingsOf tests. codertocat is the sender
// of each Event, and alias is a second login of the user 111.
var pingUsers = map[string]string{"octocat": "111", "codertocat": "222", "other": "333", "alias": "111", "autobrr/devs": "444"}

// allPingKinds keeps each Ping kind, like mentions: true.
var allPingKinds = pingKinds{"review_requested": true, "assigned": true, "comments": true, "sponsorship": true}

// assertPings checks the Ping text and the user IDs that pingsOf gives e for
// kinds. want lists the user IDs in order.
func assertPings(t *testing.T, e *Event, users map[string]string, kinds pingKinds, want ...string) {
	t.Helper()
	text, ids := pingsOf(e, users)(kinds)
	var wantText []string
	for _, id := range want {
		wantText = append(wantText, "<@"+id+">")
	}
	if !slices.Equal(ids, want) || text != strings.Join(wantText, " ") {
		t.Errorf("Pings = %q %v, want %v", text, ids, want)
	}
}

func TestPings(t *testing.T) {
	people := func(names ...string) []Subject {
		var s []Subject
		for _, n := range names {
			s = append(s, Subject{Name: n})
		}
		return s
	}
	sponsorship := &Sponsorship{Sponsorable: "octocat"}
	tests := []struct {
		name  string
		e     Event
		kinds pingKinds
		want  []string
	}{
		{"review request pings the reviewer", Event{Name: "pull_request", Action: "review_requested", Subjects: people("octocat")}, allPingKinds, []string{"111"}},
		{"assignment pings the assignee in any case", Event{Name: "pull_request", Action: "assigned", Subjects: people("OCTOCAT")}, allPingKinds, []string{"111"}},
		{"issue assignment", Event{Name: "issues", Action: "assigned", Subjects: people("octocat")}, allPingKinds, []string{"111"}},
		{"no ping when the sender assigns itself", Event{Name: "pull_request", Action: "assigned", Subjects: people("CoderTocat")}, allPingKinds, nil},
		{"no ping for a user that is not in users", Event{Name: "pull_request", Action: "review_requested", Subjects: people("stranger")}, allPingKinds, nil},
		{"no ping for another action", Event{Name: "pull_request", Action: "edited", Subjects: people("octocat")}, allPingKinds, nil},
		{"no ping for a removed review request", Event{Name: "pull_request", Action: "review_request_removed", Subjects: people("octocat")}, allPingKinds, nil},
		{"no ping for an unassignment", Event{Name: "issues", Action: "unassigned", Subjects: people("octocat")}, allPingKinds, nil},
		{"never a team", Event{Name: "pull_request", Action: "review_requested", Subjects: []Subject{{Name: "autobrr/devs", Team: true}}}, allPingKinds, nil},
		{"a Burst pings each person once, in order", Event{Name: "pull_request", Action: "review_requested",
			Subjects: append([]Subject{{Name: "autobrr/devs", Team: true}}, people("other", "codertocat", "octocat", "alias")...)}, allPingKinds, []string{"333", "111"}},
		{"review request kind off", Event{Name: "pull_request", Action: "review_requested", Subjects: people("octocat")}, pingKinds{"assigned": true, "comments": true}, nil},
		{"assignment kind off", Event{Name: "pull_request", Action: "assigned", Subjects: people("octocat")}, pingKinds{"review_requested": true}, nil},
		{"sponsorship pings the sponsored account", Event{Name: "sponsorship", Action: "created", Sponsorship: sponsorship}, pingKinds{"sponsorship": true}, []string{"111"}},
		{"sponsorship kind off", Event{Name: "sponsorship", Action: "tier_changed", Sponsorship: sponsorship}, pingKinds{"review_requested": true, "assigned": true, "comments": true}, nil},
		{"no ping for a pending sponsorship change", Event{Name: "sponsorship", Action: "pending_cancellation", Sponsorship: sponsorship}, allPingKinds, nil},
		{"no kinds", Event{Name: "pull_request", Action: "review_requested", Subjects: people("octocat")}, nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.e.Sender = "codertocat"
			assertPings(t, &tt.e, pingUsers, tt.kinds, tt.want...)
		})
	}
}

// commentEvent is a new issue comment by codertocat with the text body.
func commentEvent(body string) *Event {
	return &Event{Name: "issue_comment", Action: "created", Sender: "codertocat", Body: body}
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
			assertPings(t, commentEvent(tt.body), pingUsers, allPingKinds, "333")
		})
	}
}

func TestCommentMentionScanLimit(t *testing.T) {
	assertPings(t, commentEvent("@octocat\n"+strings.Repeat("a\n\n", maxMentionScan/3)), pingUsers, allPingKinds)
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
			users := map[string]string{}
			var body strings.Builder
			var wantIDs []string
			for i := range tt.count {
				id := fmt.Sprintf("%0*d", tt.digits, i+1)
				login := fmt.Sprintf("user%d", i)
				if tt.want == 1 {
					id, login = "1234567890123456789", "oneuser"
				}
				users[login] = id
				fmt.Fprintf(&body, "@%s ", login)
				if i < tt.want {
					wantIDs = append(wantIDs, id)
				}
			}
			assertPings(t, commentEvent(body.String()), users, allPingKinds, wantIDs...)
		})
	}
}

// pingsOf scans a comment only for a Sink that keeps the comments kind, and
// only once for all Sinks.
func TestPingsShareCommentScan(t *testing.T) {
	e := commentEvent(strings.Repeat("Text with #12 and @alice.\n\n", 2000))
	users := map[string]string{"alice": "111"}
	comments := pingKinds{"comments": true}
	allocs := func(kinds ...pingKinds) float64 {
		return testing.AllocsPerRun(1, func() {
			pings := pingsOf(e, users)
			for _, k := range kinds {
				pings(k)
			}
		})
	}
	quiet, one, mixed := allocs(nil), allocs(comments), allocs(nil, comments, comments, nil)
	if one < quiet+1000 {
		t.Errorf("quiet Sink made %.0f allocations, comment Pings made %.0f; scanning must stay lazy", quiet, one)
	}
	if mixed > one*1.2 {
		t.Errorf("one Sink made %.0f allocations, mixed Sinks made %.0f; comment scanning must be shared", one, mixed)
	}
}

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

// A message with no Renderer of its own never pings, also when the Event
// names a mapped reviewer or assignee. TestPings covers the other cases.
func TestFallbackNeverPings(t *testing.T) {
	reviewRequested := fixture(t, "github/pull_request_review_requested")
	assigned := bytes.Replace(reviewRequested, []byte(`"review_requested"`), []byte(`"assigned"`), 1)
	assigned = bytes.Replace(assigned, []byte(`"requested_reviewer"`), []byte(`"assignee"`), 1)
	tests := []struct {
		name  string
		event string
		body  []byte
	}{
		{"a Fallback message never pings", "pull_request_review_thread", reviewRequested},
		// The payload has no alert, so the Renderer gives the Fallback message.
		{"a security Event with no alert never pings", "code_scanning_alert", assigned},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, mentionConfig)
			if got := h.do(signedDelivery("github-autobrr", tt.event, tt.body)).Code; got != http.StatusAccepted {
				t.Fatalf("status = %d, want 202", got)
			}
			for range 2 {
				assertMentions(t, h.waitDiscord().Body, `{"allowed_mentions": {"parse": []}}`)
			}
		})
	}
}

// The body line names the reviewer also on the card that pings the
// reviewer, because the body is the same for each Sink.
func TestSubjectStaysWhenPinged(t *testing.T) {
	h := newHarness(t, mentionConfig)
	if got := h.do(signedDelivery("github-autobrr", "pull_request", fixture(t, "github/pull_request_review_requested"))).Code; got != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", got)
	}
	want := "A review from **[octocat](https://example.invalid/octocat)** has been requested"
	for range 2 {
		r := h.waitDiscord()
		m := decodeMessage(t, r.Body)
		if r.Sink == "ping" && pingOf(m) != "<@111>" {
			t.Errorf("sink ping got Ping %q, want <@111>", pingOf(m))
		}
		if got := excerptOf(t, m); got != want {
			t.Errorf("sink %s body = %q, want %q", r.Sink, got, want)
		}
	}
}

// assertMentions compares the Ping and the allowed_mentions of the message
// body with want. The Ping is in "content". It ignores the rest of the
// message.
func assertMentions(t *testing.T, body []byte, want string) {
	t.Helper()
	m := decodeMessage(t, body)
	got := map[string]any{"allowed_mentions": m.AllowedMentions}
	if ping := pingOf(m); ping != "" {
		got["content"] = ping
	}
	b, _ := json.Marshal(got)
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
		if excerptOf(t, msg) != body {
			t.Errorf("comment = %q, want %q", excerptOf(t, msg), body)
		}
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
		if strings.Contains(excerptOf(t, msg), "@octocat") {
			t.Error("excerpt includes the late mention")
		}
	}
}

// pingKindsConfig sends every Event to five Sinks with different mentions
// values.
const pingKindsConfig = `
sources:
  github-autobrr: { secret: test-secret }
users:
  octocat: "111"
sinks:
  all: { discord: https://discord.example.invalid/api/webhooks/1/token, mentions: true }
  work: { discord: https://discord.example.invalid/api/webhooks/2/token, mentions: [assigned, comments] }
  reviews: { discord: https://discord.example.invalid/api/webhooks/3/token, mentions: [review_requested] }
  empty: { discord: https://discord.example.invalid/api/webhooks/4/token, mentions: [] }
  nokey: { discord: https://discord.example.invalid/api/webhooks/5/token }
routes:
  - match: {}
    to: [all, work, reviews, empty, nokey]
`

func TestPingKinds(t *testing.T) {
	reviewRequested := func(t *testing.T) []byte { return fixture(t, "github/pull_request_review_requested") }
	assigned := func(t *testing.T) []byte {
		b := bytes.Replace(reviewRequested(t), []byte(`"review_requested"`), []byte(`"assigned"`), 1)
		return bytes.Replace(b, []byte(`"requested_reviewer"`), []byte(`"assignee"`), 1)
	}
	comment := func(t *testing.T) []byte { return commentPayload(t, "github/issue_comment_created", "Ask @octocat") }
	tests := []struct {
		name, event string
		body        func(*testing.T) []byte
		pinged      []string // the Sinks that get <@111>
	}{
		{"review request", "pull_request", reviewRequested, []string{"all", "reviews"}},
		{"assignment", "pull_request", assigned, []string{"all", "work"}},
		{"comment", "issue_comment", comment, []string{"all", "work"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, pingKindsConfig)
			if got := h.do(signedDelivery("github-autobrr", tt.event, tt.body(t))).Code; got != http.StatusAccepted {
				t.Fatalf("status = %d, want 202", got)
			}
			got := map[string][]byte{}
			for range 5 {
				r := h.waitDiscord()
				got[r.Sink] = r.Body
			}
			for _, sink := range []string{"all", "work", "reviews", "empty", "nokey"} {
				if got[sink] == nil {
					t.Errorf("sink %s got no message", sink)
					continue
				}
				want := `{"allowed_mentions": {"parse": []}}`
				if slices.Contains(tt.pinged, sink) {
					want = `{"content": "<@111>", "allowed_mentions": {"parse": [], "users": ["111"]}}`
				}
				assertMentions(t, got[sink], want)
			}
		})
	}
}

// sponsorshipPingConfig sends every Event to three Sinks: all has
// mentions: true, sponsors keeps only sponsorship, and work keeps the other
// kinds. The fixtures name octocat as the sponsored account and monalisa as
// the sponsor.
const sponsorshipPingConfig = `
sources:
  github-autobrr: { secret: test-secret }
users:
  octocat: "111"
sinks:
  all: { discord: https://discord.example.invalid/api/webhooks/1/token, mentions: true }
  sponsors: { discord: https://discord.example.invalid/api/webhooks/2/token, mentions: [sponsorship] }
  work: { discord: https://discord.example.invalid/api/webhooks/3/token, mentions: [review_requested, assigned, comments] }
routes:
  - match: {}
    to: [all, sponsors, work]
`

func TestSponsorshipPings(t *testing.T) {
	// withAction changes the action of the fixture name.
	withAction := func(name, from, to string) func(*testing.T) []byte {
		return func(t *testing.T) []byte {
			return bytes.Replace(fixture(t, name), []byte(`"action": "`+from+`"`), []byte(`"action": "`+to+`"`), 1)
		}
	}
	file := func(name string) func(*testing.T) []byte {
		return func(t *testing.T) []byte { return fixture(t, name) }
	}
	tests := []struct {
		name    string
		body    func(*testing.T) []byte
		ping    bool // the all and sponsors Sinks get <@111>
		private bool // the messages and the log must not show monalisa
	}{
		{"created", file("github/sponsorship_created"), true, false},
		{"cancelled", withAction("github/sponsorship_pending_cancellation", "pending_cancellation", "cancelled"), true, false},
		{"tier_changed", file("github/sponsorship_tier_changed"), true, false},
		{"Private created", withAction("github/sponsorship_edited_private", "edited", "created"), true, true},
		{"Private tier_changed", withAction("github/sponsorship_pending_tier_change_private", "pending_tier_change", "tier_changed"), true, true},
		{"edited", file("github/sponsorship_edited_private"), false, true},
		{"pending_cancellation", file("github/sponsorship_pending_cancellation"), false, false},
		{"pending_tier_change", file("github/sponsorship_pending_tier_change_private"), false, true},
		{"unmapped sponsored account", func(t *testing.T) []byte {
			return bytes.Replace(fixture(t, "github/sponsorship_created"), []byte(`"login": "octocat"`), []byte(`"login": "stranger"`), 1)
		}, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, sponsorshipPingConfig)
			if got := h.do(signedDelivery("github-autobrr", "sponsorship", tt.body(t))).Code; got != http.StatusAccepted {
				t.Fatalf("status = %d, want 202", got)
			}
			got := map[string][]byte{}
			for range 3 {
				r := h.waitDiscord()
				got[r.Sink] = r.Body
			}
			for _, sink := range []string{"all", "sponsors", "work"} {
				want := `{"allowed_mentions": {"parse": []}}`
				if tt.ping && sink != "work" {
					want = `{"content": "<@111>", "allowed_mentions": {"parse": [], "users": ["111"]}}`
				}
				assertMentions(t, got[sink], want)
				if tt.private && strings.Contains(string(got[sink]), "monalisa") {
					t.Errorf("sink %s shows the Private sponsor:\n%s", sink, got[sink])
				}
			}
			if tt.private && strings.Contains(h.logs.String(), "monalisa") {
				t.Errorf("the log shows the Private sponsor:\n%s", h.logs)
			}
		})
	}
}

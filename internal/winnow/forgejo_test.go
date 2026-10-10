package winnow

import (
	"bytes"
	"net/http"
	"strings"
	"testing"
)

// forgejoConfig sends every Event to the Sink all. The Source forgejo has
// no forge: key.
const forgejoConfig = `
sources:
  forgejo: { secret: test-secret }
sinks:
  all: { discord: https://discord.example.invalid/api/webhooks/1/token }
routes:
  - match: {}
    to: [all]
`

// forgejoPoster is the poster of each message about a Forgejo Event.
const forgejoPoster = `"username": "Forgejo", "avatar_url": "https://forgejo.org/favicon.png", "flags": 32768`

// The sender lines and the thumbnails of the senders in the Forgejo
// fixtures. Each Forgejo sender gets an identicon, because Discord cannot
// load an avatar from a Forgejo that is not public.
const (
	soupLine   = `{"type": 10, "content": "-# [soup](https://example.invalid/soup)"}`
	soupThumb  = `{"type": 11, "media": {"url": "https://www.gravatar.com/avatar/9014fac8184d7ba9e1dcd80b8c1e1aa2906af8a01fc1e6e9092e575eabf7b80d?d=identicon&s=128"}}`
	aliceLine  = `{"type": 10, "content": "-# [alice](https://example.invalid/alice)"}`
	aliceThumb = `{"type": 11, "media": {"url": "https://www.gravatar.com/avatar/2bd806c97f0e00af1a1fc3328fa763a9269723c8db8fac4f93af71db186d6e90?d=identicon&s=128"}}`
)

// winnowTest is the repository of the Forgejo fixtures, and pull5 is its
// pull request 5.
const (
	winnowTest = "https://example.invalid/soup/winnow-test"
	pull5      = winnowTest + "/pulls/5"
)

func TestForgejoRenderers(t *testing.T) {
	opened := fixture(t, "forgejo/pull_request-opened")
	// A Forgejo pull request has no Checks button and no list buttons.
	diff := buttonRowJSON("Diff", pull5+"/files")
	// The Author alice is not the sender soup, so a comment or a review
	// gets the Author button.
	review := buttonRowJSON("PR", pull5, "Diff", pull5+"/files", "alice", "https://example.invalid/alice")
	tests := []struct {
		name   string
		event  string // X-Forgejo-Event
		typ    string // X-Forgejo-Event-Type
		body   []byte
		wantEv string // the event in the decision line
		want   string // the Container
	}{
		{"push", "push", "push", fixture(t, "forgejo/push"), "push", container(0,
			section(aliceLine, "## [[soup/winnow-test:main] 2 new commits]("+winnowTest+"/compare/1111111111111111111111111111111111111111...3333333333333333333333333333333333333333)\n"+
				"[`2222222`]("+winnowTest+"/commit/2222222222222222222222222222222222222222) Fix panic on empty config\n"+
				"[`3333333`]("+winnowTest+"/commit/3333333333333333333333333333333333333333) Add test", aliceThumb))},
		{"push with more commits than the payload holds", "push", "push",
			bytes.Replace(fixture(t, "forgejo/push"), []byte(`"total_commits": 2`), []byte(`"total_commits": 20`), 1), "push", container(0,
				section(aliceLine, "## [[soup/winnow-test:main] 20 new commits]("+winnowTest+"/compare/1111111111111111111111111111111111111111...3333333333333333333333333333333333333333)\n"+
					"[`2222222`]("+winnowTest+"/commit/2222222222222222222222222222222222222222) Fix panic on empty config\n"+
					"[`3333333`]("+winnowTest+"/commit/3333333333333333333333333333333333333333) Add test", aliceThumb))},
		// The commit author is not the pusher, so the commit line names it.
		{"test delivery is a normal push", "push", "push", fixture(t, "forgejo/push-test-delivery"), "push", container(0,
			section(soupLine, "## [[soup/winnow-test:main] 1 new commit]("+winnowTest+"/compare/3333333333333333333333333333333333333333...3333333333333333333333333333333333333333)\n"+
				"[`3333333`]("+winnowTest+"/commit/3333333333333333333333333333333333333333) Add test - Alice", soupThumb))},
		{"pull request opened", "pull_request", "pull_request", opened, "pull_request.opened", container(2066493,
			section(aliceLine, "## [[soup/winnow-test] Pull request opened: #5 Add Forgejo support]("+pull5+")\nParses X-Forgejo-Event-Type.", aliceThumb), diff)},
		{"synchronized becomes synchronize", "pull_request", "pull_request_sync", fixture(t, "forgejo/pull_request_sync-synchronized"), "pull_request.synchronize", container(0,
			section(aliceLine, "## [[soup/winnow-test] Pull request synchronize: #5 Add Forgejo support]("+pull5+")", aliceThumb), diff)},
		{"pull request merged", "pull_request", "pull_request", fixture(t, "forgejo/pull_request-closed-merged"), "pull_request.closed", container(8540383,
			section(soupLine, "## [[soup/winnow-test] Pull request merged: #5 Add Forgejo support]("+pull5+")", soupThumb), diff)},
		{"review request stays a pull request Event", "pull_request", "pull_request_review_request",
			bytes.Replace(opened, []byte(`"action": "opened"`), []byte(`"action": "review_requested"`), 1), "pull_request.review_requested", container(0,
				section(aliceLine, "## [[soup/winnow-test] Pull request review requested: #5 Add Forgejo support]("+pull5+")", aliceThumb), diff)},
		{"label_updated stays as it is", "pull_request", "pull_request_label", fixture(t, "forgejo/pull_request_label-label_updated"), "pull_request.label_updated", container(0,
			section(soupLine, "## [[soup/winnow-test] Pull request label updated: #5 Add Forgejo support]("+pull5+")", soupThumb), diff)},
		// Forgejo gets no list buttons, so an issue has no buttons.
		{"issue opened", "issues", "issues", fixture(t, "forgejo/issues-opened"), "issues.opened", container(2066493,
			section(aliceLine, "## [[soup/winnow-test] Issue opened: #3 Crash on empty config]("+winnowTest+"/issues/3)\nSteps:\n1. Start with an empty file.\n2. It panics.", aliceThumb))},
		{"comment on an issue", "issue_comment", "issue_comment", fixture(t, "forgejo/issue_comment-created"), "issue_comment.created", container(0,
			section(soupLine, "## [[soup/winnow-test] New comment on issue: #3 Crash on empty config]("+winnowTest+"/issues/3#issuecomment-900)\nThanks, I can reproduce it.", soupThumb),
			buttonRowJSON("Issue", winnowTest+"/issues/3", "alice", "https://example.invalid/alice"))},
		{"comment on a pull request", "issue_comment", "pull_request_comment", fixture(t, "forgejo/pull_request_comment-created"), "issue_comment.created", container(0,
			section(soupLine, "## [[soup/winnow-test] New comment on pull request: #5 Add Forgejo support]("+pull5+"#issuecomment-901)\nLooks good, one nit below.", soupThumb), review)},
		{"review approved", "pull_request_approved", "pull_request_review_approved", fixture(t, "forgejo/pull_request_review_approved-reviewed"), "pull_request_review.submitted", container(2066493,
			section(soupLine, "## [[soup/winnow-test] Review approved: #5 Add Forgejo support]("+pull5+")\nReview text (approved).", soupThumb), review)},
		{"review rejected", "pull_request_rejected", "pull_request_review_rejected", fixture(t, "forgejo/pull_request_review_rejected-reviewed"), "pull_request_review.submitted", container(13574702,
			section(soupLine, "## [[soup/winnow-test] Review changes requested: #5 Add Forgejo support]("+pull5+")\nReview text (changes_requested).", soupThumb), review)},
		{"review with comments", "pull_request_comment", "pull_request_review_comment", fixture(t, "forgejo/pull_request_review_comment-reviewed"), "pull_request_review.submitted", container(0,
			section(soupLine, "## [[soup/winnow-test] Review commented: #5 Add Forgejo support]("+pull5+")\nReview text (commented).", soupThumb), review)},
		// A Forgejo release keeps the identicon of the sender.
		{"release published", "release", "release", fixture(t, "forgejo/release-published"), "release.published", container(0,
			section(soupLine, "## [[soup/winnow-test] Release published: v1.0.0]("+winnowTest+"/releases/tag/v1.0.0)\nFirst release.", soupThumb))},
		{"release updated becomes edited", "release", "release", fixture(t, "forgejo/release-updated"), "release.edited", container(0,
			section(soupLine, "## [[soup/winnow-test] Release edited: v1.0.0]("+winnowTest+"/releases/tag/v1.0.0)", soupThumb))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, forgejoConfig)
			if got := h.do(forgejoDelivery("forgejo", tt.event, tt.typ, tt.body)).Code; got != http.StatusAccepted {
				t.Fatalf("status = %d, want 202", got)
			}
			assertJSON(t, h.waitDiscord().Body, `{`+forgejoPoster+`, "components": [`+tt.want+`], "allowed_mentions": {"parse": []}}`)
			d := h.decision()
			if d["event"] != tt.wantEv || d["delivery"] != testDelivery {
				t.Errorf("decision event = %v, delivery = %v, want %s and %s", d["event"], d["delivery"], tt.wantEv, testDelivery)
			}
		})
	}
}

func TestForgejoRuleFields(t *testing.T) {
	review := fixture(t, "forgejo/pull_request_review_rejected-reviewed") // sender soup
	tests := []struct {
		name   string
		bots   string
		match  string
		github bool // send a GitHub delivery, not the Forgejo review
		want   int
	}{
		{"X-Forgejo-Event means forgejo", "", "{forge: forgejo}", false, http.StatusAccepted},
		{"review state", "", "{event: pull_request_review, action: submitted, review_state: changes_requested}", false, http.StatusAccepted},
		{"plain user is not a bot", "", "{sender_bot: true}", false, http.StatusNoContent},
		{"bots list without case on forgejo", "bots: [SOUP]", "{sender_bot: true}", false, http.StatusAccepted},
		{"bots list without case on github", "bots: [S0UP4200]", "{sender_bot: true}", true, http.StatusAccepted},
		{"other login is not in the bots list", "bots: [renovate]", "{sender_bot: true}", false, http.StatusNoContent},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, `
sources:
  forgejo: { secret: test-secret }
  github-autobrr: { secret: test-secret }
sinks:
  all: { discord: https://discord.example.invalid/api/webhooks/1/token }
`+tt.bots+`
routes:
  - match: `+tt.match+`
    to: [all]
`)
			req := forgejoDelivery("forgejo", "pull_request_rejected", "pull_request_review_rejected", review)
			if tt.github {
				req = signedDelivery("github-autobrr", "label", fixture(t, "github/label_created"))
			}
			if got := h.do(req).Code; got != tt.want {
				t.Errorf("status = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestForgejoSignature(t *testing.T) {
	body := fixture(t, "forgejo/push")
	hub := sign(testSecret, body)
	fj := strings.TrimPrefix(hub, "sha256=")
	tests := []struct {
		name    string
		hub, fj string // X-Hub-Signature-256 and X-Forgejo-Signature; "-" removes the header
		want    int
	}{
		{"only X-Forgejo-Signature", "-", fj, http.StatusAccepted},
		{"only X-Hub-Signature-256", hub, "-", http.StatusAccepted},
		{"webhook with no secret", "sha256=", "", http.StatusUnauthorized},
		{"X-Forgejo-Signature with another secret", "-", strings.TrimPrefix(sign("other-secret", body), "sha256="), http.StatusUnauthorized},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, forgejoConfig)
			req := forgejoDelivery("forgejo", "push", "push", body)
			for k, v := range map[string]string{"X-Hub-Signature-256": tt.hub, "X-Forgejo-Signature": tt.fj} {
				if v == "-" {
					req.Header.Del(k)
				} else {
					req.Header.Set(k, v)
				}
			}
			if got := h.do(req).Code; got != tt.want {
				t.Errorf("status = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestForgejoMentions(t *testing.T) {
	// replace fails the test when b does not hold old, so that no case
	// passes on an unchanged fixture.
	replace := func(b []byte, old, new string) []byte {
		t.Helper()
		if !bytes.Contains(b, []byte(old)) {
			t.Fatalf("fixture does not hold %q", old)
		}
		return bytes.Replace(b, []byte(old), []byte(new), 1)
	}
	opened := fixture(t, "forgejo/pull_request-opened") // sender alice
	tests := []struct {
		name       string
		event, typ string
		body       []byte
		want       string // the content and allowed_mentions of the message
	}{
		{"review request pings the reviewer", "pull_request", "pull_request_review_request",
			replace(replace(opened, `"action": "opened"`, `"action": "review_requested"`), `"requested_reviewer": null`, `"requested_reviewer": {"login": "soup"}`),
			`{"content": "<@222>", "allowed_mentions": {"parse": [], "users": ["222"]}}`},
		{"assignment does not ping: Forgejo has no top-level assignee", "pull_request", "pull_request_assign",
			replace(replace(opened, `"action": "opened"`, `"action": "assigned"`), `"assignee": null`, `"assignee": {"login": "soup"}`),
			`{"allowed_mentions": {"parse": []}}`},
		// Forgejo sets requested_reviewer to the reviewer on a review. The
		// sender here is not the reviewer, so only the action stops the ping.
		{"review does not ping requested_reviewer", "pull_request_approved", "pull_request_review_approved",
			replace(fixture(t, "forgejo/pull_request_review_approved-reviewed"), `"sender": {
    "id": 1,
    "login": "soup"`, `"sender": {
    "id": 2,
    "login": "alice"`),
			`{"allowed_mentions": {"parse": []}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, `
sources:
  forgejo: { secret: test-secret }
users:
  alice: "111"
  soup: "222"
sinks:
  ping: { discord: https://discord.example.invalid/api/webhooks/1/token, mentions: true }
routes:
  - match: {}
    to: [ping]
`)
			if got := h.do(forgejoDelivery("forgejo", tt.event, tt.typ, tt.body)).Code; got != http.StatusAccepted {
				t.Fatalf("status = %d, want 202", got)
			}
			assertMentions(t, h.waitDiscord().Body, tt.want)
		})
	}
}

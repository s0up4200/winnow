package winnow

import (
	"bytes"
	"encoding/json/v2"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"
)

// catchAllConfig sends every Event to the Sink all.
const catchAllConfig = `
sources:
  github-autobrr: { secret: test-secret }
sinks:
  all: { discord: https://discord.example.invalid/api/webhooks/1/token }
routes:
  - match: {}
    to: [all]
`

// author is the embed author of the sender in most GitHub fixtures.
const author = `"author": {
	"name": "Codertocat",
	"url": "https://example.invalid/Codertocat",
	"icon_url": "https://example.invalid/u/21031067?v=4"
}`

func TestRenderers(t *testing.T) {
	tests := []struct {
		name    string
		event   string
		fixture string
		want    string // the embed
	}{
		{"issue opened", "issues", "issues_opened", `{` + author + `,
			"title": "[Codertocat/Hello-World] Issue opened: #1 Spelling error in the README file",
			"url": "https://example.invalid/Codertocat/Hello-World/issues/1",
			"description": "It looks like you accidently spelled 'commit' with two 't's.",
			"color": 2066493
		}`},
		{"issue closed", "issues", "issues_closed", `{` + author + `,
			"title": "[Codertocat/Hello-World] Issue closed: #1 Spelling error in the README file",
			"url": "https://example.invalid/Codertocat/Hello-World/issues/1",
			"description": "It looks like you accidently spelled 'commit' with two 't's.",
			"color": 13574702
		}`},
		{"pull request opened", "pull_request", "pull_request_opened", `{` + author + `,
			"title": "[Codertocat/Hello-World] Pull request opened: #2 Update the README with new information.",
			"url": "https://example.invalid/Codertocat/Hello-World/pull/2",
			"description": "This is a pretty simple change that we need to pull into master.",
			"color": 2066493
		}`},
		{"pull request merged", "pull_request", "pull_request_merged", `{
			"author": {
				"name": "s0up4200",
				"url": "https://github.example.invalid/s0up4200",
				"icon_url": "https://avatars.example.invalid/u/3001"
			},
			"title": "[autobrr/qui] Pull request merged: #43 Add cross-seed search",
			"url": "https://github.example.invalid/autobrr/qui/pull/43",
			"description": "This adds a search for cross-seed matches.",
			"color": 8540383
		}`},
		{"pull request closed without a merge", "pull_request", "pull_request_closed_unmerged", `{` + author + `,
			"title": "[Codertocat/Hello-World] Pull request closed: #2 Update the README with new information.",
			"url": "https://example.invalid/Codertocat/Hello-World/pull/2",
			"description": "This is a pretty simple change that we need to pull into master.",
			"color": 13574702
		}`},
		{"comment on an issue", "issue_comment", "issue_comment_created", `{` + author + `,
			"title": "[Codertocat/Hello-World] New comment on issue: #1 Spelling error in the README file",
			"url": "https://example.invalid/Codertocat/Hello-World/issues/1#issuecomment-492700400",
			"description": "You are totally right! I'll get this fixed right away."
		}`},
		{"comment on a pull request", "issue_comment", "issue_comment_created_on_pull", `{` + author + `,
			"title": "[Codertocat/Hello-World] New comment on pull request: #1 Spelling error in the README file",
			"url": "https://example.invalid/Codertocat/Hello-World/pull/1#issuecomment-492700400",
			"description": "You are totally right! I'll get this fixed right away."
		}`},
		{"review with no text", "pull_request_review", "pull_request_review_submitted", `{` + author + `,
			"title": "[Codertocat/Hello-World] Review commented: #2 Update the README with new information.",
			"url": "https://example.invalid/Codertocat/Hello-World/pull/2#pullrequestreview-237895671"
		}`},
		{"review comment", "pull_request_review_comment", "pull_request_review_comment_created", `{` + author + `,
			"title": "[Codertocat/Hello-World] New review comment: #2 Update the README with new information.",
			"url": "https://example.invalid/Codertocat/Hello-World/pull/2#discussion_r284312630",
			"description": "Maybe you should use more emoji on this line."
		}`},
		{"discussion", "discussion", "discussion_created", `{` + author + `,
			"title": "[Codertocat/Hello-World] Discussion created: #4 TEST edit",
			"url": "https://example.invalid/Codertocat/Hello-World/discussions/4",
			"description": "TEST edit"
		}`},
		{"discussion comment", "discussion_comment", "discussion_comment_created", `{` + author + `,
			"title": "[Codertocat/Hello-World] New comment on discussion: #4 TEST edit",
			"url": "https://example.invalid/Codertocat/Hello-World/discussions/4#discussioncomment-550062",
			"description": "ANSWER"
		}`},
		{"push", "push", "push", `{` + author + `,
			"title": "[Codertocat/Hello-World:master] 2 new commits",
			"url": "https://example.invalid/Codertocat/Hello-World/compare/6113728f27ae...0d1a26e67d8f",
			"description": "[` + "`6113728`" + `](https://example.invalid/Codertocat/Hello-World/commit/6113728f27ae82c7b1a177c8d03f9e96e0adf246) Initial commit - Codertocat\n[` + "`0d1a26e`" + `](https://example.invalid/Codertocat/Hello-World/commit/0d1a26e67d8f5eaf1f6ba5c57fc3c7d91ac0fd1c) Update README.md - Codertocat"
		}`},
		{"push that deletes a tag", "push", "push_deleted_tag", `{` + author + `,
			"title": "[Codertocat/Hello-World] Tag deleted: simple-tag",
			"url": "https://example.invalid/Codertocat/Hello-World/compare/d70c5c6fa638^...000000000000"
		}`},
		{"release with no name", "release", "release_published", `{` + author + `,
			"title": "[Codertocat/Hello-World] Release published: 0.0.1",
			"url": "https://example.invalid/Codertocat/Hello-World/releases/tag/0.0.1"
		}`},
		{"fork", "fork", "fork", `{
			"author": {
				"name": "Octocoders",
				"url": "https://example.invalid/Octocoders",
				"icon_url": "https://example.invalid/u/38302899?v=4"
			},
			"title": "[Codertocat/Hello-World] Fork created: Octocoders/Hello-World",
			"url": "https://example.invalid/Octocoders/Hello-World"
		}`},
		{"star", "watch", "watch_started", `{` + author + `,
			"title": "[Codertocat/Hello-World] New star",
			"url": "https://example.invalid/Codertocat/Hello-World",
			"color": 14922561
		}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, catchAllConfig)
			if got := h.do(signedDelivery("github-autobrr", tt.event, fixture(t, "github/"+tt.fixture))).Code; got != http.StatusAccepted {
				t.Fatalf("status = %d, want 202", got)
			}
			assertJSON(t, h.waitDiscord().Body, `{"embeds": [`+tt.want+`], "allowed_mentions": {"parse": []}}`)
		})
	}
}

func TestRendererCutsToDiscordLimits(t *testing.T) {
	h := newHarness(t, catchAllConfig)
	body := fixture(t, "github/issues_opened")
	body = bytes.Replace(body, []byte("Spelling error in the README file"), []byte(strings.Repeat("é", 300)), 1)
	body = bytes.Replace(body, []byte("It looks like you accidently spelled 'commit' with two 't's."), []byte(strings.Repeat("ü", 5000)), 1)
	if got := h.do(signedDelivery("github-autobrr", "issues", body)).Code; got != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", got)
	}

	var msg struct {
		Embeds []struct {
			Title       string `json:"title"`
			Description string `json:"description"`
		} `json:"embeds"`
	}
	if err := json.Unmarshal(h.waitDiscord().Body, &msg); err != nil {
		t.Fatal(err)
	}
	em := msg.Embeds[0]
	if n := utf8.RuneCountInString(em.Title); n != 256 || !strings.HasSuffix(em.Title, "é…") {
		t.Errorf("title has %d characters, want 256 that end in é…: %q", n, em.Title)
	}
	if n := utf8.RuneCountInString(em.Description); n != 4096 || !strings.HasSuffix(em.Description, "ü…") {
		t.Errorf("description has %d characters, want 4096 that end in ü…", n)
	}
}

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

// githubPoster is the poster of each message about a GitHub Event.
const githubPoster = `"username": "GitHub", "avatar_url": "https://avatars.githubusercontent.com/u/9919?s=128"`

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
			"color": 8540383
		}`},
		{"pull request closed without a merge", "pull_request", "pull_request_closed_unmerged", `{` + author + `,
			"title": "[Codertocat/Hello-World] Pull request closed: #2 Update the README with new information.",
			"url": "https://example.invalid/Codertocat/Hello-World/pull/2",
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
		{"star from the star Event", "star", "star_created", `{` + author + `,
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
			assertJSON(t, h.waitDiscord().Body, `{`+githubPoster+`, "embeds": [`+tt.want+`], "allowed_mentions": {"parse": []}}`)
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

// issueDescription sends an issues.opened Event with body as the issue text
// and returns the embed description.
func issueDescription(t *testing.T, body string) string {
	t.Helper()
	h := newHarness(t, catchAllConfig)
	quoted, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	payload := bytes.Replace(fixture(t, "github/issues_opened"), []byte(`"It looks like you accidently spelled 'commit' with two 't's."`), quoted, 1)
	if got := h.do(signedDelivery("github-autobrr", "issues", payload)).Code; got != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", got)
	}
	var msg struct {
		Embeds []struct {
			Description string `json:"description"`
		} `json:"embeds"`
	}
	if err := json.Unmarshal(h.waitDiscord().Body, &msg); err != nil {
		t.Fatal(err)
	}
	return msg.Embeds[0].Description
}

func TestRendererCleansBody(t *testing.T) {
	body := "<!-- Fill in the template. -->\r\n## Summary\r\n\r\n\r\n\r\nFixes the [crash](https://example.invalid/1).\n" +
		"[![CI](https://example.invalid/ci.svg)](https://example.invalid/ci)\n![screenshot](https://example.invalid/a.png)<img src=\"https://example.invalid/b.png\" width=\"200\">\n\n  \n\n" +
		"<details>\n<summary>Logs</summary>\n\npanic: nil map in `Vec<String>`<br/>\n</details>\n\n- [x] Tests\n- [ ] Docs\n<!--\nmore\n-->"
	want := "## Summary\n\nFixes the [crash](https://example.invalid/1).\n\nLogs\n\npanic: nil map in `Vec<String>`\n\n- [x] Tests\n- [ ] Docs"
	if got := issueDescription(t, body); got != want {
		t.Errorf("description = %q, want %q", got, want)
	}
}

func TestRendererListsTable(t *testing.T) {
	body := "This PR contains the following updates:\n\n| Package | Update | Change |\n|---|:---:|---|\n" +
		"| ghcr.io/s0up4200/example-app | patch | `v0.1.1` → `v0.1.2` |\n| [hass](https://example.invalid/hass) | minor | `2026.9.3` → `2026.10.0` |\n\n---\n\nName | Size\n:-- | --:\na | 1\n\n| not a table |"
	want := "This PR contains the following updates:\n\n**Package · Update · Change**\n" +
		"- ghcr.io/s0up4200/example-app · patch · `v0.1.1` → `v0.1.2`\n- [hass](https://example.invalid/hass) · minor · `2026.9.3` → `2026.10.0`\n\n---\n\n**Name · Size**\n- a · 1\n\n| not a table |"
	if got := issueDescription(t, body); got != want {
		t.Errorf("description = %q, want %q", got, want)
	}
}

func TestRendererKeepsCodeBlocks(t *testing.T) {
	body := "<!-- x -->Example:\n\n```md\n| a | b |\n|---|---|\n<!-- kept -->\n\n\n\n```\n<br>after\n" +
		"  ````\n```\n    ````\n<details>\n````\n~~~\nopen <b>to the end"
	want := "Example:\n\n```md\n| a | b |\n|---|---|\n<!-- kept -->\n\n\n\n```\nafter\n" +
		"  ````\n```\n    ````\n<details>\n````\n~~~\nopen <b>to the end"
	if got := issueDescription(t, body); got != want {
		t.Errorf("description = %q, want %q", got, want)
	}
}

func TestRendererCutsBodyAtWord(t *testing.T) {
	want := strings.Repeat("word ", 818) + "word…"
	if got := issueDescription(t, strings.Repeat("word ", 1000)); got != want {
		t.Errorf("description has %d characters and ends in %q, want %d that end in %q",
			utf8.RuneCountInString(got), got[max(0, len(got)-12):], utf8.RuneCountInString(want), "word word…")
	}
}

// The half of the cut counts characters, not bytes, so emoji before the
// last space do not move the space into the second half.
func TestRendererCutsLongWordAfterEmoji(t *testing.T) {
	want := strings.Repeat("😀", 1000) + " " + strings.Repeat("A", 3094) + "…"
	if got := issueDescription(t, strings.Repeat("😀", 1000)+" "+strings.Repeat("A", 5000)); got != want {
		t.Errorf("description has %d characters, want %d", utf8.RuneCountInString(got), utf8.RuneCountInString(want))
	}
}

func TestRendererCutsLongWordAfterHeading(t *testing.T) {
	want := "### PoC\n" + strings.Repeat("A", 4087) + "…"
	if got := issueDescription(t, "### PoC\n"+strings.Repeat("A", 5000)); got != want {
		t.Errorf("description has %d characters and starts with %.20q, want %d", utf8.RuneCountInString(got), got, utf8.RuneCountInString(want))
	}
}

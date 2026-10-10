package winnow

import (
	"bytes"
	"encoding/json/v2"
	"net/http"
	"regexp"
	"slices"
	"strconv"
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
const githubPoster = `"username": "GitHub", "avatar_url": "https://avatars.githubusercontent.com/u/9919?s=128", "flags": 32768`

// The sender line and the avatar of the sender in most GitHub fixtures.
const (
	codertocatLine  = `{"type": 10, "content": "-# [Codertocat](https://example.invalid/Codertocat)"}`
	codertocatThumb = `{"type": 11, "media": {"url": "https://example.invalid/u/21031067?v=4"}}`
)

// section returns the JSON of a Section with the sender line, the heading
// text, and the thumbnail.
func section(line, heading, thumb string) string {
	quoted, _ := json.Marshal(heading)
	return `{"type": 9, "components": [` + line + `, {"type": 10, "content": ` + string(quoted) + `}], "accessory": ` + thumb + `}`
}

// container returns the JSON of a Container with the accent color, or no
// color for 0, and the components in.
func container(color int, in ...string) string {
	c := `{"type": 17, "components": [` + strings.Join(in, ", ") + `]`
	if color != 0 {
		c += `, "accent_color": ` + strconv.Itoa(color)
	}
	return c + `}`
}

// buttonRowJSON returns the JSON of a Separator and an Action Row with a
// link button for each label and URL pair.
func buttonRowJSON(pairs ...string) string {
	var bs []string
	for i := 0; i < len(pairs); i += 2 {
		bs = append(bs, `{"type": 2, "style": 5, "label": "`+pairs[i]+`", "url": "`+pairs[i+1]+`"}`)
	}
	return `{"type": 14}, {"type": 1, "components": [` + strings.Join(bs, ", ") + `]}`
}

// The repository, the pull requests 1 and 2, and the issue 1 of the GitHub
// fixtures.
const (
	hello  = "https://example.invalid/Codertocat/Hello-World"
	pull2  = hello + "/pull/2"
	pull1  = hello + "/pull/1"
	issue1 = hello + "/issues/1"
)

func TestRenderers(t *testing.T) {
	prs := hello + "/pulls?q=is%3Apr+author%3ACodertocat"
	issues := hello + "/issues?q=is%3Aissue+author%3ACodertocat"
	tests := []struct {
		name    string
		event   string
		fixture string
		want    string // the Container
	}{
		{"issue opened", "issues", "issues_opened", container(2066493,
			section(codertocatLine, "## [[Codertocat/Hello-World] Issue opened: #1 Spelling error in the README file]("+issue1+")\nIt looks like you accidently spelled 'commit' with two 't's.", codertocatThumb),
			buttonRowJSON("Issues by Codertocat", issues))},
		{"issue closed", "issues", "issues_closed", container(13574702,
			section(codertocatLine, "## [[Codertocat/Hello-World] Issue closed: #1 Spelling error in the README file]("+issue1+")", codertocatThumb),
			buttonRowJSON("Issues by Codertocat", issues))},
		{"pull request opened", "pull_request", "pull_request_opened", container(2066493,
			section(codertocatLine, "## [[Codertocat/Hello-World] Pull request opened: #2 Update the README with new information.]("+pull2+")\nThis is a pretty simple change that we need to pull into master.", codertocatThumb),
			buttonRowJSON("Diff", pull2+"/files", "Checks", pull2+"/checks", "PRs by Codertocat", prs))},
		{"pull request merged", "pull_request", "pull_request_merged", container(8540383,
			section(`{"type": 10, "content": "-# [s0up4200](https://github.example.invalid/s0up4200)"}`,
				"## [[autobrr/qui] Pull request merged: #43 Add cross-seed search](https://github.example.invalid/autobrr/qui/pull/43)",
				`{"type": 11, "media": {"url": "https://avatars.example.invalid/u/3001"}}`),
			buttonRowJSON("Diff", "https://github.example.invalid/autobrr/qui/pull/43/files", "Checks", "https://github.example.invalid/autobrr/qui/pull/43/checks",
				"PRs by s0up4200", "https://github.example.invalid/autobrr/qui/pulls?q=is%3Apr+author%3As0up4200"))},
		{"pull request closed without a merge", "pull_request", "pull_request_closed_unmerged", container(13574702,
			section(codertocatLine, "## [[Codertocat/Hello-World] Pull request closed: #2 Update the README with new information.]("+pull2+")", codertocatThumb),
			buttonRowJSON("Diff", pull2+"/files", "Checks", pull2+"/checks", "PRs by Codertocat", prs))},
		{"comment on an issue", "issue_comment", "issue_comment_created", container(0,
			section(codertocatLine, "## [[Codertocat/Hello-World] New comment on issue: #1 Spelling error in the README file]("+issue1+"#issuecomment-492700400)\nYou are totally right! I'll get this fixed right away.", codertocatThumb),
			buttonRowJSON("Issue", issue1, "Issues by Codertocat", issues))},
		{"comment on a pull request", "issue_comment", "issue_comment_created_on_pull", container(0,
			section(codertocatLine, "## [[Codertocat/Hello-World] New comment on pull request: #1 Spelling error in the README file]("+pull1+"#issuecomment-492700400)\nYou are totally right! I'll get this fixed right away.", codertocatThumb),
			buttonRowJSON("PR", pull1, "Diff", pull1+"/files", "Checks", pull1+"/checks", "PRs by Codertocat", prs))},
		{"review with no text", "pull_request_review", "pull_request_review_submitted", container(0,
			section(codertocatLine, "## [[Codertocat/Hello-World] Review commented: #2 Update the README with new information.]("+pull2+"#pullrequestreview-237895671)", codertocatThumb),
			buttonRowJSON("PR", pull2, "Diff", pull2+"/files", "Checks", pull2+"/checks", "PRs by Codertocat", prs))},
		{"review comment", "pull_request_review_comment", "pull_request_review_comment_created", container(0,
			section(codertocatLine, "## [[Codertocat/Hello-World] New review comment: #2 Update the README with new information.]("+pull2+"#discussion_r284312630)\nMaybe you should use more emoji on this line.", codertocatThumb),
			buttonRowJSON("PR", pull2, "Diff", pull2+"/files", "Checks", pull2+"/checks", "PRs by Codertocat", prs))},
		{"discussion", "discussion", "discussion_created", container(0,
			section(codertocatLine, "## [[Codertocat/Hello-World] Discussion created: #4 TEST edit]("+hello+"/discussions/4)\nTEST edit", codertocatThumb))},
		{"discussion comment", "discussion_comment", "discussion_comment_created", container(0,
			section(codertocatLine, "## [[Codertocat/Hello-World] New comment on discussion: #4 TEST edit]("+hello+"/discussions/4#discussioncomment-550062)\nANSWER", codertocatThumb),
			buttonRowJSON("Discussion", hello+"/discussions/4"))},
		// The commit author is the pusher, so the commit lines do not name it.
		{"push", "push", "push", container(0,
			section(codertocatLine, "## [[Codertocat/Hello-World:master] 2 new commits]("+hello+"/compare/6113728f27ae...0d1a26e67d8f)\n"+
				"[`6113728`]("+hello+"/commit/6113728f27ae82c7b1a177c8d03f9e96e0adf246) Initial commit\n"+
				"[`0d1a26e`]("+hello+"/commit/0d1a26e67d8f5eaf1f6ba5c57fc3c7d91ac0fd1c) Update README.md", codertocatThumb))},
		{"push that deletes a tag", "push", "push_deleted_tag", container(0,
			section(codertocatLine, "## [[Codertocat/Hello-World] Tag deleted: simple-tag]("+hello+"/compare/d70c5c6fa638^...000000000000)", codertocatThumb))},
		{"release with no name", "release", "release_published", container(0,
			section(codertocatLine, "## [[Codertocat/Hello-World] Release published: 0.0.1]("+hello+"/releases/tag/0.0.1)", codertocatThumb))},
		{"fork", "fork", "fork", container(0,
			section(`{"type": 10, "content": "-# [Octocoders](https://example.invalid/Octocoders)"}`,
				"## [[Codertocat/Hello-World] Fork created: Octocoders/Hello-World](https://example.invalid/Octocoders/Hello-World)",
				`{"type": 11, "media": {"url": "https://example.invalid/u/38302899?v=4"}}`))},
		{"star", "watch", "watch_started", container(14922561,
			section(codertocatLine, "## [[Codertocat/Hello-World] New star]("+hello+")", codertocatThumb))},
		{"star from the star Event", "star", "star_created", container(14922561,
			section(codertocatLine, "## [[Codertocat/Hello-World] New star]("+hello+")", codertocatThumb))},
		{"Fallback message", "label", "label_created", container(0,
			`{"type": 10, "content": "## [label.created on autobrr/qui by s0up4200](https://github.example.invalid/autobrr/qui)"}`)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, catchAllConfig)
			if got := h.do(signedDelivery("github-autobrr", tt.event, fixture(t, "github/"+tt.fixture))).Code; got != http.StatusAccepted {
				t.Fatalf("status = %d, want 202", got)
			}
			assertJSON(t, h.waitDiscord().Body, `{`+githubPoster+`, "components": [`+tt.want+`], "allowed_mentions": {"parse": []}}`)
		})
	}
}

// The Author button shows when the Author is not the sender. The list of a
// GitHub App author filters by app/<name>, and its labels have no [bot].
func TestRendererAuthorButtons(t *testing.T) {
	var p map[string]any
	if err := json.Unmarshal(fixture(t, "github/pull_request_review_submitted"), &p); err != nil {
		t.Fatal(err)
	}
	p["pull_request"].(map[string]any)["user"] = map[string]any{"login": "dependabot[bot]", "type": "Bot", "html_url": "https://example.invalid/apps/dependabot"}
	payload, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, catchAllConfig)
	h.do(signedDelivery("github-autobrr", "pull_request_review", payload))
	m := decodeMessage(t, h.waitDiscord().Body)
	row := m.Components[0].Components[2]
	var got []button
	for _, b := range row.Components {
		got = append(got, button{b.Label, b.URL})
	}
	want := []button{{"PR", pull2}, {"Diff", pull2 + "/files"}, {"Checks", pull2 + "/checks"},
		{"dependabot", "https://example.invalid/apps/dependabot"},
		{"PRs by dependabot", hello + "/pulls?q=is%3Apr+author%3Aapp%2Fdependabot"}}
	if !slices.Equal(got, want) {
		t.Errorf("buttons = %v, want %v", got, want)
	}
}

// A release shows the Icon of the repository, else the Icon of the owner,
// else the avatar of the owner from the payload.
func TestRendererReleaseThumbnail(t *testing.T) {
	payload := bytes.Replace(fixture(t, "github/release_published"), []byte(`"avatar_url": "https://example.invalid/u/21031067?v=4",`), []byte(`"avatar_url": "https://example.invalid/u/owner",`), 1)
	for _, tt := range []struct{ icons, want string }{
		{"", "https://example.invalid/u/owner"},
		{"icons: { codertocat: https://example.invalid/owner.png }", "https://example.invalid/owner.png"},
		{"icons: { Codertocat: https://example.invalid/owner.png, codertocat/hello-world: https://example.invalid/repo.png }", "https://example.invalid/repo.png"},
	} {
		h := newHarness(t, catchAllConfig+tt.icons)
		h.do(signedDelivery("github-autobrr", "release", payload))
		if got := decodeMessage(t, h.waitDiscord().Body).Components[0].Components[0].Accessory.Media.URL; got != tt.want {
			t.Errorf("%s: thumbnail = %q, want %q", tt.icons, got, tt.want)
		}
	}
}

// A short body goes into the Section. A long body goes under the Section.
// When the heading is short, the first lines of a long body go into the
// Section.
func TestRendererBodyLayout(t *testing.T) {
	long := strings.Repeat("word ", 100)
	for _, tt := range []struct {
		name, title, body, top, rest string
	}{
		{"short body", "Spelling error in the README file", "Short.", "Short.", ""},
		{"long body", "Spelling error in the README file", "First line.\n" + long, "", "First line.\n" + strings.TrimSpace(long)},
		{"long body under a short heading", "Typo", "First line.\nSecond line.\n\n" + long, "First line.\nSecond line.", strings.TrimSpace(long)},
		{"code block under a short heading", "Typo", "Logs:\n```\n" + long + "\n```", "Logs:", "```\n" + long + "\n```"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			payload := bytes.Replace(fixture(t, "github/issues_opened"), []byte("Spelling error in the README file"), []byte(tt.title), 1)
			payload = withBody(t, payload, issueText, tt.body)
			h := newHarness(t, catchAllConfig)
			h.do(signedDelivery("github-autobrr", "issues", payload))
			in := decodeMessage(t, h.waitDiscord().Body).Components[0].Components
			_, top, _ := strings.Cut(in[0].Components[1].Content, "\n")
			var rest string
			if in[1].Type == typeTextDisplay {
				rest = in[1].Content
			}
			if top != tt.top || rest != tt.rest {
				t.Errorf("Section body %q, body under it %q, want %q and %q", top, rest, tt.top, tt.rest)
			}
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
	m := decodeMessage(t, h.waitDiscord().Body)
	heading := m.Components[0].Components[0].Components[1].Content
	title, _, _ := strings.Cut(strings.TrimPrefix(heading, "## ["), "](")
	if n := utf8.RuneCountInString(title); n != 256 || !strings.HasSuffix(title, "é…") {
		t.Errorf("heading has %d characters, want 256 that end in é…: %q", n, title)
	}
	if n := textLength(m.Components); n > maxText || n < maxText-10 {
		t.Errorf("message has %d characters, want at most %d and close to it", n, maxText)
	}
	if got := excerptOf(t, m); !strings.HasSuffix(got, "ü…") {
		t.Errorf("body ends in %q, want ü…", got[max(0, len(got)-10):])
	}
}

// issueText is the issue text in the issues_opened fixture.
const issueText = "It looks like you accidently spelled 'commit' with two 't's."

// issueDescription sends an issues.opened Event with body as the issue text
// and returns the body of the message.
func issueDescription(t *testing.T, body string) string {
	t.Helper()
	return description(t, catchAllConfig, signedDelivery("github-autobrr", "issues", withBody(t, fixture(t, "github/issues_opened"), issueText, body)))
}

// withBody returns payload with the first JSON string old replaced by body.
func withBody(t *testing.T, payload []byte, old, body string) []byte {
	t.Helper()
	quoted, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.Replace(payload, []byte(`"`+old+`"`), quoted, 1)
}

// description sends req to a harness with config and returns the body of
// the message.
func description(t *testing.T, config string, req *http.Request) string {
	t.Helper()
	h := newHarness(t, config)
	if got := h.do(req).Code; got != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", got)
	}
	return excerptOf(t, decodeMessage(t, h.waitDiscord().Body))
}

func TestRendererCleansBody(t *testing.T) {
	body := "<!-- Fill in the template. -->\r\n## Summary\r\n\r\n\r\n\r\nFixes the [crash](https://example.invalid/1).\n" +
		"[![CI](https://example.invalid/ci.svg)](https://example.invalid/ci)\n![screenshot](https://example.invalid/a.png)<img src=\"https://example.invalid/b.png\" width=\"200\">\n\n  \n\n" +
		"<details>\n<summary>Logs</summary>\n\npanic: nil map in `Vec<String>`<br/>\n</details>\n\n- [x] Tests\n- [ ] Docs\n<!--\nmore\n-->"
	want := "## Summary\n\nFixes the [crash](https://example.invalid/1).\n\nLogs\n\npanic: nil map in `Vec<String>`\n\n- ☑ Tests\n- ☐ Docs"
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

func TestRendererShowsTaskBoxes(t *testing.T) {
	body := "- [x] Tests\n* [X] Lint\n  - [ ] Docs\n1. [ ] Notes\n- [ ]\n[ ] not an item\n- [y] not a box\n```\n- [ ] in code\n```"
	want := "- ☑ Tests\n* ☑ Lint\n  - ☐ Docs\n1. ☐ Notes\n- [ ]\n[ ] not an item\n- [y] not a box\n```\n- [ ] in code\n```"
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

// issueMessage sends an issues.opened Event with body as the issue text and
// returns the message.
func issueMessage(t *testing.T, body string) message {
	t.Helper()
	h := newHarness(t, catchAllConfig)
	h.do(signedDelivery("github-autobrr", "issues", withBody(t, fixture(t, "github/issues_opened"), issueText, body)))
	return decodeMessage(t, h.waitDiscord().Body)
}

// assertCut fails the test when the body of m does not match want, or when m
// has more characters than the limit or more than slack less.
func assertCut(t *testing.T, m message, want *regexp.Regexp, slack int) {
	t.Helper()
	got := excerptOf(t, m)
	if n := textLength(m.Components); n > maxText || n < maxText-slack {
		t.Errorf("message has %d characters, want at most %d and at least %d", n, maxText, maxText-slack)
	}
	if !want.MatchString(got) {
		t.Errorf("body starts with %.20q and ends in %q, want a match for %s", got, got[max(0, len(got)-12):], want)
	}
}

func TestRendererCutsBodyAtWord(t *testing.T) {
	assertCut(t, issueMessage(t, strings.Repeat("word ", 1000)), regexp.MustCompile(`^(word )+word…$`), 5)
}

// The half of the cut counts characters, not bytes, so emoji before the
// last space do not move the space into the second half.
func TestRendererCutsLongWordAfterEmoji(t *testing.T) {
	assertCut(t, issueMessage(t, strings.Repeat("😀", 1000)+" "+strings.Repeat("A", 5000)), regexp.MustCompile(`^(😀){1000} A+…$`), 1)
}

func TestRendererCutsLongWordAfterHeading(t *testing.T) {
	assertCut(t, issueMessage(t, "### PoC\n"+strings.Repeat("A", 5000)), regexp.MustCompile(`^### PoC\nA+…$`), 1)
}

func TestRendererKeepsInlineCode(t *testing.T) {
	body := "Use the `<details>` tag, not <b>bold</b>.\n`<!-- kept -->` and ``<img src=\"x\"> ` `` stay.<!-- a `code` comment goes -->\n" +
		"<a title=\"`x`\">link</a>![`y`](https://example.invalid/a.png)"
	want := "Use the `<details>` tag, not bold.\n`<!-- kept -->` and ``<img src=\"x\"> ` `` stay.\nlink"
	if got := issueDescription(t, body); got != want {
		t.Errorf("description = %q, want %q", got, want)
	}
}

// A cut in a code block closes the block with its opening fence.
func TestRendererClosesCutCodeBlock(t *testing.T) {
	for _, fence := range []string{"```", "~~~", "````"} {
		t.Run(fence, func(t *testing.T) {
			got := issueDescription(t, "Logs:\n"+fence+"go\n"+strings.Repeat("line ", 1000)+"\n"+fence+"\nafter")
			if n := utf8.RuneCountInString(got); n > maxText {
				t.Errorf("description has %d characters, want at most %d", n, maxText)
			}
			if !strings.HasSuffix(got, "line…\n"+fence) || strings.Count(got, fence)%2 != 0 {
				t.Errorf("description ends in %q, want an even number of fences and the end line…\\n%s", got[max(0, len(got)-20):], fence)
			}
		})
	}
}

// A cut in a link that ends after the cut, on the same line, moves back to
// before the link.
func TestCutWordsBeforeLink(t *testing.T) {
	s := "see [the docs](https://example.invalid/a/long/path) now\nmore text"
	if got, want := cutWords(s, 20), "see…"; got != want {
		t.Errorf("cutWords(%q, 20) = %q, want %q", s, got, want)
	}
}

// A cut next to a fence keeps the limit and adds no fence that opens a block.
func TestCutWordsAtFence(t *testing.T) {
	for _, tt := range []struct {
		s    string
		n    int
		want string
	}{
		// The shorter cut ends in an earlier block with a longer fence.
		{"`````\nx\n`````\n```\ny y y ", 18, "`````\nx…\n`````"},
		{"`````\nx\n`````\n```\ny y y ", 19, "`````\nx\n`````\n…"},
		// The cut is right after a closing fence or an opening fence. "…"
		// on the fence line makes the fence text.
		{"```\nx\n```\n" + strings.Repeat("A", 20), 12, "```\nx\n```\n…"},
		{"text\n```go\n" + strings.Repeat("A", 20), 16, "text\n```go\n…\n```"},
	} {
		got := cutWords(tt.s, tt.n)
		if got != tt.want || utf8.RuneCountInString(got) > tt.n {
			t.Errorf("cutWords(%q, %d) = %q, want %q", tt.s, tt.n, got, tt.want)
		}
	}
}

// hash is a full commit hash, and repoURL is the repository URL in the
// GitHub fixtures.
const (
	hash    = "21ba448d1c52bcce9ead8b0acba67e3ca8ba3d05"
	repoURL = "https://example.invalid/Codertocat/Hello-World"
)

func TestRendererLinksReleaseChangelog(t *testing.T) {
	body := "## Changelog\n### New Features\n* " + hash + " feat: add a flag (#202) (@alice)\n" +
		"* 0d1a26e67d8f5eaf1f6ba5c57fc3c7d91ac0fd1c fix: stop a crash (#203) (@bob)"
	want := "## Changelog\n### New Features\n* [21ba448](" + repoURL + "/commit/" + hash + ") feat: add a flag ([#202](" + repoURL + "/issues/202)) (@alice)\n" +
		"* [0d1a26e](" + repoURL + "/commit/0d1a26e67d8f5eaf1f6ba5c57fc3c7d91ac0fd1c) fix: stop a crash ([#203](" + repoURL + "/issues/203)) (@bob)"
	quoted, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	// The release name before the body is also "", so the replacement
	// names the key.
	payload := bytes.Replace(fixture(t, "github/release_published"), []byte(`"body": ""`), append([]byte(`"body": `), quoted...), 1)
	if got := description(t, catchAllConfig, signedDelivery("github-autobrr", "release", payload)); got != want {
		t.Errorf("description = %q, want %q", got, want)
	}
}

// A Reference in code, in a link, or in a URL, and text that is only like a
// Reference, stay as they are.
func TestRendererKeepsTextThatIsNotAReference(t *testing.T) {
	for _, body := range []string{
		"```\n" + hash + " #12\n```",
		"See `#12` and ``" + hash + "``.",
		"[#12](https://example.invalid/x/pull/12) and [" + hash + "](https://example.invalid/c)",
		"[see #12](https://example.invalid/a \"t\") [fix [#12]](https://example.invalid/b) [a](https://example.invalid/(x)#12)",
		"It&#39;s fixed.",
		"\\#12 and \\" + hash + " stay as text.",
		"[#12][ticket] and [" + hash + "][]\n\n[ticket]: https://example.invalid/t",
		"https://example.invalid/x/commit/" + hash + " https://example.invalid/a.go#L10 <https://example.invalid/b#12>",
		"abc#12 #12a _#12 x" + hash + " " + hash + "0",
		"sha256: " + hash + hash[:24],
	} {
		if got := issueDescription(t, body); got != body {
			t.Errorf("description = %q, want %q", got, body)
		}
	}
}

func TestRendererLinksNoReferenceWithoutRepositoryURL(t *testing.T) {
	body := "Fixes #12 in " + hash + "."
	payload := bytes.Replace(fixture(t, "github/issues_opened"), []byte(`"html_url": "`+repoURL+`",`), []byte(`"html_url": "",`), 1)
	payload = withBody(t, payload, issueText, body)
	if got := description(t, catchAllConfig, signedDelivery("github-autobrr", "issues", payload)); got != body {
		t.Errorf("description = %q, want %q", got, body)
	}
}

func TestRendererLinksForgejoReferences(t *testing.T) {
	const forgejoURL = "https://example.invalid/soup/winnow-test"
	payload := withBody(t, fixture(t, "forgejo/issues-opened"), `Steps:\n1. Start with an empty file.\n2. It panics.`, "Since #3 in "+hash+".")
	want := "Since [#3](" + forgejoURL + "/issues/3) in [21ba448](" + forgejoURL + "/commit/" + hash + ")."
	if got := description(t, forgejoConfig, forgejoDelivery("forgejo", "issues", "issues", payload)); got != want {
		t.Errorf("description = %q, want %q", got, want)
	}
}

// The cut at the Discord limit moves back to before a link that it would
// split.
func TestRendererCutsBeforeLink(t *testing.T) {
	ref := "[#12](" + repoURL + "/issues/12)"
	author := "[the crash report](https://example.invalid/crash)"
	for _, tt := range []struct {
		name, body, link string
	}{
		{"References with no space", "Refs:" + strings.Repeat("(#12)", 2000), ref},
		{"links with spaces in the text", strings.Repeat(author+" ", 200), author},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := issueDescription(t, tt.body)
			if n := utf8.RuneCountInString(got); n > maxText {
				t.Errorf("description has %d characters, want at most %d", n, maxText)
			}
			rest, ok := strings.CutSuffix(got, "…")
			if !ok || strings.Trim(strings.ReplaceAll(strings.TrimPrefix(rest, "Refs:"), tt.link, ""), "() ") != "" {
				t.Errorf("description ends in %q, want whole links and the end …", got[max(0, len(got)-60):])
			}
		})
	}
}

// A long body of short hex runs must not make one match for each run.
func TestLinkReferencesSkipsShortHexRuns(t *testing.T) {
	body := strings.Repeat("a ", 1<<19)
	if n := testing.AllocsPerRun(1, func() { linkReferences(body, repoURL) }); n > 100 {
		t.Errorf("linkReferences made %.0f allocations, want at most 100", n)
	}
}

// The work for an excerpt does not grow with the body after a limit, because
// only 4096 characters reach Discord.
func TestExcerptWorkStopsGrowing(t *testing.T) {
	allocs := func(n int) float64 {
		e := &Event{Name: "issues", Action: "opened", Body: strings.Repeat("#1 ", n), RepoURL: repoURL}
		return testing.AllocsPerRun(1, func() { excerpt(e) })
	}
	if small, large := allocs(1<<18), allocs(1<<20); large > small*1.1 {
		t.Errorf("excerpt made %.0f allocations for a 3 MiB body and %.0f for a 768 KiB body, want about the same", large, small)
	}
}

// The work for a push does not grow with the commit messages after a limit,
// because only 4000 characters reach Discord.
func TestPushWorkStopsGrowing(t *testing.T) {
	allocs := func(n int) float64 {
		e := &Event{Name: "push", Ref: new("refs/heads/main"), RepoURL: repoURL, Push: Push{Size: 2, Commits: []Commit{
			{ID: "0123456789abcdef", Message: strings.Repeat("#1 ", n)},
			{ID: "0123456789abcdef", Message: strings.Repeat("#1 ", n)},
		}}}
		return testing.AllocsPerRun(1, func() { renderPush(e) })
	}
	if small, large := allocs(1<<18), allocs(1<<20); large > small*1.1 {
		t.Errorf("renderPush made %.0f allocations for 3 MiB messages and %.0f for 768 KiB messages, want about the same", large, small)
	}
}

// BenchmarkDeliverLargeBody measures deliver for a new comment with a body
// of 256 KiB on a Route with three Sinks. The webhook handler waits for this
// work.
func BenchmarkDeliverLargeBody(b *testing.B) {
	body := strings.Repeat("Some text with <b>tags</b>, `code`, and #12.\n\n| a | b |\n|---|---|\n| 1 | 2 |\n", 256<<10/80)
	e := &Event{Forge: "github", Name: "issue_comment", Action: "created", Repo: "autobrr/qui", Sender: "octocat",
		RepoURL: "https://example.invalid/autobrr/qui", URL: "https://example.invalid/autobrr/qui/issues/1", Number: 1, Title: "t", Body: body}
	o := &outbox{sinks: map[string]*sink{}, drain: make(chan struct{})}
	route := &Route{Name: "all"}
	for _, name := range []string{"a", "b", "c"} {
		o.sinks[name] = &sink{name: name, queue: make(chan entry, 1)}
		route.To = append(route.To, name)
	}
	for b.Loop() {
		o.deliver(e, route)
		for _, sk := range o.sinks {
			<-sk.queue
		}
	}
}

package winnow

import (
	"bytes"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// cest is the time zone of the scheduler clock in the Digest tests.
var cest = time.FixedZone("CEST", 2*60*60)

// day returns a time in October 2025 in cest. 29 September 2025 is a Monday.
func day(month time.Month, d, hour, minute int) time.Time {
	return time.Date(2025, month, d, hour, minute, 0, 0, cest)
}

// digestConfig drops each Event, and has one Digest with the Period every
// and the Rules match.
func digestConfig(every, match string) string {
	return `
sources:
  github-autobrr: { secret: test-secret }
  forgejo: { secret: test-secret }
sinks:
  digest: { discord: https://discord.example.invalid/api/webhooks/1/token }
routes:
  - name: everything
    drop: true
    match: {}
digests:
  - name: test
    every: ` + every + `
    to: digest
    match: ` + match + `
`
}

// receive sends req to the handler at the time at, with a new delivery ID.
func (h *harness) receive(at time.Time, req *http.Request) {
	h.t.Helper()
	h.clock = at
	h.ids++
	id := fmt.Sprintf("delivery-%d", h.ids)
	req.Header.Set("X-GitHub-Delivery", id)
	if req.Header.Get("X-Forgejo-Event") != "" {
		req.Header.Set("X-Forgejo-Delivery", id)
	}
	if got := h.do(req).Code; got >= 300 {
		h.t.Fatalf("status = %d", got)
	}
}

// github returns a GitHub delivery of testdata/github/<name>.json.
func (h *harness) github(event, name string) *http.Request {
	return signedDelivery("github-autobrr", event, fixture(h.t, "github/"+name))
}

// sentDigest is a Digest message in the parts that the tests read.
type sentDigest struct {
	Title       string // the line over the heading, a newline, and the heading
	Description string // the Text Displays between the heading and the footer
	Footer      string // the footer line, without "-# "
	Thumb       string // the thumbnail, or ""
}

// digests stops the server, so that the queues are empty, and returns the
// Digest messages that the fake Discord got. Then it starts the server
// again on the same store.
func (h *harness) digests() []sentDigest {
	h.t.Helper()
	h.restart(h.config)
	var got []sentDigest
	for len(h.discord) > 0 {
		m := decodeMessage(h.t, (<-h.discord).Body)
		texts := displays(m.Components)
		last := texts[len(texts)-1]
		also, footer, ok := strings.CutLast(last, "\n")
		if !ok {
			also, footer = "", last
		}
		texts[len(texts)-1] = also
		d := sentDigest{
			Title:       texts[0] + "\n" + texts[1],
			Description: strings.TrimSpace(strings.Join(texts[2:], "\n")),
			Footer:      strings.TrimPrefix(footer, "-# "),
		}
		if head := m.Components[0].Components[0]; head.Accessory != nil {
			d.Thumb = head.Accessory.Media.URL
		}
		got = append(got, d)
	}
	return got
}

// oneDigest returns the one Digest message that the fake Discord got.
func (h *harness) oneDigest() sentDigest {
	h.t.Helper()
	got := h.digests()
	if len(got) != 1 {
		h.t.Fatalf("got %d Digest messages, want 1: %+v", len(got), got)
	}
	return got[0]
}

// withOther sets include_other on the last Digest of config. An empty value
// omits the option.
func withOther(config, value string) string {
	if value == "" {
		return config
	}
	return config + "    include_other: " + value + "\n"
}

// otherOnly returns a label.created delivery in the repository
// autobrr/other-only, which has no named activity.
func (h *harness) otherOnly() *http.Request {
	body := bytes.ReplaceAll(fixture(h.t, "github/label_created"), []byte("autobrr/qui"), []byte("autobrr/other-only"))
	return signedDelivery("github-autobrr", "label", body)
}

func TestDigestCountsEachMetric(t *testing.T) {
	const named = "**2** PRs merged · **1** opened · **1** closed\n**2** issues opened · **1** closed · **2** releases · **1** star · **1** fork · **1** discussion"
	repos := []string{
		"### [Codertocat/Hello-World](https://example.invalid/Codertocat/Hello-World)  ·  [0.0.1](https://example.invalid/Codertocat/Hello-World/releases/tag/0.0.1)  ·  [Pulse](https://example.invalid/Codertocat/Hello-World/pulse)\n**1** PR opened · **1** issue opened · **1** closed · **1** star",
		// Forgejo has no Pulse page.
		"### [soup/winnow-test](https://example.invalid/soup/winnow-test)  ·  [v1.0.0](https://example.invalid/soup/winnow-test/releases/tag/v1.0.0)\n**1** merged · **1** issue opened",
		"### [autobrr/qui](https://github.example.invalid/autobrr/qui)  ·  [Pulse](https://github.example.invalid/autobrr/qui/pulse)\n**1** merged",
	}
	tests := []struct {
		includeOther, totals string
		repos                []string
		footer               string
	}{
		{"", named, repos, "-# 3 repositories"},
		{"false", named, repos, "-# 3 repositories"},
		{"true", named + "\nOther: **2** label.created", append(slices.Clip(repos),
			"### [autobrr/other-only](https://github.example.invalid/autobrr/other-only)  ·  [Pulse](https://github.example.invalid/autobrr/other-only/pulse)"), "-# 4 repositories"},
	}
	for _, tt := range tests {
		t.Run("include_other="+tt.includeOther, func(t *testing.T) {
			var texts []string
			for _, l := range append([]string{"-# week 40 · 29 September – 5 October", "## Weekly digest", tt.totals, "large"}, append(tt.repos, "small", tt.footer)...) {
				switch l {
				case "large":
					texts = append(texts, `{"type": 14, "spacing": 2}`)
					continue
				case "small":
					texts = append(texts, `{"type": 14}`)
					continue
				}
				quoted, _ := json.Marshal(l)
				texts = append(texts, `{"type": 10, "content": `+string(quoted)+`}`)
			}
			testDigestCountsEachMetric(t, tt.includeOther, container(14922561, texts...))
		})
	}
}

func testDigestCountsEachMetric(t *testing.T, includeOther, want string) {
	h := newHarness(t, withOther(digestConfig("weekly", "{}"), includeOther))
	h.step(day(9, 29, 0, 0))
	at := day(10, 1, 12, 0)
	for _, d := range []struct{ event, name string }{
		{"pull_request", "pull_request_merged"},
		{"pull_request", "pull_request_opened"},
		{"pull_request", "pull_request_closed_unmerged"},
		{"issues", "issues_opened"},
		{"issues", "issues_closed"},
		{"release", "release_published"},
		{"watch", "watch_started"},
		{"star", "star_created"},
		{"fork", "fork"},
		{"discussion", "discussion_created"},
		{"label", "label_created"},
	} {
		h.receive(at, h.github(d.event, d.name))
	}
	// An unstar does not subtract.
	unstar := bytes.Replace(fixture(t, "github/star_created"), []byte(`"action": "created"`), []byte(`"action": "deleted"`), 1)
	h.receive(at, signedDelivery("github-autobrr", "star", unstar))
	for _, d := range []struct{ event, name string }{
		{"pull_request", "pull_request-closed-merged"},
		{"issues", "issues-opened"},
		{"release", "release-published"},
	} {
		h.receive(at, forgejoDelivery("forgejo", d.event, d.event, fixture(t, "forgejo/"+d.name)))
	}
	h.receive(at, h.otherOnly())
	h.step(day(10, 6, 9, 0))
	h.restart(h.config)
	assertJSON(t, h.waitDiscord().Body, `{`+githubPoster+`, "components": [`+want+`], "allowed_mentions": {"parse": []}}`)
}

func TestDigestPeriodEdges(t *testing.T) {
	h := newHarness(t, digestConfig("weekly", "{}"))
	h.step(day(9, 22, 0, 0))
	h.receive(day(9, 28, 23, 59), h.github("star", "star_created"))
	h.receive(day(9, 29, 0, 0), h.github("fork", "fork"))
	h.step(day(9, 29, 8, 59))
	if got := h.digests(); len(got) != 0 {
		t.Fatalf("got %d messages before the send time, want 0", len(got))
	}
	h.step(day(9, 29, 9, 0))
	if got := h.oneDigest(); !strings.Contains(got.Description, "**1** star") || strings.Contains(got.Description, "fork") {
		t.Errorf("week 39 = %q, want only the star", got.Description)
	}
	h.step(day(10, 6, 9, 0))
	if got := h.oneDigest(); !strings.Contains(got.Description, "**1** fork") || strings.Contains(got.Description, "star") {
		t.Errorf("week 40 = %q, want only the fork", got.Description)
	}
}

// helloLine is the Digest line of Codertocat/Hello-World, with the name
// that the line shows.
func helloLine(name string) string {
	return "### [" + name + "](" + hello + ")  ·  [Pulse](" + hello + "/pulse)"
}

func TestDigestTitles(t *testing.T) {
	// The default omits Other in each Period kind. include_other: true adds
	// it in each Period kind. The repositories of the two Events have two
	// owners, so the line over the heading names no owner.
	for _, tt := range []struct{ includeOther, owner, description, footer string }{
		{"", "Codertocat · ", "**1** star\n" + helloLine("Hello-World") + "\n**1** star", "1 repository"},
		{"true", "", "**1** star\nOther: **1** label.created\n" + helloLine("Codertocat/Hello-World") + "\n**1** star\n" +
			"### [autobrr/qui](https://github.example.invalid/autobrr/qui)  ·  [Pulse](https://github.example.invalid/autobrr/qui/pulse)", "2 repositories"},
	} {
		t.Run("include_other="+tt.includeOther, func(t *testing.T) {
			testDigestTitles(t, tt.includeOther, tt.owner, sentDigest{Description: tt.description, Footer: tt.footer})
		})
	}
}

func testDigestTitles(t *testing.T, includeOther, owner string, want sentDigest) {
	tests := []struct {
		every       string
		first, recv time.Time
		send        time.Time
		dates       string
	}{
		{"daily", day(10, 4, 0, 0), day(10, 5, 12, 0), day(10, 6, 9, 0), "Sunday 5 October"},
		{"weekly", day(9, 29, 0, 0), day(10, 5, 12, 0), day(10, 6, 9, 0), "week 40 · 29 September – 5 October"},
		{"monthly", day(9, 1, 0, 0), day(9, 30, 12, 0), day(10, 1, 9, 0), "September 2025"},
		{"yearly", time.Date(2025, 1, 1, 0, 0, 0, 0, cest), day(10, 5, 12, 0), time.Date(2026, 1, 1, 9, 0, 0, 0, cest), "2025"},
	}
	for _, tt := range tests {
		t.Run(tt.every, func(t *testing.T) {
			h := newHarness(t, withOther(digestConfig(tt.every, "{}"), includeOther))
			h.step(tt.first)
			h.receive(tt.recv, h.github("star", "star_created"))
			h.receive(tt.recv, h.github("label", "label_created"))
			h.step(tt.send)
			want := want
			want.Title = "-# " + owner + tt.dates + "\n## " + strings.ToUpper(tt.every[:1]) + tt.every[1:] + " digest"
			if got := h.oneDigest(); got != want {
				t.Errorf("got %+v\nwant %+v", got, want)
			}
		})
	}
}

// The Digest shows the Icon of the owner when all its repositories have that
// owner. A repository Icon does not count.
func TestDigestIcon(t *testing.T) {
	for _, tt := range []struct{ name, icons, want string }{
		{"owner Icon", "icons: { CODERTOCAT: https://example.invalid/owner.png }", "https://example.invalid/owner.png"},
		{"repository Icon only", "icons: { codertocat/hello-world: https://example.invalid/repo.png }", ""},
		{"two owners", "icons: { codertocat: https://example.invalid/owner.png, autobrr: https://example.invalid/autobrr.png }", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, tt.icons+digestConfig("weekly", "{}"))
			h.step(day(9, 29, 0, 0))
			h.receive(day(10, 1, 12, 0), h.github("star", "star_created"))
			if tt.name == "two owners" {
				h.receive(day(10, 1, 12, 0), h.github("pull_request", "pull_request_merged"))
			}
			h.step(day(10, 6, 9, 0))
			if got := h.oneDigest().Thumb; got != tt.want {
				t.Errorf("thumbnail = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDigestSendTimeOnDSTChange(t *testing.T) {
	oslo, err := time.LoadLocation("Europe/Oslo")
	if err != nil {
		t.Skip(err)
	}
	h := newHarness(t, digestConfig("daily", "{}"))
	h.step(time.Date(2025, 10, 25, 0, 0, 0, 0, oslo))
	h.receive(time.Date(2025, 10, 25, 12, 0, 0, 0, oslo), h.github("star", "star_created"))
	// The clocks go back one hour at 03:00 on Sunday 26 October 2025.
	h.step(time.Date(2025, 10, 26, 8, 59, 0, 0, oslo))
	if got := h.digests(); len(got) != 0 {
		t.Fatalf("got %d messages before 09:00, want 0", len(got))
	}
	h.step(time.Date(2025, 10, 26, 9, 0, 0, 0, oslo))
	if got := h.oneDigest().Title; got != "-# Codertocat · Saturday 25 October\n## Daily digest" {
		t.Errorf("title = %q", got)
	}
}

func TestDigestPeriodWithoutEventsSendsNothing(t *testing.T) {
	h := newHarness(t, digestConfig("weekly", "{event: fork}"))
	h.step(day(9, 29, 0, 0))
	h.receive(day(10, 1, 12, 0), h.github("star", "star_created"))
	h.step(day(10, 6, 9, 0))
	h.step(day(10, 6, 9, 1))
	if got := h.digests(); len(got) != 0 {
		t.Errorf("got %d messages, want 0", len(got))
	}
}

func TestDigestPeriodWithOnlyIgnoredEventsSendsNothing(t *testing.T) {
	for _, includeOther := range []string{"", "false", "true"} {
		t.Run("include_other="+includeOther, func(t *testing.T) {
			h := newHarness(t, withOther(digestConfig("weekly", "{event: [star, watch]}"), includeOther))
			h.step(day(9, 29, 0, 0))
			h.receive(day(10, 1, 12, 0), h.github("watch", "watch_started"))
			unstar := bytes.Replace(fixture(t, "github/star_created"), []byte(`"action": "created"`), []byte(`"action": "deleted"`), 1)
			h.receive(day(10, 1, 12, 0), signedDelivery("github-autobrr", "star", unstar))
			h.step(day(10, 6, 9, 0))
			if got := h.digests(); len(got) != 0 {
				t.Errorf("got %d messages, want 0: %+v", len(got), got)
			}
		})
	}
}

func TestDigestPeriodWithOnlyOtherEvents(t *testing.T) {
	for _, includeOther := range []string{"", "false"} {
		t.Run("include_other="+includeOther, func(t *testing.T) {
			h := newHarness(t, withOther(digestConfig("weekly", "{}"), includeOther))
			h.step(day(9, 29, 0, 0))
			h.receive(day(10, 1, 12, 0), h.github("label", "label_created"))
			h.step(day(10, 6, 9, 0))
			if got := h.digests(); len(got) != 0 {
				t.Fatalf("got %d messages, want 0: %+v", len(got), got)
			}
			// The Period is empty. Opt-in does not send it again.
			h.restart(withOther(digestConfig("weekly", "{}"), "true"))
			h.step(day(10, 6, 10, 0))
			if got := h.digests(); len(got) != 0 {
				t.Errorf("got %d messages after opt-in, want 0: %+v", len(got), got)
			}
		})
	}
	t.Run("include_other=true", func(t *testing.T) {
		h := newHarness(t, withOther(digestConfig("weekly", "{}"), "true"))
		h.step(day(9, 29, 0, 0))
		h.receive(day(10, 1, 12, 0), h.github("label", "label_created"))
		h.step(day(10, 6, 9, 0))
		got := h.oneDigest()
		if want := "Other: **1** label.created\n### [qui](https://github.example.invalid/autobrr/qui)  ·  [Pulse](https://github.example.invalid/autobrr/qui/pulse)"; got.Description != want || got.Footer != "1 repository" {
			t.Errorf("description %q, footer %q, want %q and 1 repository", got.Description, got.Footer, want)
		}
	})
}

func TestDigestIncludeOtherAppliesToWholePeriod(t *testing.T) {
	config := func(includeOther string) string { return withOther(digestConfig("weekly", "{}"), includeOther) }
	h := newHarness(t, config("false"))
	h.step(day(9, 29, 0, 0))
	h.receive(day(9, 30, 12, 0), h.github("star", "star_created"))
	h.receive(day(9, 30, 12, 0), h.github("label", "label_created"))
	// Opt-in includes the earlier Other Event of the pending Period.
	h.restart(config("true"))
	h.step(day(10, 6, 9, 0))
	if got := h.oneDigest(); !strings.Contains(got.Description, "Other: **1** label.created") {
		t.Errorf("week 40 = %q, want the label after opt-in", got.Description)
	}
	h.receive(day(10, 7, 12, 0), h.github("star", "star_created"))
	h.receive(day(10, 7, 12, 0), h.github("label", "label_created"))
	// Opt-out leaves out the earlier Other Event of the pending Period. The
	// sent week 40 does not go again.
	h.restart(config("false"))
	h.step(day(10, 13, 9, 0))
	got := h.oneDigest()
	if got.Title != "-# Codertocat · week 41 · 6 October – 12 October\n## Weekly digest" || strings.Contains(got.Description, "Other") {
		t.Errorf("title %q, description %q, want week 41 without Other", got.Title, got.Description)
	}
}

func TestDigestIncludeOtherIsPerDigest(t *testing.T) {
	h := newHarness(t, digestConfig("weekly", "{}")+`
  - name: detailed
    every: weekly
    to: digest
    match: {}
    include_other: true
`)
	h.step(day(9, 29, 0, 0))
	h.receive(day(10, 1, 12, 0), h.github("star", "star_created"))
	h.receive(day(10, 1, 12, 0), h.otherOnly())
	h.step(day(10, 6, 9, 0))
	got := h.digests()
	if len(got) != 2 {
		t.Fatalf("got %d messages, want 2", len(got))
	}
	want := map[string]bool{"1 repository": false, "2 repositories": true}
	for _, e := range got {
		other, ok := want[e.Footer]
		if !ok || strings.Contains(e.Description, "Other: **1** label.created") != other {
			t.Errorf("footer %q, description %q", e.Footer, e.Description)
		}
		delete(want, e.Footer)
	}
}

func TestDigestCountsDroppedEvent(t *testing.T) {
	h := newHarness(t, digestConfig("weekly", "{event: star}"))
	h.step(day(9, 29, 0, 0))
	h.receive(day(10, 1, 12, 0), h.github("star", "star_created"))
	if got := h.decision()["outcome"]; got != "dropped" {
		t.Fatalf("outcome = %v, want dropped", got)
	}
	h.step(day(10, 6, 9, 0))
	if got := h.oneDigest(); !strings.Contains(got.Description, "**1** star") {
		t.Errorf("description = %q, want 1 star", got.Description)
	}
}

func TestDigestExcludesBotAuthor(t *testing.T) {
	h := newHarness(t, digestConfig("weekly", "{not: {author_bot: true}}"))
	h.step(day(9, 29, 0, 0))
	merged := fixture(t, "github/pull_request_merged")
	// The author is Renovate. A person merged the pull request.
	renovate := bytes.Replace(merged, []byte(`"login": "s0up4200"`), []byte(`"login": "renovate[bot]"`), 1)
	h.receive(day(10, 1, 12, 0), signedDelivery("github-autobrr", "pull_request", merged))
	h.receive(day(10, 1, 12, 0), signedDelivery("github-autobrr", "pull_request", renovate))
	h.step(day(10, 6, 9, 0))
	if got := h.oneDigest(); !strings.HasPrefix(got.Description, "**1** PR merged\n") {
		t.Errorf("description = %q, want 1 merged", got.Description)
	}
}

func TestDigestCutsLongMessage(t *testing.T) {
	h := newHarness(t, digestConfig("weekly", "{}"))
	h.step(day(9, 29, 0, 0))
	fork := fixture(t, "github/fork")
	for i := range 100 {
		body := bytes.ReplaceAll(fork, []byte("Codertocat/Hello-World"), fmt.Appendf(nil, "Codertocat/repository-with-a-long-name-%03d", i))
		h.receive(day(10, 1, 12, 0), signedDelivery("github-autobrr", "fork", body))
	}
	h.step(day(10, 6, 9, 0))
	h.restart(h.config)
	m := decodeMessage(t, h.waitDiscord().Body)
	if n := textLength(m.Components); n > maxText {
		t.Errorf("message has %d characters, want at most %d", n, maxText)
	}
	texts := displays(m.Components)
	also, footer, _ := strings.Cut(texts[len(texts)-1], "\n")
	shown := strings.Count(strings.Join(texts, ""), "/pulse)") + strings.Count(also, "](")
	if want := fmt.Sprintf(" · and **%d** more repositories", 100-shown); shown < 10 || !strings.HasPrefix(also, "-# Also merged: ") || !strings.HasSuffix(also, want) {
		t.Errorf("last line = %q, want more than 10 repositories and the end %q", also, want)
	}
	if footer != "-# 100 repositories" {
		t.Errorf("footer = %q, want 100 repositories", footer)
	}
}

func TestDigestCountsRedeliveryOnce(t *testing.T) {
	h := newHarness(t, digestConfig("weekly", "{}"))
	h.step(day(9, 29, 0, 0))
	h.clock = day(10, 1, 12, 0)
	for range 2 {
		h.do(h.github("star", "star_created")) // the same delivery ID
	}
	h.step(day(10, 6, 9, 0))
	if got := h.oneDigest(); !strings.Contains(got.Description, "**1** star") {
		t.Errorf("description = %q, want 1 star", got.Description)
	}
}

func TestDigestCatchUpAfterRestart(t *testing.T) {
	h := newHarness(t, digestConfig("weekly", "{}"))
	h.step(day(9, 1, 0, 0))
	for _, d := range []int{3, 10, 17, 24} {
		h.receive(day(9, d, 12, 0), h.github("star", "star_created"))
	}
	h.receive(day(10, 1, 12, 0), h.github("fork", "fork"))
	// Winnow was down from 2 September until after the send time of week 40.
	h.restart(h.config)
	h.step(day(10, 6, 10, 0))
	h.step(day(10, 6, 10, 1))
	if got := h.oneDigest(); got.Title != "-# Codertocat · week 40 · 29 September – 5 October\n## Weekly digest" {
		t.Errorf("title = %q, want week 40", got.Title)
	}
	var skipped []any
	for _, l := range h.linesWith("digest skipped") {
		skipped = append(skipped, l["period"])
	}
	if want := []any{"2025-09-22", "2025-09-15", "2025-09-08", "2025-09-01"}; fmt.Sprint(skipped) != fmt.Sprint(want) {
		t.Errorf("skipped = %v, want %v", skipped, want)
	}
	h.step(day(10, 6, 11, 0))
	if got := h.digests(); len(got) != 0 {
		t.Errorf("got %d messages after the restart, want 0", len(got))
	}
}

func TestDigestRetriesEachHourThenFails(t *testing.T) {
	h := newHarness(t, digestConfig("weekly", "{}"))
	h.step(day(9, 29, 0, 0))
	h.receive(day(10, 1, 12, 0), h.github("star", "star_created"))
	h.script(reply{status: 500}, reply{status: 500}, reply{status: 500}, reply{status: 500})
	h.step(day(10, 6, 9, 0))
	if got := h.waitFailure()["reason"]; got != "retries_exhausted" {
		t.Fatalf("reason = %v, want retries_exhausted", got)
	}
	for len(h.discord) > 0 {
		<-h.discord
	}
	h.step(day(10, 6, 9, 30))
	if n := len(h.discord); n != 0 {
		t.Fatalf("got %d sends before one hour, want 0", n)
	}
	h.script(reply{status: 400})
	h.step(day(10, 6, 10, 0))
	h.waitDiscord()
	for deadline := time.Now().Add(2 * time.Second); len(h.linesWith("delivery failed")) < 2; time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("no second failed delivery line")
		}
	}
	h.step(day(10, 7, 9, 0))
	failed := h.linesWith("digest failed")
	if len(failed) != 1 || failed[0]["period"] != "2025-09-29" {
		t.Fatalf("digest failed lines = %v, want one for 2025-09-29", failed)
	}
	h.step(day(10, 7, 10, 0))
	if got := h.digests(); len(got) != 0 {
		t.Errorf("got %d messages after the failed state, want 0", len(got))
	}
}

func TestDigestFirstRunIsPartial(t *testing.T) {
	tests := []struct {
		every       string
		first, send time.Time
	}{
		{"daily", day(10, 8, 15, 0), day(10, 9, 9, 0)},
		{"weekly", day(10, 8, 15, 0), day(10, 13, 9, 0)},
		{"monthly", day(10, 8, 15, 0), day(11, 1, 9, 0)},
		{"yearly", day(10, 8, 15, 0), time.Date(2026, 1, 1, 9, 0, 0, 0, cest)},
	}
	for _, tt := range tests {
		t.Run(tt.every, func(t *testing.T) {
			h := newHarness(t, digestConfig(tt.every, "{}"))
			h.step(tt.first)
			h.receive(tt.first.Add(time.Hour), h.github("star", "star_created"))
			if got := h.digests(); len(got) != 0 {
				t.Fatalf("got %d messages at the first start, want 0", len(got))
			}
			if got := h.linesWith("digest skipped"); len(got) != 0 {
				t.Fatalf("skipped lines = %v, want none", got)
			}
			h.step(tt.send)
			if got := h.oneDigest().Footer; got != "1 repository · from Wednesday 8 October" {
				t.Errorf("footer = %q", got)
			}
		})
	}
}

func TestDigestAddedLaterGetsOwnFirstRun(t *testing.T) {
	h := newHarness(t, digestConfig("weekly", "{}"))
	h.step(day(9, 29, 0, 0))
	h.receive(day(10, 1, 12, 0), h.github("star", "star_created"))
	h.restart(digestConfig("weekly", "{}") + `
  - name: later
    every: weekly
    to: digest
    match: {}
`)
	h.step(day(10, 1, 13, 0))
	h.receive(day(10, 1, 14, 0), h.github("star", "star_created"))
	h.step(day(10, 6, 9, 0))
	got := h.digests()
	if len(got) != 2 {
		t.Fatalf("got %d messages, want 2", len(got))
	}
	// The later Digest counts only the star after its first run.
	want := map[string]string{
		"1 repository": "**2** stars",
		"1 repository · from Wednesday 1 October": "**1** star\n",
	}
	for _, e := range got {
		if w, ok := want[e.Footer]; !ok || !strings.Contains(e.Description, w) {
			t.Errorf("footer %q, description %q: want %q", e.Footer, e.Description, w)
		}
	}
}

func TestDigestWiderMatchCountsOnlyNewEvents(t *testing.T) {
	config := func(match string) string {
		return digestConfig("weekly", match) + `
  - name: all
    every: weekly
    to: digest
    match: {}
`
	}
	h := newHarness(t, config("{ event: fork }"))
	h.step(day(9, 29, 0, 0))
	h.receive(day(9, 30, 12, 0), h.github("star", "star_created"))
	h.restart(config("{}"))
	h.receive(day(10, 1, 12, 0), h.github("star", "star_created"))
	h.step(day(10, 6, 9, 0))
	got := h.digests()
	if len(got) != 2 {
		t.Fatalf("got %d messages, want 2", len(got))
	}
	// The Digest "all" stored the first star. The wider Digest counts only
	// the star after the change.
	stars := map[string]bool{}
	for _, e := range got {
		first, _, _ := strings.Cut(e.Description, "\n")
		stars[first] = true
	}
	if !stars["**1** star"] || !stars["**2** stars"] {
		t.Errorf("first lines = %v, want one with 1 star and one with 2", stars)
	}
}

func TestDigestChangedEveryIsNewDigest(t *testing.T) {
	h := newHarness(t, digestConfig("daily", "{}"))
	h.step(day(9, 29, 0, 0))
	h.receive(day(10, 6, 12, 0), h.github("star", "star_created"))
	h.step(day(10, 7, 9, 0))
	if got := len(h.digests()); got != 1 {
		t.Fatalf("got %d daily messages, want 1", got)
	}
	// The daily Period of Monday 6 October has a state. The weekly Period
	// that starts on that Monday must not use it.
	h.restart(digestConfig("weekly", "{}"))
	h.step(day(10, 8, 9, 0))
	h.receive(day(10, 9, 12, 0), h.github("star", "star_created"))
	h.step(day(10, 13, 9, 0))
	got := h.oneDigest()
	// The weekly Digest counts from its first run, not the star of Monday.
	if !strings.HasPrefix(got.Description, "**1** star\n") || got.Footer != "1 repository · from Wednesday 8 October" {
		t.Errorf("description %q, footer %q", got.Description, got.Footer)
	}
}

func TestDigestNarrowerMatchAppliesToWholePeriod(t *testing.T) {
	h := newHarness(t, digestConfig("weekly", "{}"))
	h.step(day(9, 29, 0, 0))
	h.receive(day(9, 30, 12, 0), h.github("star", "star_created"))
	h.restart(digestConfig("weekly", "{ event: fork }"))
	h.step(day(10, 6, 9, 0))
	if got := h.digests(); len(got) != 0 {
		t.Errorf("got %d messages, want 0: the star is outside the new match", len(got))
	}
}

func TestDigestStoreOpensPathWithURICharacters(t *testing.T) {
	h := newHarness(t, digestConfig("weekly", "{}"))
	// A relative path must stay relative to the working directory.
	t.Chdir(h.dir)
	h.dir = "a#b?c%41d"
	if err := os.Mkdir(h.dir, 0o700); err != nil {
		t.Fatal(err)
	}
	h.restart(h.config)
	if _, err := os.Stat(filepath.Join(h.dir, "winnow.db")); err != nil {
		t.Error(err)
	}
}

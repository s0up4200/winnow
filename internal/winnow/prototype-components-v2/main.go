//go:build prototype

// PROTOTYPE, throwaway. Do not merge. Issue #12.
//
// Question: what should a winnow Event message look like as Discord
// Components V2? Round 2: only variant A, with real Markdown bodies through
// the real clean of winnow, body headings at their own size, and a new
// Digest layout. The last two messages compare one PR with and without the
// avatar thumbnail.
//
// Run:
//
//	WINNOW_PROTO_WEBHOOK='https://discord.com/api/webhooks/…' \
//	  go run -tags prototype ./internal/winnow/prototype-components-v2
//
// -dry prints the bodies and sends nothing. WINNOW_PROTO_PING=<discord user
// id> adds a Ping to the review.
package main

import (
	"bytes"
	_ "embed"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/s0up4200/winnow/internal/winnow"
)

const (
	githubIcon = "https://avatars.githubusercontent.com/u/9919?s=128"

	colorOpen     = 0x1f883d
	colorMerged   = 0x8250df
	colorStar     = 0xe3b341
	colorSecurity = 0xE36209

	qui = "https://github.com/autobrr/qui"
	ab  = "https://github.com/autobrr/autobrr"
)

// The real bodies, fetched with gh on 2026-10-10.
var (
	//go:embed bodies/release.md
	releaseBody string
	//go:embed bodies/pr2752.md
	pr2752Body string
	//go:embed bodies/issue2705.md
	issue2705Body string
	//go:embed bodies/pr3041.md
	pr3041Body string
	//go:embed bodies/issue3109.md
	issue3109Body string
)

// card is the content of one Event message.
type card struct {
	ping      string // Discord user ID, or empty
	color     int
	sender    string
	senderURL string
	thumb     string // avatar URL, or empty for no thumbnail
	heading   string
	url       string
	body      string // raw markdown; the real clean runs on it
	repoURL   string // for the References in body; empty means body is already final
	buttons   [][2]string
}

func av(login string) string { return "https://github.com/" + login + ".png?size=128" }
func gh(login string) string { return "https://github.com/" + login }
func prs(repo, author string) string {
	return repo + "/pulls?q=" + url.QueryEscape("is:pr author:"+author)
}

func cards(ping string) []card {
	return []card{
		{ // pull_request.closed merged: no excerpt
			color: colorMerged, sender: "s0up4200", senderURL: gh("s0up4200"), thumb: av("s0up4200"),
			heading: "[autobrr/qui] Pull request merged: #3110 feat(automations): decode rule JSON strictly and list every error", url: qui + "/pull/3110",
			buttons: [][2]string{{"Diff", qui + "/pull/3110/files"}, {"Checks", qui + "/pull/3110/checks"}, {"PRs by s0up4200", prs(qui, "s0up4200")}},
		},
		{ // pull_request.opened
			color: colorOpen, sender: "zze0s", senderURL: gh("zze0s"), thumb: av("zze0s"),
			heading: "[autobrr/autobrr] Pull request opened: #2752 feat(actions): rTorrent ratio group and priority", url: ab + "/pull/2752",
			body: pr2752Body, repoURL: ab,
			buttons: [][2]string{{"Diff", ab + "/pull/2752/files"}, {"Checks", ab + "/pull/2752/checks"}, {"PRs by zze0s", prs(ab, "zze0s")}},
		},
		{ // pull_request_review.submitted: the Author is not the sender
			ping: ping, color: colorOpen, sender: "zze0s", senderURL: gh("zze0s"), thumb: av("zze0s"),
			heading: "[autobrr/autobrr] Review approved: #2746 feat(arr): send tvdbId to Sonarr and use feed publish date", url: ab + "/pull/2746#pullrequestreview-5429391802",
			body: "Thanks for the PR! Good improvement 👍", repoURL: ab,
			buttons: [][2]string{{"PR", ab + "/pull/2746"}, {"Diff", ab + "/pull/2746/files"}, {"Checks", ab + "/pull/2746/checks"},
				{"GeneralPractitioner-GP", gh("GeneralPractitioner-GP")}, {"PRs by GeneralPractitioner-GP", prs(ab, "GeneralPractitioner-GP")}},
		},
		{ // issues.opened
			color: colorOpen, sender: "Terrails", senderURL: gh("Terrails"), thumb: av("Terrails"),
			heading: "[autobrr/autobrr] Issue opened: #2705 Duplicate detection not comparing release name together with hash resulting in false matches", url: ab + "/issues/2705",
			body: issue2705Body, repoURL: ab,
			buttons: [][2]string{{"Issues by Terrails", ab + "/issues?q=" + url.QueryEscape("is:issue author:Terrails")}},
		},
		{ // issues.opened in qui
			color: colorOpen, sender: "s0up4200", senderURL: gh("s0up4200"), thumb: av("s0up4200"),
			heading: "[autobrr/qui] Issue opened: #3109 refactor(web): generate the query builder field constants from the Go field table", url: qui + "/issues/3109",
			body: issue3109Body, repoURL: qui,
			buttons: [][2]string{{"Issues by s0up4200", qui + "/issues?q=" + url.QueryEscape("is:issue author:s0up4200")}},
		},
		{ // pull_request.opened by a bot
			color: colorOpen, sender: "dependabot[bot]", senderURL: "https://github.com/apps/dependabot", thumb: av("dependabot[bot]"),
			heading: "[autobrr/qui] Pull request opened: #3041 chore(deps): bump proxy-addr from 2.0.7 to 2.0.8 in /documentation", url: qui + "/pull/3041",
			body: pr3041Body, repoURL: qui,
			buttons: [][2]string{{"Diff", qui + "/pull/3041/files"}, {"Checks", qui + "/pull/3041/checks"}, {"PRs by dependabot", prs(qui, "app/dependabot")}},
		},
		{ // push
			sender: "s0up4200", senderURL: gh("s0up4200"), thumb: av("s0up4200"),
			heading: "[autobrr/qui:main] 3 new commits", url: qui + "/compare/3cd6f29^...5b897ae",
			// The author of each commit is the pusher, so the lines leave it out.
			body: "[`5b897ae`](" + qui + "/commit/5b897ae) chore(docs): build faster and serve page Markdown from the site (#3102)\n" +
				"[`f14ed35`](" + qui + "/commit/f14ed35) feat(docs): add a release notes page from each GitHub release (#3099)\n" +
				"[`3cd6f29`](" + qui + "/commit/3cd6f29) ci(triage): triage only bug discussions automatically (#3075)", repoURL: qui,
		},
		{ // release.published: the owner icon, the real notes
			sender: "github-actions[bot]", senderURL: "https://github.com/apps/github-actions", thumb: "https://github.com/autobrr.png?size=128",
			heading: "[autobrr/qui] Release published: v1.31.1", url: qui + "/releases/tag/v1.31.1",
			body: releaseBody, repoURL: qui,
		},
		{ // dependabot_alert.created
			color: colorSecurity, sender: "dependabot[bot]", senderURL: "https://github.com/apps/dependabot", thumb: av("dependabot[bot]"),
			heading: "[autobrr/qui] Dependabot alert created: #258 source-map-js allows event-loop denial of service through indexed source-map section offsets", url: qui + "/security/dependabot/258",
			body:    "Severity: high\nPackage: source-map-js (npm)\nPatched in: 1.2.2",
			buttons: [][2]string{{"Advisory", "https://github.com/advisories/GHSA-68fv-2mgg-jv7q"}},
		},
		{ // star.created
			color: colorStar, sender: "bartmasz", senderURL: gh("bartmasz"), thumb: av("bartmasz"),
			heading: "[autobrr/qui] New star", url: qui,
		},
		{ // Fallback message
			heading: "label.created on autobrr/qui by s0up4200", url: qui + "/labels",
		},
	}
}

type obj = map[string]any

func text(s string) obj { return obj{"type": 10, "content": s} }

func row(bs [][2]string) obj {
	var cs []obj
	for _, b := range bs {
		cs = append(cs, obj{"type": 2, "style": 5, "label": b[0], "url": b[1]})
	}
	return obj{"type": 1, "components": cs}
}

// excerpt cleans the body with the real winnow code, cut to what the 4000
// characters leave after the other text of the message.
func excerpt(c card, used int) string {
	if c.repoURL == "" {
		return c.body
	}
	return winnow.PrototypeExcerpt(c.body, c.repoURL, 4000-used)
}

// shortBody is the largest body, in visible characters, that goes into the
// Section.
const shortBody = 400

// mdLink matches a markdown link, so that the length test counts only its
// text.
var mdLink = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)

// shortHeading is the longest heading, in visible characters, that leaves a
// gap beside the thumbnail. ponytail: a guess for desktop width; a phone
// wraps sooner.
const shortHeading = 60

// split returns the first lines of body, up to about three lines of text
// beside the thumbnail, and the rest. It never splits in a code block, and
// it stops at a blank line after the first line.
func split(body string) (top, rest string) {
	lines := strings.Split(body, "\n")
	size, i := 0, 0
	for ; i < len(lines) && i < 3; i++ {
		l := lines[i]
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") || (t == "" && i > 0) {
			break
		}
		size += visible(l)
		if size > 160 {
			break
		}
	}
	return strings.Join(lines[:i], "\n"), strings.TrimLeft(strings.Join(lines[i:], "\n"), "\n")
}

func visible(s string) int { return n(mdLink.ReplaceAllString(s, "$1")) }

func n(s string) int { return utf8.RuneCountInString(s) }

// layout turns c into the components of variant A.
func layout(c card) []obj {
	var head []obj
	sender := ""
	if c.sender != "" {
		sender = "-# [" + c.sender + "](" + c.senderURL + ")"
		head = append(head, text(sender))
	}
	heading := "## [" + c.heading + "](" + c.url + ")"

	used := n(sender) + n(heading) + n(c.ping) + 3
	for _, b := range c.buttons {
		used += n(b[0])
	}
	body := excerpt(c, used)
	// A short body goes into the Section, beside the thumbnail, so that the
	// thumbnail leaves no gap under the heading. A long body goes under the
	// Section and gets the full width.
	short := body != "" && visible(body) <= shortBody
	rest := body
	// The first body line shares the Text Display of the heading, so that
	// Discord puts the heading margin under the heading, as between two
	// headings in a body. Separate Text Displays sit closer.
	top := ""
	switch {
	case short:
		top, rest = body, ""
	case body != "" && c.thumb != "" && visible(c.heading) <= shortHeading:
		// A short heading leaves a gap beside the thumbnail. The first lines
		// of a long body fill it.
		top, rest = split(body)
	}
	if top != "" {
		heading += "\n" + top
	}
	head = append(head, text(heading))
	var in []obj
	if c.thumb != "" {
		in = append(in, obj{"type": 9, "components": head, "accessory": obj{"type": 11, "media": obj{"url": c.thumb}}})
	} else {
		in = append(in, head...)
	}
	if rest != "" {
		in = append(in, text(rest))
	}
	if len(c.buttons) > 0 {
		in = append(in, obj{"type": 14}, row(c.buttons))
	}
	return wrap(c.color, c.ping, in)
}

func wrap(color int, ping string, in []obj) []obj {
	box := obj{"type": 17, "components": in}
	if color != 0 {
		box["accent_color"] = color
	}
	if ping == "" {
		return []obj{box}
	}
	return []obj{text("<@" + ping + ">"), box}
}

func message(comps []obj, ping string) obj {
	am := obj{"parse": []string{}}
	if ping != "" {
		am["users"] = []string{ping}
	}
	return obj{"username": "GitHub", "avatar_url": githubIcon, "flags": 32768, "allowed_mentions": am, "components": comps}
}

// digest is the new Digest layout, with the real counts of the autobrr org
// for week 41 so far (gh search on 2026-10-10). Stars counted for qui,
// autobrr, and mkbrr only. Round 4 tries to fill the width: the header is a
// Section with the org icon, and each of the top repositories is a Text Display
// with an inline Pulse link after its release tag. The rest share one line.
func digest() []obj {
	type repo struct {
		name                                        string
		merged, opened, issOpened, issClosed, stars int
		tag                                         string
	}
	repos := []repo{
		{"qui", 41, 83, 36, 21, 19, "v1.31.1"},
		{"upbrr", 29, 67, 48, 37, 0, ""},
		{"mkbrr", 22, 11, 5, 7, 2, "v1.27.0"},
		{"netronome", 12, 4, 3, 5, 0, "v0.16.0"},
		{"autobrr", 9, 14, 1, 1, 20, "v1.88.0"},
		{"distribrr", 8, 6, 0, 0, 0, ""},
		{"dashbrr", 3, 4, 2, 3, 0, ""},
		{"go-rtorrent", 3, 2, 0, 0, 0, ""},
		{"autobrr.com", 2, 2, 0, 0, 0, ""},
		{"rls", 1, 0, 0, 0, 0, ""},
	}
	const top = 6
	in := []obj{
		{"type": 9, "components": []obj{
			text("-# autobrr · week 41 · Oct 5 – Oct 11"),
			text("## [Weekly digest](https://github.com/autobrr)"),
			text("**130** PRs merged · **200** opened · **3** closed\n" +
				"**96** issues opened · **74** closed · **4** releases · **41** ★"),
		}, "accessory": obj{"type": 11, "media": obj{"url": "https://github.com/autobrr.png?size=128"}}},
		{"type": 14, "spacing": 2},
	}
	var rest []string
	for i, r := range repos {
		u := "https://github.com/autobrr/" + r.name
		var parts []string
		add := func(k int, label string) {
			if k > 0 {
				parts = append(parts, fmt.Sprintf("**%d** %s", k, label))
			}
		}
		if i >= top {
			rest = append(rest, "["+r.name+"]("+u+") "+fmt.Sprint(r.merged))
			continue
		}
		add(r.merged, "merged")
		add(r.opened, "PRs opened")
		add(r.issOpened, "issues opened")
		add(r.issClosed, "closed")
		add(r.stars, "★")
		line := "### [" + r.name + "](" + u + ")"
		if r.tag != "" {
			line += "  ·  [" + r.tag + "](" + u + "/releases/tag/" + r.tag + ")"
		}
		line += "  ·  [Pulse](" + u + "/pulse)"
		in = append(in, text(line+"\n"+strings.Join(parts, " · ")))
	}
	in = append(in,
		obj{"type": 14},
		text("-# Also merged: "+strings.Join(rest, " · ")+"\n-# 10 repositories · from Oct 5"),
	)
	return wrap(colorStar, "", in)
}

func main() {
	dry := flag.Bool("dry", false, "print the bodies, send nothing")
	flag.Parse()

	hook := os.Getenv("WINNOW_PROTO_WEBHOOK")
	if hook == "" && !*dry {
		fmt.Fprintln(os.Stderr, "set WINNOW_PROTO_WEBHOOK, or pass -dry")
		os.Exit(2)
	}
	if hook != "" {
		u, err := url.Parse(hook)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		q := u.Query()
		q.Set("with_components", "true")
		u.RawQuery = q.Encode()
		hook = u.String()
	}

	label := func(s string) obj {
		return obj{"username": "winnow prototype", "flags": 32768, "allowed_mentions": obj{"parse": []string{}}, "components": []obj{text(s)}}
	}
	ping := os.Getenv("WINNOW_PROTO_PING")
	msgs := []obj{label("# Variant A, round 7: space under the heading")}
	for _, c := range cards(ping) {
		msgs = append(msgs, message(layout(c), c.ping))
	}
	msgs = append(msgs, message(digest(), ""))

	for _, m := range msgs {
		b, _ := json.Marshal(m, jsontext.WithIndent("  "))
		if *dry {
			fmt.Println(string(b))
			continue
		}
		post(hook, b)
	}
}

// post sends b and waits after a 429. ponytail: fixed 1 s spacing, fine for a
// dozen messages.
func post(hook string, b []byte) {
	for {
		resp, err := http.Post(hook, "application/json", bytes.NewReader(b))
		if err != nil {
			fmt.Fprintln(os.Stderr, "post:", err)
			os.Exit(1)
		}
		reply, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		switch {
		case resp.StatusCode == http.StatusTooManyRequests:
			time.Sleep(2 * time.Second)
			continue
		case resp.StatusCode/100 != 2:
			fmt.Fprintf(os.Stderr, "status %d: %s\nbody: %s\n", resp.StatusCode, reply, b)
			os.Exit(1)
		}
		fmt.Println("sent", resp.StatusCode)
		time.Sleep(time.Second)
		return
	}
}

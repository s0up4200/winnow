package winnow

import (
	"regexp"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/text"
)

// pingKindNames holds the Ping kinds. mentions: true means all of them.
var pingKindNames = []string{"review_requested", "assigned", "comments", "sponsorship"}

// pingKinds is the set of Ping kinds that a Sink keeps. Nil keeps no kind.
type pingKinds map[string]bool

// maxPingUsers is the Discord limit of user IDs in allowed_mentions.
const maxPingUsers = 100

// maxTargetPings is the largest number of people that one review request,
// assignment, or sponsorship card pings. A Burst seldom names more people.
const maxTargetPings = 10

// pingsOf prepares the Pings of e for the User map users and the Team map
// teams. It returns a function that gives the Ping text and the user IDs for
// the Ping kinds of one Sink. Mention scanning runs only when needed, once per
// Event. Each call returns its own ID list.
func pingsOf(e *Event, users map[string]string, teams map[string][]string) func(pingKinds) (string, []string) {
	kind, logins := pinged(e, teams)
	limit := maxTargetPings
	if kind == "comments" {
		limit = maxPingUsers
	}
	mapped := sync.OnceValue(func() []string {
		var ids []string
		for _, login := range logins() {
			if id, ok := users[login]; ok && !slices.Contains(ids, id) {
				ids = append(ids, id)
				if len(ids) == limit {
					break
				}
			}
		}
		return ids
	})
	return func(kinds pingKinds) (text string, ids []string) {
		if !kinds[kind] || len(users) == 0 {
			return "", nil
		}
		for _, id := range mapped() {
			mention := "<@" + id + ">"
			if text != "" {
				mention = " " + mention
			}
			// 2000 characters leave room for the rest of the message.
			if len(text)+len(mention) > 2000 {
				break
			}
			text += mention
			ids = append(ids, id)
		}
		return text, ids
	}
}

// pinged returns the Ping kind of e and a function that returns the logins
// that e pings: lowercase, once each, in order, never the sender. A review
// request or an assignment pings its people, with the members of each team
// in teams in place of the team. A new, cancelled, or changed sponsorship
// pings the sponsored account. A new comment, a submitted review summary,
// and the description of a new issue, pull request, or discussion ping each
// login that the text mentions. Only these actions ping, so that each change
// pings once. The text is scanned on the first call of the function for e,
// and teams must be the same on each call.
func pinged(e *Event, teams map[string][]string) (kind string, logins func() []string) {
	var targets []string
	switch e.NameAction() {
	case "issue_comment.created", "pull_request_review_comment.created", "discussion_comment.created",
		"pull_request_review.submitted", "issues.opened", "pull_request.opened", "discussion.created":
		if e.mentions == nil {
			e.mentions = sync.OnceValue(func() []string { return commentMentions(e.Body, e.Sender, teams) })
		}
		return "comments", e.mentions
	}
	switch {
	case e.Sponsorship != nil:
		switch e.Action {
		case "created", "cancelled", "tier_changed":
			kind, targets = "sponsorship", []string{e.Sponsorship.Sponsorable}
		}
	case e.Action == "review_requested", e.Action == "assigned":
		kind = e.Action
		for _, s := range e.Subjects {
			if s.Team {
				targets = append(targets, teams[strings.ToLower(s.Name)]...)
			} else {
				targets = append(targets, s.Name)
			}
		}
	}
	var out []string
	for _, t := range targets {
		t = strings.ToLower(t)
		if !strings.EqualFold(t, e.Sender) && !slices.Contains(out, t) {
			out = append(out, t)
		}
	}
	return kind, func() []string { return out }
}

// commentMarkdown parses comments as GitHub Flavored Markdown. Linkify turns
// bare URLs into autolinks, so a login in a URL stays quiet. GitHub shows a
// footnote reference as a number, so its label stays quiet.
var commentMarkdown = goldmark.New(goldmark.WithExtensions(extension.GFM, extension.Footnote))

// maxMentionScan is the size in bytes of the largest text that can ping.
// The parser builds a node for each paragraph, so a 25 MiB comment can use
// gigabytes of memory. GitHub keeps at most 65,536 characters in a comment, so
// a GitHub comment always fits.
const maxMentionScan = 256 << 10

// mentionLogin matches @login and @org/slug. Group 2 is the /slug of a team.
var mentionLogin = regexp.MustCompile(`@([[:alnum:]_][[:alnum:]_.-]*)(/[[:alnum:]_-]+)?`)

// commentMentions reads a comment, a review summary, or a description and
// returns the logins that it mentions: lowercase, once each, in text order,
// never the sender. A mention of a team in teams adds its members in list
// order, never the org. It leaves quoted text, code, HTML blocks and tags,
// and URLs quiet. A text longer than maxMentionScan returns no logins.
func commentMentions(body, sender string, teams map[string][]string) []string {
	if len(body) > maxMentionScan {
		return nil
	}
	src := []byte(body)
	var logins []string
	seen := map[string]bool{}
	add := func(login string) {
		login = strings.ToLower(login)
		if !seen[login] && !strings.EqualFold(login, sender) {
			seen[login] = true
			logins = append(logins, login)
		}
	}
	expanded := map[string]bool{} // a repeated team mention adds no one, so expand each team once
	// scan finds the mentions in src[start:stop].
	scan := func(start, stop int) {
		// Take one match at a time. A long comment can hold millions.
		for start < stop {
			loc := mentionLogin.FindSubmatchIndex(src[start:stop])
			if loc == nil {
				return
			}
			i, j := start+loc[0], start+loc[1]
			start = j
			before, _ := utf8.DecodeLastRune(src[:i])
			after, _ := utf8.DecodeRune(src[j:])
			if escapedAt(body, i) || isWord(before) || strings.ContainsRune("<@./+-", before) || isWord(after) || after == '/' {
				continue
			}
			if loc[4] < 0 {
				add(strings.TrimRight(body[i+1:j], "."))
				continue
			}
			team := strings.ToLower(body[i+1 : j])
			if !expanded[team] {
				expanded[team] = true
				for _, member := range teams[team] {
					add(member)
				}
			}
		}
	}
	// The parser can split a login over adjacent Text nodes, so scan each
	// run of adjacent text as one.
	runStart, runStop := 0, 0
	flush := func() {
		scan(runStart, runStop)
		runStart, runStop = 0, 0
	}
	doc := commentMarkdown.Parser().Parse(text.NewReader(src))
	// The walk function never returns an error.
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n := n.(type) {
		case *ast.Blockquote, *ast.CodeBlock, *ast.FencedCodeBlock, *ast.CodeSpan, *ast.HTMLBlock, *ast.RawHTML, *ast.AutoLink, *ast.Image:
			return ast.WalkSkipChildren, nil
		case *ast.Text:
			if n.Segment.Start != runStop {
				flush()
				runStart = n.Segment.Start
			}
			runStop = n.Segment.Stop
		}
		return ast.WalkContinue, nil
	})
	flush()
	return logins
}

// escapedAt reports whether an odd number of backslashes precedes i.
func escapedAt(text string, i int) bool {
	n := 0
	for i > 0 && text[i-1] == '\\' {
		n++
		i--
	}
	return n%2 != 0
}

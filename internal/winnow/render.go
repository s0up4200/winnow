package winnow

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// The embed colors, from the GitHub color scheme.
const (
	colorOpen   = 0x1f883d
	colorClosed = 0xcf222e
	colorMerged = 0x8250df
	colorStar   = 0xe3b341
	// colorSecurity is the embed color of a security Event.
	colorSecurity = 0xE36209
)

// The Discord limits of an embed, in characters.
const (
	maxTitle       = 256
	maxDescription = 4096
)

// render turns an Event into one Discord message. It selects the Renderer by
// the Event name. An Event name with no Renderer, or a Renderer that returns
// the zero embed, gets the Fallback message. The message pings the target of
// e when users holds the target and the target is not the sender. A Fallback
// message never pings.
func render(e *Event, users map[string]string) message {
	var em embed
	switch e.Name {
	case "issues":
		em = renderIssue(e)
	case "pull_request":
		em = renderPullRequest(e)
	case "issue_comment":
		em = renderIssueComment(e)
	case "pull_request_review":
		em = renderReview(e)
	case "pull_request_review_comment":
		em = titled(e, "New review comment")
	case "discussion":
		em = titled(e, "Discussion "+words(e.Action))
	case "discussion_comment":
		em = titled(e, "New comment on discussion")
	case "push":
		em = renderPush(e)
	case "release":
		em = titled(e, "Release "+words(e.Action))
	case "fork":
		em = titled(e, "Fork created")
	case "watch":
		em = titled(e, "New star")
		em.Color = colorStar
	case "dependabot_alert":
		em = renderAlert(e, "Dependabot alert")
	case "code_scanning_alert":
		em = renderAlert(e, "Code scanning alert")
	case "secret_scanning_alert":
		em = renderAlert(e, "Secret scanning alert")
	case "repository_advisory":
		em = renderAlert(e, "Repository advisory")
	}
	ping := em != (embed{})
	if !ping {
		em = fallback(e)
	}
	em.Title = cut(em.Title, maxTitle)
	em.Description = cut(em.Description, maxDescription)
	msg := message{Embeds: []embed{em}}
	if id, ok := users[strings.ToLower(e.Target)]; ok && ping && e.Target != "" && !strings.EqualFold(e.Target, e.Sender) {
		msg.Content = "<@" + id + ">"
		msg.AllowedMentions.Users = []string{id}
	}
	return msg
}

// fallback returns the embed of the Fallback message: "<event>.<action> on
// <repo> by <sender>", with a link to the main object. It reads only the
// shared fields of the Event and never pings.
func fallback(e *Event) embed {
	return embed{Title: e.NameAction() + " on " + e.Repo + " by " + e.Sender, URL: e.URL}
}

// titled returns an embed with the sender as author, the title
// "[repo] <kind>: #n title", the link to the main object, and the body.
func titled(e *Event, kind string) embed {
	title := "[" + e.Repo + "] " + kind
	switch {
	case e.Number > 0:
		title += ": #" + strconv.Itoa(e.Number) + " " + e.Title
	case e.Title != "":
		title += ": " + e.Title
	}
	return embed{
		Author:      embedAuthor{Name: e.Sender, URL: e.SenderURL, IconURL: e.SenderAvatar},
		Title:       title,
		URL:         e.URL,
		Description: e.Body,
	}
}

func renderIssue(e *Event) embed {
	em := titled(e, "Issue "+e.Action)
	em.Color = stateColor(e.Action)
	return em
}

// renderPullRequest shows a merge as "merged". A merge is the action closed
// with merged true.
func renderPullRequest(e *Event) embed {
	kind, color := words(e.Action), stateColor(e.Action)
	if e.Action == "closed" && e.Merged != nil && *e.Merged {
		kind, color = "merged", colorMerged
	}
	em := titled(e, "Pull request "+kind)
	em.Color = color
	return em
}

func renderIssueComment(e *Event) embed {
	if e.IsPull != nil && *e.IsPull {
		return titled(e, "New comment on pull request")
	}
	return titled(e, "New comment on issue")
}

// renderReview shows the review state, for example "Review approved".
func renderReview(e *Event) embed {
	var state string
	if e.ReviewState != nil {
		state = *e.ReviewState
	}
	em := titled(e, "Review "+words(state))
	switch state {
	case "approved":
		em.Color = colorOpen
	case "changes_requested":
		em.Color = colorClosed
	}
	return em
}

// renderPush shows one line for each commit, or the branch or tag that the
// push made or deleted.
func renderPush(e *Event) embed {
	var ref string
	if e.Ref != nil {
		ref = *e.Ref
	}
	kind := "Branch"
	name, ok := strings.CutPrefix(ref, "refs/heads/")
	if !ok {
		kind, name = "Tag", strings.TrimPrefix(ref, "refs/tags/")
	}
	p := e.Push
	switch {
	case p.Deleted:
		return titled(e, kind+" deleted: "+name)
	case p.Created && p.Size == 0:
		return titled(e, kind+" created: "+name)
	}
	em := titled(e, "")
	em.Title = fmt.Sprintf("[%s:%s] %d new commit", e.Repo, name, p.Size)
	if p.Size != 1 {
		em.Title += "s"
	}
	lines := make([]string, 0, len(p.Commits))
	for _, c := range p.Commits {
		msg, _, _ := strings.Cut(c.Message, "\n")
		lines = append(lines, fmt.Sprintf("[`%s`](%s) %s - %s", c.ID[:min(7, len(c.ID))], c.URL, msg, c.Author))
	}
	em.Description = strings.Join(lines, "\n")
	return em
}

// renderAlert returns the embed of a security Event: the sender as author,
// "[repo] <kind> <action>: #n summary", and one description line for each
// Alert field that is set. An Event with no Alert gets the zero embed.
func renderAlert(e *Event, kind string) embed {
	a := e.Alert
	if a == nil {
		return embed{}
	}
	title := "[" + e.Repo + "] " + kind + " " + e.Action + ": "
	if a.Number != 0 {
		title += "#" + strconv.Itoa(a.Number) + " "
	}
	var lines []string
	add := func(label, value string) {
		if value != "" {
			lines = append(lines, label+": "+value)
		}
	}
	add("Severity", a.Severity)
	if a.Package != "" {
		add("Package", a.Package+" ("+a.Ecosystem+")")
	}
	add("Patched in", a.Patched)
	add("Validity", a.Validity)
	// titled sets the author. A security Event can come with no sender. Then
	// the author is empty and the embed has no author.
	em := titled(e, "")
	em.Title, em.Description, em.Color = title+a.Summary, strings.Join(lines, "\n"), colorSecurity
	return em
}

// words changes an action such as review_requested to "review requested".
func words(action string) string {
	return strings.ReplaceAll(action, "_", " ")
}

// stateColor returns the color for an action that opens or closes an object.
func stateColor(action string) int {
	switch action {
	case "opened", "reopened":
		return colorOpen
	case "closed":
		return colorClosed
	}
	return 0
}

// cut cuts s to at most n characters. A cut text ends with "…".
func cut(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}

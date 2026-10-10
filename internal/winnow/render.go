package winnow

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// The icons of the posters. The GitHub favicon is transparent, so GitHub
// posts with the avatar of the GitHub organization.
const (
	githubIcon  = "https://avatars.githubusercontent.com/u/9919?s=128"
	forgejoIcon = "https://forgejo.org/favicon.png"
)

// The accent colors of the Containers, from the GitHub color scheme.
const (
	colorOpen   = 0x1f883d
	colorClosed = 0xcf222e
	colorMerged = 0x8250df
	colorStar   = 0xe3b341
	// colorSecurity is the color of a security Event.
	colorSecurity = 0xE36209
)

// The limits of a message, in characters. Discord counts the text of all
// components in one message together. maxTitle is the limit of winnow for
// the heading.
const (
	maxTitle = 256
	maxText  = 4000
)

// shortBody is the largest body, in visible characters, that goes into the
// Section beside the thumbnail.
const shortBody = 400

// shortTitle is the longest heading, in characters, that leaves a gap beside
// the thumbnail. The first lines of a long body then fill the gap.
//
// ponytail: a guess for the desktop width. A phone wraps sooner.
const shortTitle = 60

// maxPingUsers is the Discord limit of user IDs in allowed_mentions.
const maxPingUsers = 100

// maxExcerptBody is the number of bytes of a body that excerpt reads. Only
// 4000 characters reach Discord, so a larger body only costs memory.
//
// ponytail: when the cut is in an HTML comment, the comment has no end, so
// clean keeps its text. Raise the limit if a real body has a comment of
// 256 KiB.
const maxExcerptBody = 256 << 10

// render turns an Event into one Discord message. It selects the Renderer by
// the Event name. An Event name with no Renderer, or a Renderer that returns
// the zero card, gets the Fallback message. The Poster of each message is
// the forge of the Event. The message pings the target of e when the User
// map holds the target and the target is not the sender. New comments also
// ping mapped logins in ordinary text. Each Ping needs its kind in p. A
// Fallback message never pings. A release shows the Icon in icons.
func render(e *Event, p pings, icons map[string]string) message {
	var c card
	switch e.Name {
	case "issues":
		c = renderIssue(e)
	case "pull_request":
		c = renderPullRequest(e)
	case "issue_comment":
		c = renderIssueComment(e)
	case "pull_request_review":
		c = renderReview(e)
	case "pull_request_review_comment":
		c = titled(e, "New review comment")
	case "discussion":
		c = titled(e, "Discussion "+words(e.Action))
	case "discussion_comment":
		c = titled(e, "New comment on discussion")
	case "push":
		c = renderPush(e)
	case "release":
		c = titled(e, "Release "+words(e.Action))
		c.thumb = releaseThumb(e, icons)
	case "fork":
		c = titled(e, "Fork created")
	case "watch", "star":
		// GitHub sends watch.started and star.created for one star. An
		// unstar, star.deleted, gets the Fallback message.
		if e.Action != "deleted" {
			c = titled(e, "New star")
			c.color = colorStar
		}
	case "dependabot_alert":
		c = renderAlert(e, "Dependabot alert")
	case "code_scanning_alert":
		c = renderAlert(e, "Code scanning alert")
	case "secret_scanning_alert":
		c = renderAlert(e, "Secret scanning alert")
	case "repository_advisory":
		c = renderAlert(e, "Repository advisory")
	}
	ping := c.title != ""
	if ping {
		c.buttons = buttons(e)
	} else {
		c = fallback(e)
	}
	msg := message{Username: "GitHub", AvatarURL: githubIcon}
	if e.Forge == "forgejo" {
		msg.Username, msg.AvatarURL = "Forgejo", forgejoIcon
	}
	var mentions string // the Ping text
	if id, ok := p.users[strings.ToLower(e.Target)]; ok && ping && p.kinds[e.Action] && e.Target != "" && !strings.EqualFold(e.Target, e.Sender) {
		mentions = "<@" + id + ">"
		msg.AllowedMentions.Users = []string{id}
	}
	if ping && p.kinds["comments"] && len(p.users) > 0 {
		switch e.NameAction() {
		case "issue_comment.created", "pull_request_review_comment.created", "discussion_comment.created":
			for _, id := range commentMentions(e.Body, e.Sender, p.users) {
				mention := "<@" + id + ">"
				if mentions != "" {
					mention = " " + mention
				}
				// 2000 characters leave room for the rest of the message.
				if len(msg.AllowedMentions.Users) == maxPingUsers || len(mentions)+len(mention) > 2000 {
					break
				}
				mentions += mention
				msg.AllowedMentions.Users = append(msg.AllowedMentions.Users, id)
			}
		}
	}
	msg.Components = layout(c, mentions)
	return msg
}

// card is the content of one Event message, before the layout.
type card struct {
	color             int
	sender, senderURL string // the small line over the heading, or empty
	thumb             string // the image beside the heading, or empty
	title, url        string // the heading and its link
	body              string // the excerpt, not cut yet
	buttons           []button
}

// layout returns the components of the message of c. The Ping is a Text
// Display above the Container, so that it shows outside the box. In the
// Container, a Section holds the sender line and the heading, with the
// thumbnail on its right side. A short body goes into the Section, under the
// heading. A long body goes under the Section at full width, but when the
// heading is short, its first lines fill the gap beside the thumbnail. The
// buttons come last, after a Separator. The body gets the part of maxText
// that the other text leaves, and layout cuts it at a word boundary.
func layout(c card, ping string) []component {
	var head []component
	used := utf8.RuneCountInString(ping)
	if c.sender != "" {
		head = append(head, display("-# "+link(c.sender, c.senderURL)))
		used += utf8.RuneCountInString(head[0].Content)
	}
	heading := "## " + link(cut(c.title, maxTitle), c.url)
	used += utf8.RuneCountInString(heading) + 1 // 1 for the newline before the body
	for _, b := range c.buttons {
		used += utf8.RuneCountInString(b.label)
	}
	var body string
	if room := maxText - used; room > 0 {
		body = cutWords(c.body, room)
	}
	top, rest := "", body
	switch {
	case body == "":
	case visible(body) <= shortBody:
		top, rest = body, ""
	case c.thumb != "" && utf8.RuneCountInString(c.title) <= shortTitle:
		top, rest = split(body)
	}
	// The body lines in the Section share the Text Display of the heading,
	// so that Discord puts its heading margin under the heading.
	if top != "" {
		heading += "\n" + top
	}
	in := beside(c.thumb, append(head, display(heading))...)
	if rest != "" {
		in = append(in, display(rest))
	}
	in = append(in, buttonRow(c.buttons)...)
	if ping == "" {
		return []component{box(c.color, in...)}
	}
	return []component{display(ping), box(c.color, in...)}
}

// The limits of the first lines of a long body that go beside the
// thumbnail.
const (
	topLines   = 3
	topVisible = 160
)

// split returns the first lines of body, at most topLines lines and about
// topVisible visible characters, and the rest. The first lines stop at a
// blank line and before a code block.
func split(body string) (top, rest string) {
	lines := strings.Split(body, "\n")
	size, i := 0, 0
	for ; i < len(lines) && i < topLines; i++ {
		if fenceOpen.MatchString(lines[i]) || (i > 0 && strings.TrimSpace(lines[i]) == "") {
			break
		}
		if size += visible(lines[i]); size > topVisible {
			break
		}
	}
	return strings.Join(lines[:i], "\n"), strings.TrimLeft(strings.Join(lines[i:], "\n"), "\n")
}

// linkText matches an inline markdown link, so that visible counts only its
// text.
var linkText = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)

// visible returns the number of characters that Discord shows for the
// markdown s: a link counts with its text, not its URL.
func visible(s string) int { return utf8.RuneCountInString(linkText.ReplaceAllString(s, "$1")) }

// link returns a markdown link to url with the text t, or t when url is
// empty.
func link(t, url string) string {
	if url == "" {
		return t
	}
	return "[" + t + "](" + url + ")"
}

// pings is the Ping setting of one Sink: the User map and the kinds of Ping
// that the Sink keeps.
type pings struct {
	users map[string]string
	kinds pingKinds
}

// fallback returns the card of the Fallback message: only the heading
// "<event>.<action> on <repo> by <sender>", with a link to the main object.
// It reads only the shared fields of the Event and never pings.
func fallback(e *Event) card {
	return card{title: e.NameAction() + " on " + e.Repo + " by " + e.Sender, url: e.URL}
}

// titled returns a card with the sender and its avatar, the heading
// "[repo] <kind>: #n title", the link to the main object, and the excerpt
// of the body.
func titled(e *Event, kind string) card {
	title := "[" + e.Repo + "] " + kind
	switch {
	case e.Number > 0:
		title += ": #" + strconv.Itoa(e.Number) + " " + e.Title
	case e.Title != "":
		title += ": " + e.Title
	}
	return card{sender: e.Sender, senderURL: e.SenderURL, thumb: avatar(e), title: title, url: e.URL, body: excerpt(e)}
}

// releaseThumb returns the thumbnail of a release: the Icon of the
// repository or its owner, else on GitHub the avatar of the owner, else the
// avatar of the sender.
func releaseThumb(e *Event, icons map[string]string) string {
	if v, ok := icons[strings.ToLower(e.Repo)]; ok {
		return v
	}
	if v, ok := icons[strings.ToLower(e.Owner)]; ok {
		return v
	}
	if e.Forge == "github" && e.OwnerAvatar != "" {
		return e.OwnerAvatar
	}
	return avatar(e)
}

// buttons returns the link buttons of e. No button opens the same page as
// the heading or the sender line. Forgejo has no checks page, and its lists
// filter by the numeric ID of the poster, so a Forgejo Event gets no Checks
// button and no list button.
func buttons(e *Event) []button {
	var bs []button
	add := func(label, url string) { bs = append(bs, button{label, url}) }
	if e.Name == "dependabot_alert" && e.Alert != nil && e.Alert.GHSA != "" {
		if u, err := url.Parse(e.RepoURL); err == nil && u.Host != "" {
			add("Advisory", u.Scheme+"://"+u.Host+"/advisories/"+e.Alert.GHSA)
		}
		return bs
	}
	if e.RepoURL == "" || e.Number == 0 {
		return nil
	}
	n := strconv.Itoa(e.Number)
	github := e.Forge == "github"
	// onPull is a comment or a review on a pull request. Its heading links
	// to the comment, so it gets the PR button.
	onPull := e.Name == "pull_request_review" || e.Name == "pull_request_review_comment" || (e.IsPull != nil && *e.IsPull)
	// authors adds the Author button on a comment or a review when the
	// Author is not the sender, and the list of the Author on GitHub. The
	// labels of a GitHub App author have no [bot], and its list filters by
	// app/<name>.
	authors := func(comment bool, kind, path, is string) {
		if e.Author == "" {
			return
		}
		name, query := e.Author, e.Author
		if app, ok := strings.CutSuffix(e.Author, "[bot]"); ok {
			name, query = app, "app/"+app
		}
		if comment && e.AuthorURL != "" && !strings.EqualFold(e.Author, e.Sender) {
			add(name, e.AuthorURL)
		}
		if github {
			add(kind+" by "+name, e.RepoURL+"/"+path+"?q="+url.QueryEscape("is:"+is+" author:"+query))
		}
	}
	switch {
	case e.Name == "pull_request" || onPull:
		pull := e.RepoURL + "/pull/" + n
		if !github {
			pull = e.RepoURL + "/pulls/" + n
		}
		if onPull {
			add("PR", pull)
		}
		add("Diff", pull+"/files")
		if github {
			add("Checks", pull+"/checks")
		}
		authors(onPull, "PRs", "pulls", "pr")
	case e.Name == "issues" || e.Name == "issue_comment":
		comment := e.Name == "issue_comment"
		if comment {
			add("Issue", e.RepoURL+"/issues/"+n)
		}
		authors(comment, "Issues", "issues", "issue")
	case e.Name == "discussion_comment":
		add("Discussion", e.RepoURL+"/discussions/"+n)
	}
	return bs
}

// avatar returns the icon of the sender of e. A Forgejo sender, or a sender
// with no avatar, gets an identicon, because Discord cannot load an avatar
// from a Forgejo that is not public. An Event with no sender has no icon.
func avatar(e *Event) string {
	if e.Sender == "" {
		return ""
	}
	if e.Forge == "github" && e.SenderAvatar != "" {
		return e.SenderAvatar
	}
	sum := sha256.Sum256([]byte(strings.ToLower(e.Sender)))
	return "https://www.gravatar.com/avatar/" + hex.EncodeToString(sum[:]) + "?d=identicon&s=128"
}

// excerpt returns the clean body of e with its References linked. layout
// cuts it to the Discord limit. Only a new issue, pull request,
// discussion, comment, review, or release, and a reported or published
// repository advisory, shows the body.
func excerpt(e *Event) string {
	switch e.NameAction() {
	case "issues.opened", "pull_request.opened", "issue_comment.created",
		"pull_request_review_comment.created", "discussion_comment.created",
		"pull_request_review.submitted", "discussion.created", "release.published",
		"repository_advisory.reported", "repository_advisory.published":
		body := e.Body
		if len(body) > maxExcerptBody {
			// ToValidUTF8 drops a character that the cut splits.
			body = strings.ToValidUTF8(body[:maxExcerptBody], "")
		}
		return clean(body, e.RepoURL)
	}
	return ""
}

// inlineCodePattern matches inline code.
//
// ponytail: a code span is on one line and has a run of one or two
// backticks, and an escaped \` starts a span. Scan the backtick runs as
// CommonMark does if a real body breaks this.
const inlineCodePattern = "``[^\n]+?``|`[^`\n]+`"

// mdLinkPattern matches a markdown link on one line: an inline link, or a
// reference link such as [text][label] or [text][]. The text can hold one
// level of brackets, and the URL one level of parentheses. A title after
// the URL is part of the match.
const mdLinkPattern = `\[(?:[^\[\]\n]|\[[^\[\]\n]*\])*\](?:\((?:[^()\n]|\([^()\n]*\))*\)|\[[^\[\]\n]*\])`

var (
	// markup matches the parts that clean removes from prose, and inline
	// code, which clean keeps. The parts are HTML comments, linked images,
	// image markdown, <img> tags, and tags with a known HTML name, for
	// example <details> or </summary>. Other tag names stay, so that code
	// such as Vec<String> stays as it is. The linked image comes before the
	// image, so that no empty link stays. The match that starts first wins,
	// so code can hold a tag, and a tag or an image can hold code.
	markup = regexp.MustCompile("(?s)<!--.*?-->" +
		`|\[!\[[^\]]*\]\([^)]*\)\]\([^)]*\)|!\[[^\]]*\]\([^)]*\)|(?i:<img\b[^>]*>)` +
		`|</?(?:a|b|i|u|s|p|br|hr|em|strong|del|ins|sub|sup|kbd|code|pre|div|span|details|summary|picture|source|video|center|blockquote|h[1-6]|ul|ol|li|table|thead|tbody|tr|th|td)\b[^>]*>` +
		"|" + inlineCodePattern)
	// blanks matches two or more blank lines.
	blanks = regexp.MustCompile(`\n(?:[ \t]*\n){2,}`)
	// tableRule matches the line under the header of a markdown table, for
	// example |---|:--:|.
	tableRule = regexp.MustCompile(`^\s*\|?(?:\s*:?-+:?\s*\|)+\s*(?::?-+:?\s*)?$`)
	// fenceOpen matches the line that opens a fenced code block: three or
	// more backticks or tildes, indented by at most three spaces.
	fenceOpen = regexp.MustCompile("^ {0,3}(`{3,}|~{3,})")
	// taskBox matches a list item that starts with a task box, for example
	// "- [ ] " or "1. [x] ". Discord shows no task lists.
	taskBox = regexp.MustCompile(`(?m)^[ \t]*(?:[-*+]|[0-9]{1,9}[.)])[ \t]+\[[ xX]\][ \t]`)
	// boxes changes the box in a taskBox match to ☐ or ☑.
	boxes = strings.NewReplacer("[ ]", "☐", "[x]", "☑", "[X]", "☑")
	// mdLink matches a markdown link. cutWords does not cut in one.
	mdLink = regexp.MustCompile(mdLinkPattern)
	// references matches a Reference, a full commit hash or #n. It also
	// matches inline code, a markdown link, and a bare URL, because a
	// Reference in them stays as it is. The match that starts first wins.
	// The hex match takes the full run, so that a 64-character checksum
	// does not match as a 40-character hash. It starts at 40 characters, so
	// that a body with many short runs, for example "a a a", does not make
	// one match for each run.
	references = regexp.MustCompile(inlineCodePattern + "|" + mdLinkPattern +
		`|https?://[^\s<>]+|([0-9a-f]{40,})|#([0-9]+)`)
)

// clean removes the parts of a body that Discord cannot show: comments,
// images, and HTML tags. It keeps the text in the tags and the markdown that
// Discord shows, changes tables to lists and task boxes to ☐ and ☑, and
// collapses repeated blank lines.
// It changes each Reference to a link into repo, and adds no link when repo
// is empty. It does not change the text in a fenced code block or in inline
// code. A block that does not close continues to the end of the body, as on
// GitHub.
func clean(body, repo string) string {
	var out, prose []string
	flush := func() {
		if len(prose) > 0 {
			out = append(out, linkReferences(cleanProse(strings.Join(prose, "\n")), repo))
			prose = nil
		}
	}
	fence := "" // the opening fence of the current code block, or "" outside a block
	for line := range strings.SplitSeq(strings.ReplaceAll(body, "\r\n", "\n"), "\n") {
		inBlock := fence != ""
		fence = nextFence(fence, line)
		if inBlock || fence != "" {
			flush()
			out = append(out, line)
		} else {
			prose = append(prose, line)
		}
	}
	flush()
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// nextFence returns the fence after line. fence is the opening fence of the
// code block before line, or "" outside a block. The result is the opening
// fence of the block that line opens or continues, or "" when line closes a
// block or is outside one.
func nextFence(fence, line string) string {
	open := fenceOpen.FindStringSubmatch(line)
	switch {
	case fence == "" && open != nil:
		return open[1]
	// The closing fence has the same character as the opening fence, is at
	// least as long, and has no text after it.
	case fence != "" && open != nil && open[1][0] == fence[0] && len(open[1]) >= len(fence) && strings.TrimSpace(line[len(open[0]):]) == "":
		return ""
	}
	return fence
}

// openFence returns the opening fence of the code block that is open at the
// end of s, or "" when no block is open.
func openFence(s string) string {
	fence := ""
	for line := range strings.SplitSeq(s, "\n") {
		fence = nextFence(fence, line)
	}
	return fence
}

// cleanProse does the work of clean on text that is not in a code block.
func cleanProse(text string) string {
	text = markup.ReplaceAllStringFunc(text, func(m string) string {
		if m[0] == '`' {
			return m
		}
		return ""
	})
	text = tables(text)
	text = taskBox.ReplaceAllStringFunc(text, boxes.Replace)
	return blanks.ReplaceAllString(text, "\n\n")
}

// linkReferences changes each Reference in text to a markdown link into
// repo. A hash link shows the first 7 characters. GitHub and Forgejo
// redirect /issues/n to the pull request when n is a pull request.
func linkReferences(text, repo string) string {
	if repo == "" {
		return text
	}
	var b strings.Builder
	last := 0
	for _, loc := range references.FindAllStringSubmatchIndex(text, -1) {
		i, j := loc[0], loc[1]
		before, _ := utf8.DecodeLastRuneInString(text[:i])
		after, _ := utf8.DecodeRuneInString(text[j:])
		// A backslash escapes a Reference, so it stays as text.
		if isWord(before) || isWord(after) || before == '\\' {
			continue
		}
		m := text[i:j]
		hex, num := loc[2] >= 0, loc[4] >= 0 // the groups of references
		switch {
		case hex && len(m) == 40:
			m = "[" + m[:7] + "](" + repo + "/commit/" + m + ")"
		// & before # starts an HTML entity, for example &#39;.
		case num && before != '&':
			m = "[" + m + "](" + repo + "/issues/" + m[1:] + ")"
		default:
			continue
		}
		b.WriteString(text[last:i] + m)
		last = j
	}
	return b.String() + text[last:]
}

// isWord reports whether r is a letter, a digit, or "_".
func isWord(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

// tables changes each markdown table to a bold header line and one bullet for
// each row, because Discord does not show tables. A table starts with a line
// that has a "|" and a rule line under it.
//
// ponytail: splits cells at each "|", so a pipe in inline code or an escaped
// \| splits a cell. Parse the cells properly if a real body has one.
func tables(body string) string {
	lines := strings.Split(body, "\n")
	out := make([]string, 0, len(lines))
	for i := 0; i < len(lines); {
		if !strings.Contains(lines[i], "|") || i+1 == len(lines) || !tableRule.MatchString(lines[i+1]) {
			out = append(out, lines[i])
			i++
			continue
		}
		out = append(out, "**"+cells(lines[i])+"**")
		for i += 2; i < len(lines) && strings.Contains(lines[i], "|"); i++ {
			out = append(out, "- "+cells(lines[i]))
		}
	}
	return strings.Join(out, "\n")
}

// cells returns the cells of a table row, joined with " · ".
func cells(row string) string {
	row = strings.TrimSpace(row)
	row = strings.TrimSuffix(strings.TrimPrefix(row, "|"), "|")
	var parts []string
	for p := range strings.SplitSeq(row, "|") {
		parts = append(parts, strings.TrimSpace(p))
	}
	return strings.Join(parts, " · ")
}

func renderIssue(e *Event) card {
	c := titled(e, "Issue "+e.Action)
	c.color = stateColor(e.Action)
	return c
}

// renderPullRequest shows a merge as "merged". A merge is the action closed
// with merged true.
func renderPullRequest(e *Event) card {
	kind, color := words(e.Action), stateColor(e.Action)
	if e.Action == "closed" && e.Merged != nil && *e.Merged {
		kind, color = "merged", colorMerged
	}
	c := titled(e, "Pull request "+kind)
	c.color = color
	return c
}

func renderIssueComment(e *Event) card {
	if e.IsPull != nil && *e.IsPull {
		return titled(e, "New comment on pull request")
	}
	return titled(e, "New comment on issue")
}

// renderReview shows the review state, for example "Review approved".
func renderReview(e *Event) card {
	var state string
	if e.ReviewState != nil {
		state = *e.ReviewState
	}
	c := titled(e, "Review "+words(state))
	switch state {
	case "approved":
		c.color = colorOpen
	case "changes_requested":
		c.color = colorClosed
	}
	return c
}

// renderPush shows one line for each commit, or the branch or tag that the
// push made or deleted. A commit line names the commit author only when the
// author is not the pusher, and links the References in the message.
func renderPush(e *Event) card {
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
	c := titled(e, "")
	c.title = fmt.Sprintf("[%s:%s] %d new commit", e.Repo, name, p.Size)
	if p.Size != 1 {
		c.title += "s"
	}
	lines := make([]string, 0, len(p.Commits))
	// Only maxText characters reach Discord, and a character has at most 4
	// bytes. budget stops the messages there, before linkReferences makes
	// them longer.
	budget := 4 * maxText
	for _, cm := range p.Commits {
		if budget <= 0 {
			break
		}
		msg, _, _ := strings.Cut(cm.Message, "\n")
		if len(msg) > budget {
			msg = strings.ToValidUTF8(msg[:budget], "")
		}
		budget -= len(msg)
		line := fmt.Sprintf("[`%s`](%s) %s", cm.ID[:min(7, len(cm.ID))], cm.URL, linkReferences(msg, e.RepoURL))
		if !strings.EqualFold(cm.Author, e.Sender) {
			line += " - " + cm.Author
		}
		lines = append(lines, line)
	}
	c.body = strings.Join(lines, "\n")
	return c
}

// renderAlert returns the card of a security Event: the sender,
// "[repo] <kind> <action>: #n summary", and one body line for each Alert
// field that is set. An Event with no Alert gets the zero card.
func renderAlert(e *Event, kind string) card {
	a := e.Alert
	if a == nil {
		return card{}
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
	// A repository advisory also shows the report under the fields. The cut
	// of layout is at the end, so the report gets the space that the fields
	// leave.
	if body := excerpt(e); body != "" {
		lines = append(lines, "", body)
	}
	// titled sets the sender. A security Event can come with no sender. Then
	// the card has no sender line and no thumbnail.
	c := titled(e, "")
	c.title, c.body, c.color = title+a.Summary, strings.Join(lines, "\n"), colorSecurity
	return c
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

// cutWords cuts s to at most n characters at the last word boundary. A text
// with no boundary in the second half of the cut is cut in the word, so a
// long word after a short heading does not drop the text. A cut text ends
// with "…". A cut in a markdown link moves back to before the link. A cut in
// a fenced code block closes the block with the opening fence on a new line,
// and the result with the fence has at most n characters.
func cutWords(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	links := mdLink.FindAllStringIndex(s, -1)
	for m := n; ; {
		c := strings.TrimSuffix(cutAtWord(s, m), "…")
		for _, l := range links {
			if l[0] < len(c) && len(c) < l[1] {
				c = strings.TrimRightFunc(s[:l[0]], unicode.IsSpace)
				break
			}
		}
		end := "…"
		// "…" on a fence line makes the fence text, so it goes on a new line.
		if fenceOpen.MatchString(c[strings.LastIndexByte(c, '\n')+1:]) {
			end = "\n…"
		}
		if fence := openFence(c); fence != "" {
			end += "\n" + fence
		}
		if utf8.RuneCountInString(c+end) <= n {
			return c + end
		}
		// A shorter cut can end in a different block, so check it again.
		m = max(1, min(m-1, n+1-utf8.RuneCountInString(end)))
	}
}

// cutAtWord does the cut of cutWords, with no closing fence.
func cutAtWord(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	for i := n - 1; i > n/2; i-- {
		if unicode.IsSpace(r[i]) {
			return strings.TrimRightFunc(string(r[:i]), unicode.IsSpace) + "…"
		}
	}
	return cut(s, n)
}

// cut cuts s to at most n characters. A cut text ends with "…".
func cut(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}

package winnow

import (
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/text"
)

// commentMarkdown parses comments as GitHub Flavored Markdown. Linkify turns
// bare URLs into autolinks, so a login in a URL stays quiet. GitHub shows a
// footnote reference as a number, so its label stays quiet.
var commentMarkdown = goldmark.New(goldmark.WithExtensions(extension.GFM, extension.Footnote))

// maxMentionScan is the size in bytes of the largest comment that can ping.
// The parser builds a node for each paragraph, so a 25 MiB comment can use
// gigabytes of memory. GitHub keeps at most 65,536 characters in a comment, so
// a GitHub comment always fits.
const maxMentionScan = 256 << 10

var mentionLogin = regexp.MustCompile(`@([[:alnum:]_][[:alnum:]_.-]*)`)

// commentMentions reads a comment and returns mapped IDs in text order.
// It leaves quoted text, code, HTML blocks and tags, and URLs quiet. A comment
// longer than maxMentionScan returns no IDs.
func commentMentions(body, sender string, users map[string]string) []string {
	if len(body) > maxMentionScan {
		return nil
	}
	src := []byte(body)
	var ids []string
	// scan finds the mentions in src[start:stop].
	scan := func(start, stop int) {
		// Take one match at a time. A long comment can hold millions. A
		// message cannot ping more than maxPingUsers, so stop there.
		for start < stop && len(ids) < maxPingUsers {
			loc := mentionLogin.FindIndex(src[start:stop])
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
			login := strings.TrimRight(body[i+1:j], ".")
			if strings.EqualFold(login, sender) {
				continue
			}
			if id, ok := users[strings.ToLower(login)]; ok && !slices.Contains(ids, id) {
				ids = append(ids, id)
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
	return ids
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

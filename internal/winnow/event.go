package winnow

import (
	"cmp"
	"encoding/json/v2"
	"errors"
	"net/http"
	"slices"
	"strings"
)

// Event is winnow's record of one webhook delivery, in one shape for both
// forges. The Rule fields come first. A nil optional field means that the
// Event does not have that field.
type Event struct {
	Source    string
	Forge     string // github or forgejo
	Name      string // the Event name, for example pull_request
	Action    string
	Repo      string // full name, for example autobrr/qui
	Owner     string
	Sender    string // login
	SenderBot bool

	Ref         *string // push
	Merged      *bool   // pull_request
	Draft       *bool   // pull_request
	ReviewState *string // pull_request_review
	IsPull      *bool   // issue_comment: true on a pull request

	// Rules cannot match the fields below. The log lines show Delivery. The
	// Renderers read the others.
	Delivery     string // X-GitHub-Delivery or X-Forgejo-Delivery
	URL          string // link to the main object
	Title        string // title of the main object, for example the issue title
	Number       int    // number of the issue, pull request, or discussion; 0 if none
	Body         string // text of the most specific object, for example the comment
	SenderURL    string
	SenderAvatar string
	Target       string // login of the requested reviewer or the assignee; only for review_requested and assigned
	Push         Push   // push only
	Alert        *Alert // security Events
}

// Push is the data of a push Event.
type Push struct {
	Commits []Commit
	Size    int  // the number of commits; can be more than len(Commits)
	Created bool // the push made the ref
	Deleted bool // the push deleted the ref
}

// Commit is one commit in a push.
type Commit struct {
	ID      string
	Message string
	URL     string
	Author  string
}

// Alert holds the data of a security Event that its Renderer shows. A field
// that the Event name does not have is empty.
type Alert struct {
	Number    int    // 0 on a repository advisory
	Summary   string // what the alert is about
	Severity  string
	Package   string // dependabot_alert
	Ecosystem string // dependabot_alert, for example npm
	Patched   string // dependabot_alert: the first version with a fix
	Validity  string // secret_scanning_alert, for example active
}

// NameAction returns "<event>.<action>", or only the Event name when the
// Event has no action.
func (e *Event) NameAction() string {
	if e.Action == "" {
		return e.Name
	}
	return e.Name + "." + e.Action
}

// logAttrs returns the fields that identify e in the decision line and the
// failed delivery line.
func (e *Event) logAttrs() []any {
	return []any{"source", e.Source, "event", e.NameAction(), "repo", e.Repo, "delivery", e.Delivery}
}

// ghUser is a GitHub user in a payload.
type ghUser struct {
	Login     string `json:"login"`
	Type      string `json:"type"`
	HTMLURL   string `json:"html_url"`
	AvatarURL string `json:"avatar_url"`
}

// ghText is an object in a payload with a link and a text, for example a
// comment.
type ghText struct {
	HTMLURL string `json:"html_url"`
	Body    string `json:"body"`
}

// ghTopic is an issue, a pull request, or a discussion in a payload.
type ghTopic struct {
	HTMLURL string `json:"html_url"`
	Number  int    `json:"number"`
	Title   string `json:"title"`
	Body    string `json:"body"`
}

// ghPayload holds the fields of a GitHub payload that winnow reads. A
// Forgejo payload has the same JSON shape for most fields, so winnow reads
// it into the same struct. The comments name the fields that only Forgejo
// sends.
type ghPayload struct {
	Action       string  `json:"action"`
	Ref          *string `json:"ref"`
	Compare      string  `json:"compare"`
	CompareURL   string  `json:"compare_url"`   // Forgejo push
	TotalCommits int     `json:"total_commits"` // Forgejo push: can be more than len(Commits)
	Sender       ghUser  `json:"sender"`
	Repository   struct {
		FullName string `json:"full_name"`
		HTMLURL  string `json:"html_url"`
		Owner    ghUser `json:"owner"`
	} `json:"repository"`
	PullRequest struct {
		ghTopic
		Merged *bool `json:"merged"`
		Draft  *bool `json:"draft"`
	} `json:"pull_request"`
	Issue *struct {
		ghTopic
		PullRequest *struct{} `json:"pull_request"`
	} `json:"issue"`
	Review struct {
		ghText
		State   *string `json:"state"`
		Type    string  `json:"type"`    // Forgejo: the X-Forgejo-Event-Type of the review
		Content string  `json:"content"` // Forgejo: the review text
	} `json:"review"`
	Comment    ghText  `json:"comment"`
	Discussion ghTopic `json:"discussion"`
	Release    struct {
		ghText
		Name    string `json:"name"`
		TagName string `json:"tag_name"`
	} `json:"release"`
	Forkee struct {
		HTMLURL  string `json:"html_url"`
		FullName string `json:"full_name"`
	} `json:"forkee"`
	Commits []struct {
		ID      string `json:"id"`
		Message string `json:"message"`
		URL     string `json:"url"`
		Author  struct {
			Name     string `json:"name"`
			Username string `json:"username"`
		} `json:"author"`
	} `json:"commits"`
	Created bool `json:"created"`
	Deleted bool `json:"deleted"`
	Alert   *struct {
		Number     int    `json:"number"`
		HTMLURL    string `json:"html_url"`
		Dependency struct {
			Package struct {
				Ecosystem string `json:"ecosystem"`
				Name      string `json:"name"`
			} `json:"package"`
		} `json:"dependency"`
		SecurityAdvisory struct {
			Summary  string `json:"summary"`
			Severity string `json:"severity"`
		} `json:"security_advisory"`
		SecurityVulnerability struct {
			FirstPatchedVersion struct {
				Identifier string `json:"identifier"`
			} `json:"first_patched_version"`
		} `json:"security_vulnerability"`
		Rule struct {
			Severity    string `json:"severity"`
			Description string `json:"description"`
		} `json:"rule"`
		SecretTypeDisplayName string `json:"secret_type_display_name"`
		Validity              string `json:"validity"`
	} `json:"alert"`
	RepositoryAdvisory *struct {
		HTMLURL  string `json:"html_url"`
		Summary  string `json:"summary"`
		Severity string `json:"severity"`
	} `json:"repository_advisory"`
	// Forgejo also sets requested_reviewer on a review, so winnow reads it
	// only for review_requested. A Forgejo assigned has no assignee.
	RequestedReviewer ghUser `json:"requested_reviewer"`
	Assignee          ghUser `json:"assignee"`
}

// forgejoReviews maps the X-Forgejo-Event-Type of a Forgejo review to the
// GitHub review state. Winnow compares the full name, because a prefix test
// on pull_request_review_ also matches pull_request_review_request.
var forgejoReviews = map[string]string{
	"pull_request_review_approved": "approved",
	"pull_request_review_rejected": "changes_requested",
	"pull_request_review_comment":  "commented",
}

// parseEvent turns a delivery into an Event. A GitHub ping becomes an Event
// with the name ping. A sender is a Bot sender also when its login is in
// bots.
func parseEvent(source string, bots []string, h http.Header, body []byte) (*Event, error) {
	forge, name, delivery := "github", h.Get("X-GitHub-Event"), h.Get("X-GitHub-Delivery")
	// Forgejo also sends X-GitHub-Event, so only X-Forgejo-Event shows the
	// forge. The Event name comes from X-Forgejo-Event, not from
	// X-Forgejo-Event-Type: X-Forgejo-Event has the GitHub names, for
	// example pull_request for pull_request_sync (ADR 0006).
	if fe := h.Get("X-Forgejo-Event"); fe != "" {
		forge, name, delivery = "forgejo", fe, h.Get("X-Forgejo-Delivery")
	}
	if name == "" {
		return nil, errors.New("X-GitHub-Event header is missing")
	}
	var p ghPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, err
	}
	e := &Event{
		Source: source,
		Forge:  forge,
		Name:   name,
		Action: p.Action,
		Repo:   p.Repository.FullName,
		Owner:  p.Repository.Owner.Login,
		Sender: p.Sender.Login,
		SenderBot: p.Sender.Type == "Bot" || strings.HasSuffix(p.Sender.Login, "[bot]") ||
			slices.ContainsFunc(bots, func(b string) bool { return strings.EqualFold(b, p.Sender.Login) }),
		Ref:          p.Ref,
		Merged:       p.PullRequest.Merged,
		Draft:        p.PullRequest.Draft,
		ReviewState:  p.Review.State,
		Delivery:     delivery,
		SenderURL:    p.Sender.HTMLURL,
		SenderAvatar: p.Sender.AvatarURL,
	}
	switch p.Action {
	case "review_requested":
		e.Target = p.RequestedReviewer.Login
	case "assigned":
		e.Target = p.Assignee.Login
	}
	var issue ghTopic
	if p.Issue != nil {
		e.IsPull = new(p.Issue.PullRequest != nil)
		issue = p.Issue.ghTopic
	}
	var alertURL string
	if a := p.Alert; a != nil {
		alertURL = a.HTMLURL
		// Each alert Event name fills only one of the sources in cmp.Or.
		e.Alert = &Alert{
			Number:    a.Number,
			Summary:   cmp.Or(a.SecurityAdvisory.Summary, a.Rule.Description, a.SecretTypeDisplayName),
			Severity:  cmp.Or(a.SecurityAdvisory.Severity, a.Rule.Severity),
			Package:   a.Dependency.Package.Name,
			Ecosystem: a.Dependency.Package.Ecosystem,
			Patched:   a.SecurityVulnerability.FirstPatchedVersion.Identifier,
			Validity:  a.Validity,
		}
	}
	if a := p.RepositoryAdvisory; a != nil {
		alertURL = a.HTMLURL
		e.Alert = &Alert{Summary: a.Summary, Severity: a.Severity}
	}
	// The link goes to the most specific object in the payload.
	e.URL = cmp.Or(p.Comment.HTMLURL, p.Review.HTMLURL, p.Discussion.HTMLURL, p.Release.HTMLURL,
		alertURL, p.PullRequest.HTMLURL, issue.HTMLURL, p.Compare, p.CompareURL, p.Forkee.HTMLURL, p.Repository.HTMLURL)
	e.Title = cmp.Or(issue.Title, p.PullRequest.Title, p.Discussion.Title, p.Release.Name, p.Release.TagName, p.Forkee.FullName)
	e.Number = cmp.Or(issue.Number, p.PullRequest.Number, p.Discussion.Number)
	// The body is the text of the most specific object, also when that text
	// is empty: a review with no text must not show the pull request text.
	// GitHub always sends html_url on a comment and on a review.
	switch {
	case p.Comment.HTMLURL != "":
		e.Body = p.Comment.Body
	case p.Review.HTMLURL != "":
		e.Body = p.Review.Body
	default:
		e.Body = cmp.Or(p.Release.Body, p.Discussion.Body, issue.Body, p.PullRequest.Body)
	}
	e.Push = Push{Size: cmp.Or(p.TotalCommits, len(p.Commits)), Created: p.Created, Deleted: p.Deleted}
	for _, c := range p.Commits {
		e.Push.Commits = append(e.Push.Commits, Commit{ID: c.ID, Message: c.Message, URL: c.URL, Author: cmp.Or(c.Author.Username, c.Author.Name)})
	}
	if forge == "forgejo" {
		mapForgejo(e, h.Get("X-Forgejo-Event-Type"), &p)
		// ponytail: Discord cannot load avatars from a private Forgejo, so show its logo; add a key if a public Forgejo needs avatars.
		e.SenderAvatar = forgejoIcon
	}
	// An optional Rule field belongs to one Event name. Another Event name
	// with the same payload key does not have the field, for example ref on
	// create.
	if e.Name != "push" {
		e.Ref = nil
	}
	if e.Name != "pull_request" {
		e.Merged, e.Draft = nil, nil
	}
	if e.Name != "pull_request_review" {
		e.ReviewState = nil
	}
	if e.Name != "issue_comment" {
		e.IsPull = nil
	}
	return e, nil
}

const forgejoIcon = "https://forgejo.org/favicon.png"

// mapForgejo changes the Forgejo names in e that have an exact GitHub twin
// to the GitHub names. Every other name stays as it is, for example the
// action label_updated.
func mapForgejo(e *Event, typ string, p *ghPayload) {
	if _, ok := forgejoReviews[typ]; ok {
		// A Forgejo review has no link of its own, so the link stays on
		// the pull request.
		e.Name, e.Action, e.Body = "pull_request_review", "submitted", p.Review.Content
		if state, ok := forgejoReviews[p.Review.Type]; ok {
			e.ReviewState = new(state)
		}
		return
	}
	switch {
	case e.Action == "synchronized":
		e.Action = "synchronize"
	case e.Name == "release" && e.Action == "updated":
		e.Action = "edited"
	}
}

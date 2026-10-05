package winnow

import (
	"cmp"
	"encoding/json/v2"
	"errors"
	"net/http"
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
	Merged      *bool   // pull request
	Draft       *bool   // pull request
	ReviewState *string // review
	IsPull      *bool   // issue or comment on an issue: true on a pull request

	Delivery string // X-GitHub-Delivery
	URL      string // link to the main object

	// The Renderers read the fields below. Rules cannot match them.
	Title        string // title of the main object, for example the issue title
	Number       int    // number of the issue, pull request, or discussion; 0 if none
	Body         string // text of the most specific object, for example the comment
	SenderURL    string
	SenderAvatar string
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

// ghLink is an object in a payload of which winnow reads only the link.
type ghLink struct {
	HTMLURL string `json:"html_url"`
}

// ghPayload holds the fields of a GitHub payload that winnow reads.
type ghPayload struct {
	Action     string  `json:"action"`
	Ref        *string `json:"ref"`
	Compare    string  `json:"compare"`
	Sender     ghUser  `json:"sender"`
	Repository struct {
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
		State *string `json:"state"`
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
}

// parseEvent turns a delivery into an Event. A GitHub ping becomes an Event
// with the name ping.
func parseEvent(source string, h http.Header, body []byte) (*Event, error) {
	name := h.Get("X-GitHub-Event")
	if name == "" {
		return nil, errors.New("X-GitHub-Event header is missing")
	}
	var p ghPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, err
	}
	e := &Event{
		Source:       source,
		Forge:        "github",
		Name:         name,
		Action:       p.Action,
		Repo:         p.Repository.FullName,
		Owner:        p.Repository.Owner.Login,
		Sender:       p.Sender.Login,
		SenderBot:    p.Sender.Type == "Bot" || strings.HasSuffix(p.Sender.Login, "[bot]"),
		Ref:          p.Ref,
		Merged:       p.PullRequest.Merged,
		Draft:        p.PullRequest.Draft,
		ReviewState:  p.Review.State,
		Delivery:     h.Get("X-GitHub-Delivery"),
		SenderURL:    p.Sender.HTMLURL,
		SenderAvatar: p.Sender.AvatarURL,
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
		alertURL, p.PullRequest.HTMLURL, issue.HTMLURL, p.Compare, p.Forkee.HTMLURL, p.Repository.HTMLURL)
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
	e.Push = Push{Size: len(p.Commits), Created: p.Created, Deleted: p.Deleted}
	for _, c := range p.Commits {
		e.Push.Commits = append(e.Push.Commits, Commit{ID: c.ID, Message: c.Message, URL: c.URL, Author: cmp.Or(c.Author.Username, c.Author.Name)})
	}
	return e, nil
}

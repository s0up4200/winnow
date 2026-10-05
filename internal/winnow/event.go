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
	Login string `json:"login"`
	Type  string `json:"type"`
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
		HTMLURL string `json:"html_url"`
		Merged  *bool  `json:"merged"`
		Draft   *bool  `json:"draft"`
	} `json:"pull_request"`
	Issue *struct {
		HTMLURL     string    `json:"html_url"`
		PullRequest *struct{} `json:"pull_request"`
	} `json:"issue"`
	Review struct {
		HTMLURL string  `json:"html_url"`
		State   *string `json:"state"`
	} `json:"review"`
	Comment    ghLink `json:"comment"`
	Discussion ghLink `json:"discussion"`
	Release    ghLink `json:"release"`
	Alert      ghLink `json:"alert"`
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
		Source:      source,
		Forge:       "github",
		Name:        name,
		Action:      p.Action,
		Repo:        p.Repository.FullName,
		Owner:       p.Repository.Owner.Login,
		Sender:      p.Sender.Login,
		SenderBot:   p.Sender.Type == "Bot" || strings.HasSuffix(p.Sender.Login, "[bot]"),
		Ref:         p.Ref,
		Merged:      p.PullRequest.Merged,
		Draft:       p.PullRequest.Draft,
		ReviewState: p.Review.State,
		Delivery:    h.Get("X-GitHub-Delivery"),
	}
	var issueURL string
	if p.Issue != nil {
		e.IsPull = new(p.Issue.PullRequest != nil)
		issueURL = p.Issue.HTMLURL
	}
	// The link goes to the most specific object in the payload.
	e.URL = cmp.Or(p.Comment.HTMLURL, p.Review.HTMLURL, p.Discussion.HTMLURL, p.Release.HTMLURL,
		p.Alert.HTMLURL, p.PullRequest.HTMLURL, issueURL, p.Compare, p.Repository.HTMLURL)
	return e, nil
}

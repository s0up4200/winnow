package winnow

import (
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
)

// Matcher is a Rule: each field that is set must hold for the Event. A
// string field holds when one of its patterns matches. An empty Matcher
// matches every Event.
type Matcher struct {
	Source      Patterns `yaml:"source"`
	Forge       Patterns `yaml:"forge"`
	Event       Patterns `yaml:"event"`
	Action      Patterns `yaml:"action"`
	Repo        Patterns `yaml:"repo"`
	Owner       Patterns `yaml:"owner"`
	Sender      Patterns `yaml:"sender"`
	SenderBot   *bool    `yaml:"sender_bot"`
	AuthorBot   *bool    `yaml:"author_bot"`
	Ref         Patterns `yaml:"ref"`
	Merged      *bool    `yaml:"merged"`
	Draft       *bool    `yaml:"draft"`
	ReviewState Patterns `yaml:"review_state"`
	IsPull      *bool    `yaml:"is_pull"`
	Not         Matchers `yaml:"not"` // the Matcher fails when one of these matches
}

func (m *Matcher) matches(e *Event) bool {
	return m.Source.match(&e.Source) &&
		m.Forge.match(&e.Forge) &&
		m.Event.match(&e.Name) &&
		m.Action.match(&e.Action) &&
		m.Repo.match(new(strings.ToLower(e.Repo))) &&
		m.Owner.match(new(strings.ToLower(e.Owner))) &&
		m.Sender.match(new(strings.ToLower(e.Sender))) &&
		eq(m.SenderBot, &e.SenderBot) &&
		eq(m.AuthorBot, e.AuthorBot) &&
		m.Ref.match(e.Ref) &&
		eq(m.Merged, e.Merged) &&
		eq(m.Draft, e.Draft) &&
		m.ReviewState.match(e.ReviewState) &&
		eq(m.IsPull, e.IsPull) &&
		!m.Not.match(e)
}

// compile prepares the patterns of m and of its not: matchers for match.
// Load calls it once.
func (m *Matcher) compile(inNot bool) error {
	if inNot && m.Not != nil {
		return errors.New("not: inside not:")
	}
	for _, p := range []Patterns{m.Source, m.Forge, m.Event, m.Action, m.Ref, m.ReviewState} {
		if err := p.compile(false); err != nil {
			return err
		}
	}
	for _, p := range []Patterns{m.Repo, m.Owner, m.Sender} {
		if err := p.compile(true); err != nil {
			return err
		}
	}
	for i := range m.Not {
		if err := m.Not[i].compile(true); err != nil {
			return err
		}
	}
	return nil
}

// Matchers is one Matcher or a list of Matchers in YAML. The list matches
// when one of its Matchers matches.
type Matchers []Matcher

func (ms Matchers) match(e *Event) bool {
	return slices.ContainsFunc(ms, func(m Matcher) bool { return m.matches(e) })
}

// UnmarshalYAML uses the old callback form on purpose: only the callback
// keeps the KnownFields setting of the decoder for the Matchers inside.
func (ms *Matchers) UnmarshalYAML(unmarshal func(any) error) error {
	return oneOrList(unmarshal, (*[]Matcher)(ms))
}

// Patterns is one string pattern or a list of string patterns in YAML. A nil
// Patterns is not set and always holds.
type Patterns []string

// globEscaper escapes all path.Match syntax except "*" (ADR 0005).
var globEscaper = strings.NewReplacer(`\`, `\\`, `[`, `\[`, `]`, `\]`, `?`, `\?`)

// compile escapes each pattern, and makes it lowercase if lower is true. It
// changes p in place.
func (p Patterns) compile(lower bool) error {
	for i, s := range p {
		if lower {
			s = strings.ToLower(s)
		}
		p[i] = globEscaper.Replace(s)
		if _, err := path.Match(p[i], ""); err != nil {
			return fmt.Errorf("pattern %q: %w", s, err)
		}
	}
	return nil
}

// match reports whether one pattern matches got. A field that the Event does
// not have (nil got) never matches a pattern.
func (p Patterns) match(got *string) bool {
	if p == nil {
		return true
	}
	if got == nil {
		return false
	}
	return slices.ContainsFunc(p, func(pat string) bool {
		ok, _ := path.Match(pat, *got) // compile checked the pattern
		return ok
	})
}

func (p *Patterns) UnmarshalYAML(unmarshal func(any) error) error {
	return oneOrList(unmarshal, (*[]string)(p))
}

// oneOrList decodes a YAML sequence into list, or one value into a list of
// one item.
func oneOrList[T any](unmarshal func(any) error, list *[]T) error {
	// The callback cannot decode into a yaml.Node, so an any shows the kind.
	var v any
	if err := unmarshal(&v); err != nil {
		return err
	}
	if _, ok := v.([]any); ok {
		return unmarshal(list)
	}
	var one T
	if err := unmarshal(&one); err != nil {
		return err
	}
	*list = []T{one}
	return nil
}

// eq reports whether a Rule value holds for an Event field. A Rule value
// that is not set always holds. A field that the Event does not have never
// matches a Rule value.
func eq[T comparable](want, got *T) bool {
	return want == nil || got != nil && *want == *got
}

// route returns the first Route that matches e, or nil.
func (c *Config) route(e *Event) *Route {
	for i := range c.Routes {
		if c.Routes[i].Match.match(e) {
			return &c.Routes[i]
		}
	}
	return nil
}

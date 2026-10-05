package winnow

// Matcher is a Rule: each field that is set must equal the field of the Event.
// An empty Matcher matches every Event.
type Matcher struct {
	Source      *string `yaml:"source"`
	Forge       *string `yaml:"forge"`
	Event       *string `yaml:"event"`
	Action      *string `yaml:"action"`
	Repo        *string `yaml:"repo"`
	Owner       *string `yaml:"owner"`
	Sender      *string `yaml:"sender"`
	SenderBot   *bool   `yaml:"sender_bot"`
	Ref         *string `yaml:"ref"`
	Merged      *bool   `yaml:"merged"`
	Draft       *bool   `yaml:"draft"`
	ReviewState *string `yaml:"review_state"`
	IsPull      *bool   `yaml:"is_pull"`
}

func (m *Matcher) matches(e *Event) bool {
	return eq(m.Source, &e.Source) &&
		eq(m.Forge, &e.Forge) &&
		eq(m.Event, &e.Name) &&
		eq(m.Action, &e.Action) &&
		eq(m.Repo, &e.Repo) &&
		eq(m.Owner, &e.Owner) &&
		eq(m.Sender, &e.Sender) &&
		eq(m.SenderBot, &e.SenderBot) &&
		eq(m.Ref, e.Ref) &&
		eq(m.Merged, e.Merged) &&
		eq(m.Draft, e.Draft) &&
		eq(m.ReviewState, e.ReviewState) &&
		eq(m.IsPull, e.IsPull)
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
		if c.Routes[i].Match.matches(e) {
			return &c.Routes[i]
		}
	}
	return nil
}

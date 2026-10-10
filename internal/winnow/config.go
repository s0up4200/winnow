package winnow

import (
	"bytes"
	"cmp"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"slices"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// Config is the loaded configuration file.
type Config struct {
	Listen  string                `yaml:"listen"`
	Sources map[string]Source     `yaml:"sources"`
	Users   map[string]string     `yaml:"users"` // the User map: lowercase forge login to Discord user ID
	Sinks   map[string]SinkConfig `yaml:"sinks"`
	Routes  []Route               `yaml:"routes"`
	Bots    []string              `yaml:"bots"` // logins of Bot senders, compared without case
	Digests []Digest              `yaml:"digests"`
	Alerts  string                `yaml:"alerts"` // the name of the Sink that gets the Sweep alerts, or empty
	// Icons maps a repository (owner/name) or an owner to the image URL of
	// its Icon. Load makes the keys lowercase.
	Icons map[string]string `yaml:"icons"`
	// Database is the path of the store. A relative path is relative to the
	// directory of the configuration file. Winnow opens it only when Digests
	// is not empty or a Source has a Sweep.
	Database string `yaml:"database"`
}

// Source is one webhook endpoint, /hook/<name>, with its own secret.
type Source struct {
	Secret string `yaml:"secret"`
	Sweep  *Sweep `yaml:"sweep"` // nil when the Source gets no Sweep
}

// Sweep is the configuration of the Sweep of one GitHub org webhook.
type Sweep struct {
	Token string `yaml:"token"` // a GitHub token that can read the deliveries of the hook
	Org   string `yaml:"org"`
	Hook  int64  `yaml:"hook"` // the numeric ID of the org webhook
	// api is the base URL of the GitHub API, and transport is the HTTP
	// transport of the GitHub client; nil means the default. They are not
	// in the file. Tests set a fake.
	api       string
	transport http.RoundTripper
}

// SinkConfig is the configuration of one Sink.
type SinkConfig struct {
	Discord string `yaml:"discord"`
	// Mentions is the raw mentions value. Load reads it into kinds.
	Mentions yaml.Node `yaml:"mentions"`
	kinds    pingKinds // the kinds of Ping that the Sink keeps
	// retryBase is the retry base of the Discord sender; 0 means 1 s. It is
	// not in the file. Tests set a few milliseconds.
	retryBase time.Duration
}

// Route is one entry in the ordered route list.
type Route struct {
	Name  string   `yaml:"name"` // Load sets "#<position>" when the file has no name
	Match Matchers `yaml:"match"`
	To    []string `yaml:"to"`
	Drop  bool     `yaml:"drop"`
	// Backfill makes the Route send a Backfill to its Sinks. A Route
	// without it drops a Backfill.
	Backfill bool `yaml:"backfill"`
}

// Digest is one entry in the digest list.
type Digest struct {
	Name  string   `yaml:"name"`
	Every string   `yaml:"every"` // the Period: daily, weekly, monthly, or yearly
	At    string   `yaml:"at"`    // the send time, HH:MM in local time; Load sets 09:00 when the file has none
	To    string   `yaml:"to"`    // the name of one Sink
	Match Matchers `yaml:"match"`
	// IncludeOther adds Other to the message. The default omits it.
	IncludeOther strictBool `yaml:"include_other"`
	// sendAt is At as the time after midnight. Load sets it.
	sendAt time.Duration
}

// strictBool is a YAML boolean that takes only true or false. The decoder
// also takes yes and on for a bool.
type strictBool bool

func (b *strictBool) UnmarshalYAML(n *yaml.Node) error {
	if n.ShortTag() != "!!bool" {
		return fmt.Errorf("line %d: want true or false, not %q", n.Line, n.Value)
	}
	return n.Decode((*bool)(b))
}

// pingKindNames holds the kinds of Ping. mentions: true means all of them.
// review_requested and assigned are also the Event actions that set Target,
// so render reads the kind of a Target Ping from the action.
var pingKindNames = []string{"review_requested", "assigned", "comments"}

// pingKinds is the set of kinds of Ping that a Sink keeps. Nil keeps no kind.
type pingKinds map[string]bool

// parsePingKinds reads the mentions value of a Sink: true, false, or a list
// of kinds. A missing key keeps no kind. An empty value is an error.
func parsePingKinds(n *yaml.Node) (pingKinds, error) {
	var kinds []string
	switch {
	case n.Kind == 0: // no mentions key
		return nil, nil
	case n.ShortTag() == "!!bool":
		var on bool
		if err := n.Decode(&on); err != nil {
			return nil, err
		}
		if on {
			kinds = pingKindNames
		}
	case n.Kind == yaml.SequenceNode:
		if err := n.Decode(&kinds); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("want true, false, or a list of kinds, not %q", n.Value)
	}
	k := pingKinds{}
	for _, kind := range kinds {
		if !slices.Contains(pingKindNames, kind) {
			return nil, fmt.Errorf("unknown kind of Ping %q: want review_requested, assigned, or comments", kind)
		}
		k[kind] = true
	}
	return k, nil
}

// Load parses a configuration file. Startup and `winnow check` both call it.
// An error stops startup. A warning does not.
func Load(data []byte) (cfg *Config, errs []error, warns []string) {
	cfg = &Config{Listen: ":8080", Database: "winnow.db"}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil {
		return nil, []error{err}, nil
	}
	// required expands *v and adds an error when *v is then empty.
	required := func(key string, v *string) {
		var err error
		if *v, err = expand(*v); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", key, err))
		} else if *v == "" {
			errs = append(errs, fmt.Errorf("%s is empty", key))
		}
	}
	for _, name := range slices.Sorted(maps.Keys(cfg.Sources)) {
		s := cfg.Sources[name]
		required("sources."+name+".secret", &s.Secret)
		if w := s.Sweep; w != nil {
			required("sources."+name+".sweep.token", &w.Token)
			required("sources."+name+".sweep.org", &w.Org)
			if w.Hook <= 0 {
				errs = append(errs, fmt.Errorf("sources.%s.sweep.hook is missing", name))
			}
			w.api = "https://api.github.com"
		}
		cfg.Sources[name] = s
	}
	// A forge login is not case-sensitive.
	users := make(map[string]string, len(cfg.Users))
	for login, id := range cfg.Users {
		users[strings.ToLower(login)] = id
	}
	cfg.Users = users
	icons := make(map[string]string, len(cfg.Icons))
	for _, key := range slices.Sorted(maps.Keys(cfg.Icons)) {
		v := cfg.Icons[key]
		if u, err := url.Parse(v); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			errs = append(errs, fmt.Errorf("icons.%s: want an http or https URL, not %q", key, v))
		}
		icons[strings.ToLower(key)] = v
	}
	cfg.Icons = icons
	for _, name := range slices.Sorted(maps.Keys(cfg.Sinks)) {
		s := cfg.Sinks[name]
		required("sinks."+name+".discord", &s.Discord)
		var err error
		if s.kinds, err = parsePingKinds(&s.Mentions); err != nil {
			errs = append(errs, fmt.Errorf("sinks.%s.mentions: %w", name, err))
		}
		cfg.Sinks[name] = s
	}
	used := map[string]bool{}
	var matchAll *Route // the first Route with match: {}
	for i := range cfg.Routes {
		r := &cfg.Routes[i]
		if r.Name == "" {
			r.Name = fmt.Sprintf("#%d", i+1)
		}
		if matchAll != nil {
			errs = append(errs, fmt.Errorf("route %s: route %s before it matches every Event", r.Name, matchAll.Name))
		}
		if r.Match == nil {
			errs = append(errs, fmt.Errorf("route %s: match is missing", r.Name))
		} else if matchAll == nil && slices.ContainsFunc(r.Match, func(m Matcher) bool { return reflect.ValueOf(m).IsZero() }) {
			matchAll = r // a list with one empty matcher also matches every Event
		}
		if (len(r.To) > 0) == r.Drop {
			errs = append(errs, fmt.Errorf("route %s: set exactly one of to: and drop:", r.Name))
		}
		if r.Drop && r.Backfill {
			errs = append(errs, fmt.Errorf("route %s: backfill: true on a drop: route", r.Name))
		}
		for j := range r.Match {
			if err := r.Match[j].compile(false); err != nil {
				errs = append(errs, fmt.Errorf("route %s: %w", r.Name, err))
			}
		}
		for _, to := range r.To {
			used[to] = true
			if _, ok := cfg.Sinks[to]; !ok {
				errs = append(errs, fmt.Errorf("route %s: sink %q is not in sinks", r.Name, to))
			}
		}
		warns = append(warns, eventWarnings("route "+r.Name, r.Match)...)
	}
	digests := map[string]bool{}
	for i := range cfg.Digests {
		d := &cfg.Digests[i]
		where := fmt.Sprintf("digest %s", d.Name)
		switch {
		case d.Name == "":
			where = fmt.Sprintf("digest #%d", i+1)
			errs = append(errs, fmt.Errorf("%s: name is missing", where))
		case digests[d.Name]:
			errs = append(errs, fmt.Errorf("%s: two digests have this name", where))
		}
		digests[d.Name] = true
		if !slices.Contains([]string{"daily", "weekly", "monthly", "yearly"}, d.Every) {
			errs = append(errs, fmt.Errorf("%s: every must be daily, weekly, monthly, or yearly, not %q", where, d.Every))
		}
		d.At = cmp.Or(d.At, "09:00")
		if t, err := time.Parse("15:04", d.At); err != nil {
			errs = append(errs, fmt.Errorf("%s: at must be HH:MM, not %q", where, d.At))
		} else {
			d.sendAt = time.Duration(t.Hour())*time.Hour + time.Duration(t.Minute())*time.Minute
		}
		used[d.To] = true
		if _, ok := cfg.Sinks[d.To]; !ok {
			errs = append(errs, fmt.Errorf("%s: sink %q is not in sinks", where, d.To))
		}
		if d.Match == nil {
			errs = append(errs, fmt.Errorf("%s: match is missing", where))
		}
		for j := range d.Match {
			if err := d.Match[j].compile(false); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", where, err))
			}
		}
		warns = append(warns, eventWarnings(where, d.Match)...)
	}
	if cfg.Alerts != "" {
		used[cfg.Alerts] = true
		if _, ok := cfg.Sinks[cfg.Alerts]; !ok {
			errs = append(errs, fmt.Errorf("alerts: sink %q is not in sinks", cfg.Alerts))
		}
	}
	// An empty path opens a temporary database, which loses the send
	// states and the swept deliveries at a restart.
	if cfg.needsStore() {
		required("database", &cfg.Database)
	}
	for _, name := range slices.Sorted(maps.Keys(cfg.Sinks)) {
		if !used[name] {
			warns = append(warns, fmt.Sprintf("sinks.%s: no route or digest sends to this sink", name))
		}
	}
	return cfg, errs, warns
}

// needsStore reports whether c has Digests or a Source with a Sweep.
func (c *Config) needsStore() bool {
	for _, s := range c.Sources {
		if s.Sweep != nil {
			return true
		}
	}
	return len(c.Digests) > 0
}

// eventWarnings returns a warning for each event pattern in ms, also inside
// not:, that matches no known Event name. The patterns must be compiled.
func eventWarnings(where string, ms Matchers) (warns []string) {
	for _, m := range ms {
		for _, p := range m.Event {
			if !slices.ContainsFunc(knownEvents, func(k string) bool { return Patterns{p}.match(&k) }) {
				warns = append(warns, fmt.Sprintf("%s: event %q is not a known Event name", where, p))
			}
		}
		warns = append(warns, eventWarnings(where, m.Not)...)
	}
	return warns
}

// expand returns the value of the environment variable VAR when v is one
// whole ${VAR} placeholder. Each other value stays as it is. Load calls
// expand after the YAML parse, so a value with "#" or a newline cannot
// change the structure of the file.
func expand(v string) (string, error) {
	name, ok := strings.CutPrefix(v, "${")
	if !ok {
		return v, nil
	}
	name, ok = strings.CutSuffix(name, "}")
	if !ok || name == "" || strings.ContainsAny(name, "${}") {
		return v, nil
	}
	if val := os.Getenv(name); val != "" {
		return val, nil
	}
	return "", fmt.Errorf("${%s} is unset or empty", name)
}

// knownEvents holds each Event name that Load accepts with no warning: all
// GitHub webhook event names (the X-GitHub-Event header), and the Forgejo
// names (the X-Forgejo-Event header) that have no GitHub twin.
var knownEvents = []string{
	// GitHub, from the webhooks in the GitHub REST OpenAPI description.
	"branch_protection_configuration", "branch_protection_rule", "check_run",
	"check_suite", "code_scanning_alert", "commit_comment", "create",
	"custom_property", "custom_property_values", "delete", "dependabot_alert",
	"deploy_key", "deployment", "deployment_protection_rule",
	"deployment_review", "deployment_status", "discussion",
	"discussion_comment", "fork", "github_app_authorization", "gollum",
	"installation", "installation_repositories", "installation_target",
	"issue_comment", "issue_dependencies", "issue_relates_to", "issues",
	"label", "marketplace_purchase", "member", "membership", "merge_group",
	"meta", "milestone", "org_block", "organization", "package", "page_build",
	"personal_access_token_request", "ping", "project", "project_card",
	"project_column", "projects_v2", "projects_v2_item",
	"projects_v2_status_update", "public", "pull_request",
	"pull_request_review", "pull_request_review_comment",
	"pull_request_review_thread", "push", "registry_package", "release",
	"repository", "repository_advisory", "repository_dispatch",
	"repository_import", "repository_ruleset",
	"repository_vulnerability_alert", "secret_scanning_alert",
	"secret_scanning_alert_location", "secret_scanning_scan",
	"security_advisory", "security_and_analysis", "sponsorship", "star",
	"status", "sub_issues", "team", "team_add", "watch", "workflow_dispatch",
	"workflow_job", "workflow_run",
	// Forgejo, from HookEventType.Event in modules/webhook/type.go. Winnow
	// maps the three review names to pull_request_review, so they are not here.
	"wiki", "action_run_failure", "action_run_success",
	"workflow_run_blocked", "workflow_run_cancelled", "workflow_run_failure",
	"workflow_run_running", "workflow_run_skipped", "workflow_run_success",
	"workflow_run_waiting", "workflow_job_blocked", "workflow_job_cancelled",
	"workflow_job_failure", "workflow_job_running", "workflow_job_skipped",
	"workflow_job_success", "workflow_job_waiting",
}

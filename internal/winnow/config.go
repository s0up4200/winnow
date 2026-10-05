package winnow

import (
	"bytes"
	"fmt"
	"maps"
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
}

// Source is one webhook endpoint, /hook/<name>, with its own secret.
type Source struct {
	Secret string `yaml:"secret"`
}

// SinkConfig is the configuration of one Sink.
type SinkConfig struct {
	Discord  string `yaml:"discord"`
	Mentions bool   `yaml:"mentions"` // keep the ping of a message
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
}

// Load parses a configuration file. Startup and `winnow check` both call it.
// An error stops startup. A warning does not.
func Load(data []byte) (cfg *Config, errs []error, warns []string) {
	cfg = &Config{Listen: ":8080"}
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
		cfg.Sources[name] = s
	}
	// A forge login is not case-sensitive.
	users := make(map[string]string, len(cfg.Users))
	for login, id := range cfg.Users {
		users[strings.ToLower(login)] = id
	}
	cfg.Users = users
	for _, name := range slices.Sorted(maps.Keys(cfg.Sinks)) {
		s := cfg.Sinks[name]
		required("sinks."+name+".discord", &s.Discord)
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
		warns = append(warns, eventWarnings(r.Name, r.Match)...)
	}
	for _, name := range slices.Sorted(maps.Keys(cfg.Sinks)) {
		if !used[name] {
			warns = append(warns, fmt.Sprintf("sinks.%s: no route sends to this sink", name))
		}
	}
	return cfg, errs, warns
}

// eventWarnings returns a warning for each event pattern in ms, also inside
// not:, that matches no known Event name. The patterns must be compiled.
func eventWarnings(route string, ms Matchers) (warns []string) {
	for _, m := range ms {
		for _, p := range m.Event {
			if !slices.ContainsFunc(knownEvents, func(k string) bool { return Patterns{p}.match(&k) }) {
				warns = append(warns, fmt.Sprintf("route %s: event %q is not a known Event name", route, p))
			}
		}
		warns = append(warns, eventWarnings(route, m.Not)...)
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

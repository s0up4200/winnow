package winnow

import (
	"bytes"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Config is the loaded configuration file.
type Config struct {
	Listen  string                `yaml:"listen"`
	Sources map[string]Source     `yaml:"sources"`
	Sinks   map[string]SinkConfig `yaml:"sinks"`
	Routes  []Route               `yaml:"routes"`
}

// Source is one webhook endpoint, /hook/<name>, with its own secret.
type Source struct {
	Secret string `yaml:"secret"`
}

// SinkConfig is the configuration of one Sink.
type SinkConfig struct {
	Discord string `yaml:"discord"`
}

// Route is one entry in the ordered route list.
type Route struct {
	Name  string   `yaml:"name"` // Load sets "#<position>" when the file has no name
	Match *Matcher `yaml:"match"`
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
	for _, name := range slices.Sorted(maps.Keys(cfg.Sources)) {
		s := cfg.Sources[name]
		var err error
		if s.Secret, err = expand(s.Secret); err != nil {
			errs = append(errs, fmt.Errorf("sources.%s.secret: %w", name, err))
		} else if s.Secret == "" {
			errs = append(errs, fmt.Errorf("sources.%s: secret is empty", name))
		}
		cfg.Sources[name] = s
	}
	for _, name := range slices.Sorted(maps.Keys(cfg.Sinks)) {
		s := cfg.Sinks[name]
		var err error
		if s.Discord, err = expand(s.Discord); err != nil {
			errs = append(errs, fmt.Errorf("sinks.%s.discord: %w", name, err))
		} else if s.Discord == "" {
			errs = append(errs, fmt.Errorf("sinks.%s: discord URL is missing", name))
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
		} else if *r.Match == (Matcher{}) && matchAll == nil {
			matchAll = r
		}
		if (len(r.To) > 0) == r.Drop {
			errs = append(errs, fmt.Errorf("route %s: set exactly one of to: and drop:", r.Name))
		}
		for _, to := range r.To {
			used[to] = true
			if _, ok := cfg.Sinks[to]; !ok {
				errs = append(errs, fmt.Errorf("route %s: sink %q is not in sinks", r.Name, to))
			}
		}
		if r.Match != nil && r.Match.Event != nil && !slices.Contains(knownEvents, *r.Match.Event) {
			warns = append(warns, fmt.Sprintf("route %s: event %q is not a known Event name", r.Name, *r.Match.Event))
		}
	}
	for _, name := range slices.Sorted(maps.Keys(cfg.Sinks)) {
		if !used[name] {
			warns = append(warns, fmt.Sprintf("sinks.%s: no route sends to this sink", name))
		}
	}
	return cfg, errs, warns
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

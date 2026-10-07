package winnow

import (
	"os"
	"slices"
	"strings"
	"testing"
)

func TestLoadReadsConfiguration(t *testing.T) {
	cfg, errs, warns := Load([]byte(quiConfig))
	if len(errs) > 0 || len(warns) > 0 {
		t.Fatalf("errors = %v, warnings = %v, want none", errs, warns)
	}
	if cfg.Listen != ":8080" {
		t.Errorf("listen = %q, want :8080", cfg.Listen)
	}
	if cfg.Sources["github-autobrr"].Secret != "test-secret" {
		t.Errorf("secret = %q, want test-secret", cfg.Sources["github-autobrr"].Secret)
	}
	if cfg.Sinks["qui"].Discord != "https://discord.example.invalid/api/webhooks/1/token" {
		t.Errorf("discord = %q", cfg.Sinks["qui"].Discord)
	}
}

func TestLoadErrors(t *testing.T) {
	tests := []struct {
		name   string
		config string
		want   string // part of the error text
	}{
		{"unknown top-level key", quiConfig + "sendr: x\n", "sendr"},
		{"unknown key in a matcher", `
sources:
  s: { secret: x }
sinks:
  a: { discord: https://discord.example.invalid/1 }
routes:
  - match: { sendr: x }
    to: [a]
`, "sendr"},
		{"ping target in a matcher", `
sinks:
  a: { discord: https://discord.example.invalid/1 }
routes:
  - match: { target: x }
    to: [a]
`, "target"},
		{"empty secret", `
sources:
  s: { secret: "" }
`, "secret"},
		{"route with no match", `
sinks:
  a: { discord: https://discord.example.invalid/1 }
routes:
  - to: [a]
`, "match"},
		{"sink name that is not in sinks", `
sinks:
  a: { discord: https://discord.example.invalid/1 }
routes:
  - match: {}
    to: [b]
`, `"b"`},
		{"unset variable in a secret", `
sources:
  s: { secret: "${WINNOW_TEST_UNSET}" }
`, "${WINNOW_TEST_UNSET} is unset or empty"},
		{"empty variable in a discord URL", `
sinks:
  a: { discord: "${WINNOW_TEST_EMPTY}" }
routes:
  - match: {}
    to: [a]
`, "${WINNOW_TEST_EMPTY} is unset or empty"},
		{"sink with no discord URL", `
sinks:
  a: {}
routes:
  - match: {}
    to: [a]
`, "sinks.a.discord is empty"},
		{"route with to and drop", `
sinks:
  a: { discord: https://discord.example.invalid/1 }
routes:
  - match: {}
    to: [a]
    drop: true
`, "route #1: set exactly one of to: and drop:"},
		{"route with neither to nor drop", `
sinks:
  a: { discord: https://discord.example.invalid/1 }
routes:
  - match: { repo: autobrr/qui }
  - match: {}
    to: [a]
`, "route #1: set exactly one of to: and drop:"},
		{"route after a match-all route", `
sinks:
  a: { discord: https://discord.example.invalid/1 }
routes:
  - name: all
    match: {}
    to: [a]
  - name: late
    match: { repo: autobrr/qui }
    drop: true
`, `route late: route all before it matches every Event`},
		{"digest with no name", digestErrorConfig + "  - { every: weekly, to: a, match: {} }\n", "digest #1: name is missing"},
		{"two digests with one name", digestErrorConfig + "  - { name: w, every: weekly, to: a, match: {} }\n  - { name: w, every: daily, to: a, match: {} }\n",
			"digest w: two digests have this name"},
		{"unknown every", digestErrorConfig + "  - { name: w, every: hourly, to: a, match: {} }\n",
			`digest w: every must be daily, weekly, monthly, or yearly, not "hourly"`},
		{"bad at", digestErrorConfig + `  - { name: w, every: weekly, at: "25:00", to: a, match: {} }` + "\n", `digest w: at must be HH:MM, not "25:00"`},
		{"digest sink that is not in sinks", digestErrorConfig + "  - { name: w, every: weekly, to: b, match: {} }\n", `digest w: sink "b" is not in sinks`},
		{"digest with no match", digestErrorConfig + "  - { name: w, every: weekly, to: a }\n", "digest w: match is missing"},
		{"bad matcher in a digest", digestErrorConfig + "  - { name: w, every: weekly, to: a, match: { not: { not: { repo: x } } } }\n",
			"digest w: not: inside not:"},
		{"empty database with digests", digestErrorConfig + "  - { name: w, every: weekly, to: a, match: {} }\ndatabase: \"\"\n", "database is empty"},
		{"sweep with no org", "sources:\n  s: { secret: x, sweep: { token: t, hook: 1 } }\n", "sources.s.sweep.org is empty"},
		{"sweep with no hook", "sources:\n  s: { secret: x, sweep: { token: t, org: autobrr } }\n", "sources.s.sweep.hook is missing"},
		{"sweep with no token", "sources:\n  s: { secret: x, sweep: { org: autobrr, hook: 1 } }\n", "sources.s.sweep.token is empty"},
		{"empty database with a sweep", "sources:\n  s: { secret: x, sweep: { token: t, org: autobrr, hook: 1 } }\ndatabase: \"\"\n", "database is empty"},
		{"alerts sink that is not in sinks", "alerts: b\n", `alerts: sink "b" is not in sinks`},
		{"backfill on a drop route", `
routes:
  - match: {}
    drop: true
    backfill: true
`, "route #1: backfill: true on a drop: route"},
	}
	t.Setenv("WINNOW_TEST_EMPTY", "")
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, errs, _ := Load([]byte(tt.config))
			if len(errs) != 1 || !strings.Contains(errs[0].Error(), tt.want) {
				t.Errorf("errors = %v, want one error with %q", errs, tt.want)
			}
		})
	}
}

func TestLoadWarnings(t *testing.T) {
	cfg, errs, warns := Load([]byte(`
sources:
  s: { secret: x }
sinks:
  a: { discord: https://discord.example.invalid/1 }
  dead: { discord: https://discord.example.invalid/2 }
routes:
  - name: typo
    match: { event: pull_requests }
    to: [a]
  - match: { event: dependabot_alert }
    to: [a]
  - match: { event: workflow_run_failure, forge: forgejo }
    drop: true
  - match: { event: wiki }
    drop: true
`))
	if cfg == nil || len(errs) > 0 {
		t.Fatalf("errors = %v, want none", errs)
	}
	want := []string{
		`route typo: event "pull_requests" is not a known Event name`,
		`sinks.dead: no route or digest sends to this sink`,
	}
	if !slices.Equal(warns, want) {
		t.Errorf("warnings = %q, want %q", warns, want)
	}
}

func TestLoadExpandsWholePlaceholders(t *testing.T) {
	t.Setenv("WINNOW_SECRET", "s3cret # x\nkey: y")
	t.Setenv("WINNOW_DISCORD", "https://discord.example.invalid/api/webhooks/2/token")
	cfg, errs, warns := Load([]byte(`
sources:
  env:
    secret: ${WINNOW_SECRET}
  literal: { secret: plain-secret }
  inside:  { secret: "x${WINNOW_SECRET}" }
sinks:
  a: { discord: "${WINNOW_DISCORD}" }
routes:
  - name: ${WINNOW_SECRET}
    match: {}
    to: [a]
`))
	if len(errs) > 0 || len(warns) > 0 {
		t.Fatalf("errors = %v, warnings = %v, want none", errs, warns)
	}
	for source, want := range map[string]string{
		"env":     "s3cret # x\nkey: y",
		"literal": "plain-secret",
		"inside":  "x${WINNOW_SECRET}",
	} {
		if got := cfg.Sources[source].Secret; got != want {
			t.Errorf("sources.%s.secret = %q, want %q", source, got, want)
		}
	}
	if got := cfg.Sinks["a"].Discord; got != "https://discord.example.invalid/api/webhooks/2/token" {
		t.Errorf("sinks.a.discord = %q", got)
	}
	if got := cfg.Routes[0].Name; got != "${WINNOW_SECRET}" {
		t.Errorf("route name = %q, want the literal placeholder", got)
	}
}

// TestSampleConfiguration keeps winnow.example.yaml valid: with its
// variables set, `winnow check` passes on it. It also checks which Route
// gets an Event.
func TestSampleConfiguration(t *testing.T) {
	for _, name := range []string{
		"WINNOW_SECRET_GITHUB_AUTOBRR", "WINNOW_SECRET_GITHUB_S0UP4200", "WINNOW_SECRET_FORGEJO",
		"WINNOW_DISCORD_SECURITY", "WINNOW_DISCORD_AUTOBRR", "WINNOW_DISCORD_QUI",
		"WINNOW_DISCORD_SOUP", "WINNOW_DISCORD_RELEASES", "WINNOW_GITHUB_TOKEN",
	} {
		t.Setenv(name, "https://discord.example.invalid/api/webhooks/1/"+name)
	}
	data, err := os.ReadFile("../../winnow.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cfg, errs, warns := Load(data)
	if len(errs) > 0 || len(warns) > 0 {
		t.Fatalf("errors = %v, warnings = %v, want none", errs, warns)
	}
	qui := func(name, action string) Event {
		return Event{Source: "github-autobrr", Forge: "github", Name: name, Action: action, Repo: "autobrr/qui", Owner: "autobrr"}
	}
	forgejo := func(name, action string) Event {
		return Event{Source: "forgejo", Forge: "forgejo", Name: name, Action: action, Repo: "soup/git-tui", Owner: "soup"}
	}
	tests := []struct {
		ev   Event
		want string
	}{
		{qui("push", ""), "noise"},
		{qui("create", ""), "noise"},
		{qui("workflow_run", "completed"), "noise"},
		{qui("projects_v2_item", "created"), "noise"},
		{qui("installation_repositories", "added"), "noise"},
		{qui("watch", "started"), "stars-forks"},
		{qui("star", "created"), "qui"},
		{qui("star", "deleted"), "noise"},
		{qui("issues", "opened"), "qui"},
		{qui("issues", "edited"), "noise"},
		{qui("issues", "assigned"), "qui"},
		{qui("issues", "unassigned"), "noise"},
		{qui("pull_request", "synchronize"), "noise"},
		{qui("pull_request", "ready_for_review"), "qui"},
		{qui("release", "created"), "noise"},
		{qui("release", "published"), "qui-releases"},
		{qui("secret_scanning_alert", "unassigned"), "security"},
		{qui("repository_vulnerability_alert", "create"), "noise"},
		{forgejo("workflow_run_success", ""), "noise"},
		{forgejo("action_run_failure", ""), "soup"},
		{forgejo("pull_request", "label_updated"), "noise"},
	}
	for _, tt := range tests {
		t.Run(tt.ev.NameAction(), func(t *testing.T) {
			r := cfg.route(&tt.ev)
			if r == nil || r.Name != tt.want {
				t.Errorf("route = %v, want %s", r, tt.want)
			}
		})
	}
}

// digestErrorConfig is a valid configuration that ends with "digests:".
// TestLoadErrors adds one Digest list to it.
const digestErrorConfig = `
sinks:
  a: { discord: https://discord.example.invalid/1 }
routes:
  - match: {}
    to: [a]
digests:
`

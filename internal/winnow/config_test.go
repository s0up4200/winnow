package winnow

import (
	"maps"
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
		{"Icon that is not an http URL", quiConfig + "icons: { autobrr: ftp://example.invalid/a.png }\n", "icons.autobrr"},
		{"Icon with no host", quiConfig + "icons: { autobrr/qui: https:///a.png }\n", "icons.autobrr/qui"},
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
		{"include_other that is not a boolean", digestErrorConfig + "  - { name: w, every: weekly, to: a, match: {}, include_other: maybe }\n", `want true or false, not "maybe"`},
		{"include_other: yes", digestErrorConfig + "  - { name: w, every: weekly, to: a, match: {}, include_other: yes }\n", `want true or false, not "yes"`},
		{"empty database with digests", digestErrorConfig + "  - { name: w, every: weekly, to: a, match: {} }\ndatabase: \"\"\n", "database is empty"},
		{"sweep with no org", "sources:\n  s: { secret: x, sweep: { token: t, hook: 1 } }\n", "sources.s.sweep.org is empty"},
		{"sweep with no hook", "sources:\n  s: { secret: x, sweep: { token: t, org: autobrr } }\n", "sources.s.sweep.hook is missing"},
		{"sweep with no token", "sources:\n  s: { secret: x, sweep: { org: autobrr, hook: 1 } }\n", "sources.s.sweep.token is empty"},
		{"empty database with a sweep", "sources:\n  s: { secret: x, sweep: { token: t, org: autobrr, hook: 1 } }\ndatabase: \"\"\n", "database is empty"},
		{"alerts sink that is not in sinks", "alerts: b\n", `alerts: sink "b" is not in sinks`},
		{"unknown kind of Ping", "sinks:\n  a: { discord: x, mentions: [assigned, assign] }\n",
			`unknown kind of Ping "assign": want review_requested, assigned, comments, or sponsorship`},
		{"mentions that is a number", "sinks:\n  a: { discord: x, mentions: 1 }\n", "want true, false, or a list of kinds"},
		{"mentions that is a map", "sinks:\n  a: { discord: x, mentions: { assigned: true } }\n", "want true, false, or a list of kinds"},
		{"mentions with no value", "sinks:\n  a: { discord: x, mentions: }\n", `sinks.a.mentions: want true, false, or a list of kinds, not ""`},
		{"mentions: yes", "sinks:\n  a: { discord: x, mentions: yes }\n", "want true, false, or a list of kinds"},
		{"team key with no slash", "teams:\n  autobrr: [a]\n", "teams.autobrr: want org/slug"},
		{"team key with no org", "teams:\n  /devs: [a]\n", "teams./devs: want org/slug"},
		{"team key with no slug", "teams:\n  autobrr/: [a]\n", "teams.autobrr/: want org/slug"},
		{"team key with two slashes", "teams:\n  autobrr/devs/x: [a]\n", "teams.autobrr/devs/x: want org/slug"},
		{"team key with a space", "teams:\n  autobrr/rls review: [a]\n", "teams.autobrr/rls review: want org/slug"},
		{"team with an empty list", "teams:\n  autobrr/devs: []\n", "teams.autobrr/devs: the list of members is empty"},
		{"team with no value", "teams:\n  autobrr/devs:\n", "teams.autobrr/devs: the list of members is empty"},
		{"T3 hook with no for", t3Section("", "[bob]", "x", "{ autobrr/qui: https://t3.example.invalid/1 }"), "t3.for is empty"},
		{"T3 hook with no senders", t3Section("alice", "[]", "x", "{ autobrr/qui: https://t3.example.invalid/1 }"), "t3.senders is empty"},
		{"T3 hook with an empty sender", t3Section("alice", `[bob, ""]`, "x", "{ autobrr/qui: https://t3.example.invalid/1 }"), "t3.senders: a login is empty"},
		{"T3 hook with no secret", t3Section("alice", "[bob]", `""`, "{ autobrr/qui: https://t3.example.invalid/1 }"), "t3.secret is empty"},
		{"T3 hook secret from an empty variable", t3Section("alice", "[bob]", `"${WINNOW_TEST_EMPTY}"`, "{ autobrr/qui: https://t3.example.invalid/1 }"),
			"t3.secret: ${WINNOW_TEST_EMPTY} is unset or empty"},
		{"T3 hook with no repos", t3Section("alice", "[bob]", "x", "{}"), "t3.repos is empty"},
		{"T3 hook repo that is not owner/repo", t3Section("alice", "[bob]", "x", "{ qui: https://t3.example.invalid/1 }"), "t3.repos.qui: want owner/repo"},
		{"T3 hook repo twice", t3Section("alice", "[bob]", "x", "{ autobrr/qui: https://t3.example.invalid/1, AutoBrr/Qui: https://t3.example.invalid/2 }"),
			"t3.repos.autobrr/qui: another key names the same repository"},
		{"T3 hook URL that is not http", t3Section("alice", "[bob]", "x", "{ autobrr/qui: ftp://t3.example.invalid/t3-url-token }"),
			"t3.repos.autobrr/qui: want an http or https URL"},
		{"T3 hook URL from an empty variable", t3Section("alice", "[bob]", "x", `{ autobrr/qui: "${WINNOW_TEST_EMPTY}" }`),
			"t3.repos.autobrr/qui: ${WINNOW_TEST_EMPTY} is unset or empty"},
		{"T3 hook with no value", "t3:\n", "t3 is empty"},
		{"T3 hook that is null", "t3: ~\n", "t3 is empty"},
		{"T3 hook with an unknown key", "t3: { for: alice, sender: [bob] }\n", "sender"},
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
			if len(errs) > 0 && strings.Contains(errs[0].Error(), "t3-url-token") {
				t.Errorf("error %q shows the T3 Code URL", errs[0])
			}
		})
	}
}

func TestLoadIncludeOther(t *testing.T) {
	for value, want := range map[string]bool{"": false, ", include_other: false": false, ", include_other: true": true} {
		cfg, errs, _ := Load([]byte(digestErrorConfig + "  - { name: w, every: weekly, to: a, match: {}" + value + " }\n"))
		if len(errs) > 0 {
			t.Fatalf("%q: errors = %v", value, errs)
		}
		if got := bool(cfg.Digests[0].IncludeOther); got != want {
			t.Errorf("%q: include_other = %v, want %v", value, got, want)
		}
	}
}

func TestLoadMentions(t *testing.T) {
	all := []string{"assigned", "comments", "review_requested", "sponsorship"}
	for value, want := range map[string][]string{
		"":                                 nil,
		", mentions: false":                nil,
		", mentions: true":                 all,
		", mentions: []":                   nil,
		", mentions: [assigned, assigned]": {"assigned"},
		", mentions: [comments, sponsorship, assigned, review_requested]": all,
	} {
		cfg, errs, _ := Load([]byte("sinks:\n  a: { discord: x" + value + " }\n"))
		if len(errs) > 0 {
			t.Fatalf("%q: errors = %v", value, errs)
		}
		if got := slices.Sorted(maps.Keys(cfg.Sinks["a"].kinds)); !slices.Equal(got, want) {
			t.Errorf("%q: mentions = %v, want %v", value, got, want)
		}
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

// Load makes the Team map lowercase and warns about each member that is not
// in the User map.
func TestLoadTeams(t *testing.T) {
	cfg, errs, warns := Load([]byte(`
users:
  Alice: "111"
teams:
  AutoBrr/RLS-Review: [ALICE, Bobb]
`))
	if len(errs) > 0 {
		t.Fatalf("errors = %v, want none", errs)
	}
	if want := map[string][]string{"autobrr/rls-review": {"alice", "bobb"}}; !maps.EqualFunc(cfg.Teams, want, slices.Equal) {
		t.Errorf("teams = %q, want %q", cfg.Teams, want)
	}
	want := []string{`teams.AutoBrr/RLS-Review: "Bobb" is not in users, so it gets no Ping`}
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

// t3Section returns a configuration with only a t3: section.
func t3Section(forLogin, senders, secret, repos string) string {
	return "t3:\n  for: " + forLogin + "\n  senders: " + senders + "\n  secret: " + secret + "\n  repos: " + repos + "\n"
}

func TestLoadT3Hook(t *testing.T) {
	t.Setenv("WINNOW_T3_SECRET", "s3cret")
	t.Setenv("WINNOW_T3_QUI", "https://t3.example.invalid/hooks/qui")
	cfg, errs, warns := Load([]byte(t3Section("alice", "[bob, carol]", "${WINNOW_T3_SECRET}", `{ autobrr/qui: "${WINNOW_T3_QUI}" }`)))
	if len(errs) > 0 || len(warns) > 0 {
		t.Fatalf("errors = %v, warnings = %v, want none", errs, warns)
	}
	if cfg.T3.Secret != "s3cret" || cfg.T3.Repos["autobrr/qui"] != "https://t3.example.invalid/hooks/qui" {
		t.Errorf("t3 = %+v, want the values of the variables", cfg.T3)
	}
	if cfg, _, _ := Load([]byte(quiConfig)); cfg.T3 != nil {
		t.Errorf("t3 = %+v without a t3: section, want nil", cfg.T3)
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

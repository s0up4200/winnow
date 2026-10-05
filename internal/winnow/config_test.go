package winnow

import (
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
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, errs, _ := Load([]byte(tt.config))
			if len(errs) != 1 || !strings.Contains(errs[0].Error(), tt.want) {
				t.Errorf("errors = %v, want one error with %q", errs, tt.want)
			}
		})
	}
}

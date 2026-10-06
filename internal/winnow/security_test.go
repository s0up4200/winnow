package winnow

import (
	"bytes"
	"encoding/json/v2"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSecurityRenderers(t *testing.T) {
	tests := []struct {
		event, fixture, want string
	}{
		{"dependabot_alert", "github/dependabot_alert_created", `{
			` + githubPoster + `,
			"embeds": [{
				"author": {"name": "github", "icon_url": "https://www.gravatar.com/avatar/c0b0109d9439de57fe3cf03abeccbc52f4c98170c732d3b69af5e6395ace574e?d=identicon&s=128"},
				"title": "[autobrr/qui] Dependabot alert created: #20 semver vulnerable to Regular Expression Denial of Service",
				"url": "https://github.example.invalid/autobrr/qui/security/dependabot/20",
				"description": "Severity: medium\nPackage: semver (npm)\nPatched in: 7.5.2",
				"color": 14901769
			}],
			"allowed_mentions": {"parse": []}
		}`},
		{"code_scanning_alert", "github/code_scanning_alert_created", `{
			` + githubPoster + `,
			"embeds": [{
				"author": {"name": "github", "icon_url": "https://www.gravatar.com/avatar/c0b0109d9439de57fe3cf03abeccbc52f4c98170c732d3b69af5e6395ace574e?d=identicon&s=128"},
				"title": "[autobrr/qui] Code scanning alert created: #10 Database query built from user-controlled sources",
				"url": "https://github.example.invalid/autobrr/qui/security/code-scanning/10",
				"description": "Severity: error",
				"color": 14901769
			}],
			"allowed_mentions": {"parse": []}
		}`},
		{"secret_scanning_alert", "github/secret_scanning_alert_created", `{
			` + githubPoster + `,
			"embeds": [{
				"author": {"name": "s0up4200", "icon_url": "https://www.gravatar.com/avatar/173ba346611228577922bcfaeed4ef078667a47712c5b759da97e4e527820a55?d=identicon&s=128"},
				"title": "[autobrr/qui] Secret scanning alert created: #3 GitHub Personal Access Token",
				"url": "https://github.example.invalid/autobrr/qui/security/secret-scanning/3",
				"description": "Validity: active",
				"color": 14901769
			}],
			"allowed_mentions": {"parse": []}
		}`},
		// The payload has no sender, so the embed has no author.
		{"repository_advisory", "github/repository_advisory_published", `{
			` + githubPoster + `,
			"embeds": [{
				"title": "[autobrr/qui] Repository advisory published: Path traversal in upload handler",
				"url": "https://github.example.invalid/autobrr/qui/security/advisories/GHSA-abcd-1234-efgh",
				"description": "Severity: high\n\n### Summary\nThe upload handler joins the file name to the upload path.\n\n### Impact\nAn attacker can write files outside the upload path.",
				"color": 14901769
			}],
			"allowed_mentions": {"parse": []}
		}`},
	}
	for _, tt := range tests {
		t.Run(tt.event, func(t *testing.T) {
			h := newHarness(t, quiConfig)
			rec := h.do(signedDelivery("github-autobrr", tt.event, fixture(t, tt.fixture)))
			if rec.Code != http.StatusAccepted {
				t.Fatalf("status = %d, want 202", rec.Code)
			}
			assertJSON(t, h.waitDiscord().Body, tt.want)
		})
	}
}

// A report that starts with a long word, such as a base64 proof of concept,
// is cut in the word, not before the report.
func TestAdvisoryReportCutsLongWord(t *testing.T) {
	h := newHarness(t, quiConfig)
	payload := bytes.Replace(fixture(t, "github/repository_advisory_published"),
		[]byte("### Summary\\r\\nThe upload handler"), []byte(strings.Repeat("A", 5000)), 1)
	if got := h.do(signedDelivery("github-autobrr", "repository_advisory", payload)).Code; got != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", got)
	}
	var msg struct {
		Embeds []struct {
			Description string `json:"description"`
		} `json:"embeds"`
	}
	if err := json.Unmarshal(h.waitDiscord().Body, &msg); err != nil {
		t.Fatal(err)
	}
	want := "Severity: high\n\n" + strings.Repeat("A", 4079) + "…"
	if got := msg.Embeds[0].Description; got != want {
		t.Errorf("description has %d characters and starts with %.20q, want %d", utf8.RuneCountInString(got), got, utf8.RuneCountInString(want))
	}
}

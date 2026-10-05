package winnow

import (
	"net/http"
	"testing"
)

func TestSecurityRenderers(t *testing.T) {
	tests := []struct {
		event, fixture, want string
	}{
		{"dependabot_alert", "github/dependabot_alert_created", `{
			"embeds": [{
				"title": "[autobrr/qui] Dependabot alert created: #20 semver vulnerable to Regular Expression Denial of Service",
				"url": "https://github.example.invalid/autobrr/qui/security/dependabot/20",
				"description": "Severity: medium\nPackage: semver (npm)\nPatched in: 7.5.2",
				"color": 14901769
			}],
			"allowed_mentions": {"parse": []}
		}`},
		{"code_scanning_alert", "github/code_scanning_alert_created", `{
			"embeds": [{
				"title": "[autobrr/qui] Code scanning alert created: #10 Database query built from user-controlled sources",
				"url": "https://github.example.invalid/autobrr/qui/security/code-scanning/10",
				"description": "Severity: error",
				"color": 14901769
			}],
			"allowed_mentions": {"parse": []}
		}`},
		{"secret_scanning_alert", "github/secret_scanning_alert_created", `{
			"embeds": [{
				"title": "[autobrr/qui] Secret scanning alert created: #3 GitHub Personal Access Token",
				"url": "https://github.example.invalid/autobrr/qui/security/secret-scanning/3",
				"description": "Validity: active",
				"color": 14901769
			}],
			"allowed_mentions": {"parse": []}
		}`},
		// The payload has no sender (decision 4).
		{"repository_advisory", "github/repository_advisory_published", `{
			"embeds": [{
				"title": "[autobrr/qui] Repository advisory published: Path traversal in upload handler",
				"url": "https://github.example.invalid/autobrr/qui/security/advisories/GHSA-abcd-1234-efgh",
				"description": "Severity: high",
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

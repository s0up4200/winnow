package winnow

import (
	"bytes"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSecurityRenderers(t *testing.T) {
	const githubThumb = `{"type": 11, "media": {"url": "https://www.gravatar.com/avatar/c0b0109d9439de57fe3cf03abeccbc52f4c98170c732d3b69af5e6395ace574e?d=identicon&s=128"}}`
	tests := []struct {
		event, fixture, want string
	}{
		// The Advisory button links to the GHSA ID on the GitHub host of the
		// repository.
		{"dependabot_alert", "github/dependabot_alert_created", container(14901769,
			section(`{"type": 10, "content": "-# github"}`, "## [[autobrr/qui] Dependabot alert created: #20 semver vulnerable to Regular Expression Denial of Service](https://github.example.invalid/autobrr/qui/security/dependabot/20)\nSeverity: medium\nPackage: semver (npm)\nPatched in: 7.5.2", githubThumb),
			buttonRowJSON("Advisory", "https://github.example.invalid/advisories/GHSA-c2qf-rxjj-qqgw"))},
		{"code_scanning_alert", "github/code_scanning_alert_created", container(14901769,
			section(`{"type": 10, "content": "-# github"}`, "## [[autobrr/qui] Code scanning alert created: #10 Database query built from user-controlled sources](https://github.example.invalid/autobrr/qui/security/code-scanning/10)\nSeverity: error", githubThumb))},
		{"secret_scanning_alert", "github/secret_scanning_alert_created", container(14901769,
			section(`{"type": 10, "content": "-# s0up4200"}`, "## [[autobrr/qui] Secret scanning alert created: #3 GitHub Personal Access Token](https://github.example.invalid/autobrr/qui/security/secret-scanning/3)\nValidity: active",
				`{"type": 11, "media": {"url": "https://www.gravatar.com/avatar/173ba346611228577922bcfaeed4ef078667a47712c5b759da97e4e527820a55?d=identicon&s=128"}}`))},
		// The payload has no sender, so the message has no sender line and
		// no thumbnail.
		{"repository_advisory", "github/repository_advisory_published", container(14901769,
			`{"type": 10, "content": "## [[autobrr/qui] Repository advisory published: Path traversal in upload handler](https://github.example.invalid/autobrr/qui/security/advisories/GHSA-abcd-1234-efgh)\nSeverity: high\n\n### Summary\nThe upload handler joins the file name to the upload path.\n\n### Impact\nAn attacker can write files outside the upload path."}`)},
	}
	for _, tt := range tests {
		t.Run(tt.event, func(t *testing.T) {
			h := newHarness(t, quiConfig)
			rec := h.do(signedDelivery("github-autobrr", tt.event, fixture(t, tt.fixture)))
			if rec.Code != http.StatusAccepted {
				t.Fatalf("status = %d, want 202", rec.Code)
			}
			assertJSON(t, h.waitDiscord().Body, `{`+githubPoster+`, "components": [`+tt.want+`], "allowed_mentions": {"parse": []}}`)
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
	m := decodeMessage(t, h.waitDiscord().Body)
	got := excerptOf(t, m)
	rest, ok := strings.CutPrefix(got, "Severity: high\n\n")
	if n := textLength(m.Components); !ok || strings.Trim(rest, "A") != "…" || n > maxText || n < maxText-10 {
		t.Errorf("body has %d characters and starts with %.20q, want the report cut in its word to the limit", utf8.RuneCountInString(got), got)
	}
}

// A report that is cut in a code block closes the block.
func TestAdvisoryReportClosesCutCodeBlock(t *testing.T) {
	h := newHarness(t, quiConfig)
	payload := bytes.Replace(fixture(t, "github/repository_advisory_published"),
		[]byte("### Summary\\r\\nThe upload handler"), []byte("```\\n"+strings.Repeat("line ", 1000)), 1)
	if got := h.do(signedDelivery("github-autobrr", "repository_advisory", payload)).Code; got != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", got)
	}
	m := decodeMessage(t, h.waitDiscord().Body)
	got := excerptOf(t, m)
	if n := utf8.RuneCountInString(got); n > maxText || !strings.HasSuffix(got, "line…\n```") {
		t.Errorf("description has %d characters and ends in %q, want at most %d that end in line…\\n```", n, got[max(0, len(got)-20):], maxText)
	}
}

package winnow

import (
	"bytes"
	"encoding/json/v2"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
)

// burstConfig sends every Event to one Sink that pings. The sender of the
// fixtures is Codertocat.
const burstConfig = `
sources:
  github-autobrr: { secret: test-secret }
  forgejo: { secret: test-secret }
users:
  octocat: "111"
  hubot: "333"
  Codertocat: "222"
sinks:
  ping: { discord: https://discord.example.invalid/api/webhooks/1/token, mentions: true }
routes:
  - match: {}
    to: [ping]
`

// person changes the person of the fixture name: the requested reviewer of a
// review request, or the assignee of an assignment. sender, when not empty,
// replaces the sender.
func person(t *testing.T, name, key, login, sender string) []byte {
	t.Helper()
	var p map[string]any
	if err := json.Unmarshal(fixture(t, name), &p); err != nil {
		t.Fatal(err)
	}
	p[key] = map[string]any{"login": login, "html_url": "https://example.invalid/" + login}
	if sender != "" {
		p["sender"] = map[string]any{"login": sender, "html_url": "https://example.invalid/" + sender}
	}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// reviewer returns a review request of login by sender.
func reviewer(t *testing.T, login, sender string) []byte {
	return person(t, "github/pull_request_review_requested", "requested_reviewer", login, sender)
}

// noMoreDiscord fails the test when the fake Discord receives a request
// within d.
func (h *harness) noMoreDiscord(d time.Duration) {
	h.t.Helper()
	select {
	case r := <-h.discord:
		h.t.Fatalf("fake Discord received another request: %s", r.Body)
	case <-time.After(d):
	}
}

func (h *harness) accept(event string, body []byte) {
	h.t.Helper()
	if got := h.do(signedDelivery("github-autobrr", event, body)).Code; got != http.StatusAccepted {
		h.t.Fatalf("status = %d, want 202", got)
	}
}

// A Burst of review requests posts one card that names each reviewer once,
// in arrival order, and pings each mapped reviewer once.
func TestBurstOneCard(t *testing.T) {
	h := newHarness(t, burstConfig)
	h.srv.bursts.wait = 300 * time.Millisecond
	sent := time.Now()
	for _, login := range []string{"octocat", "hubot", "monalisa", "octocat"} {
		h.accept("pull_request", reviewer(t, login, ""))
	}
	r := h.waitDiscord()
	if d := r.At.Sub(sent); d < 300*time.Millisecond {
		t.Errorf("card posted after %v, want after the wait", d)
	}
	m := decodeMessage(t, r.Body)
	want := "A review from **[octocat](https://example.invalid/octocat)**, **[hubot](https://example.invalid/hubot)** and **[monalisa](https://example.invalid/monalisa)** has been requested"
	if got := excerptOf(t, m); got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
	assertMentions(t, r.Body, `{"content": "<@111> <@333>", "allowed_mentions": {"parse": [], "users": ["111", "333"]}}`)
	h.noMoreDiscord(400 * time.Millisecond)
}

// Another sender or another action starts another card.
func TestBurstKey(t *testing.T) {
	h := newHarness(t, burstConfig)
	h.srv.bursts.wait = 300 * time.Millisecond
	h.accept("pull_request", reviewer(t, "octocat", ""))
	h.accept("pull_request", reviewer(t, "hubot", "monalisa"))
	assigned := person(t, "github/pull_request_review_requested", "assignee", "hubot", "")
	h.accept("pull_request", bytes.Replace(assigned, []byte(`"review_requested"`), []byte(`"assigned"`), 1))
	var bodies []string
	for range 3 {
		bodies = append(bodies, excerptOf(t, decodeMessage(t, h.waitDiscord().Body)))
	}
	h.noMoreDiscord(400 * time.Millisecond)
	for _, want := range []string{
		"A review from **[octocat](https://example.invalid/octocat)** has been requested",
		"A review from **[hubot](https://example.invalid/hubot)** has been requested",
		"Assigned to **[hubot](https://example.invalid/hubot)**",
	} {
		found := false
		for _, b := range bodies {
			found = found || b == want
		}
		if !found {
			t.Errorf("no card %q in %q", want, bodies)
		}
	}
}

// Two Routes with the same name start two cards.
func TestBurstKeyRoute(t *testing.T) {
	h := newHarness(t, `
sources:
  github-autobrr: { secret: test-secret }
sinks:
  drafts: { discord: https://discord.example.invalid/api/webhooks/1/token }
  ready: { discord: https://discord.example.invalid/api/webhooks/2/token }
routes:
  - name: reviews
    match: { draft: true }
    to: [drafts]
  - name: reviews
    match: {}
    to: [ready]
`)
	h.srv.bursts.wait = 300 * time.Millisecond
	h.accept("pull_request", bytes.Replace(reviewer(t, "octocat", ""), []byte(`"draft":false`), []byte(`"draft":true`), 1))
	h.accept("pull_request", reviewer(t, "hubot", ""))
	got := map[string]string{}
	for range 2 {
		r := h.waitDiscord()
		got[r.Sink] = excerptOf(t, decodeMessage(t, r.Body))
	}
	h.noMoreDiscord(400 * time.Millisecond)
	want := map[string]string{
		"drafts": "A review from **[octocat](https://example.invalid/octocat)** has been requested",
		"ready":  "A review from **[hubot](https://example.invalid/hubot)** has been requested",
	}
	if !maps.Equal(got, want) {
		t.Errorf("cards = %q, want %q", got, want)
	}
}

// A stop sends each waiting card before the Sink queues drain.
func TestBurstSentOnStop(t *testing.T) {
	h := newHarness(t, burstConfig)
	h.srv.bursts.wait = time.Hour
	h.accept("pull_request", reviewer(t, "octocat", ""))
	h.accept("pull_request", reviewer(t, "hubot", ""))
	h.stop()
	m := decodeMessage(t, h.waitDiscord().Body)
	want := "A review from **[octocat](https://example.invalid/octocat)** and **[hubot](https://example.invalid/hubot)** has been requested"
	if got := excerptOf(t, m); got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

// An Event that cannot join a Burst posts at once. A Forgejo assignment names
// no assignee, so it cannot join a Burst.
func TestNoBurst(t *testing.T) {
	h := newHarness(t, burstConfig)
	h.srv.bursts.wait = time.Hour
	h.accept("pull_request", fixture(t, "github/pull_request_opened"))
	h.waitDiscord()
	assigned := bytes.Replace(fixture(t, "forgejo/pull_request-opened"), []byte(`"action": "opened"`), []byte(`"action": "assigned"`), 1)
	if got := h.do(forgejoDelivery("forgejo", "pull_request", "pull_request_assign", assigned)).Code; got != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", got)
	}
	h.waitDiscord()
}

// A card pings at most maxTargetPings people, and names each one.
func TestBurstPingLimit(t *testing.T) {
	config, want := burstConfig, []string{}
	var ids []string
	for i := range maxTargetPings + 1 {
		login, id := fmt.Sprintf("user%d", i), fmt.Sprint(1000+i)
		config = strings.Replace(config, "users:\n", "users:\n  "+login+": \""+id+"\"\n", 1)
		want = append(want, login)
		if i < maxTargetPings {
			ids = append(ids, id)
		}
	}
	h := newHarness(t, config)
	h.srv.bursts.wait = 300 * time.Millisecond
	for _, login := range want {
		h.accept("pull_request", reviewer(t, login, ""))
	}
	m := decodeMessage(t, h.waitDiscord().Body)
	if !slices.Equal(m.AllowedMentions.Users, ids) {
		t.Errorf("users = %q, want %q", m.AllowedMentions.Users, ids)
	}
	for _, login := range want {
		if !strings.Contains(excerptOf(t, m), "**["+login+"]") {
			t.Errorf("body does not name %s", login)
		}
	}
}

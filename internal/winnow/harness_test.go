package winnow

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// testSecret is the secret that signedDelivery uses. Give each Source in a
// test configuration this secret.
const testSecret = "test-secret"

// testDelivery is the delivery ID that signedDelivery sends.
const testDelivery = "72d3162e-cc78-11e3-81ab-4c9367dc0958"

// harness is the HTTP handler of winnow, built from a YAML configuration,
// with each Sink URL pointed at one fake Discord.
type harness struct {
	t       *testing.T
	handler http.Handler
	logs    *syncBuffer
	discord chan discordRequest
}

// discordRequest is one request that the fake Discord received.
type discordRequest struct {
	Sink string // the Sink name, from the URL path
	Body []byte
}

// newHarness loads config and builds the handler. It replaces the discord URL
// of each Sink with the URL of a fake Discord. The fake Discord records each
// request on h.discord and replies 204. The logs go to a buffer as JSON.
func newHarness(t *testing.T, config string) *harness {
	t.Helper()
	cfg, errs, _ := Load([]byte(config))
	if len(errs) > 0 {
		t.Fatalf("load configuration: %v", errs)
	}
	h := &harness{t: t, logs: &syncBuffer{}, discord: make(chan discordRequest, 100)}
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		h.discord <- discordRequest{Sink: strings.TrimPrefix(r.URL.Path, "/"), Body: body}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(fake.Close)
	for name, s := range cfg.Sinks {
		s.Discord = fake.URL + "/" + name
		cfg.Sinks[name] = s
	}
	h.handler = New(cfg, slog.New(slog.NewJSONHandler(h.logs, nil)))
	return h
}

// do sends req to the handler and returns the reply.
func (h *harness) do(req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)
	return rec
}

// waitDiscord returns the next request that the fake Discord receives.
func (h *harness) waitDiscord() discordRequest {
	h.t.Helper()
	select {
	case r := <-h.discord:
		return r
	case <-time.After(2 * time.Second):
		h.t.Fatal("fake Discord received no request")
		return discordRequest{}
	}
}

// logLines returns each log line as a map, without the time field.
func (h *harness) logLines() []map[string]any {
	h.t.Helper()
	var lines []map[string]any
	for line := range strings.Lines(h.logs.String()) {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			h.t.Fatalf("log line %q: %v", line, err)
		}
		delete(m, "time")
		lines = append(lines, m)
	}
	return lines
}

// decision returns the one decision line. It fails the test if the number of
// decision lines is not one.
func (h *harness) decision() map[string]any {
	h.t.Helper()
	var found []map[string]any
	for _, m := range h.logLines() {
		if m["msg"] == "routed" {
			found = append(found, m)
		}
	}
	if len(found) != 1 {
		h.t.Fatalf("got %d decision lines, want 1: %v", len(found), found)
	}
	return found[0]
}

// signedDelivery returns a GitHub delivery to /hook/<source> with a correct
// signature for testSecret.
func signedDelivery(source, event string, body []byte) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/hook/"+source, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Event", event)
	req.Header.Set("X-GitHub-Delivery", testDelivery)
	req.Header.Set("X-Hub-Signature-256", sign(testSecret, body))
	return req
}

// sign returns the X-Hub-Signature-256 value of body.
func sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// fixture returns testdata/<name>.json.
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// assertJSON fails the test if got and want are not the same JSON value.
func assertJSON(t *testing.T, got []byte, want string) {
	t.Helper()
	var g, w any
	if err := json.Unmarshal(got, &g); err != nil {
		t.Fatalf("got is not JSON: %v: %s", err, got)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatalf("want is not JSON: %v", err)
	}
	if !reflect.DeepEqual(g, w) {
		t.Errorf("got JSON\n%s\nwant\n%s", got, want)
	}
}

// syncBuffer is a bytes.Buffer that the Sink workers and the test can use at
// the same time.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

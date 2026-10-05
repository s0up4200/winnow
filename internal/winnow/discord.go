package winnow

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json/v2"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// message is the JSON body of one Discord webhook call.
type message struct {
	Embeds          []embed         `json:"embeds"`
	AllowedMentions allowedMentions `json:"allowed_mentions"`
}

type embed struct {
	Author      embedAuthor `json:"author,omitzero"`
	Title       string      `json:"title"`
	URL         string      `json:"url,omitempty"`
	Description string      `json:"description,omitempty"`
	Color       int         `json:"color,omitzero"`
}

type embedAuthor struct {
	Name    string `json:"name"`
	URL     string `json:"url,omitempty"`
	IconURL string `json:"icon_url,omitempty"`
}

// allowedMentions controls which mentions in a message ping. An empty Parse
// list (json/v2 encodes nil as []) makes sure that no text in the message
// pings anyone.
type allowedMentions struct {
	Parse []string `json:"parse"`
}

// retryBase is the wait before the first retry after a server error or a
// network error. Each next retry waits twice as long. Tests set a few
// milliseconds.
var retryBase = time.Second

// maxAttempts is the largest number of sends for one message, 429 replies
// included.
const maxAttempts = 5

// seconds turns a Discord time in seconds, for example 0.25, into a duration.
func seconds(s float64) time.Duration {
	return time.Duration(s * float64(time.Second))
}

// discordSender sends messages to the webhook URL of one Discord channel.
type discordSender struct {
	url    string
	client *http.Client
	next   time.Time // no send before this time, because the rate limit bucket is empty
}

func newDiscordSender(webhookURL string) *discordSender {
	return &discordSender{url: webhookURL, client: &http.Client{Timeout: 10 * time.Second}}
}

// send posts msg and retries it after a server error or a network error. When
// Discord does not take msg, send returns a *failure. The failure never holds
// the webhook URL, because the URL holds the webhook token.
func (d *discordSender) send(ctx context.Context, msg message) error {
	body, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	last := &failure{} // the last attempt that failed
	errs := 0          // server errors and network errors
	for attempt := 1; ; attempt++ {
		status, reply, err := d.post(ctx, body)
		if ctx.Err() != nil {
			break
		}
		f := &failure{reason: "rejected", attempts: attempt, status: status, detail: string(reply)}
		if err != nil {
			f.detail = err.Error()
		}
		var wait time.Duration
		switch {
		case status/100 == 2:
			return nil
		case status == http.StatusTooManyRequests:
			var r struct {
				RetryAfter float64 `json:"retry_after"`
			}
			_ = json.Unmarshal(reply, &r)
			wait = cmp.Or(seconds(r.RetryAfter), retryBase)
		case err != nil || status >= 500:
			wait = retryBase << errs
			errs++
		default:
			return f
		}
		// Retries wait 1, 2, and 4 times retryBase, so the 4th error stops.
		if errs > 3 || attempt == maxAttempts {
			f.reason = "retries_exhausted"
			return f
		}
		last = f
		if !sleep(ctx, wait) {
			break
		}
	}
	// ctx ended before Discord took msg. A send that ctx cut is not an
	// attempt.
	last.reason = "shutdown"
	return last
}

// sleep waits for d. It returns false when ctx ends first.
func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// post sends body once. It returns the HTTP status and the first 500 bytes
// of the reply body. When the rate limit bucket is empty, post waits until
// Discord fills it again.
func (d *discordSender) post(ctx context.Context, body []byte) (int, []byte, error) {
	if !sleep(ctx, time.Until(d.next)) {
		return 0, nil, ctx.Err()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.url, bytes.NewReader(body))
	var resp *http.Response
	if err == nil {
		req.Header.Set("Content-Type", "application/json")
		resp, err = d.client.Do(req)
	}
	if err != nil {
		if ue, ok := errors.AsType[*url.Error](err); ok {
			err = ue.Err
		}
		return 0, nil, err
	}
	defer resp.Body.Close()
	if resp.Header.Get("X-RateLimit-Remaining") == "0" {
		resetAfter, _ := strconv.ParseFloat(resp.Header.Get("X-RateLimit-Reset-After"), 64)
		d.next = time.Now().Add(seconds(resetAfter))
	}
	reply, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
	return resp.StatusCode, reply, nil
}

package winnow

import (
	"bytes"
	"cmp"
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
	Title string `json:"title"`
	URL   string `json:"url,omitempty"`
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
func (d *discordSender) send(msg message) error {
	body, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	errs := 0 // server errors and network errors
	for attempt := 1; ; attempt++ {
		status, reply, err := d.post(body)
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
		time.Sleep(wait)
	}
}

// post sends body once. It returns the HTTP status and the first 500 bytes
// of the reply body. When the rate limit bucket is empty, post waits until
// Discord fills it again.
func (d *discordSender) post(body []byte) (int, []byte, error) {
	time.Sleep(time.Until(d.next))
	resp, err := d.client.Post(d.url, "application/json", bytes.NewReader(body))
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

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

// message is the JSON body of one Discord webhook call. Each message is a
// Components V2 message, so it has components and no content or embeds.
type message struct {
	Username        string          `json:"username"`   // the poster name
	AvatarURL       string          `json:"avatar_url"` // the poster icon
	Flags           int             `json:"flags"`      // the sender sets isComponentsV2
	Components      []component     `json:"components"`
	AllowedMentions allowedMentions `json:"allowed_mentions"`
}

// isComponentsV2 is the message flag of a Components V2 message. Discord
// takes it from a webhook only when the URL has with_components=true.
const isComponentsV2 = 1 << 15

// The Discord component types that winnow sends.
const (
	typeActionRow   = 1
	typeButton      = 2
	typeSection     = 9
	typeTextDisplay = 10
	typeThumbnail   = 11
	typeSeparator   = 14
	typeContainer   = 17
)

// styleLink is the Button style of a link button, and spacingLarge is the
// large spacing of a Separator.
const (
	styleLink    = 5
	spacingLarge = 2
)

// component is one Discord message component. Type selects the fields that
// Discord reads.
type component struct {
	Type        int         `json:"type"`
	Content     string      `json:"content,omitempty"`     // Text Display
	AccentColor int         `json:"accent_color,omitzero"` // Container
	Spacing     int         `json:"spacing,omitzero"`      // Separator: 1 is small, 2 is large
	Components  []component `json:"components,omitempty"`  // Container, Section, Action Row
	Accessory   *component  `json:"accessory,omitzero"`    // Section
	Media       *media      `json:"media,omitzero"`        // Thumbnail
	Style       int         `json:"style,omitzero"`        // Button
	Label       string      `json:"label,omitempty"`       // Button
	URL         string      `json:"url,omitempty"`         // Button
}

type media struct {
	URL string `json:"url"`
}

// display returns a Text Display with the markdown s.
func display(s string) component { return component{Type: typeTextDisplay, Content: s} }

// box returns a Container with the accent color, or no color for 0.
func box(color int, in ...component) component {
	return component{Type: typeContainer, AccentColor: color, Components: in}
}

// beside returns a Section with the texts and the image thumb on its right
// side. With no thumb, it returns the texts, because a Section needs an
// accessory.
func beside(thumb string, texts ...component) []component {
	if thumb == "" {
		return texts
	}
	return []component{{Type: typeSection, Components: texts, Accessory: &component{Type: typeThumbnail, Media: &media{URL: thumb}}}}
}

// button is one link button.
type button struct{ label, url string }

// buttonRow returns a Separator and an Action Row of link buttons, or
// nothing when bs is empty.
func buttonRow(bs []button) []component {
	if len(bs) == 0 {
		return nil
	}
	row := component{Type: typeActionRow}
	for _, b := range bs {
		row.Components = append(row.Components, component{Type: typeButton, Style: styleLink, Label: b.label, URL: b.url})
	}
	return []component{{Type: typeSeparator}, row}
}

// allowedMentions controls which mentions in a message ping. An empty Parse
// list (json/v2 encodes nil as []) makes sure that no text in the message
// pings anyone. Only the user IDs in Users can get a ping.
type allowedMentions struct {
	Parse []string `json:"parse"`
	Users []string `json:"users,omitempty"`
}

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
	// retryBase is the wait before the first retry after a server error or
	// a network error. Each next retry waits twice as long.
	retryBase time.Duration
}

func newDiscordSender(sc SinkConfig) *discordSender {
	return &discordSender{url: withComponents(sc.Discord), client: &http.Client{Timeout: 10 * time.Second}, retryBase: cmp.Or(sc.retryBase, time.Second)}
}

// withComponents adds with_components=true to the webhook URL raw, and keeps
// the query that raw has, for example thread_id. A URL that does not parse
// stays as it is, and its send fails.
func withComponents(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	q := u.Query()
	q.Set("with_components", "true")
	u.RawQuery = q.Encode()
	return u.String()
}

// send posts msg and retries it after a server error or a network error. When
// Discord does not take msg, send returns a *failure. The failure never holds
// the webhook URL, because the URL holds the webhook token.
func (d *discordSender) send(ctx context.Context, msg message) error {
	msg.Flags = isComponentsV2
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
			wait = cmp.Or(seconds(r.RetryAfter), d.retryBase)
		case err != nil || status >= 500:
			wait = d.retryBase << errs
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

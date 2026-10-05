package winnow

import (
	"bytes"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// message is the JSON body of one Discord webhook call.
type message struct {
	Embeds          []embed         `json:"embeds"`
	AllowedMentions allowedMentions `json:"allowed_mentions"`
}

type embed struct {
	Title       string `json:"title"`
	URL         string `json:"url,omitempty"`
	Description string `json:"description,omitempty"`
	Color       int    `json:"color,omitzero"`
}

// allowedMentions controls which mentions in a message ping. An empty Parse
// list (json/v2 encodes nil as []) makes sure that no text in the message
// pings anyone.
type allowedMentions struct {
	Parse []string `json:"parse"`
}

// discordSender sends messages to the webhook URL of one Discord channel.
type discordSender struct {
	url    string
	client *http.Client
}

func newDiscordSender(webhookURL string) *discordSender {
	return &discordSender{url: webhookURL, client: &http.Client{Timeout: 10 * time.Second}}
}

// send posts msg once. The error never holds the webhook URL, because the URL
// holds the webhook token.
func (d *discordSender) send(msg message) error {
	body, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	resp, err := d.client.Post(d.url, "application/json", bytes.NewReader(body))
	if err != nil {
		if ue, ok := errors.AsType[*url.Error](err); ok {
			err = ue.Err
		}
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		reply, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
		return fmt.Errorf("discord replied %d: %s", resp.StatusCode, reply)
	}
	return nil
}

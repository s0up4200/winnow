package winnow

import (
	"strconv"
	"strings"
)

// colorSecurity is the embed color of a security Event.
const colorSecurity = 0xE36209

// render turns an Event into one Discord message.
func render(e *Event) message {
	switch e.Name {
	case "dependabot_alert":
		return renderAlert(e, "Dependabot alert")
	case "code_scanning_alert":
		return renderAlert(e, "Code scanning alert")
	case "secret_scanning_alert":
		return renderAlert(e, "Secret scanning alert")
	case "repository_advisory":
		return renderAlert(e, "Repository advisory")
	}
	return fallback(e)
}

// renderAlert returns the message of a security Event:
// "[repo] <kind> <action>: #n summary", and one description line for each
// Alert field that is set. An Event with no Alert gets the Fallback message.
func renderAlert(e *Event, kind string) message {
	a := e.Alert
	if a == nil {
		return fallback(e)
	}
	title := "[" + e.Repo + "] " + kind + " " + e.Action + ": "
	if a.Number != 0 {
		title += "#" + strconv.Itoa(a.Number) + " "
	}
	var lines []string
	add := func(label, value string) {
		if value != "" {
			lines = append(lines, label+": "+value)
		}
	}
	add("Severity", a.Severity)
	if a.Package != "" {
		add("Package", a.Package+" ("+a.Ecosystem+")")
	}
	add("Patched in", a.Patched)
	add("Validity", a.Validity)
	return message{Embeds: []embed{{
		Title:       title + a.Summary,
		URL:         e.URL,
		Description: strings.Join(lines, "\n"),
		Color:       colorSecurity,
	}}}
}

// fallback returns the Fallback message: "<event>.<action> on <repo> by
// <sender>", with a link to the main object. It reads only the shared fields
// of the Event and never pings.
func fallback(e *Event) message {
	return message{Embeds: []embed{{Title: e.NameAction() + " on " + e.Repo + " by " + e.Sender, URL: e.URL}}}
}

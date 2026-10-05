package winnow

// render turns an Event into one Discord message.
func render(e *Event) message {
	return fallback(e)
}

// fallback returns the Fallback message: "<event>.<action> on <repo> by
// <sender>", with a link to the main object. It reads only the shared fields
// of the Event and never pings.
func fallback(e *Event) message {
	return message{Embeds: []embed{{Title: e.NameAction() + " on " + e.Repo + " by " + e.Sender, URL: e.URL}}}
}

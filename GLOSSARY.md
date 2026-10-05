# Glossary

**Source**:
One configured webhook endpoint with its own secret, for example the org webhook of `autobrr` on GitHub. Two GitHub orgs are two Sources.
_Avoid_: origin, input, provider

**Event**:
Winnow's record of one incoming webhook delivery, in one shape for both forges.
_Avoid_: payload, delivery, message

**Event name**:
The forge's name for what happened, for example `pull_request` or `push`. Winnow uses the GitHub names.
_Avoid_: event type, kind

**Bot sender**:
A sender that GitHub marks as a bot, whose login ends in `[bot]`, or whose login is in the bot list of the configuration.
_Avoid_: automated user, app

**Route**:
One entry in the ordered route list. It holds Rules and either the Sinks that get a matching Event, or a drop. The first Route whose Rules match an Event decides what happens to it.
_Avoid_: destination, target, filter

**Sink**:
A delivery target, for example the webhook URL of one Discord channel.
_Avoid_: output, channel, destination

**Fallback message**:
The generic message for a routed Event that has no Renderer. It names the event, the action, the repository, and the sender. It links to the main object.
_Avoid_: default message, generic embed

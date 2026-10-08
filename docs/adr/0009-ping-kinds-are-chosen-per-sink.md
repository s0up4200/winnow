# Ping kinds are chosen per Sink

A Sink lists the kinds of Ping that it keeps: `review_requested`, `assigned`, and `comments`. A message whose kind is off still goes to the Sink without a Ping. We chose the Sink because Pings are already a Sink setting (`mentions`), and each Sink is one Discord channel, where the Pings arrive.

## Considered options

- A `mentions: false` setting on a Route. Rules already match each kind by Event name and action, so this needs no new words. Rejected because each quiet kind needs one more Route above the general Route, with its own copy of `to:`. When someone adds a Sink to the general Route and forgets the quiet Route, messages of that kind go only to the old Sinks.
- A setting for each user in the User map. Rejected because it changes the shape of `users:`, and the problem is a noisy channel, not a person.

## Consequences

To make one repository quiet in a channel that other repositories share, define a second Sink with the same webhook URL and a different `mentions` list.

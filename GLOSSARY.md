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

**Rule**:
A condition on the fields of an Event, written as a matcher in the configuration.
_Avoid_: filter, expression

**Route**:
One entry in the ordered route list. It holds Rules and either the Sinks that get a matching Event, or a drop. The first Route whose Rules match an Event decides what happens to it.
_Avoid_: destination, target, filter

**Sink**:
A delivery target, for example the webhook URL of one Discord channel.
_Avoid_: output, channel, destination

**Sink delivery failure**:
One unsuccessful delivery of a message to one Sink, including all retries within that delivery. A later delivery of the same Digest can still succeed.
_Avoid_: dropped message, lost Event

**Renderer**:
A part of winnow that turns one kind of Event into a Discord message.
_Avoid_: formatter, template

**Poster**:
The name and icon that a Discord message shows as its sender. The Poster is the forge of the Event, for example `GitHub`.
_Avoid_: bot name, webhook user

**Fallback message**:
The generic message for a routed Event that has no Renderer. It names the event, the action, the repository, and the sender. It links to the main object.
_Avoid_: default message, generic embed

**Reference**:
A commit hash or `#n` in a body that winnow turns into a link to that commit, issue, or pull request.
_Avoid_: autolink, mention

**User map**:
The list that links a forge login to a Discord user. Winnow pings only users in this list.
_Avoid_: mention list, user mapping

**Ping**:
A Discord notification that winnow sends to a user in the User map, for a review request, an assignment, or an `@login` in a new comment.
_Avoid_: mention, notify

**Digest**:
A configured summary with Rules, a Period, and a Sink. It counts named activity from matching Events and can include Other by choice. A Digest does not depend on Routes.
_Avoid_: summary, report, roundup, digest sink

**Other**:
Activity in a Digest outside its named counts for PRs, issues, releases, stars, forks, and new discussions.
_Avoid_: noise

**Period**:
The calendar day, ISO week, calendar month, or calendar year that one Digest message covers.
_Avoid_: window, interval

**Star**:
One `star.created` Event. GitHub also sends `watch.started` for the same star, so winnow counts only `star.created`. An unstar (`star.deleted`) does not subtract.
_Avoid_: watch

**Backfill**:
An Event that winnow did not receive from the forge, and that the Sweep fetches from the forge API later. Winnow stores a Backfill for the Digests, and sends it only to a Route that accepts Backfills.
_Avoid_: redelivery, replay, catch-up

**Sweep**:
One pass over the recent deliveries of one Source. It stores each missed delivery as a Backfill.
_Avoid_: poll, sync, reconcile

# Digests watch Events beside the Routes

A Digest has its own Rules and reads stored Events. It does not get Events from the Routes. Routes do live delivery. Digests do counts for a Period. Both read the same Event, and neither knows about the other.

## Considered options

- A Digest Sink that gets Events from Routes. Rejected because each live Route must name the Digest Sink. If one Route does not, the counts are too low and nothing tells you.
- A digest setting on a Route. Rejected because the send time is part of delivery, and a Sink already does delivery.
- A buffer in memory that winnow sends at shutdown. Rejected because a crash loses the buffer.
- A snapshot file for digests only. Rejected because it is a second store next to the event store.
- Ask the forge API at the send time. Rejected because it needs a token for each Source, real calls to the forge, and a Forgejo client. Also, winnow then does more than react to webhooks.

## Consequences

Winnow stores each Event that a Digest matches, also when a Route drops it. A dropped star still counts. Digests need the durable event store.

# The T3 hook watches Pings beside the Routes

The T3 hook reads each admitted Event, also a Backfill and an Event that a Route drops. It does not get Events from the Routes. Only a Ping from a Trusted sender reaches it, and its request carries facts only, never text that the sender wrote.

## Considered options

- A T3 Sink in the Routes. Rejected because the first matching Route wins, and drop Routes, such as one that drops the repositories of a whole account, drop Pings too. Each live Route must also name the T3 Sink. If one Route does not, Pings go missing and nothing tells you.
- A filter in T3 Code on the raw forge webhooks. Rejected because T3 Code filters compare whole values and cannot search a comment for a mention, so each comment on each repository must start an agent.
- A poller of GitHub notifications. Rejected because GitHub keeps the reason `mention` on a thread after the first mention, so each later comment looks like a new Ping. It also has no Forgejo.

## Consequences

The Trusted sender list is the gate between the public forge and an agent on the operator's machine. Routes, Sinks, and Digests do not know about the T3 hook.

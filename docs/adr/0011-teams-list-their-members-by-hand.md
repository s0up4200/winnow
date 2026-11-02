# Teams list their members by hand

The Team map lists the members of each team in the configuration. Winnow pings each member through the User map.

## Considered options

- Read the members of a team from the GitHub API. Rejected because winnow then needs a token and an API call for each Event with a team, and Forgejo needs its own client.
- Link each team to a Discord role and send a role Ping. Rejected because someone must keep the role members current on Discord, and the Sink settings and `allowed_mentions` then need roles too.

## Consequences

When a team changes on the forge, the operator updates `teams:`. A member that is not in the User map gets no Ping, and winnow writes a warning at start.

# Rules match a shared Event, not the raw payload

Winnow turns each GitHub or Forgejo delivery into one Event with a fixed set of fields. The fields use the GitHub names. Rules can match only these fields. Rules cannot read the raw JSON.

A Rule on raw JSON paths can match any field with no code change. But the paths are different on GitHub and Forgejo. A Rule for one forge then does not match on the other, and nothing tells the operator. The Renderers already need one shape for both forges.

## Considered options

- Raw payload: Rules match JSON paths. We rejected it because each Rule then depends on one forge.
- Shared fields with raw JSON paths as an extra. We rejected it for v1 for the same reason. Add it only if the shared fields are not sufficient.

## Consequences

A Rule that needs a new field needs a code change: a new field on the Event and a mapping for each forge.

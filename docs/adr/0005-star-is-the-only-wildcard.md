# `*` is the only wildcard in a Rule

String values in a Rule are globs. Winnow uses `path.Match` from the Go standard library, but it first escapes `[`, `]`, `?`, and `\` in each pattern. Only `*` is a wildcard.

Each GitHub bot login ends in `[bot]`, for example `renovate[bot]`. `path.Match` reads `[bot]` as a character class. Without the escape, `sender: renovate[bot]` matches `renovatet` and does not match the bot. Nothing tells the operator.

## Considered options

- Full `path.Match` syntax, and the operator writes `renovate\[bot\]`. Rejected because a missed escape fails with no error.
- Globs only on `repo` and `owner`. Rejected because the escape is simpler and keeps `*` on all string fields.

## Consequences

A Rule cannot use `?` or `[a-z]`. No Rule needs them now.

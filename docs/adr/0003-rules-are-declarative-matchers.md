# Rules are declarative matchers, not an expression language

The operator writes each Rule as a YAML matcher: a map from an Event field to the values that the field accepts. A list means "any of". Several fields must all hold. A `not:` key holds matchers that make the Rule fail. String values are globs, with `*` as the only wildcard (see ADR 0005).

An expression language such as `expr` or CEL can express any condition in one line. But it adds a dependency and a second syntax inside the YAML file, and some of its errors appear only when an Event arrives. The Event has a small, fixed set of fields, so matchers cover every Rule that we know of.

## Considered options

- `expr` or CEL expressions. Rejected for the reasons above.
- Rules in Go code. Rejected because each Rule change then needs a new build.
- A `!` prefix on values for negation. Rejected because it cannot express "this sender, but only on this repository".

## Consequences

A condition that needs OR across fields needs several matchers in one Route's `match:` list. If a real Rule cannot be written as matchers, add an expression field then.

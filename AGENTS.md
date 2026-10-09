# winnow

## Agent skills

### Issue tracker

Issues are in GitHub Issues on `s0up4200/winnow`. The repository moves to `autobrr` later. See `docs/agents/issue-tracker.md`.

### Triage labels

The repository uses the default triage labels and the `wayfinder:*` labels. See `docs/agents/triage-labels.md`.

### Domain docs

The repository has a single context: one `GLOSSARY.md` and one `docs/adr/` at the root. See `docs/agents/domain.md`.

## Code Review Rules

Codex and other AI PR reviewers read this section. The other rules in this file are for coding agents. Do not apply them to PR authors.

### General

- Report a defect only when the change causes a concrete wrong behavior. Name the trigger and the result for the user. If you cannot name both, omit the finding.
- Check the merge base. If `main` already has the problem, still report it, but label it "already on main" and do not call it a regression.
- When the PR body, a linked issue, an ADR in `docs/adr/`, or a code comment calls a behavior deliberate, respond to that reason. Report a design flaw only when you can say why the stated reason does not hold.
- Leave gofmt and golangci-lint findings to CI and the pre-commit hook. Do not ask for docstrings or for comments that restate the code.
- Read earlier review threads. Repeat a resolved or refuted finding only with new evidence.

### Flag

- Treat a change to the SQL of a migration in `internal/winnow/migrations/` that is already on `main` as P1. Winnow records each migration by file name and runs it only once, so an install that ran it never runs the new SQL. Also flag a rename or a delete of such a file. Winnow then stops at start with "migration X is not in this version of winnow".
  Safe path: put the change in a new numbered migration.
- A change that breaks an accepted ADR in `docs/adr/`. Name the ADR. Examples: winnow stores a live Event before it writes the Sweep claim, so that a stop between the two writes does not remove the Event from a Digest (ADR 0008). The first matching Route wins (ADR 0004).
- Unbounded work on webhook body text before the 256 KiB cap, and work for each Sink that needs to run only once. A delivery can be 25 MiB, and winnow renders an Event once for each Sink. Give the call path and the input size.
- A test that can reach a real host, also through a client or a configuration that the test passes several layers down. Fixtures use the harness fakes, `httptest`, or `.invalid` hosts. The Sweep client uses the real GitHub API by default, so a test that loads a `sweep:` configuration outside the harness is a likely trigger.
- A change after which the README steps, the Compose example in the README, or `winnow.example.yaml` no longer start winnow.

### Do not flag

- A Markdown or mention edge case that only the CommonMark spec text supports. A Ping follows what GitHub renders. Report such a case only with GitHub's rendering as evidence, or when real forge comments are likely to trigger it.
- A rare sequence of configuration changes or a rare error path. Report one only when it loses data, sends a Digest again, or gives a result that the operator sees, and you can name a likely trigger. One operator runs each winnow install.

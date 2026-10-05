# A Forgejo Event name comes from `X-Forgejo-Event`

Forgejo sends two event headers. `X-Forgejo-Event` holds a coarse name, for example `pull_request`, `issues`, `issue_comment`, `push`, or `release`. `X-Forgejo-Event-Type` holds a fine name, for example `pull_request_sync`, `pull_request_assign`, `issue_assign`, or `pull_request_comment`. Winnow takes the Event name from `X-Forgejo-Event`. This decision replaces the Forge parsing rule of the spec (issue #14), which took the name from `X-Forgejo-Event-Type`.

No Rule with a GitHub Event name matches a fine name. A Rule with `event: pull_request` then does not match a Forgejo pull request sync, and nothing tells the operator. The coarse names are the GitHub names, so one Rule matches on both forges.

Winnow reads `X-Forgejo-Event-Type` only for the three review types, and it compares the full name. `pull_request_review_approved`, `pull_request_review_rejected`, and `pull_request_review_comment` become `pull_request_review` with action `submitted`. The review state comes from `review.type`. `pull_request_review_request` also starts with `pull_request_review_`, so a prefix test is wrong. That delivery stays a `pull_request` Event with action `review_requested`. A review comment and a comment on the pull request conversation stay different Events.

A Forgejo name with no GitHub twin stays as it is. The list of known Event names for the `event:` warning of `winnow check` holds these coarse Forgejo names: `wiki`, `action_run_failure`, `action_run_success`, and the seven states each of `workflow_run_*` and `workflow_job_*`. `label_updated` and `label_cleared` are actions, not Event names, so the list does not hold them.

## Considered options

- The Event name from `X-Forgejo-Event-Type`, as the spec said. Rejected because no Rule with a GitHub Event name matches the fine names.
- A table that maps each fine name to a GitHub name. Rejected because the payload already holds the action, so the table adds code and gives nothing.
- A prefix test on `pull_request_review_` for the reviews. Rejected because it also matches `pull_request_review_request`.

## Consequences

A Rule with a GitHub Event name matches the same Event on Forgejo. A Rule cannot match a fine Forgejo name such as `pull_request_assign`. It matches `event: pull_request, action: assigned` instead. A new Forgejo review type needs a new entry in the exact review map.

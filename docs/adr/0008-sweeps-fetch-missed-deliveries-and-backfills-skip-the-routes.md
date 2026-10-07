# Sweeps fetch missed deliveries, and Backfills skip the Routes

GitHub does not retry a delivery that fails, and winnow runs on one homelab node. An internet outage, a node reboot, or a rollout thus loses Events. ADR 0007 rejected calls to the forge API for the Digests. Winnow now calls the GitHub API, but only to find and fetch the deliveries that it missed, and only for a Source with a `sweep` block. Winnow stores a Backfill for the Digests. It sends the Backfill to the Sinks of a Route only when the first Route that matches it has `backfill: true`.

## Considered options

- Ask GitHub to send the delivery again. Rejected because a redelivery has the same GUID, headers, and body as a live delivery. Winnow cannot tell the two apart, so it cannot keep the redelivery out of the Routes.
- A scheduled GitHub Actions workflow. Rejected because GitHub can start a scheduled run late or skip it, and it turns off a scheduled workflow after 60 days without activity in the repository.
- Send each Backfill to its Route. Rejected because old Events then look new in Discord, and they arrive out of order. For example, a PR shows as opened after the message that it was merged.

## Consequences

A GitHub token lives in the cluster. ADR 0007 stays true for the Digest send itself: a Digest counts the stored Events and does not ask the forge API.

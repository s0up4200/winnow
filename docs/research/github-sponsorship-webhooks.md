# GitHub Sponsors webhooks

This is research for issue #18 ("Support sponsor webhooks"). The research date is 2026-10-11.

Terms:

- A Sponsors listing is the GitHub Sponsors profile of one user or organization. People pay this account through GitHub Sponsors.
- A Sponsors webhook is a webhook that the owner of a Sponsors listing adds in the Sponsors dashboard. It is not an org webhook, a repository webhook, or a GitHub App webhook.
- A private sponsorship is a sponsorship with `privacy_level: private`.

These sources occur often:

- [events]: the GitHub docs page "Webhook events and payloads", section `sponsorship`.
- [oas]: the OpenAPI description in `github/rest-api-description`, file `descriptions/api.github.com/api.github.com.json`, commit `58b1e0c00b39b9e46c24dc4a0ba6a2669c90e9c1`. The webhook schemas are under `x-webhooks` and `components.schemas`.
- [octokit]: the repository `octokit/webhooks`, commit `aa80c24a3f55354465b8b1438d33dd5602956d0d`. The schemas are in `payload-schemas/api.github.com/sponsorship/`. The examples are in `payload-examples/api.github.com/sponsorship/`.
- [gql]: the public GraphQL schema in `octokit/graphql-schema`, file `schema.graphql`, commit `82ff2d4780080e6929ebb95608cefa22dfa05ac7`.

"UNCONFIRMED" marks a claim that no primary source states.

## Summary for winnow

- Only a Sponsors webhook gets the `sponsorship` event. An org webhook does not. [oas] [events] The operator must add one more Source for each Sponsors listing.
- The `autobrr` org has no Sponsors listing. The FUNDING files of the autobrr repositories name the users `zze0s` and `s0up4200`. Both users have a public Sponsors listing. Thus each Source belongs to one user, and only that user can add the webhook. See section 6.
- No API lists the deliveries of a Sponsors webhook. Thus a Sponsors Source cannot have a Sweep. [failed] If winnow misses a delivery, the owner must redeliver it by hand in the dashboard within 3 days. [redeliver]
- In the GitHub example of a private sponsorship, `sponsorship.sponsor` and `sender` still hold the login of the sponsor. [octokit] Thus winnow must hide the login itself. Today winnow shows the login in the Fallback message and writes it to the decision log line.
- The payload has no `repository`. Today the Fallback message reads "sponsorship.created on  by <login>" and has no link.
- GitHub sends `cancelled`, `pending_cancellation`, and `edited` only for a monthly sponsorship. A one-time sponsorship gets only `created`. [oas]

## 1. Where GitHub sends the event

- In [oas], each `sponsorship` webhook has `"supported-webhook-types": ["sponsors_listing"]`. [events] gives the same availability: `sponsors_listing` only.
- "You can only create a sponsorship webhook on GitHub.com." [events] [oas]
- "Only the owner of the sponsored account can create sponsorship webhooks for that account." [create]
- The owner can add the webhook "for your sponsored user or organization account". [sponsors-config]
- To add the webhook, the owner clicks the profile picture, "Your sponsors", "Dashboard" next to the account, "Webhooks", and "Add webhook". [create]
- The fields are Payload URL, Content type, Secret, and Active. The docs call the content type and the secret optional. [create]

Effect on winnow:

- Winnow rejects a delivery without a valid `X-Hub-Signature-256` (HTTP 401). It also rejects a delivery that is not `application/json` (HTTP 415). Thus the operator must set a secret and select `application/json`. The README step must say both.
- The docs do not give the default content type of a Sponsors webhook (UNCONFIRMED).
- [oas] lists `X-Hub-Signature-256` as a header of each `sponsorship` webhook. Thus the current signature test works.

## 2. Actions

The source is the `description` of each `x-webhooks` entry in [oas]. [events] gives the same list of actions.

| Action | When GitHub sends it |
|---|---|
| `created` | "A sponsor created a sponsorship for a sponsored account. This event occurs once the payment is successfully processed." |
| `cancelled` | "A sponsorship was cancelled and the last billing cycle has ended." Only for a recurring (monthly) sponsorship, "not sent for one-time sponsorships". |
| `edited` | "A monthly sponsor changed who can see their sponsorship." |
| `pending_cancellation` | "A sponsor scheduled a cancellation for their sponsorship. The cancellation will become effective on their next billing date." Only for a monthly sponsorship. |
| `pending_tier_change` | "A sponsor scheduled a downgrade to a lower sponsorship tier. The new tier will become effective on their next billing date." |
| `tier_changed` | "A sponsor changed the tier of their sponsorship and the change has taken effect." An upgrade takes effect at once. A downgrade takes effect at the next billing cycle. |

Effect on winnow:

- A downgrade gives two Events: `pending_tier_change`, and later `tier_changed` at the next billing date. A cancellation also gives two Events: `pending_cancellation`, and later `cancelled`. If winnow renders all six actions, Discord shows each of these changes two times. A Route with `action:` can drop one Event of each pair.
- `edited` is only a change of privacy. See section 4.

## 3. Payload fields

The sources are the schemas `webhooks_sponsorship` and `webhook-sponsorship-*` in [oas], and the schemas and examples in [octokit].

| Field | Type | Notes |
|---|---|---|
| `action` | string | One of the six actions. |
| `sponsorship.node_id` | string | Required. |
| `sponsorship.created_at` | string | Required. A date-time in [octokit]. |
| `sponsorship.sponsor` | user object | Required, but `nullable: true` in [oas]. Not nullable in [octokit]. |
| `sponsorship.sponsorable` | user object | The sponsored account. Required, and `nullable: true` in [oas]. |
| `sponsorship.privacy_level` | string | Required. The webhook schemas give no enum. The examples use `public` and `private`. The GraphQL enum `SponsorshipPrivacy` has only `PRIVATE` and `PUBLIC`. [gql] |
| `sponsorship.tier.name` | string | Required. Example: `"$5 a month"`. |
| `sponsorship.tier.is_one_time` | boolean | Required. |
| `sponsorship.tier.monthly_price_in_dollars` | integer | Required. |
| `sponsorship.tier.is_custom_amount` | boolean | Required in [octokit]. Not required in [oas]. |
| `sponsorship.tier.description` | string | Required. The owner writes this text. |
| `changes.tier.from` | tier object | In `tier_changed` and `pending_tier_change`. The tier before the change. |
| `changes.privacy_level.from` | string | In `edited`. The privacy level before the change. |
| `effective_date` | string | In `pending_cancellation` and `pending_tier_change`: "the date the cancellation or tier change will take effect." |
| `sender` | user object | Required. |
| `repository`, `organization`, `installation`, `enterprise` | objects | [oas] allows them but does not require them. The [octokit] schemas do not allow them (`additionalProperties: false`). |

- In the examples, the tier `name` includes the price ("$5 a month"). [octokit] UNCONFIRMED: that a real tier name always includes the price. The owner names each tier.
- A sponsor can be an organization. The GraphQL field `sponsorEntity` is "The user or organization that is sponsoring". [gql] The webhook user object has a `type` field.

Effect on winnow:

- `parseEvent` reads `repository`. Thus `Repo`, `Owner`, `RepoURL`, and `URL` are empty for a sponsorship. Only these Rule fields can match it: `event:`, `action:`, `source:`, `forge:`, `sender:`, and `sender_bot:`.
- The Renderer must accept a null `sponsor`. It can use `sender` in place of the sponsor only when the sponsorship is public.
- The Digest code already skips the repository line for an Event with an empty `Repo`.

## 4. Private sponsorships

- The [octokit] example `downgraded.payload.json` has `privacy_level: private`. Its `sponsorship.sponsor.login` and `sender.login` are both `monalisa`. Thus GitHub does not remove the login from the payload of a private sponsorship. This example is not a recorded real delivery (UNCONFIRMED for real deliveries).
- In both [octokit] examples, `sender` is the sponsor.
- The GraphQL field `Sponsorship.sponsor` "Returns null if the sponsorship is private". The field `sponsorEntity` returns the sponsor "if you have permission to view them". [gql] The webhook schema has no such rule.
- An `edited` Event can change a public sponsorship to private. `changes.privacy_level.from` holds the old value. [oas]

Effect on winnow:

- The Fallback message names `e.Sender`. Thus today a Discord user sees the login of a private sponsor. The issue is correct.
- The decision log line has the field `sender`. The stored Event JSON for a Digest has `Sender`. Thus the login of a private sponsor goes to the log and to the database. The operator is the sponsored account, but the log can go to other systems.
- One fix is to clear `Sender`, `SenderURL`, and `SenderAvatar` in `parseEvent` when the sponsorship is private. Then the Renderer, the Fallback message, the log, the store, and a `sender:` Rule get no login.
- An `edited` Event to private must also hide the login. An `edited` Event to public can show it.

## 5. Sweep: no deliveries API

- "There are no API endpoints to get data about GitHub Marketplace webhooks or GitHub Sponsors webhooks." [failed]
- [oas] has delivery paths only for `/app/hook`, `/orgs/{org}/hooks/{hook_id}`, and `/repos/{owner}/{repo}/hooks/{hook_id}`. It has no path for a Sponsors listing.
- [gql] has no type or field with "webhook" in its name.
- The owner can see and redeliver the deliveries of the past 3 days in the Sponsors dashboard. [viewing] [redeliver]

Effect on winnow:

- The Sweep calls `/orgs/{org}/hooks/{hook}/deliveries`. It cannot work for a Sponsors Source. Thus the Sponsors Source must not have `sweep:`, and the README must say so.
- If winnow is down, it loses a sponsor Event until the owner redelivers it by hand. GitHub does not retry a failed delivery. See `github-webhook-deliveries-api.md`.

## 6. Who owns the Sponsors listings for autobrr

The GitHub REST and GraphQL APIs gave these results on 2026-10-11:

- `GET /users/autobrr` returns `"type": "Organization"`.
- The GraphQL query `organization(login: "autobrr") { hasSponsorsListing }` returns `false`.
- `.github/FUNDING.yml` in `autobrr/autobrr` has `github: zze0s`.
- `autobrr/qui`, `autobrr/mkbrr`, `autobrr/netronome`, and `autobrr/dashbrr` have `github: [s0up4200, zze0s]`. `autobrr/omegabrr` has `github: [zze0s, s0up4200]`.
- The GraphQL query `user(login: ...) { sponsorsListing { name isPublic } }` returns `sponsors-zze0s` (public) and `sponsors-s0up4200` (public).

Effect on winnow:

- "A new sponsor of autobrr" is a new sponsor of `zze0s` or of `s0up4200`. Each user must add a Sponsors webhook to winnow. That is two Sources with two secrets.
- The Renderer can name the sponsored account from `sponsorship.sponsorable.login`. Then one Sink can show the two listings apart.

## 7. Ping and headers

- [oas] lists the `ping` webhook for `repository`, `organization`, `app`, `business`, and `marketplace`. It does not list `sponsors_listing`. The Sponsors section of [create] does not say that GitHub sends a ping, but the other sections say it. UNCONFIRMED: whether a new Sponsors webhook gets a ping. Winnow answers a ping with 200 in both cases.
- Each `sponsorship` webhook in [oas] has the headers `User-Agent`, `X-GitHub-Hook-ID`, `X-GitHub-Event`, `X-GitHub-Hook-Installation-Target-ID`, `X-GitHub-Hook-Installation-Target-Type`, `X-GitHub-Delivery`, and `X-Hub-Signature-256`.
- UNCONFIRMED: the value of `X-GitHub-Hook-Installation-Target-Type` for a Sponsors webhook. In [oas], the example is `repository` for every webhook. Winnow does not read this header.

## Open questions

1. Does a real private delivery contain the login of the sponsor? The [octokit] example says yes. A real delivery to a test Source can show it.
2. Who is the `sender` of `cancelled`, and of a `tier_changed` for a downgrade? GitHub makes these changes at the billing date, not the sponsor. No source gives the sender. It can be the sponsor or the `ghost` account.
3. What is the default content type of a new Sponsors webhook? The operator must select `application/json` in all cases.
4. Does a new Sponsors webhook get a `ping`?
5. Can the webhook payload have an organization as the sponsor, with `type: Organization`? [gql] says that a sponsor can be an organization. No webhook example shows one.
6. Does a Digest count new sponsors as a named count, or keep them in Other? This is a design question, not a research question. A one-time sponsor also arrives as `created`. Thus a count of `sponsorship.created` counts one-time and monthly sponsors together.

[events]: https://docs.github.com/en/webhooks/webhook-events-and-payloads#sponsorship
[oas]: https://github.com/github/rest-api-description/blob/58b1e0c00b39b9e46c24dc4a0ba6a2669c90e9c1/descriptions/api.github.com/api.github.com.json
[octokit]: https://github.com/octokit/webhooks/tree/aa80c24a3f55354465b8b1438d33dd5602956d0d/payload-schemas/api.github.com/sponsorship
[gql]: https://github.com/octokit/graphql-schema/blob/82ff2d4780080e6929ebb95608cefa22dfa05ac7/schema.graphql
[create]: https://docs.github.com/en/webhooks/using-webhooks/creating-webhooks
[sponsors-config]: https://docs.github.com/en/sponsors/integrating-with-github-sponsors/configuring-webhooks-for-events-in-your-sponsored-account
[failed]: https://docs.github.com/en/webhooks/using-webhooks/handling-failed-webhook-deliveries
[viewing]: https://docs.github.com/en/webhooks/testing-and-troubleshooting-webhooks/viewing-webhook-deliveries
[redeliver]: https://docs.github.com/en/webhooks/testing-and-troubleshooting-webhooks/redelivering-webhooks

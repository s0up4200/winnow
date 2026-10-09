# GitHub organization webhook deliveries API

This is research for the Sweep. The research date is 2026-10-07. The sources are the GitHub docs and the OpenAPI description in `github/rest-api-description` (branch `main`). The schemas for these endpoints are the same in API versions `2022-11-28` and `2026-03-10`.

These sources occur often:

- [REST: organization webhooks][rest] (docs.github.com)
- [OpenAPI description][oas], file `descriptions/api.github.com/api.github.com.json`
- [Permissions for fine-grained PATs][fgperms]

"UNCONFIRMED" marks a claim that no primary source states.

## Summary for the Sweep

- The list endpoint is `GET /orgs/{org}/hooks/{hook_id}/deliveries`. The detail endpoint is `GET /orgs/{org}/hooks/{hook_id}/deliveries/{delivery_id}`. [rest]
- A list entry has no headers and no payload. The Sweep must call the detail endpoint for each failed delivery. [oas]
- `status` is a free-text string. A good delivery has `status: "OK"`. The GitHub sample code treats any other value as a failure. [autoredeliver]
- The `status=failure` filter returns only deliveries with a `status_code` from 400 to 599. [oas] A time-out or a connection error can have a `status_code` outside that range (UNCONFIRMED). Do not use only the filter. Read all entries and test for `status != "OK"`.
- Each redelivery is a separate entry. It has its own `id`, the same `guid`, and `redelivery: true`. [oas-example] [autoredeliver] Group the entries by `guid`. Skip a `guid` that has one `OK` entry.
- Pagination uses an opaque `cursor`. Follow the `Link` header. The maximum `per_page` is 100. Newest entries come first. [oas] [autoredeliver]
- GitHub keeps deliveries for 3 days. [viewing] The Sweep must run more often than that.
- GitHub does not retry a failed delivery. The receiver must answer in 10 seconds. [failed] [handling]
- A fine-grained PAT needs the organization permission "Webhooks" at read level for list and detail. [fgperms] The token owner must be an organization owner. [rest] A token of an org owner needs no approval. The default maximum lifetime is 366 days. [patpolicy]
- `request.headers` is an object. In the docs example, the values are strings, and the headers include `X-GitHub-Event`, `X-GitHub-Delivery`, and `X-Hub-Signature-256`. `request.payload` is a JSON object, not a string. `response.payload` is a string. [oas]

## 1. Endpoints

| Use | Method and path |
|---|---|
| List deliveries | `GET /orgs/{org}/hooks/{hook_id}/deliveries` |
| Get one delivery | `GET /orgs/{org}/hooks/{hook_id}/deliveries/{delivery_id}` |
| Redeliver (winnow does not use it) | `POST /orgs/{org}/hooks/{hook_id}/deliveries/{delivery_id}/attempts`, returns `202` |

Sources: [rest], [oas].

- `hook_id` and `delivery_id` are integers. [oas]
- Each delivery also carries the value of `hook_id` in the `X-GitHub-Hook-ID` header. [oas]
- The two GET endpoints document the responses `200`, `400`, and `422`. [oas]

## 2. List response fields

The schema is `hook-delivery-item` ("Simple webhook delivery"). Source: [oas].

| Field | Type | Nullable | Notes |
|---|---|---|---|
| `id` | integer, int64 | no | ID of this delivery attempt |
| `guid` | string | no | "Unique identifier for the event (shared with all deliveries for all webhooks that subscribe to this event)" |
| `delivered_at` | string, date-time | no | |
| `redelivery` | boolean | no | |
| `duration` | number | no | "Time spent delivering", example `0.03`. The schema does not give the unit. |
| `status` | string | no | "Describes the response returned after attempting the delivery", example `"failed to connect"` |
| `status_code` | integer | no | Example `502` |
| `event` | string | no | Example `"issues"` |
| `action` | string | yes | |
| `installation_id` | integer, int64 | yes | |
| `repository_id` | integer, int64 | yes | |
| `throttled_at` | string, date-time | yes | Not in `required` |

All fields except `throttled_at` are in `required`. [oas]

### Values of status and status_code for a failure

- A good delivery has `status: "OK"` and `status_code: 200`. [oas-example]
- The schema example for a failure is `status: "failed to connect"` with `status_code: 502`. [oas]
- The troubleshooting page names these errors, and others: `failed to connect to host`, `failed to connect to network`, `timed out`, and `invalid HTTP response`. [troubleshoot]
- "The `timed out` error indicates that GitHub did not receive a response from your server within 10 seconds." [troubleshoot]
- "The `invalid HTTP response` error occurs when your server returns a 4xx or 5xx status." [troubleshoot]
- UNCONFIRMED: the exact `status` string for each error. No source gives the case, or a suffix such as the code.
- UNCONFIRMED: the `status_code` for a time-out or a connection failure. No source says `0`. The schema example gives `502` for "failed to connect".
- The GitHub sample code finds failures only with `delivery.status === "OK"`. [autoredeliver] Winnow can use the same test.

### Redeliveries

- Each redelivery is a new list entry. The docs example list has two entries with the same `guid` and different `id` values. The first has `redelivery: false`, and the second has `redelivery: true`. [oas-example]
- "The GUID is constant across redeliveries of the same delivery." [autoredeliver]

## 3. Pagination and filters

Source: [oas], unless another source is given.

- `per_page` is an integer. The default is 30 and the maximum is 100.
- `cursor` is a string. "The starting delivery from which the page of deliveries is fetched. Refer to the `link` header for the next and previous page cursors."
- `status` is an enum with the values `success` and `failure`. "A `status` of `success` returns deliveries with a `status_code` in the 200-399 range (inclusive). A `status` of `failure` returns deliveries with a `status_code` in the 400-599 range (inclusive)."
- The endpoint has no `since` parameter and no other time filter. The only parameters are `org`, `hook_id`, `per_page`, `cursor`, and `status`.
- The `Link` header has the form `<URL>; rel="next"`. The other relations are `prev`, `first`, and `last`. Use the URLs from the header. Do not make them yourself. [pagination]
- UNCONFIRMED: which relations this endpoint sends. The parameter text names only "next and previous page cursors".
- If `per_page` is above the maximum, GitHub uses the maximum and returns no error. [pagination]
- The newest entries come first. The docs do not say this in words, but the GitHub sample code depends on it. The code reads pages until the last entry of a page is older than the previous run, and then it stops. [autoredeliver]

## 4. Retention

- "You can view details about webhook deliveries from the past 3 days." [viewing]
- "You can redeliver webhook deliveries that occurred in the past 3 days." [redeliver]
- The 2021 changelog said "the last 30 days". [changelog] The current docs say 3 days. Use 3 days.

## 5. Delivery time-out and retries

- "Your server should respond with a 2XX response within 10 seconds of receiving a webhook delivery. If your server takes longer than that to respond, then GitHub terminates the connection and considers the delivery a failure." [handling]
- "GitHub does not automatically redeliver failed webhook deliveries." [failed]
- A delivery "can take a few minutes" to show in the log. [troubleshoot]

## 6. Detail response

The schema is `hook-delivery` ("Webhook delivery"). It has all the list fields, plus the fields below. Source: [oas].

| Field | Type | Nullable | Notes |
|---|---|---|---|
| `url` | string | no | Target URL of the delivery. Not in `required`. |
| `request.headers` | object, `additionalProperties: true` | yes | "The request headers sent with the webhook delivery." |
| `request.payload` | object, `additionalProperties: true` | yes | "The webhook payload." It is a JSON object, not a string. |
| `response.headers` | object, `additionalProperties: true` | yes | |
| `response.payload` | string | yes | "The response payload received." |

- `request` and `response` are required. Each of their two fields is required but nullable. [oas]
- The schema does not give the type of a header value. In the example, each value is a string, and each header has one value. Decode the headers to `map[string]string`, and return an error for other shapes.
- The case of header names is mixed in the example (`content-type` and `X-GitHub-Event`). Find headers without regard to case.
- The example `request.headers` has `X-GitHub-Delivery` (the same value as `guid`), `X-GitHub-Event`, `X-GitHub-Hook-ID`, `X-GitHub-Hook-Installation-Target-ID`, `X-GitHub-Hook-Installation-Target-Type`, `X-Hub-Signature`, `X-Hub-Signature-256`, `User-Agent`, `Accept`, and `content-type`. [oas-example]
- UNCONFIRMED: that real responses always have the signature headers. GitHub sends them only when the hook has a secret. The example shows them.
- `request.payload` is parsed JSON, so it is not the exact bytes that GitHub sent. Winnow cannot test `X-Hub-Signature-256` against a re-encoded payload. The Sweep gets the data with a trusted token, so it does not need the signature.

## 7. Authentication

- A fine-grained PAT needs the organization permission "Webhooks". List and detail need read access. Redeliver needs write access. [fgperms]
- "You must be an organization owner to use this endpoint." All three endpoints have this text. [rest] [oas]
- "Only organization owners can view deliveries for webhooks in that organization." [viewing]
- A classic PAT or an OAuth app token needs the `admin:org_hook` scope. [rest] [oas]
- "OAuth apps cannot list, view, or edit webhooks that they did not create and users cannot list, view, or edit webhooks that were created by OAuth apps." [rest]
- The endpoints work with GitHub Apps (`enabledForGitHubApps: true`). [oas]
- A fine-grained PAT can access the resources of one owner only. [pat]
- An organization can block fine-grained PATs. [pat]
- The default organization policy is: "An organization owner must approve each fine-grained personal access token that can access the organization. Fine-grained personal access tokens created by organization owners will not need approval. This is the default value." [patpolicy] The Sweep token belongs to an org owner, so it needs no approval.
- Before approval, a pending token can read only public resources. [pat]
- "For fine-grained personal access tokens, the default the maximum lifetime policy for organizations is set to expire within 366 days." [patpolicy] A PAT with no expiry date is possible, but an organization or enterprise policy can block it. [pat]
- UNCONFIRMED: the range of values that an organization can set for the maximum lifetime.

## 8. Rate limits

Source: [ratelimits].

- The primary limit for a PAT is 5,000 requests per hour. All other requests of that user count toward the same limit.
- The secondary limits are 100 concurrent requests, 900 points per minute for REST, and 90 seconds of CPU time per 60 seconds of real time.
- Most `GET` requests cost 1 point. `POST`, `PATCH`, `PUT`, and `DELETE` requests usually cost 5 points.
- One Sweep costs 1 list call per 100 deliveries, plus 1 detail call for each failed `guid`.

## Examples

This is a list entry, from the docs example `hook-delivery-items`. [oas-example]

```json
{
  "id": 123456789,
  "guid": "0b989ba4-242f-11e5-81e1-c7b6966d2516",
  "delivered_at": "2019-06-04T00:57:16Z",
  "redelivery": true,
  "duration": 0.28,
  "status": "OK",
  "status_code": 200,
  "event": "issues",
  "action": "opened",
  "installation_id": 123,
  "repository_id": 456,
  "throttled_at": null
}
```

This is a detail response, from the docs example `hook-delivery`. [oas-example]

```json
{
  "id": 12345678,
  "guid": "0b989ba4-242f-11e5-81e1-c7b6966d2516",
  "delivered_at": "2019-06-03T00:57:16Z",
  "redelivery": false,
  "duration": 0.27,
  "status": "OK",
  "status_code": 200,
  "event": "issues",
  "action": "opened",
  "installation_id": 123,
  "repository_id": 456,
  "url": "https://www.example.com",
  "throttled_at": "2019-06-03T00:57:16Z",
  "request": {
    "headers": {
      "X-GitHub-Delivery": "0b989ba4-242f-11e5-81e1-c7b6966d2516",
      "X-Hub-Signature-256": "sha256=6dcb09b5b57875f334f61aebed695e2e4193db5e",
      "Accept": "*/*",
      "X-GitHub-Hook-ID": "42",
      "User-Agent": "GitHub-Hookshot/b8c71d8",
      "X-GitHub-Event": "issues",
      "X-GitHub-Hook-Installation-Target-ID": "123",
      "X-GitHub-Hook-Installation-Target-Type": "repository",
      "content-type": "application/json",
      "X-Hub-Signature": "sha1=a84d88e7554fc1fa21bcbc4efae3c782a70d2b9d"
    },
    "payload": {
      "action": "opened",
      "issue": { "body": "foo" },
      "repository": { "id": 123 }
    }
  },
  "response": {
    "headers": { "Content-Type": "text/html;charset=utf-8" },
    "payload": "ok"
  }
}
```

[rest]: https://docs.github.com/en/rest/orgs/webhooks
[oas]: https://github.com/github/rest-api-description/blob/main/descriptions/api.github.com/api.github.com.json
[oas-example]: https://github.com/github/rest-api-description/blob/main/descriptions/api.github.com/api.github.com.json
[fgperms]: https://docs.github.com/en/rest/authentication/permissions-required-for-fine-grained-personal-access-tokens
[autoredeliver]: https://docs.github.com/en/webhooks/using-webhooks/automatically-redelivering-failed-deliveries-for-an-organization-webhook
[failed]: https://docs.github.com/en/webhooks/using-webhooks/handling-failed-webhook-deliveries
[handling]: https://docs.github.com/en/webhooks/using-webhooks/handling-webhook-deliveries
[viewing]: https://docs.github.com/en/webhooks/testing-and-troubleshooting-webhooks/viewing-webhook-deliveries
[redeliver]: https://docs.github.com/en/webhooks/testing-and-troubleshooting-webhooks/redelivering-webhooks
[troubleshoot]: https://docs.github.com/en/webhooks/testing-and-troubleshooting-webhooks/troubleshooting-webhooks
[pagination]: https://docs.github.com/en/rest/using-the-rest-api/using-pagination-in-the-rest-api
[changelog]: https://github.blog/changelog/2021-06-30-webhook-deliveries-api/
[pat]: https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/managing-your-personal-access-tokens
[patpolicy]: https://docs.github.com/en/organizations/managing-programmatic-access-to-your-organization/setting-a-personal-access-token-policy-for-your-organization
[ratelimits]: https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api

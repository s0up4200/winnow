# winnow

Winnow receives GitHub and Forgejo webhooks and sends them to Discord channels. Each forge sends its webhooks to winnow, not to Discord. Winnow checks the signature of each delivery and turns it into one Event. Then it uses your Routes to send the Event to one or more Discord channels, or to drop it.

Winnow has one binary, one container, and one YAML configuration file. [GLOSSARY.md](GLOSSARY.md) defines the terms in this README (Source, Event, Route, Rule, Sink).

## Run winnow

The image is `ghcr.io/s0up4200/winnow`. It supports linux/amd64. A GitHub Actions workflow builds and pushes it for each tag that starts with `v`. Each push also moves the `latest` tag.

1. Make a `config` directory. Copy [winnow.example.yaml](winnow.example.yaml) to `config/winnow.yaml` and change it for your forges and channels.
2. Put the secrets and the Discord webhook URLs in a `winnow.env` file next to the Compose file, one `NAME=value` on each line.
3. If you use Digests or a Sweep, make the `config` directory writable for the user `65532` of the container: `sudo chown 65532 config`. Winnow keeps its store in this directory.
4. Start winnow with Docker Compose:

```yaml
services:
  winnow:
    image: ghcr.io/s0up4200/winnow:latest
    restart: unless-stopped
    stop_grace_period: 15s
    env_file: winnow.env
    volumes:
      - ./config:/config
    ports:
      - "127.0.0.1:8080:8080"
```

Keep `stop_grace_period: 15s`. On a stop, winnow uses up to 10 s to send the messages that are still in its queues. By default, Docker kills the container 10 s after the stop signal. Without the longer grace period, you can lose the log lines that name the messages that winnow did not send.

Put a reverse proxy with HTTPS in front of port 8080. The forges send each delivery to `https://<your-host>/hook/<source-name>`.

The image has a health check. `winnow healthcheck` sends `GET /healthz` to the port of `listen` and exits with 0 when the reply is 200. The image has no `curl`, so the health check uses this command.

## Configuration

Winnow reads `/config/winnow.yaml`. To use a different file, start winnow with `--config <path>`. The [sample configuration](winnow.example.yaml) shows a complete file.

### Sources

A Source is one webhook endpoint with its own secret. Each Source has the URL path `/hook/<source-name>`. For example, the Source `github-autobrr` receives deliveries on `/hook/github-autobrr`.

```yaml
sources:
  github-autobrr: { secret: "${WINNOW_SECRET_GITHUB_AUTOBRR}" }
  forgejo:
    secret: ${WINNOW_SECRET_FORGEJO}
```

Each Source must have a secret. Winnow rejects a delivery with a missing or wrong signature (HTTP 401). You cannot turn this check off. Winnow finds the forge from the request headers, so a Source has no `forge:` key.

A GitHub Source can also have a `sweep` block. See [Sweeps](#sweeps).

Winnow listens on `:8080`. To use a different address, set `listen:`, for example `listen: ":9000"`.

### Secrets from the environment

Winnow reads a value from an environment variable when the value is one `${VAR}` placeholder and nothing else. This works only for a Source `secret`, a Sweep `token`, and a Sink `discord` URL. If the variable is unset or empty, winnow does not start.

A literal value also works, for example `secret: test-secret`. Use this only for a local test.

In a flow mapping (a mapping in `{ }`), put the placeholder in quotes, for example `{ secret: "${WINNOW_SECRET_FORGEJO}" }`. Without the quotes, the YAML parser reads the `}` of the placeholder as the end of the mapping. In block style (one key on each line), you do not need the quotes.

### Sinks

A Sink is the webhook URL of one Discord channel.

```yaml
sinks:
  qui: { discord: "${WINNOW_DISCORD_QUI}", mentions: true }
```

A Sink pings a Discord user only when the Sink keeps the kind of Ping and the user is in `users:`. The `mentions` value of a Sink sets the kinds of Ping that it keeps:

- `mentions: true` keeps all kinds.
- `mentions: false`, `mentions: []`, or no `mentions` key keeps no kind.
- A list keeps only the kinds in the list, for example `mentions: [assigned, comments]`.

The kinds are:

- `review_requested`: a Ping to the requested reviewer of a pull request.
- `assigned`: a Ping to the assignee of an issue or a pull request. A Forgejo assignment does not ping, because the Forgejo payload does not name the assignee.
- `comments`: a Ping for each direct `@login` mention in a new comment.

If the Sink does not keep the kind, the Sink still gets the message, but without a Ping. Winnow does not start when the list has an unknown kind. The error names the valid kinds. Winnow also does not start when the `mentions` key has no value.

To make one repository quiet in a channel that other repositories share, define a second Sink with the same webhook URL and a different `mentions` list. Then route that repository to the second Sink:

```yaml
sinks:
  dev: { discord: "${WINNOW_DISCORD_DEV}", mentions: true }
  dev-quiet: { discord: "${WINNOW_DISCORD_DEV}", mentions: [assigned, comments] }
routes:
  - match: { repo: autobrr/qui }
    to: [dev-quiet]
  - match: {}
    to: [dev]
```

Login matching ignores letter case. Repeated mentions and aliases produce one ping per Discord user ID. Winnow does not ping the sender for their own login.

Comment pings apply to new issue comments, comments on pull requests, code review comments, and discussion comments on GitHub and Forgejo. A submitted review summary does not cause comment pings. Edits, deletions, and issue, pull request, or discussion bodies do not cause comment pings.

Mentions in blockquotes, inline code, fenced and indented code blocks, URLs, image alt text, hidden HTML comments, lines of raw HTML such as `<details>` without a blank line after it, footnote labels, and escaped mentions stay quiet. Team mentions and unmapped users stay quiet. Raw Discord mention text cannot add pings. Pings appear above the embed, and the comment keeps its forge login text. A mention beyond the excerpt limit still pings. A comment larger than 256 KiB causes no pings. GitHub comments are never that large. Each message pings distinct users in mention order, up to 100 users and 2,000 content characters. Excess users stay quiet so that Discord can accept the comment notification.

```yaml
users:
  octocat: "123456789012345678"   # forge login: Discord user ID
```

A Forgejo assignment does not ping. The Forgejo payload does not name the assigned user.

### Routes

The Routes are one ordered list. Winnow tries each Route in file order. The first Route that matches the Event decides. If no Route matches, winnow drops the Event and writes a DEBUG log line.

```yaml
routes:
  - name: bot-comments
    drop: true
    match:
      event: [issue_comment, pull_request_review, pull_request_review_comment]
      sender_bot: true
      not:
        - sender: ["renovate[bot]", renovate]
  - name: qui
    to: [qui]
    match: { repo: autobrr/qui }
```

Each Route must have `match:` and exactly one of `to:` (a list of Sinks) and `drop: true`. The optional `name:` is the name of the Route in the log lines. A Route with no name gets its position, for example `#3`. The optional `backfill: true` makes the Route send a Backfill to its Sinks. See [Sweeps](#sweeps).

A matcher has these fields:

- `source`, `forge` (`github` or `forgejo`), `event`, `action`, `repo` (for example `autobrr/qui`), `owner`, `sender`, `ref`, and `review_state` take a string or a list of strings.
- `sender_bot`, `author_bot`, `merged`, `draft`, and `is_pull` take `true` or `false`.
- `not:` takes one matcher or a list of matchers.

These rules apply:

- A string value means "equals". A list means "any of".
- All fields in one matcher must match.
- `match:` with a list of matchers matches when one of them matches.
- `not:` makes the matcher fail when one of its matchers matches.
- An empty matcher, `match: {}`, matches each Event. A Route after it can never match, so winnow does not start.
- A field that the Event does not have never matches. For example, `merged: true` does not match a push.
- `repo`, `owner`, and `sender` match without case.
- `sender_bot` is true when GitHub marks the sender as a bot, when the login ends in `[bot]`, or when the login is in `bots:`.
- `author_bot` uses the same test on the author of the pull request or the issue, not on the sender. A Renovate pull request that a person merges has `author_bot: true`. An Event without a pull request or an issue has no author.

`*` is the only wildcard. It matches any characters except `/`. Thus `repo: "autobrr/*"` matches each repository of `autobrr`, but `repo: "*"` matches no repository. To match all repositories, leave out the `repo` field. `[`, `]`, and `?` are normal characters, so `renovate[bot]` matches the bot login.

In a flow sequence (a list in `[ ]`) or a flow mapping, put a login with `[bot]` in quotes, for example `["renovate[bot]", renovate]`. Without the quotes, the YAML parser reads the brackets as a list. In block style, `sender: renovate[bot]` needs no quotes.

### Forgejo Event names

Winnow gives a Forgejo Event the GitHub name, so that one Rule matches on both forges:

- The Event name comes from the `X-Forgejo-Event` header, for example `pull_request`, `issues`, `issue_comment`, `push`, or `release`.
- A Forgejo review (approved, rejected, or comment) becomes `pull_request_review` with action `submitted`. The `review_state` field holds the review result.
- A review request stays a `pull_request` Event with action `review_requested`.
- The action `synchronized` becomes `synchronize`. A release `updated` becomes `edited`.
- A name with no GitHub twin stays as it is. These Event names are `wiki`, `action_run_failure`, `action_run_success`, `workflow_run_<state>`, and `workflow_job_<state>`. Actions such as `label_updated` and `label_cleared` also stay as they are.

The Forgejo `workflow_run_<state>` and `workflow_job_<state>` names do not match `event: workflow_run`. To drop them, add `workflow_run_*` and `workflow_job_*` to the Route.

### Digests

A Digest sends one message for each Period. The message counts the Events that the Rules of the Digest matched in that Period. A Digest does not use the Routes. It also counts an Event that a Route drops, for example a star.

```yaml
digests:
  - name: autobrr-weekly
    every: weekly
    at: "09:00"
    to: autobrr
    match: { owner: autobrr, not: { author_bot: true } }
```

Each Digest must have these keys:

- `name`: a name that no other Digest has.
- `every`: `daily`, `weekly`, `monthly`, or `yearly`. A week is Monday to Sunday.
- `to`: the name of one Sink.
- `match`: Rules with the same fields as the `match` of a Route.

`include_other` adds the Other line to the message. Other Events are the Events outside the named counts, for example CI runs, packages, and comments. Each Other Event shows as `<event>.<action>`. The value must be `true` or `false`, and the default is `false`. With `false`, the message shows only the named counts: pull requests, issues, releases, stars, forks, and new discussions. It also leaves out each repository that has only Other Events, and the footer does not count that repository. If a Period has only Other Events, winnow sends no message for it.

`at` is the send time, `HH:MM` in the time zone of the container (`TZ`). The default is `09:00`. Winnow sends the message of a Period at `at` on the first day after the Period. For example, a weekly Digest sends on Monday.

The message shows the totals first. Then it shows one line for each repository, with the most active repository first. A Digest message never pings anyone. If a Period has no Events, winnow sends no message.

The counts include only the Events that winnow received. The first Period of a new Digest is partial, and its footer tells the day of the first count. If you rename a Digest or change its `every`, winnow counts it as a new Digest. If you change the name or `every` back to an earlier value, winnow continues the earlier Digest. If you make `match` wider, the counts include only the new Events after the change. If you make `match` narrower, the next message uses the new `match` for the whole Period.

Winnow keeps each Other Event that `match` matches, also when `include_other` is `false`. A change to `include_other` takes effect when winnow restarts. The next message uses the new value for the whole Period. So `true` also includes the Other Events from before the change. Winnow does not send a Period again if it already sent it, skipped it, found it empty, or gave up on it.

If winnow is down at the send time, it sends the message of the last Period when it starts. It skips each older Period and writes a `digest skipped` line. If Discord does not take the message, winnow tries again each hour. After 24 hours, it writes a `digest failed` line. Winnow keeps the tries in memory. If winnow restarts in these 24 hours, the start handles the Period as after downtime.

### Sweeps

GitHub does not send a failed delivery again. If winnow is down, or the tunnel to winnow is down, winnow never sees the deliveries of that time. A Sweep finds these missed deliveries with the GitHub API and fetches them. Each missed delivery becomes a Backfill.

A Sweep works only for a GitHub org webhook. Add a `sweep` block to the Source:

```yaml
sources:
  github-autobrr:
    secret: "${WINNOW_SECRET_GITHUB_AUTOBRR}"
    sweep:
      token: "${WINNOW_GITHUB_TOKEN}"
      org: autobrr
      hook: 123456789
alerts: soup
```

- `token`: a GitHub token. Use a `${VAR}` placeholder, so that the token stays in a secret.
- `org`: the organization of the webhook.
- `hook`: the ID of the org webhook. The ID is the number at the end of the URL of the webhook settings page.

Winnow does not start when `token`, `org`, or `hook` is missing.

Create the organization webhook in the GitHub web UI, under the organization's Settings, then Webhooks. Follow [Set up the webhooks](#set-up-the-webhooks) for the URL, content type, secret, and selected events.

An OAuth app can create an organization webhook that a personal access token (PAT) cannot access, even with the required permission. For example, GitHub CLI with its OAuth login can create such a webhook. The CLI can also use a PAT, so CLI use alone does not mean that an OAuth app created the webhook. See [GitHub's organization webhook API documentation](https://docs.github.com/en/rest/orgs/webhooks?apiVersion=2022-11-28#list-deliveries-for-an-organization-webhook) and [GitHub CLI token overrides](https://cli.github.com/manual/gh_help_environment).

To replace an organization webhook that an OAuth app created, do these steps:

1. Save the webhook's payload URL and selected events. Get the secret from the Source's configured value or environment variable.
2. Delete the old webhook. GitHub can reject a second webhook with the same URL, as it did in the reported incident.
3. Create the replacement in the GitHub web UI, under the organization's Settings, then Webhooks. Follow [Set up the webhooks](#set-up-the-webhooks), with the saved URL, secret, and selected events, and content type `application/json`.
4. Set the Source's `sweep.hook` to the new webhook ID, the number at the end of the replacement webhook's settings URL.
5. Restart winnow. With Docker Compose, run `docker compose restart winnow`.

Deliveries can be missed between deletion and recreation of the webhook.

To make the token, do these steps:

1. Sign in to GitHub as an owner of the organization. Only an org owner can read the deliveries of an org webhook.
2. Make a fine-grained personal access token. Set the resource owner to the organization.
3. Give the token the organization permission "Webhooks" with read access. The token needs no other permission.

A token that an org owner makes needs no approval. By default, an organization lets a fine-grained token live for 366 days or less. When the token expires, each Sweep fails with status 401. Make a new token before that date.

Winnow does a Sweep when it starts, every 15 minutes, and before it sends a Digest message. A Sweep reads the deliveries of the last 3 days, because GitHub keeps deliveries for 3 days. A delivery is missed when no attempt got a 2xx status and no attempt got a 4xx status. Winnow skips a delivery with a 4xx status, for example a bad signature, because the same failure occurs again. Winnow also skips a delivery that it received but that GitHub marked as failed. This occurs when the tunnel drops the reply of winnow.

A Backfill counts in the Digests, in the Period in which GitHub first tried to send it. The Digests always count Backfills. You do not set a flag on a Digest. A delivery can need a few minutes to show in the GitHub log. Thus, if a Source has a Sweep, set the `at` of each Digest to 15 minutes after midnight or later. A Backfill that winnow finds after the Digest send does not change that Digest message.

Winnow does not send a Backfill to Discord, because an old Event then looks new. A Backfill uses the first Route that matches it, the same as a live Event. If that Route has `backfill: true`, winnow sends the Backfill to the Sinks of the Route, with the same message as a live Event. Otherwise winnow drops the Backfill. It does not try the Routes after it. Use `backfill: true` for an Event that must reach Discord also when it is late, for example a published release.

Winnow sends each Backfill one time at most, also after a restart, and also when two winnow pods share the store during a rollout. If winnow stops after it marks a delivery as handled but before it sends the message, that Backfill is lost.

`alerts` names the Sink that gets the Sweep alerts. Winnow sends an alert when the Sweep of a Source starts to fail, and when it works again. After a Sweep that found missed deliveries, winnow sends one line, for example "Sweep of github-autobrr found 4 missed deliveries". Without `alerts`, winnow only writes log lines. Winnow does not start when `alerts` names no Sink.

A failed Sweep writes a `sweep failed` Warn line, and winnow tries again one minute later. A 401 or 404 status writes an Error line. A 401 can mean that the token expired. For a 404, make sure that the organization and webhook ID are correct, the token belongs to an org owner, and it has Webhooks read access. Also find out whether an OAuth app created the webhook. These are possible causes, not a diagnosis. The 404 alert lists these checks and points to this section. Winnow suppresses repeated failure alerts, even when the status changes, and sends an alert when the Sweep works again. If the Sweep before a Digest send fails, winnow sends the Digest and writes a `digest can count too few Events` line.

### The store

Winnow keeps the Events that a Digest matches in a SQLite file. It also keeps the deliveries that a Sweep handled. Winnow opens the file only when the configuration has Digests or a Sweep. The default path is `winnow.db` in the directory of the configuration file, thus `/config/winnow.db` in the container. To use a different path, set `database:`. A relative path is relative to the directory of the configuration file. If the file does not exist, winnow makes it. If winnow cannot open the file, or the file is damaged, winnow does not start.

The user `65532` must be able to write to the directory of the file, because SQLite also writes the files `winnow.db-wal` and `winnow.db-shm` next to it. If `/config` is read-only, for example a Kubernetes ConfigMap, set `database:` to a path on a writable volume.

Winnow keeps the stored Events and does not delete them. It deletes a handled delivery of a Sweep after 4 days. Two years of 200 Events each day use approximately 90 MB.

To make a backup, stop winnow and copy the file. Alternatively, take a snapshot of the volume that includes the `winnow.db-wal` file.

## Check a change

Winnow reads the configuration only at startup. To apply a change, do these steps:

1. Run `winnow check --config <path>` with the same environment variables as winnow. With Docker Compose, run `docker compose run --rm winnow check`.
2. If `winnow check` prints an error or a warning, correct the file and run the check again. The check exits with 1 when it finds an error or a warning.
3. Restart winnow with `docker compose restart winnow`.

`winnow check` uses the same checks as startup. An error stops startup, for example an unknown key such as `sendr:`, a Sink name in `to:` that is not in `sinks:`, or an unset variable. A warning does not stop startup. Winnow warns about an Event name that it does not know, for example `pull_requests`, and about a Sink that no Route or Digest uses. `winnow check` does not open the store.

## Set up the webhooks

On GitHub, add a webhook to the organization or the repository:

1. Set the payload URL to `https://<your-host>/hook/<source-name>`.
2. Set the content type to `application/json`. Winnow rejects a form body.
3. Set the secret to the value of the Source `secret`.
4. Select the events that you want. GitHub sends a `ping` when you save the webhook. Winnow replies 200 to it and sends nothing to Discord.

On Forgejo, add a webhook to the organization or the repository:

1. Select the webhook type `Forgejo`, not `Gitea`.
2. Set the target URL to `https://<your-host>/hook/<source-name>`.
3. Set the content type to `application/json`.
4. Set the secret to the value of the Source `secret`. Set a secret on each webhook. Winnow rejects a delivery with no signature.

The Forgejo "Test delivery" button sends a normal push Event.

## Metrics

Prometheus can scrape `GET /metrics` on the existing Winnow listener. The endpoint needs no configuration switch or separate port.
Keep access on the internal network. The Cloudflare Tunnel must expose only `/hook/`, so that `/metrics` stays internal.

Winnow exposes only these two counters. It does not expose Go runtime or process metrics.
Every configured Sink and Source has zero-valued series at startup for all its reasons or statuses.

| Counter | Labels | Values |
| --- | --- | --- |
| `winnow_sink_delivery_failures_total` | `sink`, `reason` | Configured Sink name, and `queue_full`, `rejected`, `retries_exhausted`, or `shutdown` |
| `winnow_webhook_rejections_total` | `source`, `status` | Configured Source name, and `400`, `401`, `405`, `413`, or `415` |

The Sink counter counts one failed delivery cycle to one Sink, after Discord retries finish or when enqueue fails.
An Event sent to two Sinks can produce two failures. Live Events, accepted Backfills, Digests, and Sweep alert messages all count.
A later Digest retry can fail again and add another count. A later successful send does not remove an earlier failure.
Successful sends, individual Discord retries, intentional Route drops, unmatched Events, and excluded Backfills do not count.
Sweep API failures and store failures do not count as Sink delivery failures.

The webhook counter counts each rejected request for a configured Source once, under its response status.
Status `400` includes body-read and parse errors. Status `401` includes missing and bad signatures.
Status `405` means a wrong method, `413` means an oversized body, and `415` means an unsupported content type.
Unknown Sources return `404` and do not create series. Unrelated paths, health checks, metrics scrapes, and successful webhook requests do not count.

Each Server keeps its own counters in memory. The counters reset on restart.
Failures after the last successful scrape can disappear before Prometheus observes them.
The listener closes before the Sink queues drain, so shutdown failures can occur after `/metrics` stops serving.
These counters support operational alerts. They are not a complete audit of lost messages or a count of unique permanently lost Events or Digests.

## Logs

Winnow logs `matched` at INFO when a Route selects Sinks for an Event. This line does not confirm delivery to Discord. Dropped and unmatched Events are logged at DEBUG. The default level is DEBUG. Start winnow with `--debug=false` to log only INFO and higher levels. With Docker Compose, add `command: ["--debug=false"]` to the service.

Each Route decision line names the outcome (`matched`, `dropped`, or `unmatched`), the Route, the Sinks, and the delivery ID. The line of a Backfill has `"backfill": true`. Winnow writes one ERROR line for each message that it cannot send to Discord. If winnow cannot write an Event to the store, it writes a `store write failed` line and sends the Event to its Routes as usual. To send a delivery again, find its delivery ID in the forge.

The logs are JSON when the output is not a terminal. In a terminal, they are colored text.

## License

Winnow is licensed under the GNU General Public License, version 2 or any later version. See [LICENSE](LICENSE).

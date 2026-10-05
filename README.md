# winnow

Winnow receives GitHub and Forgejo webhooks and sends them to Discord channels. Each forge sends its webhooks to winnow, not to Discord. Winnow checks the signature of each delivery and turns it into one Event. Then it uses your Routes to send the Event to one or more Discord channels, or to drop it.

Winnow has one binary, one container, and one YAML configuration file. [GLOSSARY.md](GLOSSARY.md) defines the terms in this README (Source, Event, Route, Rule, Sink).

## Run winnow

The image is `ghcr.io/s0up4200/winnow`. It is private and for linux/amd64 only. A GitHub Actions workflow builds and pushes it for each tag that starts with `v`. Each push also moves the `latest` tag.

1. Run `docker login ghcr.io` with a token that can read packages.
2. Copy [winnow.example.yaml](winnow.example.yaml) to `winnow.yaml` and change it for your forges and channels.
3. Put the secrets and the Discord webhook URLs in a `winnow.env` file next to it, one `NAME=value` on each line.
4. Start winnow with Docker Compose:

```yaml
services:
  winnow:
    image: ghcr.io/s0up4200/winnow:latest
    restart: unless-stopped
    stop_grace_period: 15s
    env_file: winnow.env
    volumes:
      - ./winnow.yaml:/config/winnow.yaml:ro
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

Winnow listens on `:8080`. To use a different address, set `listen:`, for example `listen: ":9000"`.

### Secrets from the environment

Winnow reads a value from an environment variable when the value is one `${VAR}` placeholder and nothing else. This works only for a Source `secret` and a Sink `discord` URL. If the variable is unset or empty, winnow does not start.

A literal value also works, for example `secret: test-secret`. Use this only for a local test.

In a flow mapping (a mapping in `{ }`), put the placeholder in quotes, for example `{ secret: "${WINNOW_SECRET_FORGEJO}" }`. Without the quotes, the YAML parser reads the `}` of the placeholder as the end of the mapping. In block style (one key on each line), you do not need the quotes.

### Sinks

A Sink is the webhook URL of one Discord channel.

```yaml
sinks:
  qui: { discord: "${WINNOW_DISCORD_QUI}", mentions: true }
```

A Sink pings a Discord user only when it has `mentions: true`. Winnow pings a user only for a review request or an assignment, and only when the user is in `users:`. Winnow does not ping you for your own actions. Text in an issue or a comment never pings anyone.

```yaml
users:
  octocat: "123456789012345678"   # forge login: Discord user ID
```

A Forgejo assignment does not ping. The Forgejo payload does not name the assigned user.

### Routes

The Routes are one ordered list. Winnow tries each Route in file order. The first Route that matches the Event decides. If no Route matches, winnow drops the Event and writes a log line.

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

Each Route must have `match:` and exactly one of `to:` (a list of Sinks) and `drop: true`. The optional `name:` is the name of the Route in the log lines. A Route with no name gets its position, for example `#3`.

A matcher has these fields:

- `source`, `forge` (`github` or `forgejo`), `event`, `action`, `repo` (for example `autobrr/qui`), `owner`, `sender`, `ref`, and `review_state` take a string or a list of strings.
- `sender_bot`, `merged`, `draft`, and `is_pull` take `true` or `false`.
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

## Check a change

Winnow reads the configuration only at startup. To apply a change, do these steps:

1. Run `winnow check --config <path>` with the same environment variables as winnow. With Docker Compose, run `docker compose run --rm winnow check`.
2. If `winnow check` prints an error or a warning, correct the file and run the check again. The check exits with 1 when it finds an error or a warning.
3. Restart winnow with `docker compose restart winnow`.

`winnow check` uses the same checks as startup. An error stops startup, for example an unknown key such as `sendr:`, a Sink name in `to:` that is not in `sinks:`, or an unset variable. A warning does not stop startup. Winnow warns about an Event name that it does not know, for example `pull_requests`, and about a Sink that no Route uses.

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

## Logs

Winnow writes one log line for each Event. The line names the outcome (`sent`, `dropped`, or `unmatched`), the Route, the Sinks, and the delivery ID. Winnow writes one Error line for each message that it cannot send to Discord. To send a delivery again, find its delivery ID in the forge.

The logs are JSON when the output is not a terminal. In a terminal, they are colored text.

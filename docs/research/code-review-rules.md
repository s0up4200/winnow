# Code review rules for AI reviewers

This is research for the `## Code Review Rules` section in AGENTS.md and for `.coderabbit.yaml`. The research date is 2026-10-09. Part A tells how Codex and CodeRabbit read rules. Part B gives the outcome of each Codex finding in the private repository. Part C lists the repository rules that a reviewer cannot see in a diff. Part D gives the evidence for each rule.

Use this note to remove a rule. If a rule causes noise, or its evidence no longer applies, remove the rule and its row in Part D.

## Summary

- Codex reads a `## Code Review Rules` section in AGENTS.md. CodeRabbit reads all of AGENTS.md. Thus the section must say that the other rules in the file are for coding agents.
- The 21 merged PRs of the private repository `s0up4200/winnow-private` (#25 to #64) got 53 Codex findings. 41 were fixed. 12 were refuted, answered with a README change, made moot by a later change, or got no reply.
- PR #55 got 22 of the 53 findings in 8 review rounds. Most were Markdown edge cases from the CommonMark spec text. In that PR, a goldmark parser replaced the hand-written mention scanner. This change made 4 findings moot.
- The 12 findings that were not fixed have three sources: a deliberate design (2), a rare sequence of configuration changes or a rare timing (4), and Markdown edge cases from the spec text (6).

## Part A: how the bots read rules

The sources are qui's note `docs/research-codex-review-rules-2026-10.md` in `autobrr/qui` (checked 2026-10-04) and these docs:

- Codex GitHub review: https://learn.chatgpt.com/docs/third-party/github
- Codex AGENTS.md: https://learn.chatgpt.com/docs/agent-configuration/agents-md
- CodeRabbit code guidelines: https://docs.coderabbit.ai/knowledge-base/code-guidelines
- CodeRabbit schema: https://coderabbit.ai/integrations/schema.v2.json

The facts:

- Codex reads the `## Code Review Rules` section of the AGENTS.md file closest to the changed code. `###` headings can group the rules. The old name of the section was `## Review guidelines`.
- Codex does not read CLAUDE.md. In winnow, CLAUDE.md is a symlink to AGENTS.md, so this does not matter.
- The Codex docs say that in GitHub, Codex reports only P0 and P1 findings. The private repository does not agree: 46 of the 53 findings had a P2 badge, and 7 had a P1 badge.
- OpenAI gives this advice. Start with a few rules. Name the consequence, and give a "Safe path" for the exception. Leave lint and format checks to CI. Test the rules with `@codex review` on a representative PR, and remove a rule that causes noise.
- By default, CodeRabbit reads the full content of AGENTS.md and CLAUDE.md (`knowledge_base.code_guidelines`). It applies a guideline file to its directory and the subdirectories. Thus CodeRabbit also reads the rules for coding agents, when AGENTS.md gets such rules.
- In winnow, `tone_instructions` in `.coderabbit.yaml` holds only style text, and AGENTS.md holds the rules. The migration rule is the one exception. It is also a CodeRabbit path instruction (see Part D).
- `.coderabbit.yaml` passes the schema above. `uvx check-jsonschema --schemafile schema.v2.json .coderabbit.yaml` returned "ok". As a control, `poem: maybe` failed with "'maybe' is not of type 'boolean'".

## Part B: Codex findings in the private repository

The PR numbers are the same in this repository, because each squash commit on `main` names its PR. The commits in the "Fix" and "Outcome" columns are on the PR branches of the private repository. They are not on `main` here. 11 of the 21 PRs got no Codex findings: #25, #26, #27, #30, #43, #44, #47, #51, #53, #58, and #60.

### Fixed (41)

| PR | Finding | Fix |
| --- | --- | --- |
| #28 | The cut removes a report that starts with a long unbroken word. Only the severity line stays. | d3ce7df4 |
| #28 | The first cut, before the field budget, still removes a report after a short heading. | 545cd0c3 |
| #28 | The word boundary uses byte offsets, not characters. | 8b4de320 |
| #31 | Table-shaped text in a code fence becomes a list. | 4bb4f884 |
| #31 | A table without a leading pipe stays raw text. | bbe495f0 |
| #31 | A closing fence with 4 spaces of indent closes the block. | 86689491 |
| #37 | Inline code in an HTML attribute stops the removal of the tag. | 91423f32 |
| #37 | The ellipsis on a closing fence line leaves the block open. | 91423f32 |
| #39 | A new or renamed Digest counts Events from before its first run. | fd7bfbf1 |
| #39 | A change of `every` reuses the Period key of the old cadence. | 92427da0 |
| #39 | `database: ""` opens a temporary SQLite database. | 09d604ad |
| #39 | A Period with only ignored star Events sends a Digest with no counts. | 09d604ad |
| #39 | `?`, `#`, and `%HH` in the database path change the SQLite URI. | c94f34c3 |
| #39 | A relative database path becomes the URI authority. | 63b9bddb |
| #39 | The sample Digest needs `/data`, and the starter Compose file does not mount it. | 179961e8 |
| #40 | The pre-commit hook lints the working tree, not the staged files. | eb1b0d6e |
| #42 | Each short hex run makes a match, and all matches stay in memory. | 4ab9cc07 |
| #42 | A full reference link `[#12][ticket]` gets a second link inside it. | 4ab9cc07 |
| #42 | Numeric References in a 25 MiB body stay in memory. | 7a466e58 (cut to 256 KiB) |
| #42 | `\#12` becomes `\[#12](...)`. | d8ade1c7 |
| #49 | A live delivery with no matching Digest gets no claim, so a Sweep sends it again. | f6c0bc53 |
| #55 | The mention list has no cap at the Discord limits. | 645fab07 |
| #55 | A mention in an indented code block pings. | 645fab07 |
| #55 | The lazy blockquote skip continues after a quoted heading. | 645fab07 |
| #55 | Each indented line scans the earlier indented lines again. | 0c4dcd61 |
| #55 | A login in an HTML attribute pings. | 0c4dcd61 |
| #55 | An HTML comment opener in inline code hides the mentions after it. | 0c4dcd61 |
| #55 | Each unmatched `](` starts a backward scan. | ce0e3d4b |
| #55 | A blockquote in a list item pings. | ce0e3d4b |
| #55 | All matches stay in memory before the scan. | 79940809 |
| #55 | A second paragraph in a list item counts as indented code. | 79940809 |
| #55 | After the goldmark change, all matches stay in memory again. | be5b0578 |
| #55 | A login in an HTML declaration pings. | be5b0578 |
| #55 | goldmark builds a node for each paragraph of a 25 MiB comment. | fcabecce (no pings above 256 KiB) |
| #55 | A footnote label pings. | fcabecce |
| #55 | A declaration in a type-6 HTML block pings. | fcabecce |
| #55 | The mentions in an HTML block come before the mentions in the text in front of it. | ecb0ff9c |
| #55 | A login in image alt text pings. | ecb0ff9c |
| #55 | The `slices.Contains` deduplication is quadratic for a large User map. | ecb0ff9c (stop at 100 IDs) |
| #63 | The comment parse runs for each Sink when the User map is empty. | d3119798 |
| #64 | The refactor writes the Sweep claim before it stores the Event. | 7553545c |

### Not fixed (12)

| PR | Finding | Outcome | Source |
| --- | --- | --- | --- |
| #31 | A delimiter row with one hyphen in each cell is not a GFM table. | Refuted. For `A \| B` / `-\|-` / `c \| d`, the GitHub Markdown API in gfm mode returned a `<table>`. | Spec text |
| #39 | The repository order omits stars, forks, and other metrics. | Refuted. #38 defines activity this way on purpose. | Deliberate design |
| #39 | A narrower `match` drops Events that matched earlier in the Period. | 92427da0 made the README say that a narrower `match` applies to the whole Period. | Configuration sequence |
| #39 | Winnow records the first-run time after the listener starts. | No reply. | Rare timing |
| #39 | A change of `every` and then back reuses the old key. | No reply in the thread. 09d604ad made the README say that a change back continues the earlier Digest. | Configuration sequence |
| #42 | A shortcut reference link `[#12]` gets a link inside it. | No reply. | Spec text |
| #49 | The bare GUID claim key can collide across Sources. | No reply. GitHub gives each delivery its own GUID. | Deliberate design |
| #49 | A Digest just after midnight can miss a failed delivery that GitHub does not list yet. | d981b220 made the README tell the operator to set the Digest time after midnight. | Rare timing |
| #55 | A `2.` line after a quoted paragraph continues the quote. | Moot. The goldmark change (04f926a4) removed the hand-written rule and its test. goldmark starts a list there. | Spec text |
| #55 | Each backslash in a link destination scans the backslash run again. | Moot. The goldmark change removed the scanner. | Spec text |
| #55 | A fenced block in a list item stays prose. | Moot. goldmark parses it. | Spec text |
| #55 | An HTML block does not stop the quote laziness. | Moot. goldmark parses it. | Spec text |

### Patterns

- Body size: 10 findings were about unbounded work on a body of up to 25 MiB, or about work that ran again for each Sink. 9 were fixed, and the goldmark change made the tenth moot. Each fix commit gave a size and a measurement, for example "A 25 MiB comment of "@a " allocated about 1.5 GiB". Winnow now cuts the excerpt and the mention scan at 256 KiB.
- Markdown edge cases: PR #55 had 8 rounds. Each fix to the hand-written scanner found a new spec case. The rounds stopped after the goldmark parser and the 256 KiB cap. The findings that cite only the CommonMark spec text, and not GitHub's rendering, used the most review time.
- Configuration sequences: PR #39 got 4 findings about a change to a Digest during a Period. 2 were fixed, and 2 became README text.
- ADR guarantees: the one finding on #64 cited ADR 0007 and AGENTS.md. It was a real regression.

## Part C: rules that a reviewer cannot see in the diff

| Rule | Source | Does CI test it? |
| --- | --- | --- |
| A migration on `main` never changes. Winnow records migrations by file name and runs each one once. It stops at start when a recorded file is missing. | `init` in `internal/winnow/store.go` | No. |
| Winnow stores a live Event before its Sweep claim. | ADR 0008, #64 | No. |
| The first matching Route wins. | ADR 0004 | Yes, `TestFirstMatchingRouteWins`. |
| A Backfill goes to the Sinks only when its Route accepts Backfills. | ADR 0008 | Yes, the sweep tests. |
| A delivery can be 25 MiB. Work on body text stops at 256 KiB. | `maxBody` in `server.go`, `maxExcerptBody` in `render.go`, `maxMentionScan` in `mentions.go` | No. |
| Winnow renders an Event once for each Sink. | `render.go`, `sink.go` | No. |
| A test never reaches a real host. The Sweep client uses `https://api.github.com` by default. | `config.go`, `harness_test.go` | No. |
| The README steps, its Compose example, and `winnow.example.yaml` start winnow. | README | Partly. A test loads the sample configuration. No test reads the Compose example. |
| A Ping follows what GitHub renders. | #31, #55 | No. |
| gofmt and golangci-lint pass. | `.github/workflows/ci.yml`, `.githooks/pre-commit` | Yes. CI runs golangci-lint after `go test -race ./...`. The pre-commit hook lints the staged files. |

## Part D: evidence for each rule

| Rule | Evidence |
| --- | --- |
| Report a defect only with a trigger and a result for the user. | The 12 findings that were not fixed. Each named a trigger, but 4 named a trigger that is unlikely in an install with one operator. |
| Check the merge base. Label a problem on `main` "already on main". | qui #2963 and #2983. No winnow case yet. |
| Respond to a stated reason before you report a design flaw. | #39 repository order (the reason is in #38). |
| Leave gofmt and golangci-lint to CI and the hook. | No winnow case. CI and the hook run both. |
| Read earlier review threads. | 7 findings on #28, #42, and #55 said "fresh evidence" after an earlier review. Codex already does this. The rule is mostly for CodeRabbit. |
| Migrations are P1, with the safe path. | `store.go`. No finding yet. A missed case breaks each install that runs winnow. CodeRabbit also gets a path instruction. |
| ADR guarantees. | #64 (fixed). Codex cited ADR 0007, but ADR 0008 now states the order. |
| Body size and work for each Sink, with the call path and the input size. | 9 fixed findings on #42, #55, and #63. |
| Tests that can reach a real host. | The global rule for agents, and the Sweep client default in `config.go`. No finding yet. |
| README, Compose example, and sample configuration. | #39 sample Digest and Compose mount (fixed). |
| Markdown edge cases only with GitHub's rendering. | #31 delimiter row (refuted with the GitHub API), #42 shortcut link, and 4 moot findings and 8 rounds on #55. |
| Rare configuration sequences and rare error paths. | #39 narrower `match`, cadence change and back, and first-run time. #49 Digest at midnight. |

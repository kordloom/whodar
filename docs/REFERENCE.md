# Reference

Every command, flag, source, and environment variable. For a guided
walkthrough, start with [GETTING_STARTED.md](GETTING_STARTED.md) instead.

## Global flags

Every command accepts these.

| Flag         | Default     | What it does                                        |
| ------------ | ----------- | --------------------------------------------------- |
| `--data-dir` | `~/.whodar` | Directory holding the index and feedback files.&nbsp;&nbsp;&nbsp; |
| `--policy`   | `strict`    | Egress policy: `strict`, `redacted`, or `open`.     |
| `--pretty`   | off         | Indent JSON output.                                 |
| `--human`    | off         | Force human-readable output, even when piped.       |
| `--json`     | off         | Force JSON output, even at a terminal.              |

Without `--human` or `--json`, output is human-readable at a terminal and JSON
when piped or redirected, so a person reads a clean answer and a script gets the
same machine format as before.

## Sources

Each source is one connector against a single interface; all of them merge
into one index with `--merge`. People join across sources by email, or by an
[alias file](#identity-aliases) when a source only knows a handle.

| Source       | Reads                                        | Credentials                  | Dated  |
| ------------ | -------------------------------------------- | ---------------------------- | ------ |
| `org-csv`    | Org chart CSV: names, titles, teams, topics  | none                         | no     |
| `codeowners` | CODEOWNERS paths per owner                   | none                         | no     |
| `git`        | Commit authors per changed path              | none                         | yes    |
| `slack`      | Users, channels, message history             | `WHODAR_SLACK_TOKEN`         | yes    |
| `github`     | Repos, contributors, PRs, issues, CODEOWNERS | `WHODAR_GITHUB_TOKEN`        | yes    |
| `jira`       | Issue assignees and reporters                | `WHODAR_JIRA_*`              | yes    |
| `confluence` | Page creators and editors                    | `WHODAR_CONFLUENCE_*`        | yes    |
| `pagerduty`  | Services and current on-calls                | `WHODAR_PAGERDUTY_TOKEN`     | no     |
| `json`       | JSON array of records from a file or stdin   | none                         | no     |
| `graph`      | Microsoft Graph users and reporting lines    | `WHODAR_GRAPH_TOKEN`         | no     |

Dated sources decay: see [recency](#recency). Undated sources describe the
present and keep full weight. Bot accounts (dependabot and friends) are
skipped in the git and github sources.

With `--episodes`, four of them also record the past work behind the graph, which
is what [`whodar recall`](#whodar-recall) points back at: Slack threads and runs
of channel conversation, merged GitHub pull requests, resolved Jira issues, and
resolved PagerDuty incidents.

## whodar index

Builds or extends the index from one source per run.

    whodar index --source SOURCE [scope flags] [--merge]

| Flag                | Default   | Applies to | What it does                                     |
| ------------------- | --------- | ---------- | ------------------------------------------------ |
| `--source`          | `org-csv` | all        | Which source to ingest.                          |
| `--merge`           | off       | all        | Add to the existing index instead of replacing. Re-indexing an incremental source this way fetches only what changed. |
| `--full`            | off       | jira, confluence, github, slack | With `--merge`, re-read everything and recompact instead of only what changed. |
| `--allow-shrink`    | off       | all        | Accept a source returning far less than last time, otherwise refused as a truncated read. |
| `--aliases`         |           | all        | JSON alias file joining one person across sources. |
| `--half-life-days`  | `180`     | all        | Days for a dated record's weight to halve; `0` disables decay. |
| `--changes-file`    |           | all        | Write the joiner and leaver diff as JSON.        |
| `--embed`           | off       | all        | Generate embeddings via Ollama for semantic search. |
| `--embed-model`     |           | all        | Ollama embed model (default `nomic-embed-text`). |
| `--ollama-url`      | localhost | all        | Ollama base URL for `--embed`.                   |
| `--file`            |           | org-csv, codeowners | Path to the CSV or CODEOWNERS file (or repo root). |
| `--episodes`        | off       | slack, github, jira, pagerduty | Record past conversations so `recall` can point back at them. |
| `--archive`         | off       | slack      | Keep the words of each conversation, not just a link. Needs a Memory license and an encryption key; implies `--episodes`. |
| `--max-episodes-per-channel` | `200` | slack | Conversation cap per channel.                   |
| `--max-archive-messages` | `50`  | slack      | Retained message cap per conversation.           |
| `--include-private` | off       | slack      | Ingest private channels if policy allows.        |
| `--slack-join`      | off       | slack      | Self-join public channels the bot is not in (needs `channels:join`; posts a join notice per channel). |
| `--since-days`      | `180`     | slack      | History window in days.                          |
| `--max-messages`    | `5000`    | slack      | Message cap per channel.                         |
| `--repo`            |           | github     | Repo as `owner/name`, repeatable.                |
| `--github-org`      |           | github     | Index every repository of an org.                |
| `--max-repos`       | `0` = all | github     | Cap repositories taken from `--github-org`.      |
| `--github-emails`   | off       | github     | Resolve user emails to join other sources.       |
| `--jira-project`    |           | jira       | Project key, repeatable.                         |
| `--jira-jql`        |           | jira       | JQL query; overrides `--jira-project`.           |
| `--jira-url`        |           | jira       | Site URL; or `WHODAR_JIRA_URL`.                  |
| `--max-issues`      | `1000`    | jira       | Cap issues read.                                 |
| `--jira-server`     | `false`   | jira       | Self-hosted Server/DC (v2 API, bearer/anonymous).|
| `--confluence-space`|           | confluence | Space key, repeatable.                           |
| `--confluence-url`  |           | confluence | Site URL; or `WHODAR_CONFLUENCE_URL` (falls back to the Jira URL). |
| `--confluence-cql`  |           | confluence | CQL query; overrides `--confluence-space`.       |
| `--max-pages`       | `2000`    | confluence | Cap pages read.                                  |
| `--confluence-server`| `false`  | confluence | Self-hosted Server/DC (REST at root, bearer/anonymous).|
| `--repo-path`       |           | git        | Local repository root, repeatable.               |
| `--git-since-days`  | `365`     | git        | History window in days.                          |
| `--git-until-days`  | `0`       | git        | Stop the window short of today, for a past view. |
| `--max-commits`     | `2000`    | git        | Commit cap per repository.                       |
| `--git-workers`     | machine   | git        | Commits diffed at once; a walk's whole cost.     |

### Incremental re-indexing

Re-indexing Jira, Confluence, GitHub, or Slack with `--merge` is incremental: it
fetches only the items changed since the last run and folds them into the index,
keeping everyone it did not re-read. A per-source watermark is kept in
`index.state.json` beside the index. Other sources always read in full.

- Jira and Confluence query for items updated since the watermark, oldest first.
- GitHub reads only the pull requests and issues changed since the watermark, and
  skips its whole-repo contributor and CODEOWNERS lists on an incremental run,
  since re-counting those every time would inflate their weight. A full run
  reads them.
- Slack reads only messages posted since the watermark. An edit to an older
  message is missed until a full run.

Pass `--full` to re-read everything and recompact, which also picks up anything
an incremental run skipped. A source driven by an explicit `--jira-jql` or
`--confluence-cql` always reads in full, since that query is authoritative and
cannot be narrowed to a delta.

## whodar refresh

    whodar refresh

Re-index every source that has been indexed at least once, reusing the flags it
was last indexed with and merging the result. Each `index` run records its flags
in refresh.json; a source read from stdin is not recorded, since a scheduled run
could not supply the input.

## whodar schedule

    whodar schedule --install | --remove | --status

Install a launchd agent that runs `whodar refresh` every Sunday at 3am, so the
graph stays current without remembering to. macOS only; on other systems run
`whodar refresh` from cron. Logs go to ~/Library/Logs/whodar-refresh.log.

## whodar connect

Sets up a source interactively: it explains the source, shows how to create the
credential, reads and validates it, runs the first index, and prints the `export`
line to save. Credentials are validated in memory and never written to disk.
connect needs a terminal; scripts use `whodar index`.

    whodar connect [source]

| Flag       | Default | What it does                                            |
| ---------- | ------- | ------------------------------------------------------- |
| `--status` | off     | Report which sources are configured, without prompting. |

With no argument it shows a menu of every source, marked configured or not. With a
source (`org-csv`, `codeowners`, `git`, `slack`, `github`, `jira`, `confluence`, or
`pagerduty`) it sets up just that one.

## whodar vault

Encrypts the on-disk index at rest so a stolen disk or a stray backup cannot read
your people graph. Encryption turns on whenever a key is configured, through
`WHODAR_INDEX_KEY` (a base64 32-byte key) or `WHODAR_INDEX_PASSPHRASE`.

    whodar vault keygen     # print a fresh key as an export line
    whodar vault status     # report the key and whether the index is encrypted
    whodar vault encrypt    # encrypt an existing plain index in place
    whodar vault decrypt    # rewrite the index back to plain JSON

With a key set, every `whodar index` write is encrypted and every read decrypts.
Reading an encrypted index without the key fails rather than exposing anything. See
[PRIVACY.md](PRIVACY.md) for the full model.

## whodar ask

Answers a question from the index.

    whodar ask [flags] QUESTION

| Flag            | Default   | What it does                                        |
| --------------- | --------- | --------------------------------------------------- |
| `--mode`        | `keyword` | Resolver: `keyword`, `semantic`, or `llm`.          |
| `--limit`       | `5`       | Maximum results per section.                        |
| `--provider`    | `ollama`  | LLM provider: `ollama`, `anthropic`, `openai`, or `gemini`. |
| `--model`       |           | Chat model for llm mode (defaults per provider).    |
| `--embed-model` |           | Ollama embed model for semantic and llm modes.      |
| `--ollama-url`  | localhost | Ollama base URL.                                    |
| `--openai-url`  |           | OpenAI-compatible base URL including the version path, e.g. `http://localhost:1234/v1`. |
| `--feedback`    | `normal`  | How hard votes move ranking: `off`, `low`, `normal`, `high`. |

Modes: `keyword` needs no model and always works. `semantic` blends meaning
with your exact words, using embeddings built with `index --embed`, so a
paraphrase can find what the words alone would miss without losing the
matches the words already had. `llm` retrieves
candidates, then a model re-ranks them and writes a short recommendation; it
cannot invent people. The default provider is a local Ollama server. The
`anthropic` (Claude), `openai`, and `gemini` providers are cloud models gated
by the egress policy: strict refuses them, `--policy redacted` sends the question and
anonymized numbered candidates (people as title, team, and matched terms;
channels as matched terms only) and writes the summary locally, and
`--policy open` sends candidates as-is. The `openai` provider
also speaks to any compatible server via `--openai-url`; a local one, such as
LM Studio, needs no policy opt-in, while a remote one needs `--policy open`.
Each result carries a `confidence`
from zero to one: query coverage scaled by evidence strength, where an
explicit topic is proof, a title slightly less, a passing mention half.

## whodar recall

Finds the past conversation where something was worked out, and who was in it.
An answer is a pointer, not a transcript: the people, the place, the date, and a
link back to the conversation in the tool it happened in. Opening the link uses
your own access to that tool.

    whodar recall [flags] QUESTION

Results cover only conversations you took part in, so whodar has to know who you
are. It takes `--me`, then `WHODAR_ME`, then your git email.

| Flag                  | Default   | What it does                                     |
| --------------------- | --------- | ------------------------------------------------ |
| `--me`                |           | Who is asking: an email or a source identifier such as `slack:U123`. |
| `--limit`             | `5`       | Maximum conversations to return.                 |
| `--meaning`           | off       | Match by meaning instead of exact words. Needs an index built with `--episodes --embed`. |
| `--how`               | off       | Show how it was worked out, for conversations whodar keeps. Needs a Memory license at index time. |
| `--provider`          | `ollama`  | Model provider for `--how`. Cloud providers need `--policy open`, because the conversation is sent whole. |
| `--model`             |           | Chat model that writes the account for `--how`.  |
| `--openai-url`        |           | OpenAI-compatible base URL.                      |
| `--embed-model`       |           | Ollama embed model for `--meaning`.              |
| `--ollama-url`        | localhost | Ollama base URL.                                 |
| `--link-horizon-days` | `0`       | Warn that links older than this many days may have expired. Zero makes no claim. |

Recall is also a Slack command (`/whodar recall ...` or `recall ...` in a
mention), where it answers only to the person who asked, and an MCP tool
(`whodar_recall`). The web app serves it on localhost only: the serve token
gates the server, not one person's history.

## whodar near

    whodar near PERSON [--limit N]

Rank the people who work nearest PERSON by shared team and channel membership and
shared topics. Co-membership is normalized by group size, so a small tight team
counts for far more than a broad channel; groups that span most of the org are
ignored as administrative; and permission tiers of one group (store-admin,
store-write) fold together. PERSON is an email, an id, or an exact name.

## whodar search

    whodar search QUERY [--limit N]

Find people and channels whose name, email, title, team, or topic contains
QUERY, ranked by how directly they match. This is a direct lookup, so use it
when you know what you are looking for; use `ask` when you want the people who
know a subject ranked by expertise.

## whodar related

    whodar related TOPIC [--limit N]

Report the subjects related to TOPIC, on two kinds of evidence, and say which
is speaking for each.

The stronger is what changes together. Two areas altered in the same commits
belong to one body of knowledge whoever altered them, which makes this the only
thing whodar knows about a subject that does not run through the people holding
it. A commit sweeping across many areas is ignored, since a rename does not make
everything it touched one subject, and so are the directory all the changed
paths sit under and the language they are written in, which every change touches
by construction.

The weaker is shared experts: a subject held by the same people is likely the
same body of knowledge, and one held by fewer of them is a specialty within it.
It fills in where nothing has been seen changing together, which is often, since
most commits touch a single area. Read it knowing that any subject one person
holds overlaps perfectly with everything else they hold.

Neither needs a taxonomy anyone had to write, and neither needs a model.

## whodar risk

    whodar risk [PERSON] [--limit N] [--html PATH]

Score knowledge concentration across the graph: the subjects where one or two
people hold most of the expertise, so a single departure is visible before it
hurts. Name a person for the offboarding view instead: what leaves with them.

`--html` writes a self-contained knowledge-risk brief to PATH rather than
printing. The file needs no network, no server, and no whodar to open, so it can
be forwarded to somebody who has neither. It leads with the headline counts,
lists every scored subject in a sortable and filterable table with a CSV export,
reads the same finding back per person as what would leave with them, and states
which sources it saw so a reader can judge how much of the company it covered.
The headline counts are taken over every scored subject, so capping the table
with `--limit` never shrinks them.

Deterministic arithmetic over the graph, no model needed.

## whodar ownership

    whodar ownership

Compare declared ownership, from a source of record such as CODEOWNERS, against
who actually has the expertise. A mismatch is ownership drift: the file says one
person or team owns an area, but the work and the knowledge sit somewhere else.

The drifted areas are split three ways, because they are three different
problems: an owner with no recorded work at all, an owner who is active
elsewhere but has never worked in the area they own, and an owner who does work
there but less than whoever now leads. Only the last is a judgement call. Weight
that a source of record assigned does not count as work, or indexing a
CODEOWNERS file would make every owner look active in everything they own.

Read the first of those three carefully. A source of record names people by
handle and an activity source names them by address, so an owner whose handle
was never linked to their commits is indistinguishable from one who has left.
Check that bucket against an [alias file](#identity-aliases) before believing it.

It leads with the share, not the list, because in every organization measured so
far most declared ownership does not match who does the work, and a list on its
own reads as a handful of exceptions. An area is only counted as moved when it
has moved to somebody who really works on it: weight is discounted by how much
that person does everywhere else, so the few people who touch everything do not
take ownership of every area at once. The comparison can only speak for what was
indexed, so the share is an upper bound on drift rather than a measurement of it.

## whodar attest

    whodar attest

Produce a LoomSeal evidence bundle for the current knowledge-risk finding: the
claim, a digest of the evidence behind it, and an ed25519 signature. Anyone can
verify it offline with the loomseal verifier, with no account and nothing sent
anywhere, so the finding can be trusted without trusting the machine that made
it. The signing key is created once under the data directory and reused.

## whodar identity

    whodar identity [PERSON]

List the inferred identity merges: each handle folded into a person, how
confident the merge is, and the evidence for it. Joins by shared email or
provider id are certain and are not listed. Correct a wrong merge by editing the
[alias file](#identity-aliases) and re-indexing.

## whodar archive

Reports and prunes the conversations whodar keeps.

    whodar archive status                          # what is kept and how far back
    whodar archive prune --older-than-days 365     # forget conversations over a year old
    whodar archive prune --content-only            # keep the links, drop the words

`prune` is the only command that deletes remembered conversations, and it deletes
exactly what you name.

## whodar license

Reports which features this install is licensed for.

    whodar license status

A license is a small signed file. whodar verifies it against a key compiled into
the binary and never contacts a server, so a licensed install works offline.
Put it at `license.json` in the data directory, or point `WHODAR_LICENSE` at it
(the file itself or a path to it).

Without one the free tier is in force: the people graph, and recall pointing back
at the conversations you took part in. A Memory license adds the organization's
memory: it keeps the words of Slack conversations on your own machines, so an
answer can still show how something was solved after the messages are gone. It is
$5,000 a year, flat per organization, with no seat count; larger organizations
talk to us. See [COMMERCIAL.md](../COMMERCIAL.md). An expired license drops to the
free tier and leaves every byte already indexed on disk.

## whodar feedback

Records, reviews, and clears votes on answers.

    whodar feedback record QUESTION (--person ID | --channel NAME) (--helpful | --not-helpful) [--comment TEXT]
    whodar feedback list [--query Q | --person ID | --channel NAME]
    whodar feedback clear (--query Q | --person ID | --channel NAME | --all)

| Flag            | Applies to    | What it does                              |
| --------------- | ------------- | ----------------------------------------- |
| `--person`      | all           | Person identifier from the answer.        |
| `--channel`     | all           | Channel name from the answer.             |
| `--query`       | list, clear   | Match votes for this exact question.      |
| `--helpful`     | record        | The result answered the question.         |
| `--not-helpful` | record        | The result was wrong for the question.    |
| `--comment`     | record        | Optional note explaining the vote.        |
| `--all`         | clear         | Clear every recorded vote.                |

By default each net vote multiplies the result's score by 1.25 for that
question and its close variants, clamped at three votes either way. Tune it
with `--feedback off|low|normal|high` on `ask`, `serve`, and `bot`: low is a
gentle 1.1x capped at two votes, high is 1.5x capped at four, off ignores
votes entirely. Votes live in `feedback.json` under the data directory and
survive re-indexing.

## whodar fact

    whodar fact record SUBJECT RELATION OBJECT [--detail D] [--source S]
    whodar fact list [SUBJECT] [--relation R] [--source S] [--json]
    whodar fact forget [SUBJECT [RELATION [OBJECT]]] [--source S]
    whodar fact import [FILE|-] [--source S]

Facts state what no crawl can find: which team owns a service, who to escalate
to, and above all what a team does not own. Each is a subject, a relation, and an
object, labeled with the source that asserted it, and lives in facts.json next to
the index so it survives re-indexing. Relations are `owned_by`, `not_owned_by`,
`escalates_to`, `reports_to`, `runs_on`, and `answers_questions_about`; an unknown
relation is refused. An `import` with `--source` replaces that source's facts, so
it is the whole of what the source currently asserts, which makes a catalog easy
to pipe in with `curl ... | jq ... | whodar fact import --source catalog -`.
Recorded facts also appear alongside `whodar ask` answers when they mention a
term from the question.

## whodar directory

    whodar directory

List everyone in the index with their team and topics, a read-only inventory of
what has been indexed. It answers "who is in here?" without running a query.

## whodar status

    whodar status

Report the index: when it was last built; how many people, channels, teams, and
topics it holds; how many records each source contributed; whether it carries
embeddings; whether a key encrypts it at rest; and the license tier.

## whodar doctor

    whodar doctor

Diagnose configuration and index problems and print the fix for each. It checks
that the index loads, is non-empty, and is fresh, whether it carries vectors and
is encrypted at rest, which connector credentials are set, and how many declared
owners have no work recorded against them. Every problem
prints the exact command that resolves it, and `doctor` exits nonzero when
something stops whodar from answering, so it works as a gate in a script.

## whodar serve

Runs the local web UI over the same engine.

    whodar serve [--addr HOST:PORT] [--mode keyword|semantic|llm]

| Flag            | Default          | What it does                                                 |
| --------------- | ---------------- | ------------------------------------------------------------ |
| `--addr`        | `127.0.0.1:8765` | Address to listen on.                                        |
| `--mode`        | `keyword`        | Default resolver.                                            |
| `--provider`    | `ollama`         | LLM provider: `ollama`, `anthropic`, `openai`, or `gemini`.  |
| `--model`       |                  | Ollama chat model for llm mode.                              |
| `--embed-model` |                  | Ollama embed model.                                          |
| `--ollama-url`  | localhost        | Ollama base URL.                                             |
| `--openai-url`  |                  | OpenAI-compatible base URL including the version path.       |
| `--feedback`    | `normal`         | How hard votes move ranking: `off`, `low`, `normal`, `high`. |

Queries are shareable links: `/?q=who+owns+billing` runs on load. Every
result has feedback buttons.

## whodar demo

Explores whodar on a simulated company: all eight sources are built in
process and served in the web UI, with no credentials and nothing fetched
from the network. Sample data only; it is discarded when the demo stops.

    whodar demo

Takes the same flags as `serve`.

## whodar mcp

Serves the index to MCP clients over stdio, so agents such as Claude Code
and Claude Desktop can ask who knows what mid-conversation. Tools:
`whodar_ask`, `whodar_recall`, `whodar_person`, `whodar_directory`.

    whodar mcp [--embed-model name] [--ollama-url url]

Register with `claude mcp add whodar -- whodar mcp`, or a `mcpServers`
entry in Claude Desktop's config. Semantic mode works when the index was
built with `--embed` and local Ollama is running; keyword needs nothing.

## whodar bot

Runs the Slack bot. Mention it, send it a direct message, or use the
`/whodar` slash command; a trailing `--llm` or `--keyword` in the text
overrides the mode for that answer. Each user gets ten questions a minute,
Slack redeliveries are never answered twice, and a dropped connection
reconnects with backoff.

    whodar bot [--transport socket|events]

| Flag            | Default          | What it does                                                 |
| --------------- | ---------------- | ------------------------------------------------------------ |
| `--transport`   | `socket`         | `socket` needs no public URL; `events` serves HTTP.          |
| `--addr`        | `127.0.0.1:8766` | Address for the events transport.                            |
| `--mode`        | `keyword`        | Default answer mode.                                         |
| `--limit`       | `5`              | Maximum results per section.                                 |
| `--provider`    | `ollama`         | LLM provider: `ollama`, `anthropic`, `openai`, or `gemini`.  |
| `--model`       |                  | Ollama chat model for llm mode.                              |
| `--embed-model` |                  | Ollama embed model.                                          |
| `--ollama-url`  | localhost        | Ollama base URL.                                             |
| `--openai-url`  |                  | OpenAI-compatible base URL including the version path.       |
| `--feedback`    | `normal`         | How hard votes move ranking: `off`, `low`, `normal`, `high`. |

## whodar version

    whodar version

Prints the version stamped at build time. Release binaries carry the tag; a plain
`go build` or `go install` prints `dev`.

## Environment variables

Credentials are read from the OS keychain, when `whodar connect` saved them
there, or from the environment, never from flags, and are never logged. An
environment variable always wins over the keychain, so a one-off run can
override a stored value.

| Variable                      | Used by            | What it is                                |
| ----------------------------- | ------------------ | ----------------------------------------- |
| `WHODAR_SLACK_TOKEN`          | slack source, bot  | Bot token (`xoxb-`).                      |
| `WHODAR_SLACK_APP_TOKEN`      | bot (socket)       | App-level token (`xapp-`).                |
| `WHODAR_SLACK_SIGNING_SECRET` | bot (events)       | Request signature secret.                 |
| `WHODAR_GITHUB_TOKEN`         | github source      | Personal access token.                    |
| `WHODAR_JIRA_URL`             | jira source        | Site URL, e.g. `https://x.atlassian.net`. |
| `WHODAR_JIRA_EMAIL`           | jira source        | Account email for basic auth.             |
| `WHODAR_JIRA_TOKEN`           | jira source        | API token.                                |
| `WHODAR_CONFLUENCE_URL`       | confluence source  | Site URL; falls back to `WHODAR_JIRA_URL`. |
| `WHODAR_CONFLUENCE_EMAIL`     | confluence source  | Account email; falls back to Jira's.      |
| `WHODAR_CONFLUENCE_TOKEN`     | confluence source  | API token; falls back to Jira's.          |
| `WHODAR_PAGERDUTY_TOKEN`      | pagerduty source   | Read-only API token.                      |
| `WHODAR_ANTHROPIC_KEY`        | llm anthropic provider | Claude API key.                       |
| `WHODAR_OPENAI_KEY`           | llm openai provider    | OpenAI-compatible API key.            |
| `WHODAR_GEMINI_KEY`           | llm gemini provider    | Gemini API key.                       |
| `WHODAR_SERVE_TOKEN`          | serve, demo        | Bearer token; required to bind beyond localhost. |
| `WHODAR_POLICY_FILE`          | all commands       | Extra policy file; a locked `/etc/whodar/policy.json` overrides it. |
| `WHODAR_ME`                   | recall, serve      | Who is asking, so recall covers your own conversations. |
| `WHODAR_LICENSE`              | index, license     | A license, or the path to one.            |
| `WHODAR_INDEX_KEY`            | index encryption   | Base64 32-byte key that enables at-rest encryption. |
| `WHODAR_INDEX_PASSPHRASE`     | index encryption   | Passphrase for at-rest encryption; prompted if unset on a terminal. |

## Identity aliases

Handles that clearly belong to one person join automatically: a handle-only
identifier such as `codeowners:carol-lee` or `github:carollee` merges with the
one person whose name or email local-part flattens to the same string, so
Carol Lee, carol-lee, and carol.lee@example.com stay one entry. A handle matching
nobody, or matching more than one person, stays separate.

An alias file joins the rest, when neither email nor name can. The file maps
a canonical identifier to its aliases:

    {"angela.malone@example.com": ["github:angela-malone", "codeowners:angela-malone"]}

Pass it once with `index --aliases`; the mapping persists in the index and
joins entries indexed before the file existed. Joined identifiers appear in
answers under `identities`. See `examples/aliases.json`.

## Ranking

Keyword scores weight rarer query terms higher, then cap and saturate each
term's accumulated weight, so repeating a word in chat all day cannot outrank
the person with the explicit topic, title, or ownership signal. People with
far more indexed text than average are further discounted for verbosity, though
only mildly, for a measured reason: the people who own the most of an
organization are also the people who appear in the most of it, and discounting
them heavily for that hands their own subjects to whoever passed through once.
Removing the discount is worse again, since then whoever appears most often
wins everything. Recency decay and feedback votes then scale the result.

How often somebody touched a subject counts one for one at first and
logarithmically after that, so an owner with three hundred commits in an area
outranks somebody with four, while no single subject can run away with a
person's whole profile.

Both of those are tuned against real repositories rather than by taste. The
measure is a project's own CODEOWNERS file: index only its commit history, hold
the ownership file out, and ask whodar who knows each area. The labels are
written by that project's maintainers, so the answer is not graded by us.

A term that matches nothing falls back to fuzzy matching: the closest indexed
term within one edit (four-letter terms and up) or two edits (seven and up)
scores instead, at a penalty per edit so an exact match always outranks a
corrected one. A corrected term says so in the reasons and names what it
was read as, e.g. `terraform (topic, read for "terrafrom")`, so a lucky save can
be told from a wrong guess.

## Recency

Dated records lose half their weight per half-life, 180 days by default, so
today's owner outranks one from years ago. Tune with `--half-life-days` at
index time; `0` disables decay. Undated sources (org chart, CODEOWNERS,
on-call) describe the present and never decay.

## Policy

The egress policy decides what whodar may send to a model. `strict` (default)
permits nothing beyond a local model server; non-local `--ollama-url` and
`--openai-url` values and the cloud providers count as egress and are refused.
`redacted` permits only the known provider hosts (`api.anthropic.com`,
`api.openai.com`, `generativelanguage.googleapis.com`) and strips identifiers:
the question goes out as typed,
people leave as numbered roles (title, team, matched terms), channels leave as
numbered matched terms with no names or topics, the model returns numbers, and
the summary is written locally. `open` sends full candidate detail to any
host. The policy does not gate indexing, which talks only to the sources you
name with your tokens when you run `whodar index`, or the bot posting answers
back to your Slack workspace.

An organization can also pin `"private_channels": "deny"` to keep private
channels out of the index, and `"archive": "deny"` to forbid keeping the words
of any conversation, whatever the license says.

An organization can pin the policy with a locked file. A locked
`/etc/whodar/policy.json` always wins: `WHODAR_POLICY_FILE` and `--policy` are
then ignored. The lock constrains the installed binary for regular users; it
is not a security boundary against an administrator. Private-channel ingest
can be denied the same way. See `examples/policy.json`.

## Files

Everything lives under `--data-dir` (default `~/.whodar`):

| File            | What it holds                                                                    |
| --------------- | -------------------------------------------------------------------------------- |
| `index.json`    | The graph, postings, embeddings, aliases, and a capped Slack text sample per person. |
| `feedback.json` | Votes and the queries behind them, kept apart so they survive re-indexing. Not covered by `vault` encryption today. |
| `episodes.json` | Past conversations: who took part, where, when, the link back, and matched terms. Written by `whodar index --episodes`, and by `whodar connect`, which records conversations as a matter of course. Holds the words themselves only with a Memory license and `--archive`. Encrypted with the same key as the index. |
| `license.json`  | The signed license, when one is installed. Verified offline against a key in the binary. |
| `index.sources.json` | The per-source records the index was built from, redacted to stemmed terms, so a `--merge` rebuilds without re-reading every source. Encrypted with the same key as the index. |
| `index.state.json` | Per-source incremental watermarks: the newest activity time indexed for each source and scope, so a `--merge` fetches only what changed. Plain JSON of timestamps and scope names. |

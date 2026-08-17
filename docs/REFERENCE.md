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
| `--merge`           | off       | all        | Add to the existing index instead of replacing.  |
| `--aliases`         |           | all        | JSON alias file joining one person across sources. |
| `--half-life-days`  | `180`     | all        | Days for a dated record's weight to halve; `0` disables decay. |
| `--changes-file`    |           | all        | Write the joiner and leaver diff as JSON.        |
| `--embed`           | off       | all        | Generate embeddings via Ollama for semantic search. |
| `--embed-model`     |           | all        | Ollama embed model (default `nomic-embed-text`). |
| `--ollama-url`      | localhost | all        | Ollama base URL for `--embed`.                   |
| `--file`            |           | org-csv, codeowners | Path to the CSV or CODEOWNERS file (or repo root). |
| `--episodes`        | off       | slack, github, jira, pagerduty | Record past conversations so `recall` can point back at them. |
| `--archive`         | off       | slack      | Keep the words of each conversation, not just a link. Needs a Memory license; implies `--episodes`. |
| `--max-episodes-per-channel` | `200` | slack | Conversation cap per channel.                   |
| `--max-archive-messages` | `50`  | slack      | Retained message cap per conversation.           |
| `--include-private` | off       | slack      | Ingest private channels if policy allows.        |
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
| `--confluence-space`|           | confluence | Space key, repeatable.                           |
| `--confluence-cql`  |           | confluence | CQL query; overrides `--confluence-space`.       |
| `--max-pages`       | `2000`    | confluence | Cap pages read.                                  |
| `--repo-path`       |           | git        | Local repository root, repeatable.               |
| `--git-since-days`  | `365`     | git        | History window in days.                          |
| `--max-commits`     | `2000`    | git        | Commit cap per repository.                       |

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

Modes: `keyword` needs no model and always works. `semantic` matches on
meaning using embeddings built with `index --embed`. `llm` retrieves
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
at past conversations. A Memory license adds the archive, which keeps the words of
each conversation on your own machines and can show how something was solved. It
is $5,000 a year, flat per organization, with no seat count; see
[COMMERCIAL.md](../COMMERCIAL.md). An expired license drops to the free tier and
leaves every byte already indexed on disk.

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
`whodar_ask`, `whodar_person`, `whodar_directory`.

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

Credentials are read only from the environment, never from flags, and are
never logged or stored.

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
far more indexed text than average are further discounted for verbosity.
Recency decay and feedback votes then scale the result.

A term that matches nothing falls back to fuzzy matching: the closest indexed
term within one edit (four-letter terms and up) or two edits (seven and up)
scores instead, at a penalty per edit so an exact match always outranks a
corrected one. Corrected terms say so in the reasons, e.g. `terrafrom
(topic, fuzzy)`.

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
| `episodes.json` | Past conversations: who took part, where, when, the link back, and matched terms. Written only with `--episodes`. Holds the words themselves only with a Memory license and `--archive`. Encrypted with the same key as the index. |
| `license.json`  | The signed license, when one is installed. Verified offline against a key in the binary. |

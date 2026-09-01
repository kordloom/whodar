# Architecture

whodar turns scattered work data into a queryable map of who knows what and which
channel to ask in. It is built in layers, each with one job, so a new data source
or a new way to ask is a small, isolated addition.

## Data flow

A connector reads a source and emits records. The index merges records into a
graph of people, teams, orgs, topics, and channels, and builds keyword postings
and optional embeddings. A resolver answers a query against the index. A frontend
calls a resolver and presents the answer.

    source -> connector -> records -> index -> resolver -> frontend

## Layers

Connectors implement one method, Fetch, returning normalized records. Ten
exist today: org-CSV, Slack, GitHub, Jira, Confluence, PagerDuty, git history,
CODEOWNERS, Microsoft Graph, and a JSON import. Each new source is one connector
and changes nothing else.

The model is the normalized graph: people, teams, orgs, topics, and channels,
with weighted edges. People merge across sources by email, so one human is one
entry.

The index holds the graph plus a keyword posting list and, when built with
embeddings, a vector per person and channel. It ranks people and channels for a
query and explains why each matched.

Resolvers answer a query and share one Answer shape. The keyword resolver needs
no model. The semantic resolver ranks by embedding similarity. The LLM resolver
retrieves candidates, ranks and summarizes with a local model, and stays grounded
in the real candidates.

Policy governs model egress. The default is strict: answers never leave the
machine. Redacted admits only known providers and only anonymized numbered
candidates. An organization can pin the policy from a locked system file that
user flags and environment variables cannot loosen.

Frontends are thin and share the engine: a CLI, a localhost web UI, a Slack bot
over Socket Mode or the Events API, and an MCP server over stdio for agent
clients such as Claude Code and Claude Desktop.

## Adding a source

Implement the connector Source interface, returning records for people or
channels, and add a case to the index command. The index, resolvers, web UI, and
bot then work with the new data without change. Every source after the first
was added this way.

## Measured scale

The index and the episode store are each one JSON file read whole by every
command, so the cost that grows with company size is the cold start, not the
answer. Measured on an M-series laptop with the opt-in scale suite
(`WHODAR_SCALE=1 go test ./internal/simorg/ -run TestScale`):

| Size       | People | Conversations | Ingest | Index file | Episode file | Cold start | Ask  | Heap  |
| ---------- | ------ | ------------- | ------ | ---------- | ------------ | ---------- | ---- | ----- |
| Team       | 50     | 520           | 0.3s   | 146KB      | 784KB        | 9ms        | 1ms  | 4MB   |
| Company    | 1,000  | 15,150        | 5.9s   | 4MB        | 13MB         | 170ms      | 3ms  | 95MB  |
| Enterprise | 5,000  | 48,400        | 20s    | 17MB       | 40MB         | 575ms      | 12ms | 318MB |
| Huge       | 10,000 | 151,000       | 59s    | 44MB       | 119MB        | 1.7s       | 32ms | 902MB |

Answer latency stays in tens of milliseconds throughout. The costs that grow
are the cold start each command pays to load the files and the heap held while
serving. Both are acceptable to ten thousand people and known: crossing well
past that scale means moving the store off one-file JSON, and nothing in the
answer path needs to change to do it.

Incremental refresh is bounded by the delta, not the corpus: Jira, Confluence,
GitHub, and Slack query for items changed since the stored watermark, and git
resumes from the last commit read per repository.

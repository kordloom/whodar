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

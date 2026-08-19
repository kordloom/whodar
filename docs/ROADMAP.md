# Roadmap

whodar answers "who do I talk to about X" from the data it has indexed. The next
versions widen what it can see. Because every source is one connector against a
single interface, each item below is additive and changes nothing downstream.

## More sources

GitHub, Jira, Confluence, PagerDuty, and git history are shipped. Still to
come, roughly in order of how many orgs they unlock:

- Microsoft 365: Teams messages and Outlook mail through one Graph API
  connector, so Microsoft-centric orgs get first-class coverage.
- Google Workspace: Docs, Drive, and Groups activity.
- Opsgenie: on-call schedules and service owners.
- Notion, Linear, and GitLab as the connector surface grows.

Each maps its data to people, teams, topics, and channels, and joins other
sources by email or an alias file, so one person stays one entry across them.

## Engine

- Memory-mapped vector store: embeddings are int8-quantized in the index today,
  a quarter of their float32 size; a separate memory-mapped store would let a
  very large graph search without loading every vector into memory.

## Experience

- Result deep links: open a channel or a profile directly from an answer.

## Hosted tier

Everything above assumes the self-hosted binary, which stays the default and the
whole point: your data never leaves your walls. Alongside it, a separate hosted
tier would offer a fully managed whodar you add to Slack in one click, for teams
that want zero setup and are fine with a service running it for them.

This is a distribution model, not a feature of the binary. A one-click,
directory-listed Slack app needs a public server to run the OAuth install,
receive Events API callbacks, and hold each workspace's token and index. Socket
Mode, the no-public-URL path the self-hosted bot uses, cannot be listed in the
Slack Marketplace, so the hosted tier means a server in the loop by design.

That server changes the privacy posture, so the tier is explicitly opt-in and
kept apart from the self-hosted promise. The self-hosted tool does not change: no
account, no vendor, nothing sent out for review. The hosted tier is a choice for
a different buyer, and the two never blur into one story.

Building it is real service work, not a small toggle: a public install and OAuth
flow, per-workspace token and index storage, an Events API endpoint, Slack app
review, billing, and on-call. It also needs a clear data-handling posture, since
a hosted whodar processes the people graph on infrastructure the customer does
not own. Scoped as its own track so the self-hosted core stays small and private.

## Personal digest

A second way to ask: "what did I miss that matters to me." Instead of a topic
query returning people, the digest scans the channels the running user can see,
scores each message for personal relevance, and returns a ranked, actionable
roll-up on demand, on a schedule, or live. It reuses the Slack client, the
messaging client, the LLM layer, and the policy egress rules, and adds only a
per-message relevance path beside the existing pipeline. See DIGEST.md for the
full plan.

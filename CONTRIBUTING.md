# Contributing

## Build and test

    make build
    make test
    make vet

Format with gofmt before committing. The project follows the Go standard library
style.

## Adding a data source

A source is one connector. Implement the Source interface in internal/connector,
returning records for people or channels, then add a case to the index command in
cmd/index.go. Nothing else needs to change: the index, resolvers, web UI, and bot
work with the new records automatically. See docs/ARCHITECTURE.md for the layers
and docs/ROADMAP.md for sources planned next.

## Tests

Table-driven tests live alongside the code. Cover the happy path, the error
paths, and edge cases. Run the full suite with make test before opening a change.

internal/simorg simulates a whole company and serves each tool's wire format
from in-process HTTP servers, so the full pipeline runs end to end with no
credentials: every source, identity joins, recency, confidence, and feedback.
When you add a source, add it to the simulation and its assertions.

## Measuring a change

Nothing about ranking or identity is obvious enough to change on judgement.
Several plausible improvements to this codebase were measured and turned out to
make things worse, and without a number every one of them would have shipped.

`whodar eval` scores the current index and compares it against a measurement
saved earlier:

```sh
whodar index --source git --repo-path ~/src/some-repo
whodar eval --save before.json
# make the change, rebuild the index
whodar eval --baseline before.json
```

The score is agreement: how often the owner an organization declared for an area
is also the person whodar says leads it. It is reported with the reasons it could
be wrong, because the same score can mean three different things. An owner with
no recorded work anywhere is usually an identity that was never joined to their
commits. An owner who works elsewhere but never in the area they own is paper
ownership, and whodar is probably right to disagree. An owner merely out-worked
in their own area is the arguable case, and the only one that is really about
ranking.

Read **On the same questions** first. Every score here is conditioned on what
was indexed, so a change that alters coverage makes the totals move for reasons
that are not about quality. That section scores only the areas both runs could
answer, and names which ones were won and lost, because a change that wins ten
and loses ten has improved nothing.

The trap is not hypothetical. Widening the history window on a real project
moved the overall Ranked score from 72.1% to 57.1%, which reads as a serious
regression. On the areas both runs could answer it went from 74.6% to 80.5%: the
wider index was being scored on 224 extra questions the narrower one never had
to face, and they were the hard ones.

Two runs are only compared when they cover the same sources. Adding one moves
nearly every number for reasons that have nothing to do with quality.

Any public repository with a CODEOWNERS file works as a test set, so a change
can be measured against real data without pointing whodar at anything private.

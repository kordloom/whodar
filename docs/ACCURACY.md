# Accuracy: how the named people are checked

Whodar names individuals. An assessment that names the wrong person is worse
than no assessment, so this page says how the naming is measured, on what, and
what the numbers were. Everything here is reproducible from a public
repository with the commands given.

## The problem with measuring yourself

Two earlier ways of judging whodar were not good enough to publish.

The simulation gauntlet scores against a synthetic company whose answers
whodar's own generator planted. It is useful for catching regressions, which is
why it runs in CI, and it is worthless as evidence to somebody outside: the
answer key and the thing being tested come from the same code.

Tuning on the target is worse. A threshold chosen until it agreed with a
repository, and then reported as agreement with that repository, is a
measurement of nothing.

So the checks below use targets whodar was never tuned against and judges
written independently of it: git itself for the sanity check, and the
ownership files project maintainers wrote for their own governance for the
real one. There is no single public ground truth for organizational
expertise, so separate observable properties are measured separately rather
than pretending one benchmark proves the whole system.

## The method

Pick a public repository. Run an assessment. For each of the heaviest subjects,
ask whodar who holds it, then ask `git log` who touched that directory most,
and see whether whodar's name is among git's top three.

The two measures are deliberately different. Whodar counts a subject once per
commit, saturates repetition, decays by recency, and joins one human's several
identities. Git's file-touch count does none of that. They should broadly
agree and should not agree exactly: perfect agreement would mean whodar is a
slower `git shortlog`.

Run it yourself:

    git clone https://github.com/prometheus/prometheus.git
    whodar assess --repo-path ./prometheus --codeowners ./prometheus \
      --out prometheus-assessment

    git -C prometheus log --since="2 years ago" --no-merges \
      --format='@@%aN' --name-only

The second command is the judge. Group its output by author, count directory
segments, and compare the top three per directory against the `experts` field
in `findings.json`.

## Measurement one: agreement with git, on prometheus/prometheus

Measured 2026-09-01 against the tip of main, a two-year window, 2,759 commits,
428 people, 190 subjects scored.

The named person appeared in git's own top three for 17 of 19 subjects heavy
enough to judge. Read this as a sanity check rather than a validation: the
judge is commit frequency, which shares its biases with the thing being
judged. The measurement below is the one that does not.

One correction was applied to git's side, and it matters. Prometheus has no
`.mailmap`, so its history holds "György Krajcsovits" and "George
Krajcsovits", and "Bartlomiej Plotka" and "Bartek Plotka", as four people
where there are two. Without folding those, git's own ranking disagrees with
itself, and whodar scores 3 of 19 rather than 17. The identity join is not a
detail on top of the ranking; on a repository without a mailmap it is most of
what makes the ranking mean anything.

## Measurement two: recovering human-written ownership

The stronger test uses ground truth humans wrote for their own reasons: the
per-directory OWNERS files projects in the Kubernetes ecosystem maintain for
real governance, aliases expanded to individuals. The harness ships in the
repository as `whodar eval owners`; the index is built from git history alone,
with the CODEOWNERS connector off, so declared ownership never leaks into the
input. The naive baseline is git's top three committers per directory, and
the directories where that baseline misses are scored as their own cohort,
because a tool that only finds the maintainers who are also the top
committers is a slower `git shortlog`.

Measured 2026-09-02, two-year windows, place-scoped ownership model with
review fusion:

| Index                       | Repository            | whodar top 3 | git baseline | Cohort C |
| --------------------------- | --------------------- | ------------ | ------------ | -------- |
| git history only            | kubernetes/test-infra | 56% (44/79)  | 43% (34/79)  | 24% (11/45) |
| git plus GitHub review data | kubernetes/test-infra | 45% (53/117) | 29% (34/117) | 28% (23/83) |
| git history only            | kubernetes/kubernetes | 37% (14/38)  | 42% (16/38)  | 9% (2/22)   |

Three readings.

**Ownership is asked about places, and scoring it by place is what wins.**
whodar's subjects pool every path sharing a name; the place model reads the
same walk by directory, with the identity join, bot filtering, trailer
credit, and a breadth discount layered on. On test-infra that beats the
commit-counting baseline by thirteen points from git history alone, and by
sixteen with review data fused in, on a judged set half again larger because
reviewer identities resolve approvers commit history cannot map.

**The breadth discount is tuned on one repository and validated on the
other.** A half-power discount over-punished the maintainers who work across
a whole tree, which on a large project is exactly who the owners are; a
quarter power held on both repositories. The number is in the source with
its reasoning, not in a config nobody can audit.

**kubernetes is the honest frontier.** Its approvers approve through review
rather than commits, so git history alone tops out near the baseline there,
and the month of review depth a bounded API read returns is enough to double
the judgeable population through identity resolution but not to rank on.
Closing it is not a research question: it is a deeper review fetch, sized to
that project's velocity.

Commit trailers (Reviewed-by, Acked-by, Co-authored-by) are also credited at
ingest, which moved test-infra's git-only score two points; the mechanism
matters more than the size here, since the Kubernetes ecosystem barely uses
trailers and trailer-heavy ecosystems will see more.

## Measurement three: not yet built

A temporal holdout, indexing a project as of a past date and scoring against
who actually did the work afterward, would measure prediction rather than
description. The git connector's `--git-until-days` exists for exactly this
and has not yet been used for it. Until it runs, no forward-looking claim
here is measured.

## The assessment itself

The report this was measured on is published as it came out of the command,
unedited, at [whodar.dev/sample-assessment.html](https://whodar.dev/sample-assessment.html),
alongside the seal that certifies it at
[sample-assessment.loomseal](https://whodar.dev/sample-assessment.loomseal).
It carries an evaluation mark, because it was produced by an unlicensed
install, and the mark cannot be removed without breaking the signature.

## What this does and does not show

It shows that on a large repository nobody tuned against, whodar's heaviest
subjects name people git agrees are doing that work.

It does not show the ranking is right where git is a poor judge, which is most
of what the product claims. Commit counts say nothing about the person who
reviews every change, answers every question in Slack, and commits rarely.
Whodar reads those sources precisely because the commit log does not hold
them, and this check cannot reach that.

It also does not measure concentration findings, only who is named. On
prometheus the concentration result was that no significant subject is
critically concentrated: the core systems carry bus factors above twenty. A
tool that reports a healthy project as healthy is the same tool that can be
believed when it reports otherwise.

## Rerunning this

The numbers above will drift as the repository moves. Rerun both commands and
recompute rather than trusting this page: a published accuracy figure that is
never rechecked becomes a claim rather than a measurement.

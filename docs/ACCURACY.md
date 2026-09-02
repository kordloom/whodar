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

So the check below uses a target whodar was never tuned against, and a judge
written independently of whodar: git itself.

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

## The result, on prometheus/prometheus

Measured 2026-09-01 against the tip of main, a two-year window, 2,759 commits,
428 people, 190 subjects scored.

**The named person appeared in git's own top three for 17 of 19 subjects.**

The nineteen are every subject heavy enough to judge: at least twenty commits
touching the directory, taken from the highest-weight subjects in the
assessment. The two misses were `promql`, where whodar named a heavy
contributor who is not top three by file count, and `discovery`.

One correction was applied to git's side, and it matters. Prometheus has no
`.mailmap`, so its history holds "György Krajcsovits" and "George
Krajcsovits", and "Bartlomiej Plotka" and "Bartek Plotka", as four people
where there are two. Without folding those, git's own ranking disagrees with
itself, and whodar scores 3 of 19 rather than 17. The identity join is not a
detail on top of the ranking; on a repository without a mailmap it is most of
what makes the ranking mean anything.

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

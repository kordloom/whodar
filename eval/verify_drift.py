#!/usr/bin/env python3
"""Check every drift finding against raw git, independently of whodar.

A finding is right when whodar's "actual" owner is also the top focused
committer of that area in the same window: component plus its tests, bots
excluded, and any commit touching more than 18 components excluded because a
codemod is not ownership.
"""
import json, os, subprocess, sys, collections

repo, findings = sys.argv[1], sys.argv[2]
d = json.load(open(findings))
drift = [x for x in d["drift"] if "leads less" in (x.get("why") or "")]

def git(*a):
    return subprocess.run(["git", "-C", repo, *a], capture_output=True, text=True).stdout

# One pass over history: commit -> (author, components touched).
raw = git("log", "--since=365 days ago", "--no-merges", "--format=%H%x00%an", "--name-only")
commits, cur, author, files = [], None, None, []
for line in raw.split("\n"):
    if "\x00" in line:
        if cur: commits.append((author, files))
        cur, files = line, []
        author = line.split("\x00", 1)[1]
    elif line.strip():
        files.append(line)
if cur: commits.append((author, files))

per = collections.defaultdict(collections.Counter)
for author, files in commits:
    if "[bot]" in author or "dependabot" in author:
        continue
    comps = {p.split("/")[2] for p in files
             if p.startswith(("homeassistant/components/", "tests/components/")) and len(p.split("/")) > 2}
    if not comps or len(comps) > 18:
        continue
    for c in comps:
        per[c][author] += 1

right = wrong = skipped = 0
misses = []
for x in drift:
    comp = x["topic"].replace("-", "_")
    if not os.path.isdir(os.path.join(repo, "homeassistant/components", comp)) or not per[comp]:
        skipped += 1
        continue
    top = per[comp].most_common(1)[0]
    if x["actual"] == top[0]:
        right += 1
    else:
        wrong += 1
        mine = per[comp].get(x["actual"], 0)
        misses.append((comp, x["actual"], mine, top[0], top[1]))

total = right + wrong
print(f"  correct {right} / {total} = {100*right//total if total else 0}%   (skipped {skipped} with no component)")
for c, a, m, t, n in misses[:10]:
    print(f"    MISS {c:22} whodar={a} ({m}) git-top={t} ({n})")

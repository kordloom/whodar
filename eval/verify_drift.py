#!/usr/bin/env python3
"""Check ownership-drift findings against the history, not against whodar.

whodar eval scores ranking against declared ownership, but it reads the same
index the finding came from, so it cannot say whether a finding is TRUE. This
reads raw git and nothing else, and asks one question of each reported area: is
the person whodar names also the one who has worked in that area most?

Two exclusions, both learned the hard way. Bots are not people. And a commit
touching a large part of the tree is a codemod rather than ownership: counting
those made every finding look wrong, because the sweep authors appear
everywhere.

Usage:  verify_drift.py <repo> <ownership-json>  [--window-days N]
"""
import collections, json, os, subprocess, sys

# A commit touching more than this many distinct directories is a sweep, not
# work on an area. Measured on home-assistant/core, the rival authors of every
# checked finding were commits of 900 to 1271 components.
MAX_BREADTH = 18


def commits(repo, window):
    """Yield (author, [paths]) for each non-merge commit in the window."""
    out = subprocess.run(
        ["git", "-C", repo, "log", f"--since={window} days ago", "--no-merges",
         "--format=%H%x00%an", "--name-only"],
        capture_output=True, text=True).stdout
    author, files = None, []
    for line in out.split("\n"):
        if "\x00" in line:
            if author is not None:
                yield author, files
            author, files = line.split("\x00", 1)[1], []
        elif line.strip():
            files.append(line)
    if author is not None:
        yield author, files


def directories(repo):
    """Map a normalized directory name to every path with that basename.

    A subject is usually a directory somewhere. Which directory differs by
    project, so this finds them rather than assuming a layout.
    """
    found = collections.defaultdict(set)
    for root, dirs, _ in os.walk(repo):
        dirs[:] = [d for d in dirs if d != ".git"]
        for d in dirs:
            rel = os.path.relpath(os.path.join(root, d), repo)
            found[d.lower().replace("_", "-")].add(rel)
    return found


def main():
    repo, findings = sys.argv[1], sys.argv[2]
    window = 365
    if "--window-days" in sys.argv:
        window = int(sys.argv[sys.argv.index("--window-days") + 1])

    drift = [x for x in (json.load(open(findings)).get("drift") or [])
             if "leads less" in (x.get("why") or "")]
    dirs = directories(repo)

    # Who worked where, counting a commit once per area it touched.
    worked = collections.defaultdict(collections.Counter)
    for author, files in commits(repo, window):
        if "[bot]" in author or "dependabot" in author:
            continue
        touched = {os.path.dirname(p) for p in files if "/" in p}
        if len(touched) > MAX_BREADTH:
            continue
        for path in touched:
            worked[path][author] += 1

    right = wrong = skipped = 0
    misses = []
    for x in drift:
        key = x["topic"].lower().replace("_", "-")
        paths = dirs.get(key)
        if not paths:
            skipped += 1
            continue
        tally = collections.Counter()
        for p in paths:
            for path, who in worked.items():
                if path == p or path.startswith(p + "/"):
                    tally.update(who)
        if not tally:
            skipped += 1
            continue
        top, n = tally.most_common(1)[0]
        if x["actual"] == top:
            right += 1
        else:
            wrong += 1
            misses.append((x["topic"], x["actual"], tally.get(x["actual"], 0), top, n))

    total = right + wrong
    pct = (100 * right // total) if total else 0
    print(f"  correct {right} / {total} = {pct}%   (skipped {skipped}: no matching directory)")
    for topic, who, mine, top, n in misses[:8]:
        print(f"    MISS {topic:24} whodar={who} ({mine})  git-top={top} ({n})")


if __name__ == "__main__":
    main()

// Reading one repository: scanning commits from the resume mark, diffing them
// in parallel, and tallying who touched what, where, and when.

package connector

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/storer"

	"github.com/kordloom/whodar/internal/util"
)

// readRepo walks one repository's log, accumulating per-author topic counts,
// display names, and latest activity. Authors are canonicalized through the
// repository's .mailmap so one person's several emails merge. It returns the
// number of commits read.
func (g *GitHistory) readRepo(
	ctx context.Context,
	path string,
	counts map[string]map[string]int,
	names map[string]map[string]int,
	latest map[string]time.Time,
	curated map[string]bool,
	recent map[string]map[string]int,
	direct map[string]map[string]int,
	ties *togetherIndex,
) (read, skipped int, err error) {
	jobs, mark, skips, err := g.scanCommits(ctx, path)
	if err != nil {
		return 0, 0, err
	}
	if mark != "" {
		g.marks[path] = mark
	}
	skips.report(g.opts.Log, path)
	jobs = unshareSquashMailboxes(jobs)
	return g.diffCommits(ctx, path, jobs, counts, names, latest, curated, recent, direct, ties)
}

// unshareSquashMailboxes re-keys commits caught in a shared squash mailbox: a
// GitHub noreply address that authored commits under two or more different
// names is not a person, it is an artifact of squash-merging, and every commit
// under it inflates whoever the address nominally belongs to with other
// people's work. Measured on esphome, one such address carried three real
// maintainers and cost four ownership answers.
//
// Each commit under a shared mailbox moves to the author name's own email when
// the window shows one: the email that authored commits under that name alone.
// A person with two such emails gets their most active one, which is the same
// human either way. A single-word name is never re-keyed, since a bare given
// name identifies nobody, and a commit whose name has no unique email stays
// where it was: mis-keyed but at least untouched by guesswork.
func unshareSquashMailboxes(jobs []commitJob) []commitJob {
	namesOf := make(map[string]map[string]bool)
	for _, j := range jobs {
		nm := normName(j.Name)
		if nm == "" {
			continue
		}
		if namesOf[j.Email] == nil {
			namesOf[j.Email] = make(map[string]bool)
		}
		namesOf[j.Email][nm] = true
	}
	shared := make(map[string]bool)
	for email, nms := range namesOf {
		if _, ok := util.GitHubNoreplyLogin(email); ok && len(nms) >= 2 {
			shared[email] = true
		}
	}
	if len(shared) == 0 {
		return jobs
	}
	// An email uniquely named by one multi-word name, outside the shared set,
	// is that person's own mailbox; count activity to pick a primary.
	own := make(map[string]map[string]int)
	for _, j := range jobs {
		nm := normName(j.Name)
		if nm == "" || shared[j.Email] || len(namesOf[j.Email]) != 1 {
			continue
		}
		if own[nm] == nil {
			own[nm] = make(map[string]int)
		}
		own[nm][j.Email]++
	}
	for i, j := range jobs {
		if !shared[j.Email] {
			continue
		}
		nm := normName(j.Name)
		if nm == "" || !strings.Contains(strings.TrimSpace(j.Name), " ") {
			continue
		}
		best, bestN := "", 0
		for email, n := range own[nm] {
			if n > bestN || (n == bestN && email < best) {
				best, bestN = email, n
			}
		}
		if best != "" {
			jobs[i].Email = best
		}
	}
	return jobs
}

// normName reduces a display name to its letters, so J. Nick Koston and
// J.Nick Koston are one name.
func normName(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		if r >= 'a' && r <= 'z' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// maxNamedSkips caps how many automation accounts one report line names.
const maxNamedSkips = 15

// scanSkips records what a scan left out on purpose, so the operator hears
// about every dropped commit by name rather than finding a person missing from
// the map and having to guess why.
type scanSkips struct {
	// bots counts skipped commits per automation account, keyed by the
	// account's "name <email>" form.
	bots map[string]int
	// noEmail counts commits with no author email, which cannot be credited to
	// anyone.
	noEmail int
}

// note records one skipped commit under the author it belonged to.
func (s *scanSkips) note(name, email string) {
	if email == "" {
		s.noEmail++
		return
	}
	if s.bots == nil {
		s.bots = make(map[string]int)
	}
	s.bots[name+" <"+email+">"]++
}

// report writes what was skipped and why, naming each automation account. A
// who-knows-what map that silently drops people is worse than one that says
// who it left out.
func (s *scanSkips) report(w io.Writer, path string) {
	if len(s.bots) > 0 {
		names := make([]string, 0, len(s.bots))
		total := 0
		for name, n := range s.bots {
			names = append(names, name)
			total += n
		}
		sort.Strings(names)
		if len(names) > maxNamedSkips {
			names = append(names[:maxNamedSkips],
				fmt.Sprintf("and %d more", len(s.bots)-maxNamedSkips))
		}
		fmt.Fprintf(w, "git: %s: skipped %d commits from automation accounts: %s\n",
			path, total, strings.Join(names, ", "))
	}
	if s.noEmail > 0 {
		fmt.Fprintf(w, "git: %s: %d commits have no author email and are credited to nobody\n",
			path, s.noEmail)
	}
}

// commitJob is one commit worth walking: everything cheap about it, read once
// during the scan so the parallel phase only has to do the expensive part.
type commitJob struct {
	// Hash identifies the commit to diff.
	Hash plumbing.Hash
	// Email is the author, already resolved through the mailmap.
	Email string
	// Name is the author's display name at that commit.
	Name string
	// When is when the commit was authored.
	When time.Time
	// Subject is the commit's first line, mined for vocabulary the filenames
	// do not carry.
	Subject string
	// Trailers are the other people the commit message credits: co-authors,
	// reviewers, ackers. They are the review signal a repository carries
	// offline, and the people they name are often exactly the ones a commit
	// count never surfaces.
	Trailers []trailerPerson
	// Root marks a commit with no parent, whose diff is its whole tree.
	Root bool
}

// trailerPerson is one person a commit trailer credits.
type trailerPerson struct {
	// Email is the person's address, mailmap-resolved and lowercased.
	Email string
	// Name is their display name at that trailer.
	Name string
	// CoAuthor marks a Co-authored-by trailer: they changed the files, so
	// their credit is the author's. A review trailer holds the area without
	// having changed it, which is the same distinction Direct draws.
	CoAuthor bool
}

// trailerRe matches the credit trailers a commit message carries. Only
// trailers that name a person who engaged with the change count: Cc and
// Signed-off-by are routing and process, not evidence of holding anything.
var trailerRe = regexp.MustCompile(
	"(?mi)^(Co-authored-by|Reviewed-by|Acked-by|Tested-by|Suggested-by):" +
		"[ \\t]*([^<\\n]*?)[ \\t]*<([^>\\n]+)>[ \\t]*$")

// parseTrailers extracts credited people from a commit message, resolved
// through the mailmap, with automation accounts skipped the same way authors
// are, and the author themselves never double-credited.
func parseTrailers(msg string, mm mailmap, authorEmail string, skips *scanSkips) []trailerPerson {
	matches := trailerRe.FindAllStringSubmatch(msg, -1)
	if len(matches) == 0 {
		return nil
	}
	var out []trailerPerson
	seen := map[string]bool{authorEmail: true}
	for _, m := range matches {
		kind, name, email := strings.ToLower(m[1]), strings.TrimSpace(m[2]), m[3]
		if mm != nil {
			name, email = mm.resolve(name, email)
		}
		email = strings.ToLower(strings.TrimSpace(email))
		if email == "" || seen[email] {
			continue
		}
		if isBotAuthor(name, email) {
			skips.note(name, email)
			seen[email] = true
			continue
		}
		seen[email] = true
		out = append(out, trailerPerson{
			Email: email, Name: name, CoAuthor: kind == "co-authored-by",
		})
	}
	return out
}

// scanCommits walks the log and keeps the commits worth diffing. Reading the
// log is thousands of commits a second because it touches only commit objects;
// everything slow happens afterwards, per commit and independently, which is
// what makes the second phase worth running in parallel.
func (g *GitHistory) scanCommits(ctx context.Context, path string) ([]commitJob, string, scanSkips, error) {
	var skips scanSkips
	repo, closeRepo, err := openRepo(path)
	if err != nil {
		return nil, "", skips, fmt.Errorf("git: open %s: %w", path, err)
	}
	defer func() { _ = closeRepo() }()

	mm := loadMailmap(path)
	now := time.Now()
	since := now.AddDate(0, 0, -g.opts.SinceDays)
	opts := &git.LogOptions{Since: &since}
	if g.opts.UntilDays > 0 {
		until := now.AddDate(0, 0, -g.opts.UntilDays)
		opts.Until = &until
	}
	iter, err := repo.Log(opts)
	if err != nil {
		return nil, "", skips, fmt.Errorf("git: log %s: %w", path, err)
	}
	defer iter.Close()

	stop := g.opts.StopAt[path]
	var jobs []commitJob
	newest, reached := "", false
	err = iter.ForEach(func(c *object.Commit) error {
		// The newest commit seen, before any filtering, is where the next run
		// stops. Marking the newest one KEPT would re-read the merges and bot
		// commits above it every time.
		if newest == "" {
			newest = c.Hash.String()
		}
		// Everything from here down was read by an earlier run. Commits are
		// walked newest first, so this is the whole of the saving.
		if stop != "" && c.Hash.String() == stop {
			reached = true
			return storer.ErrStop
		}
		if len(jobs) >= g.opts.MaxCommits {
			return storer.ErrStop
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		name, email := c.Author.Name, c.Author.Email
		if mm != nil {
			name, email = mm.resolve(name, email)
		}
		email = strings.ToLower(strings.TrimSpace(email))
		if c.NumParents() > 1 {
			return nil
		}
		if email == "" || isBotAuthor(name, email) {
			skips.note(name, email)
			return nil
		}
		jobs = append(jobs, commitJob{
			Hash: c.Hash, Email: email, Name: name, When: c.Author.When,
			Subject:  commitSubject(c.Message),
			Trailers: parseTrailers(c.Message, mm, email, &skips),
			Root:     c.NumParents() == 0,
		})
		return nil
	})
	// A shallow clone ends at a commit whose parent was never fetched, which
	// the walker reports as a missing object. That is where the history this
	// copy has runs out, not a broken repository, so keep what was read.
	if err != nil && isShallow(path) && errors.Is(err, plumbing.ErrObjectNotFound) {
		err = nil
	}
	if err != nil {
		return nil, "", skips, fmt.Errorf("git: log %s: %w", path, err)
	}
	// A mark this history no longer contains means it was rewritten under us,
	// so what was just read is the whole window and not an increment.
	if stop != "" && !reached {
		fmt.Fprintf(g.opts.Log, "git: %s has no commit %.12s, reading the whole window\n", path, stop)
	}
	// A window that stops short of today did not walk to the tip, so its newest
	// commit is not a safe place to resume: commits between it and the tip are
	// never reached by either run. Rather than record a position that would skip
	// them, record none, and let the next run read the window again.
	if g.opts.UntilDays > 0 {
		return jobs, "", skips, nil
	}
	// Nothing new means the previous mark still stands.
	if newest == "" {
		newest = stop
	}
	return jobs, newest, skips, nil
}

// tally is one worker's share of the walk, kept to itself so the workers never
// contend over the same maps and are merged once at the end.
type tally struct {
	// counts is per-author topic weight.
	counts map[string]map[string]int
	// names counts how often each author signed with each display name.
	names map[string]map[string]int
	// latest is each author's most recent commit time.
	latest map[string]time.Time
	// curated are the tokens this worker saw in a file path rather than only in
	// a commit subject, which is the difference between demonstrated work and
	// a word somebody wrote.
	curated map[string]bool
	// recent counts, per author, the subjects they have worked on lately. See
	// recentWindowDays.
	recent map[string]map[string]int
	// direct counts, per author, the subjects their own changed directories
	// named, as against a file elsewhere carrying the name.
	direct map[string]map[string]int
	// ties is this worker's share of what was worked on together.
	ties *togetherIndex
	// dirWork counts, per directory prefix and per author, the commits that
	// touched it: the directory-scoped view of the same walk. Subjects answer
	// what somebody knows; directories answer what they own, and conflating
	// the two made ownership questions pool every path sharing a name.
	dirWork map[string]map[string]float64
	// workTotals counts every commit per author, sweeps included, which is
	// the breadth a directory score is discounted by.
	workTotals map[string]float64
	// read is how many commits this worker took in.
	read int
	// skipped is how many it could not diff.
	skipped int
}

// diffCommits computes what each commit touched, in parallel. Every commit is
// diffed against its own parent and nothing else, so the work divides cleanly;
// each worker opens the repository for itself rather than sharing one, since
// the saving comes from holding a packfile open and a shared one would only
// move the contention.
func (g *GitHistory) diffCommits(
	ctx context.Context,
	path string,
	jobs []commitJob,
	counts map[string]map[string]int,
	names map[string]map[string]int,
	latest map[string]time.Time,
	curated map[string]bool,
	recent map[string]map[string]int,
	direct map[string]map[string]int,
	ties *togetherIndex,
) (read, skipped int, err error) {
	if len(jobs) == 0 {
		return 0, 0, nil
	}
	workers := g.opts.Workers
	if workers > len(jobs) {
		workers = len(jobs)
	}

	var done atomic.Int64
	tallies := make([]tally, workers)
	var wg sync.WaitGroup
	for w := range workers {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			tallies[w] = g.diffShare(ctx, path, jobs, w, workers, &done)
		}(w)
	}

	// Report from here rather than from the workers, so progress is one steady
	// line rather than several racing to print the same number.
	finished := make(chan struct{})
	go func() { wg.Wait(); close(finished) }()
	ticker := time.NewTicker(gitProgressEvery)
	defer ticker.Stop()
	for waiting := true; waiting; {
		select {
		case <-finished:
			waiting = false
		case <-ticker.C:
			fmt.Fprintf(g.opts.Log, "git: %s: %d of %d commits read\n", path, done.Load(), len(jobs))
		}
	}

	for _, t := range tallies {
		read += t.read
		skipped += t.skipped
		for dir, people := range t.dirWork {
			into := g.dirWork[dir]
			if into == nil {
				into = make(map[string]float64)
				g.dirWork[dir] = into
			}
			for email, n := range people {
				into[email] += n
			}
		}
		for email, n := range t.workTotals {
			g.workTotals[email] += n
		}
		for tok := range t.curated {
			curated[tok] = true
		}
		for email, subs := range t.recent {
			into := recent[email]
			if into == nil {
				into = make(map[string]int)
				recent[email] = into
			}
			for sub, n := range subs {
				into[sub] += n
			}
		}
		for email, subs := range t.direct {
			into := direct[email]
			if into == nil {
				into = make(map[string]int)
				direct[email] = into
			}
			for sub, n := range subs {
				into[sub] += n
			}
		}
		ties.absorb(t.ties)
		for email, topics := range t.counts {
			m := counts[email]
			if m == nil {
				m = make(map[string]int)
				counts[email] = m
			}
			for topic, n := range topics {
				m[topic] += n
			}
		}
		for email, when := range t.latest {
			if when.After(latest[email]) {
				latest[email] = when
			}
		}
		for email, seen := range t.names {
			into := names[email]
			if into == nil {
				into = make(map[string]int)
				names[email] = into
			}
			for name, n := range seen {
				into[name] += n
			}
		}
	}
	return read, skipped, ctx.Err()
}

// diffShare walks every worker'th commit, so each worker covers a spread of the
// history rather than one contiguous era of it, and the shares stay even when
// some stretch of the history is much heavier than the rest.
func (g *GitHistory) diffShare(
	ctx context.Context,
	path string,
	jobs []commitJob,
	worker, workers int,
	done *atomic.Int64,
) tally {
	t := tally{
		counts:     make(map[string]map[string]int),
		names:      make(map[string]map[string]int),
		latest:     make(map[string]time.Time),
		curated:    make(map[string]bool),
		recent:     make(map[string]map[string]int),
		direct:     make(map[string]map[string]int),
		ties:       newTogether(),
		dirWork:    make(map[string]map[string]float64),
		workTotals: make(map[string]float64),
	}
	repo, closeRepo, err := openRepo(path)
	if err != nil {
		t.skipped = len(jobs)/workers + 1
		return t
	}
	defer func() { _ = closeRepo() }()

	for i := worker; i < len(jobs); i += workers {
		if ctx.Err() != nil {
			return t
		}
		job := jobs[i]
		commit, err := repo.CommitObject(job.Hash)
		if err != nil {
			t.skipped++
			continue
		}
		paths, err := changedPaths(commit)
		if err != nil {
			t.skipped++
			continue
		}
		if job.Root && len(paths) > maxRootCommitFiles {
			continue
		}
		t.read++
		done.Add(1)
		t.workTotals[job.Email]++

		if job.When.After(t.latest[job.Email]) {
			t.latest[job.Email] = job.When
		}
		if job.Name != "" {
			seen := t.names[job.Email]
			if seen == nil {
				seen = make(map[string]int)
				t.names[job.Email] = seen
			}
			seen[job.Name]++
		}
		m := t.counts[job.Email]
		if m == nil {
			m = make(map[string]int)
			t.counts[job.Email] = m
		}
		byPath := make([][]string, 0, len(paths))
		deepest := make([][]string, 0, len(paths))
		// Which subjects this one commit touched at all, so each is counted
		// once for it no matter how many of its files moved.
		inCommit := make(map[string]bool, len(paths)*2)
		// Which of those subjects this commit's own directories named, rather
		// than a file inside somebody else's area happening to carry the name.
		directHere := make(map[string]bool, len(paths))
		lately := time.Since(job.When) <= recentWindow
		direct := t.direct[job.Email]
		if direct == nil {
			direct = make(map[string]int)
			t.direct[job.Email] = direct
		}
		var fresh map[string]int
		if lately {
			fresh = t.recent[job.Email]
			if fresh == nil {
				fresh = make(map[string]int)
				t.recent[job.Email] = fresh
			}
		}
		for _, name := range paths {
			dirs, leaf := pathSubjects(name)
			byPath = append(byPath, dirs)
			deepest = append(deepest, deepestSubjects(name))
			// The directories a change landed in are where the work
			// demonstrably went, so the subjects they name are stated rather
			// than guessed at. Everything that tells a real subject from a
			// passing word reads this: without it a repository full of
			// expertise reports none of it.
			//
			// Only a WHOLE directory name is stated, though. The words inside a
			// compound one are there so somebody who types "zwave" still finds
			// the zwave_js expert, the same way the words of a ticket summary
			// are, and they are subjects for exactly the same reason: none. A
			// directory called data_grand_lyon is one integration, not four. A
			// word that names a directory of its own somewhere else, like
			// energy, is stated by that directory and keeps its standing here.
			stated := make(map[string]bool, 8)
			for _, seg := range segmentNames(dirPart(name)) {
				stated[seg] = true
			}
			for _, seg := range patternNames(name) {
				stated[seg] = true
			}
			for _, tok := range dirs {
				inCommit[tok] = true
				if stated[tok] {
					t.curated[tok] = true
					directHere[tok] = true
				}
			}
			// The file's own name still counts towards the work, but it does
			// not establish a subject on its own.
			for _, tok := range leaf {
				inCommit[tok] = true
			}
		}
		// A commit counts once towards each subject it touched, however many
		// files it moved inside that subject.
		//
		// Counting per file made one wide change indistinguishable from a
		// history of narrow ones, and that is not a detail: on
		// home-assistant/core it reported ownership of ecovacs as having moved
		// to somebody with a single twelve-file commit, over the maintainer
		// with twenty-seven separate ones. What makes somebody the person to
		// ask is having come back to an area, not having touched a lot of it
		// once.
		for tok := range inCommit {
			m[tok]++
			if fresh != nil {
				fresh[tok]++
			}
		}
		// A sweep is not direct work anywhere. The tie graph already refuses
		// commits this broad, and the ownership report leans on Direct being
		// clean of them: with sweeps inside it, the ranking needed a discount
		// by career breadth to keep sweepers from out-holding every owner, and
		// that discount then handed areas to whoever had done least elsewhere.
		// Gate the sweep out here and neither correction is needed.
		// Gated on the directories the commit changed, not on how many survived
		// into stated tokens: the token count runs lower than the directory
		// count whenever names are filtered as scaffolding, and a cleanup pass
		// over twenty-five directories was slipping under a token gate of
		// eighteen and collecting ownership credit across all of them.
		changedDirs := make(map[string]bool, len(paths))
		for _, name := range paths {
			changedDirs[dirPart(name)] = true
		}
		if len(changedDirs) <= maxTogether {
			for tok := range directHere {
				direct[tok]++
			}
			// Directory credit, once per commit per prefix, for the author and
			// everyone the message credits. Ownership is asked about places,
			// not words, and word-level subjects pool every path sharing a
			// name; this is the same walk read by place.
			prefixes := make(map[string]bool, len(paths)*2)
			for _, name := range paths {
				parts := strings.Split(name, "/")
				for i := 1; i < len(parts); i++ {
					prefixes[strings.Join(parts[:i], "/")] = true
				}
			}
			creditDir := func(email string) {
				for pre := range prefixes {
					m := t.dirWork[pre]
					if m == nil {
						m = make(map[string]float64)
						t.dirWork[pre] = m
					}
					m[email]++
				}
			}
			creditDir(job.Email)
			for _, tp := range job.Trailers {
				creditDir(tp.Email)
			}
		}
		// Which of this commit's subjects name something of their own, so the
		// words of one compound name are not read as subjects meeting.
		for _, p := range paths {
			t.ties.standing(segmentNames(p))
		}
		noteTogether(&t, byPath, deepest, job.Email, path)
		// The commit subject carries the domain vocabulary the filenames often
		// hide: "fix rate-limiter backoff" against a generically named limiter.go.
		// Mine it once per commit, so it weighs less than the per-file paths but
		// still lets ask match what the work was about, not just which files moved.
		for _, tok := range phraseTokens(job.Subject) {
			m[tok]++
		}
		// The people the message credits engaged with the same change: a
		// co-author wrote it, a reviewer or acker held the area well enough to
		// judge it. Each gets the commit's subjects the way the author does,
		// because trailers are exactly where the people a commit count never
		// surfaces show up. Only a co-author counts as direct, the same line
		// Direct draws everywhere: they changed the files, a reviewer did not.
		for _, tp := range job.Trailers {
			t.workTotals[tp.Email]++
			if job.When.After(t.latest[tp.Email]) {
				t.latest[tp.Email] = job.When
			}
			if tp.Name != "" {
				seen := t.names[tp.Email]
				if seen == nil {
					seen = make(map[string]int)
					t.names[tp.Email] = seen
				}
				seen[tp.Name]++
			}
			tm := t.counts[tp.Email]
			if tm == nil {
				tm = make(map[string]int)
				t.counts[tp.Email] = tm
			}
			var tfresh map[string]int
			if lately {
				tfresh = t.recent[tp.Email]
				if tfresh == nil {
					tfresh = make(map[string]int)
					t.recent[tp.Email] = tfresh
				}
			}
			for tok := range inCommit {
				tm[tok]++
				if tfresh != nil {
					tfresh[tok]++
				}
			}
			if tp.CoAuthor && len(changedDirs) <= maxTogether {
				td := t.direct[tp.Email]
				if td == nil {
					td = make(map[string]int)
					t.direct[tp.Email] = td
				}
				for tok := range directHere {
					td[tok]++
				}
			}
		}
	}
	return t
}

// commitSubject returns the first line of a commit message, the summary that
// carries the domain vocabulary, without the body or trailers such as
// Signed-off-by that would only add name and email noise.
func commitSubject(msg string) string {
	if i := strings.IndexByte(msg, '\n'); i >= 0 {
		return msg[:i]
	}
	return msg
}

// automationNames are automation accounts whose names carry no "bot" word at
// all. GitHub's coding agent commits as plain "Copilot", and on a real
// repository it was named as the sole holder of a subject: an assessment that
// tells an acquirer a machine is the only one who understands something is
// worse than useless. Names here are matched whole, case-insensitively, so a
// person called Robin Copilot-Smith is untouched.
var automationNames = map[string]bool{
	"copilot": true, "github-actions": true, "github actions": true,
	"semantic-release": true, "release-please": true, "snyk": true,
	"imgbot": true, "allcontributors": true, "codecov": true,
}

// isBotAuthor reports whether a commit author is an automation account, whose
// activity says nothing about human expertise.
//
// The [bot] suffix is GitHub's convention and not everyone's. PyTorch lands
// every change re-authored as "PyTorch MergeBot", which made a robot the top
// committer of most of the repository and the ownership report name it as the
// person areas had drifted to. A name is automation when "bot" stands alone as
// a word in it, hyphens included, so facebook-github-bot and MergeBot are
// caught while Talbot and Abbott stay people.
func isBotAuthor(name, email string) bool {
	if strings.HasSuffix(name, "[bot]") || strings.Contains(email, "[bot]") {
		return true
	}
	local := email
	if i := strings.IndexByte(local, '@'); i >= 0 {
		local = local[:i]
	}
	// A GitHub noreply local part is "12345+login", and the login is what
	// names the account.
	if _, after, ok := strings.Cut(local, "+"); ok {
		local = after
	}
	if automationNames[strings.ToLower(strings.TrimSpace(name))] ||
		automationNames[strings.ToLower(strings.TrimSpace(local))] {
		return true
	}
	return hasBotWord(name) || hasBotWord(local)
}

// hasBotWord reports whether "bot" appears as its own word, where camel case,
// hyphens, dots, underscores and spaces all separate words.
func hasBotWord(s string) bool {
	s = strings.ToLower(s)
	for _, sep := range []string{"-", "_", ".", " ", "+"} {
		s = strings.ReplaceAll(s, sep, " ")
	}
	for _, w := range strings.Fields(s) {
		if w == "bot" || w == "robot" ||
			strings.HasSuffix(w, "bot") && (w == "mergebot" || w == "dependabot" || w == "renovatebot") {
			return true
		}
	}
	return false
}

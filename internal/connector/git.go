package connector

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-git/go-billy/v5"
	"github.com/go-git/go-billy/v5/osfs"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/cache"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/storer"
	"github.com/go-git/go-git/v5/storage/filesystem"
	"github.com/kordloom/whodar/internal/util"
)

// Git history ingest bounds. A year of history captures current ownership;
// the commit cap keeps one huge repository from dominating a run.
const (
	// defaultGitSinceDays bounds how far back history is read.
	defaultGitSinceDays = 365
	// defaultMaxCommits caps commits read per repository.
	defaultMaxCommits = 2000
	// maxRootCommitFiles bounds the files a parentless commit may credit. A root
	// commit diffs against the empty tree, so a wholesale import would otherwise
	// credit its committer with every path in the repository.
	maxRootCommitFiles = 100
)

// GitOptions configures the git history connector.
type GitOptions struct {
	// Paths are local repository roots to read.
	Paths []string
	// SinceDays bounds how far back to read history; zero means one year.
	SinceDays int
	// UntilDays stops the window short of today, excluding anything more recent
	// than this many days ago. Zero reads up to the present. It exists so an
	// index can be built from what was known at a past date, which is the only
	// way to ask whether whodar would have pointed at the right person before
	// the answer was visible in the history it was reading.
	UntilDays int
	// MaxCommits caps commits read per repository; zero means 2000.
	MaxCommits int
	// Log receives progress lines; nil discards them.
	Log io.Writer
	// Workers is how many commits are diffed at once; zero picks a default
	// from the machine. Diffing is the whole cost of a walk and each commit is
	// independent of the others, so this is what decides how fast a large
	// repository is read.
	Workers int
}

// withDefaults fills unset options.
func (o GitOptions) withDefaults() GitOptions {
	if o.SinceDays <= 0 {
		o.SinceDays = defaultGitSinceDays
	}
	if o.MaxCommits <= 0 {
		o.MaxCommits = defaultMaxCommits
	}
	if o.Log == nil {
		o.Log = io.Discard
	}
	if o.Workers <= 0 {
		o.Workers = defaultGitWorkers()
	}
	return o
}

// defaultGitWorkers picks how many commits to diff at once. It leaves a core
// free so a walk does not take the machine over, and caps out because the work
// becomes memory-bound before it runs out of cores.
func defaultGitWorkers() int {
	n := runtime.NumCPU() - 1
	if n < 1 {
		n = 1
	}
	if n > maxGitWorkers {
		n = maxGitWorkers
	}
	return n
}

// GitHistory is a Source that mines commit authors per changed path, so the
// people doing the work on a system surface even when nothing declares
// ownership. Authors join other sources by commit email.
type GitHistory struct {
	// opts holds the ingest configuration.
	opts GitOptions
}

// NewGitHistory returns a git history source over the given repositories.
func NewGitHistory(opts GitOptions) *GitHistory {
	return &GitHistory{opts: opts.withDefaults()}
}

// Fetch reads each repository's recent history and returns one record per
// author, weighted by how often they touched each topic.
func (g *GitHistory) Fetch(ctx context.Context) ([]Record, error) {
	if len(g.opts.Paths) == 0 {
		return nil, ErrNoRepoPaths
	}

	counts := make(map[string]map[string]int)
	// How often each author has used each display name, rather than whichever
	// they used last. See pickNames.
	nameCounts := make(map[string]map[string]int)
	latest := make(map[string]time.Time)
	// Tokens taken from a file path, which is where work demonstrably landed.
	// Commit subject words stay weak.
	curated := make(map[string]bool)
	// Which subjects appeared, and which appeared together. See topicRecords.
	seen := make(map[string]int)
	together := make(map[subjectPair]int)
	commits := 0
	for _, path := range g.opts.Paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		read, skipped, err := g.readRepo(ctx, path, counts, nameCounts, latest, curated, seen, together)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil, err
			}
			fmt.Fprintf(g.opts.Log, "git: skipping %s: %v\n", path, err)
			continue
		}
		commits += read
		fmt.Fprintf(g.opts.Log, "git: %s: %d commits\n", path, read)
		if isShallow(path) {
			fmt.Fprintf(g.opts.Log,
				"git: %s: shallow clone, so only the most recent history exists here. "+
					"Everything older is invisible: fetch the full history to see who built what\n", path)
		}
		if read == 0 {
			fmt.Fprintf(g.opts.Log,
				"git: %s: no commits were read. Check --git-since-days, which is %d, "+
					"and that this path is a repository with history\n", path, g.opts.SinceDays)
		}
		if skipped > 0 {
			// One line, not one per commit. A partial clone makes every commit
			// unreadable, and a message each would bury the run in its own log.
			fmt.Fprintf(g.opts.Log,
				"git: %s: %d commits could not be read, most likely a partial clone missing objects\n",
				path, skipped)
		}
	}

	names := pickNames(nameCounts)
	records := make([]Record, 0, len(counts))
	for email, c := range counts {
		rec := Record{
			Kind:   KindPerson,
			Source: "git",
			Weight: 1,
			Email:  email,
			Name:   names[email],
			Time:   latest[email],
		}
		// A file path is where work demonstrably landed, so it establishes a
		// subject; a commit subject is prose and only corroborates one.
		rec.Topics, rec.WeakTopics = splitCurated(expandTopics(c), curated)
		// A GitHub noreply commit email encodes the author's login, so key the
		// person by that login to join their commits to their GitHub reviews
		// and pull requests. Without this the same engineer appears once from
		// git and again from github, since the two share no other identifier.
		if login, ok := util.GitHubNoreplyLogin(email); ok {
			rec.PersonID = "github:" + strings.ToLower(login)
		}
		records = append(records, rec)
	}
	records = append(records, topicRecords(seen, together, commits)...)
	return records, nil
}

// openRepo opens a repository with its packfiles held open for the whole walk.
// go-git's ordinary open reopens the packfile for every object it decodes, and
// diffing one commit against its parent decodes a tree object per directory
// involved, so almost all of the time goes into opening the same file over and
// over: holding the descriptors makes a walk of a real repository more than ten
// times faster. The returned function releases them.
func openRepo(path string) (*git.Repository, func() error, error) {
	noop := func() error { return nil }
	dot, err := dotGit(path)
	if err != nil {
		// Anything unusual, such as a worktree or a submodule whose .git points
		// somewhere else, goes through go-git's own resolution instead. Speed is
		// not worth refusing a repository over.
		repo, err := git.PlainOpen(path)
		return repo, noop, err
	}
	store := filesystem.NewStorageWithOptions(dot, cache.NewObjectLRUDefault(),
		filesystem.Options{KeepDescriptors: true})
	repo, err := git.Open(store, nil)
	if err != nil {
		_ = store.Close()
		return nil, noop, err
	}
	return repo, store.Close, nil
}

// dotGit locates the directory holding a repository's objects: the .git
// directory of a working copy, the repository itself when it is bare, or the
// shared directory a linked worktree borrows from.
func dotGit(path string) (billy.Filesystem, error) {
	dot := filepath.Join(path, ".git")
	fi, err := os.Stat(dot)
	switch {
	case err == nil && fi.IsDir():
		return osfs.New(dot), nil
	case err == nil:
		// A linked worktree's .git is a file naming its own directory, which
		// holds that worktree's HEAD but none of the objects: those stay in the
		// repository it was added from. Following the link alone finds a
		// repository whose every commit is missing, so follow it the rest of
		// the way to the shared directory. History is shared between them, and
		// history is all whodar reads.
		return worktreeCommon(dot)
	}
	if _, err := os.Stat(filepath.Join(path, "HEAD")); err == nil {
		return osfs.New(path), nil
	}
	return nil, fmt.Errorf("git: %s has no repository directory", path)
}

// worktreeCommon resolves a linked worktree's .git file to the shared
// repository directory holding the objects.
func worktreeCommon(dotFile string) (billy.Filesystem, error) {
	raw, err := os.ReadFile(dotFile)
	if err != nil {
		return nil, fmt.Errorf("git: read %s: %w", dotFile, err)
	}
	gitDir := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(raw)), "gitdir:"))
	if gitDir == "" {
		return nil, fmt.Errorf("git: %s names no directory", dotFile)
	}
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(filepath.Dir(dotFile), gitDir)
	}
	common, err := os.ReadFile(filepath.Join(gitDir, "commondir"))
	if err != nil {
		// No commondir means this is not a linked worktree after all, so the
		// directory it names is the repository.
		return osfs.New(gitDir), nil
	}
	shared := strings.TrimSpace(string(common))
	if !filepath.IsAbs(shared) {
		shared = filepath.Join(gitDir, shared)
	}
	return osfs.New(filepath.Clean(shared)), nil
}

// isShallow reports whether a repository was cloned with truncated history.
// It matters because the truncation is invisible in the result: a shallow
// clone reads cleanly and yields an index that simply has no memory of who
// built anything, which looks the same as a company where nobody did.
func isShallow(path string) bool {
	for _, p := range []string{
		filepath.Join(path, ".git", "shallow"),
		filepath.Join(path, "shallow"),
	} {
		if fi, err := os.Stat(p); err == nil && fi.Size() > 0 {
			return true
		}
	}
	return false
}

// gitProgressEvery is how often a long walk reports where it has got to. It is
// a time rather than a count because the rate varies by orders of magnitude
// with repository size: a fixed count either says nothing for minutes on a
// large history or floods a small one.
// maxGitWorkers caps how many commits are diffed at once.
const maxGitWorkers = 8

const gitProgressEvery = 3 * time.Second

// Bounds on what counts as two subjects being worked on together.
const (
	// maxTogether is the most subjects a commit may touch and still say
	// anything about them. A rename swept across two hundred areas does not
	// make those areas one body of knowledge. It counts the scaffolding a
	// change unavoidably touches as well, since which subjects are scaffolding
	// is not known until every commit has been read, so it sits above the
	// number of real subjects worth pairing.
	maxTogether = 18
	// minTogether is how many separate commits must have paired two subjects
	// before the pairing is more than a coincidence.
	minTogether = 3
	// minSubjectCommits is how often a subject must appear at all before its
	// ties mean anything.
	minSubjectCommits = 5
	// ubiquitousShare is the share of commits above which a subject is the
	// scaffolding every change touches rather than a subject of its own, and so
	// is tied to everything by construction.
	ubiquitousShare = 0.35
	// ubiquitousFloor is the history below which that share means nothing. In a
	// young repository a real subject is genuinely touched by half the commits,
	// and calling it scaffolding would leave nothing tied to anything.
	ubiquitousFloor = 200
	// maxLinks bounds how many ties one subject keeps, strongest first.
	maxLinks = 12
	// minLinkWeight only guards against arithmetic noise. It is deliberately
	// almost zero: how much of the time two subjects move as one thing is a
	// tiny number whenever both are also worked on alone, which is normal, and
	// a floor set by eye cuts the real ties along with the noise. What makes a
	// tie trustworthy is minTogether, several separate commits, and what bounds
	// the result is maxLinks keeping only the strongest.
	minLinkWeight = 0.001
)

// subjectPair is two subject names in a fixed order, so a pairing is counted
// once however the two were encountered.
type subjectPair struct {
	// A is the alphabetically first subject.
	A string
	// B is the other one.
	B string
}

// pairOf orders two subjects into a pair.
func pairOf(a, b string) subjectPair {
	if a > b {
		a, b = b, a
	}
	return subjectPair{A: a, B: b}
}

// noteTogether records which of a commit's subjects were worked on together.
// Only subjects named by DIFFERENT paths count as having met: one path yields
// both zwave_js and zwave, and a name agreeing with itself is not evidence.
func noteTogether(t *tally, byPath, deepest [][]string) {
	distinct := make(map[string]bool)
	for _, subs := range byPath {
		for _, s := range subs {
			distinct[s] = true
		}
	}
	if len(distinct) == 0 {
		return
	}
	for s := range distinct {
		t.seen[s]++
	}
	if len(distinct) < 2 || len(distinct) > maxTogether {
		return
	}
	// A subject every changed path names is this commit's own scaffolding: the
	// directory they all sit under, or the language they are all written in. It
	// is tied to everything the commit touched by construction, so pairing it
	// says nothing. When that leaves nothing, the commit described one subject
	// from several angles and there was never a pairing to find.
	common := make(map[string]bool, len(byPath[0]))
	for _, s := range byPath[0] {
		common[s] = true
	}
	for _, subs := range byPath[1:] {
		here := make(map[string]bool, len(subs))
		for _, s := range subs {
			here[s] = true
		}
		for s := range common {
			if !here[s] {
				delete(common, s)
			}
		}
	}
	// A subject that names the most specific directory of some path is what
	// that path is about, not the shelf it sits on, however many other paths
	// also name it. Two files under ovo_energy and srp_energy have energy in
	// common because both are about energy.
	for _, subs := range deepest {
		for _, s := range subs {
			delete(common, s)
		}
	}
	counted := make(map[subjectPair]bool)
	for i := range byPath {
		for j := i + 1; j < len(byPath); j++ {
			for _, a := range byPath[i] {
				for _, b := range byPath[j] {
					if a == b || common[a] || common[b] || sameFamily(a, b) {
						continue
					}
					p := pairOf(a, b)
					if !counted[p] {
						counted[p] = true
						t.together[p]++
					}
				}
			}
		}
	}
}

// sameFamily reports whether two subjects are one name seen twice, such as
// zwave and zwave_js, which the path tokenizer produces from a single
// directory. Those agree by construction and say nothing.
func sameFamily(a, b string) bool {
	if strings.Contains(a, b) || strings.Contains(b, a) {
		return true
	}
	aw := strings.FieldsFunc(a, func(r rune) bool { return r == '_' || r == '-' })
	for _, w := range aw {
		if w == b {
			return true
		}
	}
	bw := strings.FieldsFunc(b, func(r rune) bool { return r == '_' || r == '-' })
	for _, w := range bw {
		if w == a {
			return true
		}
	}
	return false
}

// topicRecords turns the pairings a walk observed into one record per subject,
// naming what it is worked on alongside. This is the one thing about a subject
// that does not come through the people who hold it, which is what makes it
// worth carrying: whodar can otherwise only call two subjects related when the
// same people work on both, and cannot then use that as evidence about people.
func topicRecords(seen map[string]int, together map[subjectPair]int, commits int) []Record {
	if commits == 0 {
		return nil
	}
	ceiling := float64(commits) * ubiquitousShare
	if commits < ubiquitousFloor {
		ceiling = float64(commits)
	}
	links := make(map[string][]TopicLink)
	for p, n := range together {
		if n < minTogether {
			continue
		}
		na, nb := seen[p.A], seen[p.B]
		if na < minSubjectCommits || nb < minSubjectCommits {
			continue
		}
		if float64(na) > ceiling || float64(nb) > ceiling {
			continue
		}
		// How much of the time the two move as one thing.
		weight := float64(n) / float64(na+nb-n)
		if weight < minLinkWeight {
			continue
		}
		links[p.A] = append(links[p.A], TopicLink{To: p.B, Weight: weight})
		links[p.B] = append(links[p.B], TopicLink{To: p.A, Weight: weight})
	}
	out := make([]Record, 0, len(links))
	names := make([]string, 0, len(links))
	for name := range links {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		ties := links[name]
		sort.Slice(ties, func(i, j int) bool {
			if ties[i].Weight != ties[j].Weight {
				return ties[i].Weight > ties[j].Weight
			}
			return ties[i].To < ties[j].To
		})
		if len(ties) > maxLinks {
			ties = ties[:maxLinks]
		}
		out = append(out, Record{Kind: KindTopic, Source: "git", Name: name, Links: ties})
	}
	return out
}

// pickNames settles on the name each author is known by: the one they have
// signed with most often, not the one they signed with last. A real repository
// carries mis-attributed commits, and a single one of them was enough to rename
// somebody with sixteen hundred commits to their name. Ties fall to the
// alphabetically first name so a rebuild produces the same directory.
func pickNames(counts map[string]map[string]int) map[string]string {
	out := make(map[string]string, len(counts))
	for email, seen := range counts {
		best, bestN := "", 0
		for name, n := range seen {
			if n > bestN || (n == bestN && name < best) {
				best, bestN = name, n
			}
		}
		if best != "" {
			out[email] = best
		}
	}
	return out
}

// changedPaths lists the files one commit touched, by comparing its tree with
// its first parent's. Only the trees are needed for that. Asking the commit for
// its stats instead builds a full textual patch of every changed file, which is
// far more work for a result whodar throws away, and fails outright on the
// partial clones large repositories are usually fetched as, where the file
// contents were never downloaded.
func changedPaths(c *object.Commit) ([]string, error) {
	to, err := c.Tree()
	if err != nil {
		return nil, err
	}
	var from *object.Tree
	if c.NumParents() > 0 {
		parent, err := c.Parent(0)
		if err != nil {
			return nil, err
		}
		if from, err = parent.Tree(); err != nil {
			return nil, err
		}
	}
	changes, err := object.DiffTree(from, to)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(changes))
	for _, ch := range changes {
		name := ch.To.Name
		if name == "" {
			name = ch.From.Name
		}
		if name != "" {
			out = append(out, name)
		}
	}
	return out, nil
}

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
	seen map[string]int,
	together map[subjectPair]int,
) (read, skipped int, err error) {
	jobs, err := g.scanCommits(ctx, path)
	if err != nil {
		return 0, 0, err
	}
	return g.diffCommits(ctx, path, jobs, counts, names, latest, curated, seen, together)
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
	// Root marks a commit with no parent, whose diff is its whole tree.
	Root bool
}

// scanCommits walks the log and keeps the commits worth diffing. Reading the
// log is thousands of commits a second because it touches only commit objects;
// everything slow happens afterwards, per commit and independently, which is
// what makes the second phase worth running in parallel.
func (g *GitHistory) scanCommits(ctx context.Context, path string) ([]commitJob, error) {
	repo, closeRepo, err := openRepo(path)
	if err != nil {
		return nil, fmt.Errorf("git: open %s: %w", path, err)
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
		return nil, fmt.Errorf("git: log %s: %w", path, err)
	}
	defer iter.Close()

	var jobs []commitJob
	err = iter.ForEach(func(c *object.Commit) error {
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
		if email == "" || c.NumParents() > 1 || isBotAuthor(name, email) {
			return nil
		}
		jobs = append(jobs, commitJob{
			Hash: c.Hash, Email: email, Name: name, When: c.Author.When,
			Subject: commitSubject(c.Message), Root: c.NumParents() == 0,
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
		return nil, fmt.Errorf("git: log %s: %w", path, err)
	}
	return jobs, nil
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
	// seen counts the commits each subject appeared in, and together counts the
	// commits in which each pair of subjects appeared at once.
	seen     map[string]int
	together map[subjectPair]int
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
	seen map[string]int,
	together map[subjectPair]int,
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
		for tok := range t.curated {
			curated[tok] = true
		}
		for sub, n := range t.seen {
			seen[sub] += n
		}
		for p, n := range t.together {
			together[p] += n
		}
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
		counts:   make(map[string]map[string]int),
		names:    make(map[string]map[string]int),
		latest:   make(map[string]time.Time),
		curated:  make(map[string]bool),
		seen:     make(map[string]int),
		together: make(map[subjectPair]int),
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
		for _, name := range paths {
			dirs, leaf := pathSubjects(name)
			byPath = append(byPath, dirs)
			deepest = append(deepest, deepestSubjects(name))
			// The directories a change landed in are where the work
			// demonstrably went, so the subjects they name are stated rather
			// than guessed at. Everything that tells a real subject from a
			// passing word reads this: without it a repository full of
			// expertise reports none of it.
			for _, tok := range dirs {
				m[tok]++
				t.curated[tok] = true
			}
			// The file's own name still counts towards the work, but it does
			// not establish a subject on its own.
			for _, tok := range leaf {
				m[tok]++
			}
		}
		noteTogether(&t, byPath, deepest)
		// The commit subject carries the domain vocabulary the filenames often
		// hide: "fix rate-limiter backoff" against a generically named limiter.go.
		// Mine it once per commit, so it weighs less than the per-file paths but
		// still lets ask match what the work was about, not just which files moved.
		for _, tok := range phraseTokens(job.Subject) {
			m[tok]++
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

// isBotAuthor reports whether a commit author is an automation account, such
// as dependabot, whose activity says nothing about human expertise.
func isBotAuthor(name, email string) bool {
	return strings.HasSuffix(name, "[bot]") || strings.Contains(email, "[bot]")
}

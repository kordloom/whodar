package connector

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/go-git/go-billy/v5"
	"github.com/go-git/go-billy/v5/osfs"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/cache"
	"github.com/go-git/go-git/v5/plumbing/object"
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
	// StopAt is the newest commit a previous run already read, per repository
	// path. Reading stops there, so a refresh costs what has happened since
	// rather than the whole window again. A hash this repository no longer has,
	// which is what a rewritten history looks like, falls back to a full read.
	StopAt map[string]string
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
	// marks is the newest commit read per repository path, for the next run to
	// stop at. Written during Fetch and read afterwards through Marks.
	marks map[string]string
}

// Marks returns where reading stopped in each repository, so a later run can
// resume from there rather than reading the same history again.
func (g *GitHistory) Marks() map[string]string { return g.marks }

// NewGitHistory returns a git history source over the given repositories.
func NewGitHistory(opts GitOptions) *GitHistory {
	return &GitHistory{opts: opts.withDefaults(), marks: make(map[string]string)}
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
	// How often each author has worked on each subject lately.
	recent := make(map[string]map[string]int)
	direct := make(map[string]map[string]int)
	// Which subjects appeared, which appeared together, and who worked across
	// each pairing.
	ties := newTogether()
	for _, path := range g.opts.Paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		read, skipped, err := g.readRepo(ctx, path, counts, nameCounts, latest, curated, recent, direct, ties)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil, err
			}
			fmt.Fprintf(g.opts.Log, "git: skipping %s: %v\n", path, err)
			continue
		}
		fmt.Fprintf(g.opts.Log, "git: %s: %d commits\n", path, read)
		if isShallow(path) {
			fmt.Fprintf(g.opts.Log,
				"git: %s: shallow clone, so only the most recent history exists here. "+
					"Everything older is invisible: fetch the full history to see who built what\n", path)
		}
		// The cap truncates exactly the way a shallow clone does, and silently:
		// a repository with a long history reads cleanly, reports a plausible
		// number, and leaves out most of the people who built it. Measured on a
		// real project, the default read 2,000 of 115,755 commits and found 386
		// of 5,649 authors, and nothing in the output said so.
		if read >= g.opts.MaxCommits {
			fmt.Fprintf(g.opts.Log,
				"git: %s: stopped at the --max-commits cap of %d, so anything older is "+
					"invisible and people who have not committed recently are missing. "+
					"Raise --max-commits to read further back\n", path, g.opts.MaxCommits)
		}
		// Reading nothing is normal when resuming: it means nothing has been
		// committed since last time, which is the point of resuming. Only a run
		// that had nowhere to resume from has a problem worth reporting.
		if read == 0 && g.opts.StopAt[path] == "" {
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
	// One record per author, carrying everything they touched in the window and
	// dated at their most recent commit, so recency decay applies to the person
	// rather than to each contribution.
	//
	// That is deliberate for the question whodar is usually asked, which is who
	// to go to now, and it is invisible over a window of a year. It stops being
	// invisible over a long one. Indexing thirteen years of home-assistant/core
	// tripled the areas whose declared owner is out-worked, from 113 to 270,
	// because one recent commit restores full weight to a decade of old work and
	// whoever wrote an integration in 2017 outranks whoever maintains it today.
	// Fixing it means carrying a time per subject rather than per author; until
	// then, a long --git-since-days buys coverage at the cost of ranking.
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
		rec.RecentTopics = expandTopics(recent[email])
		rec.DirectTopics = expandTopics(direct[email])
		// A GitHub noreply commit email encodes the author's login, so key the
		// person by that login to join their commits to their GitHub reviews
		// and pull requests. Without this the same engineer appears once from
		// git and again from github, since the two share no other identifier.
		if login, ok := util.GitHubNoreplyLogin(email); ok {
			rec.PersonID = "github:" + strings.ToLower(login)
		}
		records = append(records, rec)
	}
	records = append(records, ties.records("git")...)
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

// recentWindow is how lately somebody must have worked on a subject to still
// count as being in it. Measured rather than chosen: on a real repository the
// leading expert of two subjects in five had already stopped touching them, and
// such a lead was less than half as likely to still hold the subject six months
// on. Half a year is long enough that an ordinary gap between contributions
// does not read as leaving.
const recentWindow = 180 * 24 * time.Hour

// noteTogether records which of a commit's subjects were worked on together.
// Only subjects named by DIFFERENT paths count as having met: one path yields
// both zwave_js and zwave, and a name agreeing with itself is not evidence.
func noteTogether(t *tally, byPath, deepest [][]string, who, where string) {
	distinct := make(map[string]bool)
	for _, subs := range byPath {
		for _, s := range subs {
			distinct[s] = true
		}
	}
	if len(distinct) == 0 {
		return
	}
	if !t.ties.begin(distinct, where) {
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
	//
	// This does let the words of a single directory pair with each other, so a
	// commit touching only data_grand_lyon reports data, grand, and lyon as
	// three subjects one person works across. Exempting a leaf only where two
	// DIFFERENT directories name it was tried and is worse: it also cut
	// energy from ovo, planet, green, and srp, since most commits touch one
	// utility integration at a time. The real difference is that energy names a
	// directory of its own somewhere in the repository and grand never does,
	// which needs the tokenizer to keep whole segments apart from the words
	// inside them.
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
					if a == b || common[a] || common[b] || util.SameFamily(a, b) {
						continue
					}
					p := pairOf(a, b)
					if !counted[p] {
						counted[p] = true
						t.ties.pair(a, b, who)
					}
				}
			}
		}
	}
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

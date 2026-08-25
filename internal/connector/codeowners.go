package connector

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/kordloom/whodar/internal/util"
)

// codeStop are path segments and file extensions too generic to be topics.
var codeStop = map[string]bool{
	"internal": true, "src": true, "pkg": true, "cmd": true, "lib": true,
	"test": true, "tests": true, "common": true, "util": true, "utils": true,
	"core": true, "app": true, "apps": true, "main": true, "vendor": true,
	"dist": true, "build": true, "docs": true, "doc": true, "node_modules": true,
	"go": true, "js": true, "ts": true, "jsx": true, "tsx": true, "py": true,
	"rb": true, "java": true, "md": true, "txt": true, "yaml": true, "yml": true,
	"json": true, "html": true, "css": true, "sh": true, "tf": true, "sql": true,
	// Where generated and test material lives. These directories exist in every
	// area of a code base, so counting them as subjects makes the busiest
	// subjects the ones nobody has expertise in. Words that could name real
	// work, such as cache or target, are deliberately left out.
	"fixtures": true, "fixture": true, "snapshots": true, "snapshot": true,
	"testdata": true, "mocks": true, "generated": true, "coverage": true,
	"__pycache__": true, "venv": true, "egg-info": true, "min": true,
}

// extTopics maps file extensions to the topic words people search for, so an
// owner of "*.tf" surfaces under "terraform".
var extTopics = map[string]string{
	"tf": "terraform", "tfvars": "terraform", "tfstate": "terraform",
	"py": "python", "rb": "ruby", "js": "javascript", "jsx": "javascript",
	"ts": "typescript", "tsx": "typescript", "go": "golang", "rs": "rust",
	"java": "java", "kt": "kotlin", "swift": "swift", "scala": "scala",
	"cc": "cpp", "cpp": "cpp", "hpp": "cpp", "cs": "csharp", "php": "php",
	"clj": "clojure", "ex": "elixir", "erl": "erlang", "hs": "haskell",
	"lua": "lua", "sh": "shell", "bash": "shell", "ps1": "powershell",
	"sql": "sql", "graphql": "graphql", "proto": "protobuf",
	"yml": "yaml", "yaml": "yaml", "toml": "toml", "html": "html",
	"css": "css", "scss": "css", "md": "markdown",
}

// fileTopics maps special filenames with no extension to a topic.
var fileTopics = map[string]string{
	"dockerfile": "docker", "makefile": "make", "jenkinsfile": "jenkins",
	"vagrantfile": "vagrant", "gemfile": "ruby", "rakefile": "ruby",
	"go.mod": "golang", "go.sum": "golang", "package.json": "javascript",
	"cargo.toml": "rust", "requirements.txt": "python",
}

// CodeOwners is a Source that reads a CODEOWNERS file and maps each owner to the
// topics implied by the paths they own. It answers "who owns this system".
type CodeOwners struct {
	// Path is a CODEOWNERS file or a repo root to search for one.
	Path string
}

// NewCodeOwners returns a CodeOwners source for path.
func NewCodeOwners(path string) *CodeOwners {
	return &CodeOwners{Path: path}
}

// Fetch finds and parses the CODEOWNERS file, returning one record per owner.
func (c *CodeOwners) Fetch(ctx context.Context) ([]Record, error) {
	path, err := findCodeOwners(c.Path)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("codeowners: open: %w", err)
	}
	defer func() { _ = f.Close() }()
	return parseCodeOwners(ctx, f)
}

// findCodeOwners returns path when it is a file, or searches the standard
// locations when it is a directory.
func findCodeOwners(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("codeowners: stat %s: %w", path, err)
	}
	if !info.IsDir() {
		return path, nil
	}
	for _, rel := range []string{"CODEOWNERS", ".github/CODEOWNERS", "docs/CODEOWNERS"} {
		cand := filepath.Join(path, rel)
		if _, err := os.Stat(cand); err == nil {
			return cand, nil
		}
	}
	return "", fmt.Errorf("%w in %s", ErrNoCodeOwners, path)
}

// parseCodeOwners reads CODEOWNERS lines and returns one record per owner, in
// first-seen order.
func parseCodeOwners(ctx context.Context, r io.Reader) ([]Record, error) {
	patterns := make(map[string][]string)
	var order []string

	sc := bufio.NewScanner(r)
	for sc.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 1 || isSectionHeader(fields[0]) {
			continue
		}
		for _, owner := range fields[1:] {
			if !strings.Contains(owner, "@") {
				continue
			}
			if patterns[owner] == nil {
				order = append(order, owner)
			}
			patterns[owner] = append(patterns[owner], fields[0])
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("codeowners: scan: %w", err)
	}

	records := make([]Record, 0, len(order))
	for _, owner := range order {
		records = append(records, ownerRecord(owner, patterns[owner]))
	}
	return records, nil
}

// isSectionHeader reports whether a first field opens a CODEOWNERS section, such
// as "[Docs]" or the optional "^[Reviewers]". Section lines carry no path
// pattern, so their tokens must never be mined as owners or topics.
func isSectionHeader(field string) bool {
	return strings.HasPrefix(field, "[") || strings.HasPrefix(field, "^[")
}

// ownerRecord builds a record for an owner. Owners always carry an "@": an
// email owner joins other sources by email, while an @handle or @org/team
// becomes its own contact entry.
func ownerRecord(owner string, patterns []string) Record {
	rec := Record{
		Kind:   KindPerson,
		Source: "codeowners",
		Weight: 1,
		Name:   owner,
		Topics: topicsFromPatterns(patterns),
	}
	if after, ok := strings.CutPrefix(owner, "@"); ok {
		rec.PersonID = "codeowners:" + strings.ToLower(after)
	} else {
		rec.Email = util.NormalizeEmail(owner)
	}
	return rec
}

// topicsFromPatterns derives topic tags from the path segments of patterns,
// mapping file extensions and special filenames to the words people search and
// dropping generic directory names.
func topicsFromPatterns(patterns []string) []string {
	seen := make(map[string]bool)
	var topics []string
	for _, p := range patterns {
		for _, t := range pathTopics(p) {
			if t != "" && !seen[t] {
				seen[t] = true
				topics = append(topics, t)
			}
		}
	}
	return topics
}

// pathTopics derives topic tokens from one path or pattern: extension and
// special-filename names plus meaningful path segments, duplicates kept so
// callers can weight by volume.
func pathTopics(p string) []string {
	out := append([]string(nil), patternNames(p)...)
	for seg := range strings.SplitSeq(p, "/") {
		seg = strings.Trim(seg, "*?.")
		if seg == "" {
			continue
		}
		for part := range strings.SplitSeq(seg, ".") {
			part = strings.ToLower(strings.TrimSpace(part))
			if len(part) < 3 || codeStop[part] || !isSubject(part) {
				continue
			}
			out = append(out, part)
			// Directories and files are named in snake_case and kebab-case, and
			// the words inside are what people actually ask about: somebody
			// wanting the zwave expert types "zwave", not "zwave_js". Keep the
			// whole name too, since that is what the repository calls it.
			out = append(out, wordsIn(part)...)
		}
	}
	return out
}

// pathSubjects splits the tokens of a file path by how much they establish.
// The directories a file sits in name the area it belongs to, and that is what
// somebody asks about. The file's own name is mostly boilerplate that repeats
// in every one of them: const, manifest, strings, an __init__ and a test per
// area. Counting those the same way makes the busiest subjects in a code base
// its filenames, so the leaf corroborates and the directories establish.
//
// An extension that maps to a technology stays strong, since a file being
// Terraform is a statement about the work regardless of its name.
func pathSubjects(p string) (dirs, leaf []string) {
	cut := strings.LastIndex(p, "/")
	if cut < 0 {
		// A file at the root has no directory to belong to, so its own name is
		// the only thing on offer.
		return patternNames(p), pathTopics(p)
	}
	dirs = pathTopics(p[:cut])
	dirs = append(dirs, patternNames(p)...)
	for _, tok := range pathTopics(p[cut+1:]) {
		if !slices.Contains(dirs, tok) {
			leaf = append(leaf, tok)
		}
	}
	return dirs, leaf
}

// wordsIn splits a compound name on the separators code uses, returning the
// parts worth treating as subjects in their own right. The name itself is not
// repeated here; the caller already has it.
func wordsIn(name string) []string {
	if !strings.ContainsAny(name, "_-") {
		return nil
	}
	var out []string
	for part := range strings.FieldsFuncSeq(name, func(r rune) bool { return r == '_' || r == '-' }) {
		if len(part) < 3 || codeStop[part] || !isSubject(part) {
			continue
		}
		out = append(out, part)
	}
	return out
}

// isSubject reports whether a token from a path is a name somebody could ask
// about. Repositories are full of tokens that look like words and are not:
// fixture files called 000001, device identifiers, content hashes. Nobody asks
// who knows 0001, and left in they crowd the top of any report that ranks
// subjects. A name has more letters in it than digits; 2fa and oauth2 keep
// their place, 002s and 0101x do not.
func isSubject(token string) bool {
	var letters, digits int
	for _, r := range token {
		switch {
		case r >= '0' && r <= '9':
			digits++
		case r >= 'a' && r <= 'z':
			letters++
		}
	}
	return letters > digits
}

// patternNames maps a pattern's file extension or special filename to the topic
// words people search, so "*.tf" surfaces under "terraform".
func patternNames(pattern string) []string {
	base := pattern
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	base = strings.ToLower(strings.Trim(base, "*?"))

	var out []string
	if name := fileTopics[base]; name != "" {
		out = append(out, name)
	}
	if i := strings.LastIndex(base, "."); i >= 0 {
		if name := extTopics[base[i+1:]]; name != "" {
			out = append(out, name)
		}
	}
	return out
}

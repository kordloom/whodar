package connector

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
			if len(part) < 3 || codeStop[part] {
				continue
			}
			out = append(out, part)
		}
	}
	return out
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

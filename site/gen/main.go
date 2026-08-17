// Command gen renders whodar's markdown docs into styled HTML pages for the
// landing site. It reads the repo docs, gives every heading a GitHub-style id so
// existing anchor links resolve, rewrites cross-document links to the generated
// pages, and writes one page per doc plus an index under site/docs.
package main

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	ghtml "github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// Asset cache-busting versions, mirrored in the landing page's link tags. Bump
// stylesV when styles.css or app.js change, docsV when docs.css changes.
const (
	stylesV = "10"
	docsV   = "2"
)

// tmplFS holds the page templates.
//
//go:embed templates/*.html
var tmplFS embed.FS

// Doc describes one source document and where it renders to.
type Doc struct {
	// Src is the absolute path to the markdown file.
	Src string
	// Slug is the output basename without extension, also the URL segment.
	Slug string
	// Title is the display name in navigation and the page title.
	Title string
	// Desc is the one-line summary for the index card and meta description.
	Desc string
}

// NavItem is one entry in the sidebar and the prev/next footer.
type NavItem struct {
	// Title is the link text.
	Title string
	// Href is the destination, relative to the docs directory.
	Href string
	// Active marks the current page in the sidebar.
	Active bool
}

// pageData is the template model for one rendered doc page.
type pageData struct {
	// Title is the document title.
	Title string
	// Desc is the meta description.
	Desc string
	// Slug is the URL segment, used for the canonical link.
	Slug string
	// Body is the rendered document HTML.
	Body template.HTML
	// Nav is the full sidebar with the current page marked.
	Nav []NavItem
	// Prev and Next link the sequential neighbors, nil at the ends.
	Prev *NavItem
	Next *NavItem
	// StylesV and DocsV are the asset cache-busting versions.
	StylesV string
	DocsV   string
}

// indexData is the template model for the docs landing page.
type indexData struct {
	// Docs are all documents in reading order.
	Docs []Doc
	// StylesV and DocsV are the asset cache-busting versions.
	StylesV string
	DocsV   string
}

// docList returns every document to render, in reading order.
func docList(root string) []Doc {
	return []Doc{
		{filepath.Join(root, "docs", "GETTING_STARTED.md"), "getting-started", "Getting started",
			"From nothing to a working setup: install, index a source, and ask."},
		{filepath.Join(root, "docs", "CONNECT.md"), "connect", "Connect your tools",
			"A recipe for every source whodar reads, plus the connect wizard."},
		{filepath.Join(root, "docs", "PRIVACY.md"), "privacy", "Privacy and encryption",
			"Where your data lives, encrypting the index at rest, and what a model sees."},
		{filepath.Join(root, "docs", "REFERENCE.md"), "reference", "Reference",
			"Every command, flag, source, and environment variable."},
		{filepath.Join(root, "docs", "ARCHITECTURE.md"), "architecture", "Architecture",
			"How whodar turns scattered work data into a map of who knows what."},
		{filepath.Join(root, "docs", "DEPLOY.md"), "deploy", "Deploying",
			"Run the web app and the Slack bot as long-running services."},
		{filepath.Join(root, "docs", "DIGEST.md"), "digest", "Personal digest",
			"A planned second way to ask: what did I miss that matters to me."},
		{filepath.Join(root, "docs", "ROADMAP.md"), "roadmap", "Roadmap",
			"Where whodar is headed: more sources, engine work, and a hosted tier."},
		{filepath.Join(root, "CONTRIBUTING.md"), "contributing", "Contributing",
			"Build, test, and the conventions the project follows."},
	}
}

// main renders every document plus the docs index.
func main() {
	root, err := repoRoot()
	if err != nil {
		fatal(err)
	}
	docs := docList(root)

	// Map each source basename to its clean output URL so cross-links resolve
	// without hitting the .html-to-clean redirect that Pages serves.
	hrefs := make(map[string]string, len(docs))
	for _, d := range docs {
		hrefs[strings.ToLower(filepath.Base(d.Src))] = "/docs/" + d.Slug
	}

	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithParserOptions(
			parser.WithAutoHeadingID(),
			parser.WithASTTransformers(util.Prioritized(&linkRewriter{hrefs: hrefs}, 100)),
		),
		goldmark.WithRendererOptions(ghtml.WithUnsafe()),
	)

	tmpl := template.Must(template.ParseFS(tmplFS, "templates/*.html"))
	outDir := filepath.Join(root, "site", "docs")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		fatal(err)
	}

	nav := make([]NavItem, len(docs))
	for i, d := range docs {
		nav[i] = NavItem{Title: d.Title, Href: "/docs/" + d.Slug}
	}

	for i, d := range docs {
		src, err := os.ReadFile(d.Src)
		if err != nil {
			fatal(err)
		}
		var body bytes.Buffer
		ctx := parser.NewContext(parser.WithIDs(newGitHubIDs()))
		if err := md.Convert(src, &body, parser.WithContext(ctx)); err != nil {
			fatal(fmt.Errorf("convert %s: %w", d.Src, err))
		}

		sidebar := make([]NavItem, len(nav))
		copy(sidebar, nav)
		sidebar[i].Active = true

		data := pageData{
			Title: d.Title, Desc: d.Desc, Slug: d.Slug,
			Body: template.HTML(body.String()), Nav: sidebar,
			Prev: neighbor(nav, i-1), Next: neighbor(nav, i+1),
			StylesV: stylesV, DocsV: docsV,
		}
		writePage(tmpl, "doc", filepath.Join(outDir, d.Slug+".html"), data)
	}

	writePage(tmpl, "index", filepath.Join(outDir, "index.html"),
		indexData{Docs: docs, StylesV: stylesV, DocsV: docsV})
	fmt.Printf("gen: wrote %d doc pages plus index into %s\n", len(docs), outDir)
}

// neighbor returns the nav item at i, or nil when i is out of range.
func neighbor(nav []NavItem, i int) *NavItem {
	if i < 0 || i >= len(nav) {
		return nil
	}
	n := nav[i]
	return &n
}

// writePage renders the named template to path.
func writePage(t *template.Template, name, path string, data any) {
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, name, data); err != nil {
		fatal(fmt.Errorf("render %s: %w", path, err))
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		fatal(err)
	}
}

// repoRoot walks up from the working directory to the module root, the first
// ancestor holding both a go.mod and a docs directory.
func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		_, goErr := os.Stat(filepath.Join(dir, "go.mod"))
		_, docErr := os.Stat(filepath.Join(dir, "docs"))
		if goErr == nil && docErr == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("repo root not found from working directory")
		}
		dir = parent
	}
}

// fatal prints err and exits non-zero.
func fatal(err error) {
	fmt.Fprintln(os.Stderr, "gen:", err)
	os.Exit(1)
}

// linkRewriter rewrites in-document links and images during parsing so a doc
// that links to another doc's markdown file points at its generated page, and an
// image reference resolves to the raw file on GitHub.
type linkRewriter struct {
	// hrefs maps a source markdown basename to its output page.
	hrefs map[string]string
}

// Transform walks the tree and rewrites every link and image destination.
func (r *linkRewriter) Transform(doc *ast.Document, _ text.Reader, _ parser.Context) {
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch t := n.(type) {
		case *ast.Link:
			t.Destination = []byte(rewriteHref(string(t.Destination), r.hrefs))
		case *ast.Image:
			t.Destination = []byte(rewriteImage(string(t.Destination)))
		}
		return ast.WalkContinue, nil
	})
}

// rewriteHref maps a link destination to its site location. Known doc files
// point at their generated pages, README at the landing page, and any other
// relative markdown or the license fall back to the file on GitHub. External,
// anchor, and mail links pass through unchanged.
func rewriteHref(dest string, hrefs map[string]string) string {
	if dest == "" || strings.HasPrefix(dest, "#") || hasScheme(dest) {
		return dest
	}
	path, anchor := splitAnchor(dest)
	base := strings.ToLower(trimRelative(path))
	if href, ok := hrefs[base]; ok {
		return href + anchor
	}
	switch base {
	case "readme.md":
		return "/" + anchor
	case "license":
		return "https://github.com/kordloom/whodar/blob/main/LICENSE"
	}
	if strings.HasSuffix(base, ".md") {
		return "https://github.com/kordloom/whodar/blob/main/" + trimRelative(path) + anchor
	}
	return dest
}

// rewriteImage resolves a relative doc image to its raw file on GitHub, leaving
// absolute URLs untouched.
func rewriteImage(dest string) string {
	if dest == "" || hasScheme(dest) {
		return dest
	}
	return "https://raw.githubusercontent.com/kordloom/whodar/main/docs/" +
		strings.TrimPrefix(trimRelative(dest), "docs/")
}

// hasScheme reports whether dest is an absolute URL or a mail link.
func hasScheme(dest string) bool {
	return strings.HasPrefix(dest, "http://") ||
		strings.HasPrefix(dest, "https://") ||
		strings.HasPrefix(dest, "mailto:")
}

// splitAnchor splits a destination into its path and its "#fragment" tail.
func splitAnchor(dest string) (path, anchor string) {
	if i := strings.IndexByte(dest, '#'); i >= 0 {
		return dest[:i], dest[i:]
	}
	return dest, ""
}

// trimRelative strips leading "./" and "../" and a leading "docs/" so a link
// written relative to any doc reduces to a bare file name.
func trimRelative(path string) string {
	for {
		switch {
		case strings.HasPrefix(path, "./"):
			path = path[2:]
		case strings.HasPrefix(path, "../"):
			path = path[3:]
		default:
			return strings.TrimPrefix(path, "docs/")
		}
	}
}

// githubIDs generates GitHub-style heading anchors so anchor links written for
// the repository's rendered markdown resolve to the same ids on the site.
type githubIDs struct {
	// used counts how many times a slug has been seen, for de-duplication.
	used map[string]int
}

// newGitHubIDs returns an id generator for one document.
func newGitHubIDs() *githubIDs { return &githubIDs{used: map[string]int{}} }

// Generate returns the heading id for value, appending a numeric suffix on a
// repeated slug exactly as GitHub does.
func (g *githubIDs) Generate(value []byte, _ ast.NodeKind) []byte {
	slug := slugify(string(value))
	if slug == "" {
		slug = "section"
	}
	if n := g.used[slug]; n > 0 {
		g.used[slug] = n + 1
		return []byte(fmt.Sprintf("%s-%d", slug, n))
	}
	g.used[slug] = 1
	return []byte(slug)
}

// Put records an externally supplied id so a later heading cannot collide.
func (g *githubIDs) Put(value []byte) { g.used[string(value)]++ }

// slugify lowercases text, drops punctuation, and turns spaces and hyphens into
// hyphens, matching GitHub's heading-anchor algorithm.
func slugify(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '-':
			b.WriteByte('-')
		}
	}
	return b.String()
}

package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/kordloom/whodar/internal/connector"
	"github.com/kordloom/whodar/internal/episode"
	"github.com/kordloom/whodar/internal/httputil"
	"github.com/kordloom/whodar/internal/prompt"
	"github.com/kordloom/whodar/internal/secret"
	"github.com/kordloom/whodar/internal/slack"
)

// credField describes one credential or config value connect collects.
type credField struct {
	// env is the environment variable the value belongs in, e.g. WHODAR_SLACK_TOKEN.
	env string
	// label is the human prompt, e.g. "Slack bot token".
	label string
	// secret reads the value without echo when true.
	secret bool
	// hint is a short format reminder shown with the prompt, e.g. "starts with xoxb-".
	hint string
}

// sourceSpec is the static description of one connectable source: what it gives,
// how to create its credential, which values to collect, and how to validate
// them. The connect driver is one loop over this, not eight commands.
type sourceSpec struct {
	// id is the source identifier, matching the index --source value.
	id string
	// title is the display name, e.g. "Slack".
	title string
	// summary is the one-line "what you get".
	summary string
	// steps are the setup or credential click-path lines shown before collecting.
	steps []string
	// creds are the values to collect; empty for file and repo sources.
	creds []credField
	// authFix is the guidance shown when a credential is rejected.
	authFix string
	// validate makes a cheap read-only auth check from collected creds; nil for
	// sources that need no credentials.
	validate func(ctx context.Context, c map[string]string) error
}

// connectSpecs returns the source descriptions in setup order: the no-credential
// sources first, then everything that needs a token, matching the cookbook.
func connectSpecs() []sourceSpec {
	return []sourceSpec{
		{
			id:      "org-csv",
			title:   "Org chart (CSV)",
			summary: "The backbone of the graph: names, titles, teams, and topics every source joins onto by email.",
			steps: []string{
				"Point this at a CSV with a header row. Recognized columns: name, email,",
				"title, team, org, manager, and topics (semicolon-separated). A row needs a name or email.",
			},
		},
		{
			id:      "codeowners",
			title:   "CODEOWNERS",
			summary: "Who owns which paths, straight from a repository's CODEOWNERS file.",
			steps:   []string{"Point this at a CODEOWNERS file or a repo root that contains one."},
		},
		{
			id:      "git",
			title:   "Git history",
			summary: "Who actually commits to what, read from local clones. Works for any repo you can clone.",
			steps:   []string{"Point this at one or more local repo roots. Bot accounts like dependabot are skipped."},
		},
		{
			id:      "slack",
			title:   "Slack",
			summary: "The strongest single source: which channels exist, what they are about, and who is active on each topic.",
			steps: []string{
				"1. Go to https://api.slack.com/apps and choose Create New App, then From scratch.",
				"2. Under OAuth & Permissions, add these Bot Token Scopes:",
				"     channels:read  channels:history  users:read  users:read.email",
				"3. Install to Workspace, then copy the Bot User OAuth Token (starts with xoxb-).",
			},
			creds: []credField{
				{env: slackTokenEnv, label: "Slack bot token", secret: true, hint: "starts with xoxb-"},
			},
			authFix: "The token is wrong or missing a scope. Recreate it with the four scopes above, then re-enter.",
			validate: func(ctx context.Context, c map[string]string) error {
				return connector.NewSlack(c[slackTokenEnv], connector.SlackOptions{}).Ping(ctx)
			},
		},
		{
			id:      "github",
			title:   "GitHub",
			summary: "Contributors, PR authors, reviewers, assignees, labels, issues, repo topics, and CODEOWNERS.",
			steps: []string{
				"1. Go to https://github.com/settings/tokens and create a token.",
				"2. Grant read access: a classic token needs the repo scope; a fine-grained token",
				"   needs read-only Contents, Metadata, Pull requests, and Issues on the target repos.",
				"3. Copy the token (ghp_... or github_pat_...).",
			},
			creds: []credField{
				{env: githubTokenEnv, label: "GitHub token", secret: true, hint: "ghp_... or github_pat_..."},
			},
			authFix: "The token is wrong, expired, or lacks read access to the repos. Recreate it and re-enter.",
			validate: func(ctx context.Context, c map[string]string) error {
				return connector.NewGitHub(c[githubTokenEnv], connector.GitHubOptions{}).Ping(ctx)
			},
		},
		{
			id:      "jira",
			title:   "Jira",
			summary: "Issue assignees and reporters, weighted by components, labels, summary words, and project.",
			steps: []string{
				"1. Go to https://id.atlassian.com/manage-profile/security/api-tokens.",
				"2. Click Create API token and copy it.",
				"3. You will also need your site URL and the account email that owns the token.",
			},
			creds: []credField{
				{env: jiraURLEnv, label: "Jira site URL", hint: "https://your-site.atlassian.net"},
				{env: jiraEmailEnv, label: "Atlassian account email", hint: "you@example.com"},
				{env: jiraTokenEnv, label: "Jira API token", secret: true},
			},
			authFix: "Check the email matches the account that owns the token, and the URL is the site root with no trailing path.",
			validate: func(ctx context.Context, c map[string]string) error {
				return connector.NewJira(c[jiraURLEnv], c[jiraEmailEnv], c[jiraTokenEnv], connector.JiraOptions{}).Ping(ctx)
			},
		},
		{
			id:      "confluence",
			title:   "Confluence",
			summary: "Page creators and last editors, weighted by labels, title words, and space.",
			steps: []string{
				"Confluence uses the same Atlassian site and token as Jira. If you set up Jira,",
				"reuse those values below. Otherwise create a token at",
				"https://id.atlassian.com/manage-profile/security/api-tokens.",
			},
			creds: []credField{
				{env: confluenceURLEnv, label: "Confluence site URL", hint: "https://your-site.atlassian.net"},
				{env: confluenceEmailEnv, label: "Atlassian account email", hint: "you@example.com"},
				{env: confluenceTokenEnv, label: "Confluence API token", secret: true},
			},
			authFix: "Check the email matches the token owner, and the URL is the site root with no trailing path.",
			validate: func(ctx context.Context, c map[string]string) error {
				return connector.NewConfluence(
					c[confluenceURLEnv], c[confluenceEmailEnv], c[confluenceTokenEnv], connector.ConfluenceOptions{}).Ping(ctx)
			},
		},
		{
			id:      "pagerduty",
			title:   "PagerDuty",
			summary: "Every service and who is on call, so each on-call person gets the topics they answer for.",
			steps: []string{
				"1. In PagerDuty, go to Integrations > API Access Keys.",
				"2. Create a read-only API key and copy it.",
			},
			creds: []credField{
				{env: pagerdutyTokenEnv, label: "PagerDuty API token", secret: true, hint: "read-only key"},
			},
			authFix: "The token is wrong. Create a read-only API key and re-enter.",
			validate: func(ctx context.Context, c map[string]string) error {
				return connector.NewPagerDuty(c[pagerdutyTokenEnv], connector.PagerDutyOptions{}).Ping(ctx)
			},
		},
	}
}

// newConnectCmd builds the connect command, the interactive setup wizard.
func newConnectCmd(opts *options) *cobra.Command {
	var status bool
	cmd := &cobra.Command{
		Use:   "connect [source]",
		Short: "Set up a source interactively",
		Long: `Set up a source with a guided flow: it explains what the source gives you, shows
how to create the credential, collects and validates it, runs the first index,
and shows how to ask.

Run with no argument for a menu of every source and whether it is configured.
Run with a source to set up just that one. Use --status for a non-interactive
report. Credentials are read from the environment, validated in memory, and never
written to disk; connect prints the export line for you to save yourself.

Sources: org-csv, codeowners, git, slack, github, jira, confluence, pagerduty.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if status {
				return runStatus(cmd, opts)
			}
			var chosen *sourceSpec
			if len(args) == 1 {
				spec, ok := specByID(args[0])
				if !ok {
					return fmt.Errorf("%w: %q (want %s)", ErrUnknownSource, args[0], strings.Join(specIDs(), ", "))
				}
				chosen = &spec
			}
			ui := prompt.New(cmd.InOrStdin(), cmd.ErrOrStderr(), cmd.ErrOrStderr())
			if !ui.Interactive() {
				return fmt.Errorf(
					"%w: connect needs an interactive terminal; use 'whodar index' for scripts (see docs/CONNECT.md)",
					ErrBadArgs)
			}
			if chosen != nil {
				return quietAbort(runConnect(cmd, opts, ui, *chosen))
			}
			return quietAbort(runMenu(cmd, opts, ui))
		},
	}
	cmd.Flags().BoolVar(&status, "status", false, "Report which sources are configured, without prompting.")
	return cmd
}

// runMenu shows every source with its configured state and sets up the chosen one.
func runMenu(cmd *cobra.Command, opts *options, ui *prompt.IO) error {
	specs := connectSpecs()
	ui.Blank()
	ui.Step("Connect a source")
	ui.Detail("Pick a source to set up or refresh. Its current state is shown.")
	ui.Blank()
	labels := make([]string, len(specs))
	for i, s := range specs {
		labels[i] = fmt.Sprintf("%-11s %s", s.id, statusNote(s))
	}
	idx, err := ui.Choose("Source", labels)
	if err != nil {
		return err
	}
	return runConnect(cmd, opts, ui, specs[idx])
}

// runConnect walks one source: explain, create the credential, collect and
// validate it, print the export line, run the first index, and show what is next.
func runConnect(cmd *cobra.Command, opts *options, ui *prompt.IO, spec sourceSpec) error {
	ui.Blank()
	ui.Step("Connect %s", spec.title)
	ui.Detail("%s", spec.summary)
	ui.Blank()
	printPrivacy(ui, opts)

	if len(spec.steps) > 0 {
		ui.Blank()
		if len(spec.creds) > 0 {
			ui.Step("Create the credential")
		} else {
			ui.Step("Before you start")
		}
		for _, s := range spec.steps {
			ui.Detail("%s", s)
		}
	}

	creds := map[string]string{}
	if len(spec.creds) > 0 {
		ui.Blank()
		ui.Step("Enter your credentials")
		var err error
		if creds, err = collectAndValidate(cmd, ui, spec); err != nil {
			return err
		}
		if err := persistCreds(ui, spec, creds); err != nil {
			return err
		}
	}

	ui.Blank()
	run, err := ui.Confirm(fmt.Sprintf("Index %s now?", spec.title), true)
	if err != nil {
		return err
	}
	if !run {
		ui.Hint("Index later with:  whodar index --source %s%s", spec.id, mergeSuffix(opts))
	} else {
		ui.Blank()
		ui.Step("Indexing %s", spec.title)
		if ierr := runFirstIndex(cmd, opts, ui, spec, creds); ierr != nil {
			ui.Blank()
			ui.Fail("Indexing failed: %v", ierr)
			return ierr
		}
		ui.Success("%s indexed.", spec.title)
	}

	ui.Blank()
	ui.Step("Next")
	ui.Detail("Ask a question:")
	ui.Command(`whodar ask "who owns billing retries"`)
	ui.Detail("Open the web UI:")
	ui.Command("whodar serve")
	ui.Blank()
	return nil
}

// collectAndValidate collects each credential and validates it against the
// source, re-prompting until it is accepted or the user backs out.
func collectAndValidate(cmd *cobra.Command, ui *prompt.IO, spec sourceSpec) (map[string]string, error) {
	for {
		creds, err := collect(ui, spec)
		if err != nil {
			return nil, err
		}
		ui.Blank()
		ui.Step("Validating")
		verr := spec.validate(cmd.Context(), creds)
		if verr == nil {
			ui.Success("Credentials accepted.")
			return creds, nil
		}
		ui.Fail("%s", explainAuthError(spec, verr))
		again, cerr := ui.Confirm("Try again?", true)
		if cerr != nil {
			return nil, cerr
		}
		if !again {
			return nil, prompt.ErrAborted
		}
		ui.Blank()
	}
}

// collect prompts for each credential, reusing a value already in the
// environment when the user allows it, and reading secrets without echo.
func collect(ui *prompt.IO, spec sourceSpec) (map[string]string, error) {
	creds := make(map[string]string, len(spec.creds))
	for _, cf := range spec.creds {
		if existing := secret.Resolve(cf.env); existing != "" {
			use, err := ui.Confirm(fmt.Sprintf("%s is already set. Use it?", cf.env), true)
			if err != nil {
				return nil, err
			}
			if use {
				creds[cf.env] = existing
				continue
			}
		}
		label := cf.label
		if cf.hint != "" {
			label = fmt.Sprintf("%s (%s)", cf.label, cf.hint)
		}
		var (
			v   string
			err error
		)
		if cf.secret {
			v, err = ui.Secret(label)
		} else {
			v, err = ui.Line(label)
		}
		if err != nil {
			return nil, err
		}
		if v == "" {
			return nil, fmt.Errorf("%w: %s is required", ErrBadArgs, cf.env)
		}
		creds[cf.env] = v
	}
	return creds, nil
}

// persistCreds offers to store the collected credentials in the OS keychain so
// future runs read them without environment variables, falling back to printing
// export lines when the keychain is declined or unavailable. connect never edits
// dotfiles.
func persistCreds(ui *prompt.IO, spec sourceSpec, creds map[string]string) error {
	ui.Blank()
	ui.Step("Make it permanent")
	save, err := ui.Confirm("Save these to your OS keychain?", true)
	if err != nil {
		return err
	}
	if save {
		if kerr := saveToKeychain(ui, spec, creds); kerr == nil {
			return nil
		}
		// A headless or locked keychain must never leave the user stuck, so fall
		// through to export lines they can persist by hand.
		ui.Detail("Could not use the keychain; add these to your shell profile instead.")
	} else {
		ui.Detail("Add these to your shell profile so future runs stay connected.")
	}
	ui.Detail("They hold your credentials, so treat that file as a secret.")
	ui.Blank()
	printExports(ui, spec, creds)
	return nil
}

// saveToKeychain writes each collected credential to the OS keychain. An error
// from the store aborts so the caller can fall back to export lines.
func saveToKeychain(ui *prompt.IO, spec sourceSpec, creds map[string]string) error {
	for _, cf := range spec.creds {
		v := creds[cf.env]
		if v == "" {
			continue
		}
		if err := secret.Save(cf.env, v); err != nil {
			return err
		}
	}
	ui.Success("Saved to your OS keychain. Future runs read them from there, no environment variables needed.")
	return nil
}

// printExports prints the export lines the user adds to their shell profile,
// naming a secret variable without echoing its value.
func printExports(ui *prompt.IO, spec sourceSpec, creds map[string]string) {
	for _, cf := range spec.creds {
		v := creds[cf.env]
		if v == "" {
			continue
		}
		if cf.secret {
			// Do not echo the secret to the terminal, where it would land in
			// scrollback and shell history. The user just entered it, so name the
			// variable and let them paste the value in themselves.
			ui.Command("export %s='...'   # paste the value you just entered", cf.env)
			continue
		}
		ui.Command("export %s=%s", cf.env, v)
	}
}

// runFirstIndex loads the freshly collected credentials into the environment for
// this run, prompts for any source-specific scope, then reuses the same fetch and
// index path as the index command. Credentials stay in memory; nothing is written
// to disk beyond the local index the index command already writes.
func runFirstIndex(cmd *cobra.Command, opts *options, ui *prompt.IO, spec sourceSpec, creds map[string]string) error {
	for k, v := range creds {
		if v == "" {
			continue
		}
		if err := os.Setenv(k, v); err != nil {
			return fmt.Errorf("set %s: %w", k, err)
		}
	}

	merge := indexExists(opts)
	var (
		recs []connector.Record
		eps  []episode.Episode
		err  error
	)
	switch spec.id {
	case "slack":
		includePrivate := false
		if opts.pol.AllowPrivateChannels() {
			if includePrivate, err = ui.Confirm("Include private channels the bot can read?", false); err != nil {
				return err
			}
		}
		remember, cerr := ui.Confirm(
			"Remember conversations, so whodar recall can find who helped you and when?", true)
		if cerr != nil {
			return cerr
		}
		recs, eps, err = fetchSlack(cmd, opts, slackArgs{
			includePrivate: includePrivate, sinceDays: 180, maxMessages: 5000,
			episodes: remember, maxEpisodes: 200,
		})
	case "github":
		a, gerr := promptGitHubScope(ui)
		if gerr != nil {
			return gerr
		}
		// Merged changes, resolved tickets, and resolved incidents are recorded
		// as a matter of course: they are the same metadata already being
		// indexed, and they are what recall points back at.
		a.episodes = true
		recs, eps, err = fetchGitHub(cmd, a)
	case "jira":
		projects, perr := promptList(ui, "Jira project keys to index (space-separated, blank for all)")
		if perr != nil {
			return perr
		}
		recs, eps, err = fetchJira(cmd, jiraArgs{
			projects: projects, maxIssues: 1000, episodes: true,
		})
	case "confluence":
		spaces, serr := promptList(ui, "Confluence space keys to index (space-separated, blank for all)")
		if serr != nil {
			return serr
		}
		recs, err = fetchConfluence(cmd, confluenceArgs{spaces: spaces, maxPages: 2000})
	case "pagerduty":
		recs, eps, err = fetchPagerDuty(cmd, true)
	case "org-csv":
		recs, err = fetchLocal(cmd, ui, "Path to the org chart CSV", func(path string) connector.Source {
			oc := connector.NewOrgCSV(path)
			oc.Log = cmd.ErrOrStderr()
			return oc
		})
	case "codeowners":
		recs, err = fetchLocal(cmd, ui, "Path to a CODEOWNERS file or a repo root", func(path string) connector.Source {
			return connector.NewCodeOwners(path)
		})
	case "git":
		paths, perr := promptList(ui, "Local repo paths (space-separated)")
		if perr != nil {
			return perr
		}
		if len(paths) == 0 {
			return fmt.Errorf("%w: at least one repo path is required for git", ErrBadArgs)
		}
		recs, err = connector.NewGitHistory(connector.GitOptions{
			Paths: paths, SinceDays: 365, MaxCommits: 2000, Log: cmd.ErrOrStderr(),
		}).Fetch(cmd.Context())
	default:
		return fmt.Errorf("%w: %q", ErrUnknownSource, spec.id)
	}
	if err != nil {
		return err
	}
	return indexRecords(cmd, opts, recs, indexParams{merge: merge, halfLifeDays: 180, episodes: eps})
}

// fetchLocal prompts for a required path and fetches records from the source the
// build function constructs for it.
func fetchLocal(
	cmd *cobra.Command, ui *prompt.IO, label string, build func(path string) connector.Source,
) ([]connector.Record, error) {
	path, err := ui.Line(label)
	if err != nil {
		return nil, err
	}
	if path == "" {
		return nil, fmt.Errorf("%w: a path is required", ErrBadArgs)
	}
	return build(path).Fetch(cmd.Context())
}

// promptGitHubScope asks whether to index a single repo or a whole org and
// returns the matching fetch arguments, resolving emails so people join sources.
func promptGitHubScope(ui *prompt.IO) (githubArgs, error) {
	ui.Detail("Index a single repo (owner/name) or a whole org.")
	repo, err := ui.Line("GitHub repo owner/name, or blank to index an org")
	if err != nil {
		return githubArgs{}, err
	}
	a := githubArgs{emails: true}
	if repo != "" {
		a.repos = []string{repo}
		return a, nil
	}
	org, err := ui.Line("GitHub org to index")
	if err != nil {
		return githubArgs{}, err
	}
	if org == "" {
		return githubArgs{}, fmt.Errorf("%w: a repo or an org is required for github", ErrBadArgs)
	}
	a.org = org
	return a, nil
}

// promptList reads a whitespace-separated list, returning nil when left blank.
func promptList(ui *prompt.IO, label string) ([]string, error) {
	line, err := ui.Line(label)
	if err != nil {
		return nil, err
	}
	return strings.Fields(line), nil
}

// runStatus prints which sources are configured, without prompting, so it works
// over a pipe or in a script. The report goes to stdout; nothing is collected.
func runStatus(cmd *cobra.Command, opts *options) error {
	ui := prompt.New(cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
	ui.Step("whodar sources")
	for _, s := range connectSpecs() {
		ui.Detail("%-11s %s", s.id, statusNote(s))
	}
	ui.Blank()
	if indexExists(opts) {
		ui.Detail("index: %s", opts.indexPath())
	} else {
		ui.Detail("index: none yet (%s)", opts.indexPath())
	}
	return nil
}

// statusNote describes a source's readiness: sources with no credentials are
// always ready, credentialed ones report whether their credentials resolve.
func statusNote(spec sourceSpec) string {
	if len(spec.creds) == 0 {
		return "ready, no credentials needed"
	}
	if credsSet(spec) {
		return "configured"
	}
	return "not configured"
}

// credsSet reports whether a source's credentials resolve, from either the
// keychain or the environment. Confluence also counts as configured when the
// Jira variables are set, because it falls back to them.
func credsSet(spec sourceSpec) bool {
	if allCredsSet(spec.creds) {
		return true
	}
	if spec.id == "confluence" {
		return secret.Resolve(jiraURLEnv) != "" && secret.Resolve(jiraEmailEnv) != "" &&
			secret.Resolve(jiraTokenEnv) != ""
	}
	return false
}

// allCredsSet reports whether every field resolves to a non-empty value.
func allCredsSet(fields []credField) bool {
	for _, cf := range fields {
		if secret.Resolve(cf.env) == "" {
			return false
		}
	}
	return len(fields) > 0
}

// explainAuthError turns a validation error into a specific fix. A 401 or 403,
// or Slack's logical auth error, points at the source's credential guidance; a
// transport error points at the URL and network.
func explainAuthError(spec sourceSpec, err error) string {
	var se *httputil.StatusError
	if errors.As(err, &se) {
		switch se.Code {
		case http.StatusUnauthorized, http.StatusForbidden:
			return spec.authFix
		case http.StatusNotFound:
			return "Not found (HTTP 404). Check the site URL is the root, with no trailing path."
		default:
			return fmt.Sprintf("The server returned HTTP %d. %s", se.Code, spec.authFix)
		}
	}
	if errors.Is(err, slack.ErrAPI) {
		return spec.authFix
	}
	return fmt.Sprintf("Could not reach the service (%v). Check the URL and your network, then try again.", err)
}

// printPrivacy states, up front, that credentials stay in memory and the index
// stays local, so a work user can wire in whodar and keep people data private.
func printPrivacy(ui *prompt.IO, opts *options) {
	ui.Hint("Privacy: credentials you enter are held in memory for this run only.")
	ui.Hint("They are never written to disk and never logged. Your index stays local")
	ui.Hint("at %s, readable only by you, and is never uploaded.", opts.indexPath())
}

// mergeSuffix returns " --merge" when an index already exists, matching the
// flag a later manual index run would need to add onto it.
func mergeSuffix(opts *options) string {
	if indexExists(opts) {
		return " --merge"
	}
	return ""
}

// indexExists reports whether an on-disk index is already present.
func indexExists(opts *options) bool {
	_, err := os.Stat(opts.indexPath())
	return err == nil
}

// specByID returns the source with the given id.
func specByID(id string) (sourceSpec, bool) {
	for _, s := range connectSpecs() {
		if s.id == id {
			return s, true
		}
	}
	return sourceSpec{}, false
}

// specIDs returns every source id, for the unknown-source error message.
func specIDs() []string {
	specs := connectSpecs()
	ids := make([]string, len(specs))
	for i, s := range specs {
		ids[i] = s.id
	}
	return ids
}

// quietAbort maps a user backing out, by menu quit or end of input, to a clean
// exit rather than an error.
func quietAbort(err error) error {
	// Only a prompt-level abort or end-of-input is a clean exit. A network
	// error that happens to unwrap to io.EOF, such as a server closing a
	// connection mid-fetch, must not be swallowed as if the user backed out,
	// which would report a failed first index as success.
	if errors.Is(err, prompt.ErrAborted) || errors.Is(err, prompt.ErrInputClosed) {
		return nil
	}
	return err
}

package cmd

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/kordloom/whodar/internal/httputil"
	"github.com/kordloom/whodar/internal/prompt"
	"github.com/kordloom/whodar/internal/slack"
)

// TestConnectSpecs verifies each source description is internally consistent: a
// credentialed source has a validator, a fix message, and complete fields; a
// no-credential source has neither validator nor fields.
func TestConnectSpecs(t *testing.T) {
	t.Parallel()
	valid := map[string]bool{
		"org-csv": true, "codeowners": true, "git": true, "slack": true,
		"github": true, "jira": true, "confluence": true, "pagerduty": true,
	}
	seen := make(map[string]bool)
	for _, s := range connectSpecs() {
		if s.id == "" || s.title == "" || s.summary == "" {
			t.Errorf("spec %q is missing an id, title, or summary", s.id)
		}
		if seen[s.id] {
			t.Errorf("duplicate source id %q", s.id)
		}
		seen[s.id] = true
		if !valid[s.id] {
			t.Errorf("source id %q is not an index source", s.id)
		}
		if len(s.creds) > 0 {
			if s.validate == nil {
				t.Errorf("%q has credentials but no validate func", s.id)
			}
			if s.authFix == "" {
				t.Errorf("%q has credentials but no authFix", s.id)
			}
			for _, c := range s.creds {
				if c.env == "" || c.label == "" {
					t.Errorf("%q has an incomplete credential field: %+v", s.id, c)
				}
			}
		} else if s.validate != nil {
			t.Errorf("%q has no credentials but a validate func", s.id)
		}
	}
	for _, id := range []string{"slack", "github", "jira", "confluence", "pagerduty"} {
		if !seen[id] {
			t.Errorf("missing credentialed source %q", id)
		}
	}
}

// TestConnectStatus verifies the non-interactive report lists every source and
// reflects whether its credentials are set, on stdout and without color.
func TestConnectStatus(t *testing.T) {
	for _, e := range []string{
		slackTokenEnv, githubTokenEnv, pagerdutyTokenEnv, jiraURLEnv, jiraEmailEnv,
		jiraTokenEnv, confluenceURLEnv, confluenceEmailEnv, confluenceTokenEnv,
	} {
		t.Setenv(e, "")
	}
	dir := t.TempDir()
	out, _, err := runCmd(t, "connect", "--status", "--data-dir", dir)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	report := string(out)
	for _, id := range []string{
		"org-csv", "codeowners", "git", "slack", "github", "jira", "confluence", "pagerduty",
	} {
		if !strings.Contains(report, id) {
			t.Errorf("status missing source %q:\n%s", id, report)
		}
	}
	if !strings.Contains(report, "not configured") {
		t.Errorf("expected a not-configured source:\n%s", report)
	}
	if strings.Contains(report, "\033[") {
		t.Errorf("color leaked into piped status:\n%q", report)
	}

	t.Setenv(slackTokenEnv, "xoxb-test")
	out2, _, err := runCmd(t, "connect", "--status", "--data-dir", dir)
	if err != nil {
		t.Fatalf("status after configuring slack: %v", err)
	}
	if !strings.Contains(string(out2), "configured") {
		t.Errorf("slack not reported configured:\n%s", out2)
	}
}

// TestConnectNonInteractive verifies connect refuses to prompt without a
// terminal and points at the scriptable index command.
func TestConnectNonInteractive(t *testing.T) {
	t.Parallel()
	_, _, err := runCmdStdin(t, "", "connect")
	if !errors.Is(err, ErrBadArgs) {
		t.Fatalf("err = %v, want ErrBadArgs", err)
	}
}

// TestConnectUnknownSource verifies an unrecognized source is rejected before
// the terminal check, so it fails clearly even in a script.
func TestConnectUnknownSource(t *testing.T) {
	t.Parallel()
	_, _, err := runCmdStdin(t, "", "connect", "not-a-source")
	if !errors.Is(err, ErrUnknownSource) {
		t.Fatalf("err = %v, want ErrUnknownSource", err)
	}
}

// TestExplainAuthError verifies validation errors map to a specific fix: an auth
// status or Slack's logical error points at the credential guidance, and a
// transport error points at the URL and network.
func TestExplainAuthError(t *testing.T) {
	t.Parallel()
	spec := sourceSpec{authFix: "recreate the token"}
	tests := []struct {
		Name         string
		Err          error
		WantContains string
	}{ // Test 0-5: the status and error mappings.
		{Name: "unauthorized", Err: &httputil.StatusError{Code: 401}, WantContains: "recreate the token"},
		{Name: "forbidden", Err: &httputil.StatusError{Code: 403}, WantContains: "recreate the token"},
		{Name: "notfound", Err: &httputil.StatusError{Code: 404}, WantContains: "404"},
		{Name: "teapot", Err: &httputil.StatusError{Code: 418}, WantContains: "418"},
		{Name: "slack", Err: slack.ErrAPI, WantContains: "recreate the token"},
		{Name: "transport", Err: errors.New("dial tcp: no route to host"), WantContains: "Could not reach"},
	}
	for testNum, test := range tests {
		got := explainAuthError(spec, test.Err)
		if !strings.Contains(got, test.WantContains) {
			t.Errorf("test %d (%s): got %q, want contains %q", testNum, test.Name, got, test.WantContains)
		}
	}
}

// TestCollect verifies collection reads a plain value and a secret without
// echoing the secret, and reuses a value already set in the environment.
func TestCollect(t *testing.T) {
	spec := sourceSpec{creds: []credField{
		{env: "WHODAR_TEST_URL", label: "URL"},
		{env: "WHODAR_TEST_TOKEN", label: "Token", secret: true},
	}}

	t.Setenv("WHODAR_TEST_URL", "")
	t.Setenv("WHODAR_TEST_TOKEN", "")
	var out bytes.Buffer
	ui := prompt.New(strings.NewReader("https://x\ns3cr3t\n"), &out, &out)
	creds, err := collect(ui, spec)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if creds["WHODAR_TEST_URL"] != "https://x" || creds["WHODAR_TEST_TOKEN"] != "s3cr3t" {
		t.Errorf("creds = %v", creds)
	}
	if strings.Contains(out.String(), "s3cr3t") {
		t.Errorf("secret echoed into output: %q", out.String())
	}

	t.Setenv("WHODAR_TEST_URL", "https://preset")
	var out2 bytes.Buffer
	ui2 := prompt.New(strings.NewReader("y\nsecret2\n"), &out2, &out2)
	creds2, err := collect(ui2, spec)
	if err != nil {
		t.Fatalf("collect reuse: %v", err)
	}
	if creds2["WHODAR_TEST_URL"] != "https://preset" || creds2["WHODAR_TEST_TOKEN"] != "secret2" {
		t.Errorf("creds2 = %v", creds2)
	}
}

// runCmdStdin executes the root command with a scripted, non-terminal stdin,
// capturing stdout and stderr.
func runCmdStdin(t *testing.T, stdin string, args ...string) (stdout, stderr []byte, err error) {
	t.Helper()
	var out, errBuf bytes.Buffer
	root := newRootCmd()
	root.SetIn(strings.NewReader(stdin))
	root.SetOut(&out)
	root.SetErr(&errBuf)
	root.SetArgs(args)
	err = root.Execute()
	return out.Bytes(), errBuf.Bytes(), err
}

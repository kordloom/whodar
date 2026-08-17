package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/kordloom/whodar/internal/policy"
)

// TestResolvePolicy covers flag, system file, env file, and lock precedence.
func TestResolvePolicy(t *testing.T) {
	t.Parallel()
	const (
		lockedStrict   = `{"mode":"strict","locked":true,"private_channels":"deny"}`
		lockedOpen     = `{"mode":"open","locked":true}`
		unlockedRedact = `{"mode":"redacted","locked":false}`
		unlockedOpen   = `{"mode":"open","locked":false}`
		unlockedDeny   = `{"mode":"redacted","locked":false,"private_channels":"deny"}`
	)
	tests := []struct {
		SystemBody     string // system policy body; empty means no system file
		EnvBody        string // env policy body; empty means no env file
		EnvAbsent      bool   // point the env override at a missing file
		PolicyName     string
		FlagChanged    bool
		WantMode       policy.Mode
		WantPrivateOff bool
		WantWarn       string // substring expected on stderr; empty means silence
	}{{ // Test 0: No files: the flag governs.
		PolicyName: "open", FlagChanged: true, WantMode: policy.Open,
	}, { // Test 1: An unlocked env file supplies the default mode.
		EnvBody: unlockedRedact, PolicyName: "strict", WantMode: policy.Redacted,
	}, { // Test 2: A changed flag overrides an unlocked env file.
		EnvBody: unlockedRedact, PolicyName: "open", FlagChanged: true, WantMode: policy.Open,
	}, { // Test 3: A locked env file pins over the flag.
		EnvBody: lockedStrict, PolicyName: "open", FlagChanged: true, WantMode: policy.Strict,
		WantPrivateOff: true, WantWarn: "--policy ignored",
	}, { // Test 4: An env override at an absent file falls back to the flag.
		EnvAbsent: true, PolicyName: "open", FlagChanged: true, WantMode: policy.Open,
	}, { // Test 5: A locked system policy beats the env file and the flag.
		SystemBody: lockedStrict, EnvBody: unlockedOpen, PolicyName: "open", FlagChanged: true,
		WantMode: policy.Strict, WantPrivateOff: true, WantWarn: policyEnvVar + " ignored",
	}, { // Test 6: A locked system policy beats an env override at an absent file.
		SystemBody: lockedStrict, EnvAbsent: true, PolicyName: "strict", WantMode: policy.Strict,
		WantPrivateOff: true, WantWarn: policyEnvVar + " ignored",
	}, { // Test 7: An unlocked system file yields to the env file.
		SystemBody: unlockedRedact, EnvBody: unlockedOpen, PolicyName: "strict", WantMode: policy.Open,
	}, { // Test 8: An unlocked system file supplies the default alone.
		SystemBody: unlockedRedact, PolicyName: "strict", WantMode: policy.Redacted,
	}, { // Test 9: A locked env file pins whatever it says when nothing outranks it.
		EnvBody: lockedOpen, PolicyName: "strict", WantMode: policy.Open,
	}, { // Test 10: An unlocked file's private_channels:deny is honored, mode from file.
		EnvBody: unlockedDeny, PolicyName: "strict", WantMode: policy.Redacted, WantPrivateOff: true,
	}, { // Test 11: A changed flag overrides the mode, but private_channels:deny still holds.
		EnvBody: unlockedDeny, PolicyName: "open", FlagChanged: true, WantMode: policy.Open,
		WantPrivateOff: true,
	}}
	for testNum, test := range tests {
		t.Run(fmt.Sprintf("test %d", testNum), func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			o := &options{policyName: test.PolicyName}
			if test.SystemBody != "" {
				o.systemPolicyFile = writePolicy(t, dir, "system.json", test.SystemBody)
			}
			switch {
			case test.EnvAbsent:
				o.envPolicyFile = filepath.Join(dir, "absent.json")
			case test.EnvBody != "":
				o.envPolicyFile = writePolicy(t, dir, "env.json", test.EnvBody)
			}

			var errOut strings.Builder
			if err := o.resolvePolicy(test.FlagChanged, &errOut); err != nil {
				t.Fatalf("resolvePolicy: %v", err)
			}
			if diff := cmp.Diff(test.WantMode.String(), o.pol.Mode().String()); diff != "" {
				t.Errorf("mode mismatch (-want +got):\n%s", diff)
			}
			if got := !o.pol.AllowPrivateChannels(); got != test.WantPrivateOff {
				t.Errorf("private off = %t, want %t", got, test.WantPrivateOff)
			}
			if test.WantWarn == "" && errOut.Len() != 0 {
				t.Errorf("unexpected warning: %q", errOut.String())
			}
			if test.WantWarn != "" && !strings.Contains(errOut.String(), test.WantWarn) {
				t.Errorf("warnings = %q, want containing %q", errOut.String(), test.WantWarn)
			}
		})
	}
}

// writePolicy writes a policy body to dir/name and returns the path.
func writePolicy(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

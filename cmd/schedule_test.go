package cmd

import (
	"strings"
	"testing"
)

func TestLaunchdPlist(t *testing.T) {
	t.Parallel()
	p := launchdPlist("dev.whodar.refresh", "/usr/local/bin/whodar", "/data", "/logs/r.log")
	wants := []string{
		"dev.whodar.refresh", "/usr/local/bin/whodar", "<string>refresh</string>",
		"<string>/data</string>", "/logs/r.log", "StartCalendarInterval",
	}
	for _, want := range wants {
		if !strings.Contains(p, want) {
			t.Errorf("plist missing %q", want)
		}
	}
}

func TestCountTrue(t *testing.T) {
	t.Parallel()
	if got := countTrue(true, false, true); got != 2 {
		t.Errorf("countTrue = %d, want 2", got)
	}
	if got := countTrue(false, false); got != 0 {
		t.Errorf("countTrue = %d, want 0", got)
	}
}

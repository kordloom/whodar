package license

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"time"
)

// EnvLicense names the environment variable holding a license: either the
// license itself or a path to it.
const EnvLicense = "WHODAR_LICENSE"

// FileName is the license file whodar looks for in its data directory.
const FileName = "license.json"

// State is what a whodar install is entitled to right now, and why.
type State struct {
	// Tier is the feature set in force. It is Free whenever no valid,
	// unexpired license is present.
	Tier Tier
	// License is the license that was read, present even when it has expired
	// so the reason can name it.
	License License
	// Err explains why the tier is not what a license claimed: no license,
	// an invalid one, or an expired one. It is nil on the free tier with no
	// license configured.
	Err error
	// Raw is the verified license file exactly as signed, kept so a sealed
	// finding can embed the license without re-serializing it.
	Raw []byte
}

// Has reports whether the state grants a tier. Tiers are a ladder: a Memory
// license grants Risk, and every state grants Free.
func (s State) Has(t Tier) bool {
	return s.Tier.rank() >= t.rank()
}

// Reason explains the state in one sentence for a person reading output.
func (s State) Reason() string {
	switch {
	case s.Err == nil && s.Tier == Free:
		return "No license configured: the free tier is in force."
	case errors.Is(s.Err, ErrExpired):
		return fmt.Sprintf(
			"The license for %s expired on %s, so the free tier is in force. "+
				"Everything already indexed stays on disk.",
			s.License.Org, s.License.Expires.Format(time.DateOnly))
	case errors.Is(s.Err, ErrInvalid):
		return "The license could not be verified, so the free tier is in force: " + s.Err.Error() + "."
	case s.Err != nil:
		return "The license could not be read, so the free tier is in force: " + s.Err.Error() + "."
	default:
		return fmt.Sprintf("Licensed to %s for the %s tier.", s.License.Org, s.License.Tier)
	}
}

// Resolve determines the tier in force. It reads the license named by the
// environment, or the license file in dataDir, and never fails: an absent,
// unreadable, invalid, or expired license leaves the free tier in force and
// records why. Data already on disk is untouched by any outcome here.
func Resolve(dataDir string, now time.Time) State {
	raw, err := read(dataDir)
	if err != nil {
		if errors.Is(err, ErrNoLicense) {
			return State{Tier: Free}
		}
		return State{Tier: Free, Err: err}
	}
	lic, err := Verify(raw, now)
	if err != nil {
		return State{Tier: Free, License: lic, Err: err}
	}
	return State{Tier: lic.Tier, License: lic, Raw: raw}
}

// read returns the license bytes from the environment or the data directory.
// The environment value may be the license itself, which suits a container,
// or a path to it.
func read(dataDir string) ([]byte, error) {
	if v := strings.TrimSpace(os.Getenv(EnvLicense)); v != "" {
		if strings.HasPrefix(v, "{") {
			return []byte(v), nil
		}
		data, err := os.ReadFile(v)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", EnvLicense, err)
		}
		return data, nil
	}
	if dataDir == "" {
		return nil, ErrNoLicense
	}
	data, err := os.ReadFile(dataDir + string(os.PathSeparator) + FileName)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNoLicense
	}
	if err != nil {
		return nil, fmt.Errorf("read license: %w", err)
	}
	return data, nil
}

package policy

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
)

// Config is the on-disk policy an organization can ship to pin behavior. When
// Locked is true, user flags cannot loosen it.
type Config struct {
	// Mode is the egress mode name: strict, redacted, or open.
	Mode string `json:"mode"`
	// Locked pins the policy so user flags cannot change it.
	Locked bool `json:"locked"`
	// PrivateChannels is "allow" or "deny" for private-channel ingest.
	PrivateChannels string `json:"private_channels"`
	// Archive is "allow" or "deny" for retaining conversation content.
	Archive string `json:"archive"`
}

// Load reads a policy Config from path. found is false when the file is absent;
// an unreadable or malformed file is an error.
func Load(path string) (cfg Config, found bool, err error) {
	if path == "" {
		return Config{}, false, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Config{}, false, nil
	}
	if err != nil {
		return Config{}, false, fmt.Errorf("policy: read %s: %w", path, err)
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, false, fmt.Errorf("policy: parse %s: %w", path, err)
	}
	return cfg, true, nil
}

// Policy builds an enforced Policy from the config.
func (c Config) Policy() (Policy, error) {
	mode, err := ParseMode(c.Mode)
	if err != nil {
		return Policy{}, err
	}
	p := New(mode, c.Locked)
	if strings.EqualFold(strings.TrimSpace(c.PrivateChannels), "deny") {
		p = p.WithoutPrivateChannels()
	}
	if strings.EqualFold(strings.TrimSpace(c.Archive), "deny") {
		p = p.WithoutArchive()
	}
	return p, nil
}

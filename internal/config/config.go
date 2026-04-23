// Package config loads .markgate.yml.
//
// The file is optional: when absent, every key defaults to hash=git-tree.
// The file is looked up at $(git rev-parse --show-toplevel)/.markgate.yml
// and nowhere else (no parent-dir walking).
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/go-to-k/markgate/internal/key"
)

// Filename is the fixed config file name.
const Filename = ".markgate.yml"

// Hash type identifiers.
const (
	HashGitTree = "git-tree"
	HashFiles   = "files"
)

// Config mirrors the YAML document.
type Config struct {
	Gates map[string]Gate `yaml:"gates"`
}

// Gate is a single entry under gates.<key>.
type Gate struct {
	Hash    string   `yaml:"hash"`
	Include []string `yaml:"include,omitempty"`
	Exclude []string `yaml:"exclude,omitempty"`
	// StateDir overrides the marker storage directory for this gate.
	// Relative paths resolve against the repo top-level (same semantics as
	// the --state-dir flag). Committing an absolute path is an anti-pattern
	// because it won't exist on other machines; prefer a relative path.
	StateDir string `yaml:"state_dir,omitempty"`
}

// Load reads topLevel/.markgate.yml. A missing file yields an empty
// Config (never nil) so callers can always call c.Gate(...) safely.
func Load(topLevel string) (*Config, error) {
	path := filepath.Join(topLevel, Filename)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &Config{}, nil
		}
		return nil, err
	}
	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", Filename, err)
	}
	if err := c.validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Config) validate() error {
	for k, g := range c.Gates {
		if err := key.Validate(k); err != nil {
			return fmt.Errorf("gates.%s: %w", k, err)
		}
		switch g.Hash {
		case "", HashGitTree:
			// git-tree accepts optional include/exclude for narrowing the
			// hash target while keeping HEAD-aware invalidation.
		case HashFiles:
			if len(g.Include) == 0 {
				return fmt.Errorf("gates.%s: hash=files requires a non-empty include list", k)
			}
		default:
			return fmt.Errorf("gates.%s: unknown hash %q (want %q or %q)", k, g.Hash, HashGitTree, HashFiles)
		}
	}
	return nil
}

// Gate returns the configuration for k, defaulting to hash=git-tree when
// either the config or the key is absent.
func (c *Config) Gate(k string) Gate {
	if c != nil {
		if g, ok := c.Gates[k]; ok {
			if g.Hash == "" {
				g.Hash = HashGitTree
			}
			return g
		}
	}
	return Gate{Hash: HashGitTree}
}

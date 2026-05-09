// Package config loads .markgate.yml.
//
// The file is optional: when absent, every key defaults to hash=git-tree.
// The file is looked up at $(git rev-parse --show-toplevel)/.markgate.yml
// and nowhere else (no parent-dir walking).
package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/go-to-k/markgate/internal/duration"
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
	// TTL, if non-empty, makes verify treat a marker older than this
	// duration as a mismatch even when the digest still matches. Useful
	// for gates that verify external state (e.g. cloud APIs) that drift
	// independently of the repo. Format is the union of time.ParseDuration
	// and the d/w extension (see internal/duration).
	TTL string `yaml:"ttl,omitempty"`
}

// LoadStrict is like Load but rejects unknown YAML fields, surfacing typos
// or leftover keys via the returned error. Default Load stays forgiving so
// older binaries can read configs that pick up new keys later. A missing
// file is an error here: strict callers want explicit feedback, not the
// silent empty-config default Load returns.
func LoadStrict(topLevel string) (*Config, error) {
	path := filepath.Join(topLevel, Filename)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var c Config
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", Filename, err)
	}
	return &c, nil
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
		if g.TTL != "" {
			if _, err := duration.Parse(g.TTL); err != nil {
				return fmt.Errorf("gates.%s.ttl: %w", k, err)
			}
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

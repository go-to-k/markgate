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
	"sort"
	"strings"

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
	// Composes / Requires list child gate keys this gate depends on.
	// composes (loose): parent is mismatch if any child is mismatch, but
	//   `set` of the parent is unconditional.
	// requires (strict): same propagation, plus `set` of the parent is
	//   refused if any required child is stale.
	// A gate may set at most one of the two; using both is a load error.
	Composes []string `yaml:"composes,omitempty"`
	Requires []string `yaml:"requires,omitempty"`
}

// HasDeps reports whether this gate has any composes or requires children.
func (g Gate) HasDeps() bool {
	return len(g.Composes) > 0 || len(g.Requires) > 0
}

// HasOwnScope reports whether the gate computes its own digest. Gates with
// dependencies but no explicit include omit the own-scope check so they
// don't inherit the git-tree default and become almost always stale.
func (g Gate) HasOwnScope() bool {
	if g.HasDeps() && len(g.Include) == 0 {
		return false
	}
	return true
}

// Children returns the union of composes and requires (in that order).
// At most one is set thanks to validation, so the order is deterministic.
func (g Gate) Children() []string {
	out := make([]string, 0, len(g.Composes)+len(g.Requires))
	out = append(out, g.Composes...)
	out = append(out, g.Requires...)
	return out
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
		if len(g.Composes) > 0 && len(g.Requires) > 0 {
			return fmt.Errorf("gates.%s: composes and requires cannot both be set", k)
		}
		for _, child := range g.Children() {
			if _, ok := c.Gates[child]; !ok {
				return fmt.Errorf("gates.%s: references undeclared gate %q", k, child)
			}
			if child == k {
				return fmt.Errorf("gates.%s: cycle detected (self-reference)", k)
			}
		}
	}
	if cycle := c.findCycle(); cycle != "" {
		return fmt.Errorf("gates: cycle detected (%s)", cycle)
	}
	return nil
}

// findCycle returns a human-readable cycle path (e.g. "a -> b -> a") if any
// composes/requires edges form one, or "" when the graph is acyclic.
// Iterative DFS keeps the stack bounded for arbitrarily deep configs.
func (c *Config) findCycle() string {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make(map[string]int, len(c.Gates))
	parent := make(map[string]string, len(c.Gates))

	keys := make([]string, 0, len(c.Gates))
	for k := range c.Gates {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	type frame struct {
		node string
		idx  int
		kids []string
	}

	for _, root := range keys {
		if color[root] != white {
			continue
		}
		stack := []frame{{node: root, idx: 0, kids: c.Gates[root].Children()}}
		color[root] = gray
		for len(stack) > 0 {
			top := &stack[len(stack)-1]
			if top.idx >= len(top.kids) {
				color[top.node] = black
				stack = stack[:len(stack)-1]
				continue
			}
			child := top.kids[top.idx]
			top.idx++
			switch color[child] {
			case white:
				parent[child] = top.node
				color[child] = gray
				stack = append(stack, frame{node: child, idx: 0, kids: c.Gates[child].Children()})
			case gray:
				return formatCycle(parent, top.node, child)
			}
		}
	}
	return ""
}

// formatCycle reconstructs the cycle path "child -> ... -> from -> child"
// from the DFS parent map for use in error messages.
func formatCycle(parent map[string]string, from, child string) string {
	rev := []string{from}
	for n := from; n != child; {
		p, ok := parent[n]
		if !ok {
			break
		}
		rev = append(rev, p)
		n = p
	}
	path := make([]string, 0, len(rev)+1)
	for i := len(rev) - 1; i >= 0; i-- {
		path = append(path, rev[i])
	}
	path = append(path, child)
	return strings.Join(path, " -> ")
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

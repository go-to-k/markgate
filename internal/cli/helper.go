package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/go-to-k/markgate/internal/config"
	"github.com/go-to-k/markgate/internal/duration"
	"github.com/go-to-k/markgate/internal/gitutil"
	"github.com/go-to-k/markgate/internal/hasher"
	"github.com/go-to-k/markgate/internal/key"
	"github.com/go-to-k/markgate/internal/state"
)

// now is the package's clock. Tests override it to advance time without
// waiting; production code MUST go through this indirection rather than
// calling time.Now directly.
var now = time.Now

// EnvStateDir overrides the directory that stores marker files.
// Precedence: --state-dir flag > this env > gate.StateDir in
// .markgate.yml > default (<git-dir>/markgate).
const EnvStateDir = "MARKGATE_STATE_DIR"

// DefaultKey is the key used when the user omits the positional argument.
const DefaultKey = "default"

// resolveKey returns args[0] when present, otherwise DefaultKey.
func resolveKey(args []string) string {
	if len(args) == 0 {
		return DefaultKey
	}
	return args[0]
}

// gateFlagValues holds CLI flags that can override the config-derived
// gate (hash/include/exclude) plus the marker storage directory, on a
// per-invocation basis.
type gateFlagValues struct {
	hash     string
	include  []string
	exclude  []string
	stateDir string
}

// addGateFlags registers --hash / --include / --exclude / --state-dir on
// cmd and returns a pointer whose fields are populated when RunE fires.
func addGateFlags(cmd *cobra.Command) *gateFlagValues {
	v := &gateFlagValues{}
	cmd.Flags().StringVar(&v.hash, "hash", "",
		"override hash type for this invocation: git-tree or files")
	cmd.Flags().StringArrayVar(&v.include, "include", nil,
		"glob to include (repeatable); overrides config include list")
	cmd.Flags().StringArrayVar(&v.exclude, "exclude", nil,
		"glob to exclude (repeatable); overrides config exclude list")
	cmd.Flags().StringVar(&v.stateDir, "state-dir", "",
		"directory to store marker files; overrides "+EnvStateDir+" env and state_dir: in .markgate.yml (default: <git-dir>/markgate)")
	return v
}

// override applies non-empty flag values on top of g.
func (v *gateFlagValues) override(g config.Gate) config.Gate {
	if v == nil {
		return g
	}
	if v.hash != "" {
		g.Hash = v.hash
	}
	if v.include != nil {
		g.Include = v.include
	}
	if v.exclude != nil {
		g.Exclude = v.exclude
	}
	return g
}

// gateCtx bundles the resolved context for a single gate key so subcommands
// can stay focused on their own logic.
type gateCtx struct {
	key        string
	repo       *gitutil.Repo
	topLevel   string
	gitDir     string
	gate       config.Gate
	hasher     hasher.Hasher
	markerPath string
	// cfg is retained so child gates referenced via composes/requires can be
	// resolved without re-loading .markgate.yml.
	cfg *config.Config
}

func newGateCtx(k string, overrides *gateFlagValues) (*gateCtx, error) {
	if err := key.Validate(k); err != nil {
		return nil, &ExitError{Code: 2, Err: err}
	}
	repo := gitutil.New("")
	top, err := repo.TopLevel()
	if err != nil {
		return nil, &ExitError{Code: 2, Err: err}
	}
	gitDir, err := repo.GitDir()
	if err != nil {
		return nil, &ExitError{Code: 2, Err: err}
	}
	cfg, err := config.Load(top)
	if err != nil {
		return nil, &ExitError{Code: 2, Err: err}
	}
	gate := overrides.override(cfg.Gate(k))
	if vErr := validateGate(gate); vErr != nil {
		return nil, &ExitError{Code: 2, Err: vErr}
	}
	h, err := hasher.For(gate)
	if err != nil {
		return nil, &ExitError{Code: 2, Err: err}
	}
	return &gateCtx{
		key:        k,
		repo:       repo,
		topLevel:   top,
		gitDir:     gitDir,
		gate:       gate,
		hasher:     h,
		markerPath: resolveMarkerPath(overrides, gate, top, gitDir, k),
		cfg:        cfg,
	}, nil
}

// child builds a gateCtx for a child gate referenced via composes/requires.
// Per-invocation overrides do NOT propagate to children — each child is
// resolved purely from .markgate.yml so its scope is what its own entry
// declares. State-dir override flags are also intentionally dropped: the
// child's storage location follows the child's own state_dir (or default).
func (c *gateCtx) child(k string) (*gateCtx, error) {
	if err := key.Validate(k); err != nil {
		return nil, err
	}
	gate := c.cfg.Gate(k)
	if vErr := validateGate(gate); vErr != nil {
		return nil, vErr
	}
	h, err := hasher.For(gate)
	if err != nil {
		return nil, err
	}
	return &gateCtx{
		key:        k,
		repo:       c.repo,
		topLevel:   c.topLevel,
		gitDir:     c.gitDir,
		gate:       gate,
		hasher:     h,
		markerPath: resolveMarkerPath(nil, gate, c.topLevel, c.gitDir, k),
		cfg:        c.cfg,
	}, nil
}

// evalResult is what evaluate returns to callers (verify, status, run).
// Reason is populated when matched is false so callers can render context.
// childKey, when set, names the first descendant whose mismatch caused this
// gate to fail — useful for #24's --explain output and for set-time
// requires-enforcement messaging.
type evalResult struct {
	matched  bool
	reason   string
	childKey string
}

// evaluate computes the recursive freshness verdict for c. It loads the
// marker, optionally compares the own-scope digest, and ANDs in every
// composes/requires child. Cycles are impossible here because config
// validation rejects them.
func (c *gateCtx) evaluate() (evalResult, error) {
	if c.gate.HasOwnScope() {
		m, err := state.Load(c.markerPath)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return evalResult{matched: false, reason: "no marker"}, nil
			}
			return evalResult{}, err
		}
		digest, err := c.hasher.Hash(c.repo)
		if err != nil {
			return evalResult{}, err
		}
		if m.HashType != c.hasher.Type() || m.Digest != digest {
			return evalResult{matched: false, reason: "own digest mismatch"}, nil
		}
	} else {
		// Deps-only gates still need an explicit set to count as fresh: a
		// brand-new gate with no marker must not pass on first verify just
		// because all its children happen to be fresh.
		if _, err := state.Load(c.markerPath); err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return evalResult{matched: false, reason: "no marker"}, nil
			}
			return evalResult{}, err
		}
	}
	for _, childKey := range c.gate.Children() {
		cc, err := c.child(childKey)
		if err != nil {
			return evalResult{}, err
		}
		res, err := cc.evaluate()
		if err != nil {
			return evalResult{}, err
		}
		if !res.matched {
			return evalResult{matched: false, reason: "child " + childKey + " is stale", childKey: childKey}, nil
		}
	}
	return evalResult{matched: true}, nil
}

// staleRequiredChild returns the key of the first direct requires child
// whose recursive evaluate is mismatch — for set-time enforcement.
// Returns "" when every required child is fresh.
func (c *gateCtx) staleRequiredChild() (string, error) {
	for _, k := range c.gate.Requires {
		cc, err := c.child(k)
		if err != nil {
			return "", err
		}
		res, err := cc.evaluate()
		if err != nil {
			return "", err
		}
		if !res.matched {
			return k, nil
		}
	}
	return "", nil
}

// resolveStateDir picks the marker storage directory based on precedence:
// --state-dir flag > MARKGATE_STATE_DIR env > gate.StateDir (from
// .markgate.yml) > default (<gitDir>/markgate). When an override is used,
// the "markgate" subdirectory is not injected: the user-specified
// directory is treated as the final storage location. Relative override
// paths resolve against the repo top-level so the location is stable
// across cwds (e.g. when invoked from a git hook).
func resolveStateDir(overrides *gateFlagValues, gate config.Gate, topLevel, gitDir string) string {
	dir := ""
	switch {
	case overrides != nil && overrides.stateDir != "":
		dir = overrides.stateDir
	case os.Getenv(EnvStateDir) != "":
		dir = os.Getenv(EnvStateDir)
	case gate.StateDir != "":
		dir = gate.StateDir
	}
	if dir == "" {
		return filepath.Join(gitDir, "markgate")
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(topLevel, dir)
	}
	return dir
}

// resolveMarkerPath returns the marker file path for key k. Thin wrapper
// over resolveStateDir so per-key callers and the bare-status walker
// share one precedence-resolution path (no lockstep invariant to
// maintain by hand).
func resolveMarkerPath(overrides *gateFlagValues, gate config.Gate, topLevel, gitDir, k string) string {
	return state.PathIn(resolveStateDir(overrides, gate, topLevel, gitDir), k)
}

// validateGate enforces the invariants that config.validate also enforces,
// so CLI overrides cannot construct an illegal gate.
func validateGate(g config.Gate) error {
	switch g.Hash {
	case "", config.HashGitTree:
		return nil
	case config.HashFiles:
		if len(g.Include) == 0 {
			return fmt.Errorf("hash=files requires --include or an include list in config")
		}
		return nil
	default:
		return fmt.Errorf("unknown hash type %q (want %q or %q)", g.Hash, config.HashGitTree, config.HashFiles)
	}
}

// ttlExpiry holds the verdict of a TTL check: when expired, age and ttl
// describe the offence (used in --explain-style messages and status
// output). When the gate has no TTL configured or the marker is fresh,
// expired is false and the other fields are zero.
type ttlExpiry struct {
	configured bool
	expired    bool
	ttl        time.Duration
	age        time.Duration
}

// checkTTL parses gate.TTL (if any) and compares it against the marker's
// age. Returns a non-nil error only on a malformed TTL string; that error
// path is unreachable when the marker came from a config that already
// passed config.Load's validation, but CLI overrides bypass that path so
// we still defend here.
func checkTTL(gate config.Gate, m *state.Marker) (ttlExpiry, error) {
	if gate.TTL == "" {
		return ttlExpiry{}, nil
	}
	ttl, err := duration.Parse(gate.TTL)
	if err != nil {
		return ttlExpiry{}, err
	}
	age := now().Sub(m.CreatedAt)
	return ttlExpiry{
		configured: true,
		expired:    age > ttl,
		ttl:        ttl,
		age:        age,
	}, nil
}

// formatAge renders d in the d/h/m/s shape used in TTL messages and
// status output (e.g. "8d3h", "4h7m", "12s"). The two largest non-zero
// components are kept; smaller ones are dropped to stay readable.
func formatAge(d time.Duration) string {
	if d < 0 {
		d = -d
	}
	days := d / (24 * time.Hour)
	d -= days * 24 * time.Hour
	hours := d / time.Hour
	d -= hours * time.Hour
	mins := d / time.Minute
	d -= mins * time.Minute
	secs := d / time.Second
	switch {
	case days > 0:
		if hours > 0 {
			return fmt.Sprintf("%dd%dh", days, hours)
		}
		return fmt.Sprintf("%dd", days)
	case hours > 0:
		if mins > 0 {
			return fmt.Sprintf("%dh%dm", hours, mins)
		}
		return fmt.Sprintf("%dh", hours)
	case mins > 0:
		if secs > 0 {
			return fmt.Sprintf("%dm%ds", mins, secs)
		}
		return fmt.Sprintf("%dm", mins)
	default:
		return fmt.Sprintf("%ds", secs)
	}
}

// newMarker computes the current digest and returns a marker ready to save.
// HEAD is recorded only for git-tree, to aid status output. CreatedAt is
// stamped here (via the package's now indirection) rather than left for
// state.Save to fill in, so tests that pin the clock for TTL coverage
// observe the pinned value. Deps-only gates (no own scope) get a sentinel
// marker so their freshness is purely a function of children but `set`
// still leaves a record that an explicit `markgate set <key>` happened.
func newMarker(c *gateCtx) (*state.Marker, error) {
	if !c.gate.HasOwnScope() {
		return &state.Marker{HashType: hashTypeDepsOnly}, nil
	}
	digest, err := c.hasher.Hash(c.repo)
	if err != nil {
		return nil, err
	}
	m := &state.Marker{
		HashType:  c.hasher.Type(),
		Digest:    digest,
		CreatedAt: now().UTC(),
	}
	if _, ok := c.hasher.(hasher.GitTree); ok {
		if head, err := c.repo.HeadSHA(); err == nil {
			m.Head = head
		}
	}
	return m, nil
}

// hashTypeDepsOnly is the sentinel HashType written for gates that have
// composes/requires but no include of their own. The digest field stays
// empty: there is nothing to hash. Status output recognises this value.
const hashTypeDepsOnly = "deps-only"

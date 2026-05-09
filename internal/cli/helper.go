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

// newGateCtxWithConfig builds a gateCtx from already-resolved
// components, skipping the config / git / hasher I/O newGateCtx does.
// Used by callers that walk multiple keys (bare `status`) to avoid
// re-loading .markgate.yml per row. The caller is responsible for
// applying overrides and validating the gate before calling.
func newGateCtxWithConfig(k string, gate config.Gate, h hasher.Hasher, repo *gitutil.Repo, top, gitDir, markerPath string, cfg *config.Config) *gateCtx {
	return &gateCtx{
		key:        k,
		repo:       repo,
		topLevel:   top,
		gitDir:     gitDir,
		gate:       gate,
		hasher:     h,
		markerPath: markerPath,
		cfg:        cfg,
	}
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
//
// matched is the freshness verdict (own scope ANDed with TTL ANDed
// with every recursive composes/requires child).
//
// reason / childKey explain why matched is false; childKey names the
// offending descendant for set-time requires enforcement and #24's
// --explain output.
//
// marker / digest / hashTypeChanged / ownDigestDiff / ttl carry the
// work evaluate already did so callers don't reload or re-hash. marker
// is nil when no marker exists. digest is empty when the gate has no
// own scope. hashTypeChanged and ownDigestDiff are only meaningful
// when marker is non-nil. ttl is populated whenever the gate has a
// TTL configured (regardless of whether it's expired) so status can
// render "expires in 4d" / "expired 1d ago" notes from one source.
type evalResult struct {
	matched         bool
	reason          string
	childKey        string
	marker          *state.Marker
	digest          string
	hashTypeChanged bool
	ownDigestDiff   bool
	ttl             ttlExpiry
}

// evaluate computes the recursive freshness verdict for c. It loads the
// marker, optionally compares the own-scope digest, applies any TTL,
// and ANDs in every composes/requires child. Cycles are impossible
// here because config validation rejects them. The result carries the
// loaded marker, computed digest, and TTL details so callers don't
// repeat the work.
//
// TTL applies to every gate with `ttl:` set (own-scope or deps-only):
// it caps the marker's wall-clock age. Because evaluate recurses, a
// child's expired TTL propagates up — the parent's evaluate will
// receive matched=false from the child and bubble it.
func (c *gateCtx) evaluate() (evalResult, error) {
	m, err := state.Load(c.markerPath)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return evalResult{reason: "no marker"}, nil
		}
		return evalResult{}, err
	}
	res := evalResult{marker: m}
	expectedKind := state.KindHash
	if !c.gate.HasOwnScope() {
		expectedKind = state.KindDepsOnly
	}
	if m.Kind != expectedKind {
		// Gate flipped between own-scope and deps-only since last set;
		// the marker is from a different freshness model. Treat it as
		// stale so the next set rewrites it under the current model.
		res.hashTypeChanged = true
		res.reason = "marker kind changed"
		return res, nil
	}
	if c.gate.HasOwnScope() {
		digest, hashErr := c.hasher.Hash(c.repo)
		if hashErr != nil {
			return evalResult{}, hashErr
		}
		res.digest = digest
		res.hashTypeChanged = m.HashType != c.hasher.Type()
		res.ownDigestDiff = m.Digest != digest
		if res.hashTypeChanged || res.ownDigestDiff {
			res.reason = "own digest mismatch"
			return res, nil
		}
	}
	if c.gate.TTL != "" {
		ttl, ttlErr := checkTTL(c.gate, m)
		if ttlErr != nil {
			return evalResult{}, ttlErr
		}
		res.ttl = ttl
		if ttl.expired {
			res.reason = "expired by ttl"
			return res, nil
		}
	}
	// Deps-only path falls through with marker loaded but no digest
	// work — the marker's mere presence is what proves an explicit set
	// happened (otherwise a brand-new deps-only gate would pass on first
	// verify just because its children happen to be fresh).
	for _, childKey := range c.gate.Children() {
		cc, ccErr := c.child(childKey)
		if ccErr != nil {
			return evalResult{}, ccErr
		}
		childRes, childErr := cc.evaluate()
		if childErr != nil {
			return evalResult{}, childErr
		}
		if !childRes.matched {
			res.reason = "child " + childKey + " is stale"
			res.childKey = childKey
			return res, nil
		}
	}
	res.matched = true
	return res, nil
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
// observe the pinned value. Deps-only gates (no own scope) get a marker
// tagged Kind=KindDepsOnly with no hash_type/digest: their freshness is
// purely a function of children, but `set` still leaves a record that an
// explicit `markgate set <key>` happened.
func newMarker(c *gateCtx) (*state.Marker, error) {
	if !c.gate.HasOwnScope() {
		return &state.Marker{Kind: state.KindDepsOnly, CreatedAt: now().UTC()}, nil
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

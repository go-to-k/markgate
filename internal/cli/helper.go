package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
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
	base     string
	stateDir string
}

// addGateFlags registers --hash / --include / --exclude / --base /
// --state-dir on cmd and returns a pointer whose fields are populated
// when RunE fires.
func addGateFlags(cmd *cobra.Command) *gateFlagValues {
	v := &gateFlagValues{}
	cmd.Flags().StringVar(&v.hash, "hash", "",
		"override hash type for this invocation: git-tree, files or diff")
	cmd.Flags().StringArrayVar(&v.include, "include", nil,
		"glob to include (repeatable); overrides config include list")
	cmd.Flags().StringArrayVar(&v.exclude, "exclude", nil,
		"glob to exclude (repeatable); overrides config exclude list")
	cmd.Flags().StringVar(&v.base, "base", "",
		"base ref a hash=diff gate measures its delta from (e.g. origin/main)")
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
	if v.base != "" {
		g.Base = v.base
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

// clearTarget is everything `clear` needs and nothing more: which
// marker file to remove.
type clearTarget struct {
	key        string
	markerPath string
}

// newClearTarget resolves the marker path without validating the config.
//
// `clear` deletes a file; its only config dependency is state_dir. The
// whole-document validation every other command inherits from
// newGateCtx is an incidental coupling here, and an expensive one: it
// makes the recovery path unusable in exactly the situation it exists
// for, leaving hand-editing YAML as the only way to remove a marker —
// including markers for gates that are perfectly fine.
//
// Validation is skipped, not weakened. Every command that resolves a
// hasher still goes through Load and still refuses a bad config, so a
// typo is still caught the next time anything is actually gated.
func newClearTarget(k string, overrides *gateFlagValues, errOut io.Writer) (*clearTarget, error) {
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

	var gate config.Gate
	cfg, parseErr := config.Parse(top)
	switch {
	case parseErr != nil:
		// Unparseable: state_dir is unknowable, so say which location is
		// being used instead of reporting a removal that may have
		// happened somewhere else.
		fmt.Fprintf(errOut, "markgate: %s could not be read (%v); using the default marker location\n",
			config.Filename, parseErr)
	default:
		gate = cfg.Gate(k)
		// Clearing must not double as a way to stop noticing the errors.
		if findings := cfg.Validate(); len(findings) > 0 {
			fmt.Fprintf(errOut, "markgate: %s still has errors (%s); clearing anyway\n",
				config.Filename, findings[0].Message)
		}
	}

	return &clearTarget{
		key:        k,
		markerPath: resolveMarkerPath(overrides, gate, top, gitDir, k),
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
	// deadScope carries hasher.ErrDeadScope when the gate's include list
	// can no longer match anything. It is a mismatch rather than an
	// error so `run` still executes the command that would refill the
	// scope; only `set` refuses, and it does so from newMarker.
	deadScope error
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
			// Nothing to compare against, so no digest gets computed on
			// this path and the hasher's preconditions would go
			// unchecked — leaving `run` to execute its child first and
			// refuse to record the result afterwards.
			if pErr := c.preflight(); pErr != nil {
				return evalResult{}, pErr
			}
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
		// Same reasoning as the no-marker path: this returns without
		// hashing, so preconditions need their own check.
		if pErr := c.preflight(); pErr != nil {
			return evalResult{}, pErr
		}
		res.hashTypeChanged = true
		res.reason = "marker kind changed"
		return res, nil
	}
	if c.gate.HasOwnScope() {
		digest, hashErr := c.hasher.Hash(c.repo)
		if errors.Is(hashErr, hasher.ErrDeadScope) {
			// Not fresh — that is the bug this closes — but not fatal
			// either. A gate on build output is legitimately empty
			// between `make clean` and the next build, and erroring here
			// would stop `run` from executing the command that refills
			// it, leaving the gate recoverable only via `clear`.
			res.deadScope = hashErr
			res.reason = "dead scope"
			return res, nil
		}
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

// preflight surfaces hasher preconditions that must fail loudly even
// when no marker exists yet. Without it a diff gate with an unusable
// base would report a plain mismatch, `run` would execute its (by
// assumption expensive) child, and only the closing `set` would refuse —
// after the cost was already paid.
//
// Callers invoke it ONLY on paths that return without hashing: when a
// digest is computed, Hash reports the same errors, and running both
// would double every diff gate's git work on the hot path.
func (c *gateCtx) preflight() error {
	if !c.gate.HasOwnScope() {
		// Freshness is purely the AND of children; the hasher is never
		// consulted, so its preconditions are not this gate's business.
		return nil
	}
	d, ok := c.hasher.(hasher.Diff)
	if !ok {
		return nil
	}
	return d.Preflight(c.repo)
}

// warnEmptyDiffScope reports a diff gate recording a marker whose
// in-scope delta is empty. That digest is a constant: legitimate when
// the branch genuinely changes nothing the gate covers (the case this
// hash type exists to keep fresh), and the signature of a typo'd
// include otherwise. Refusing would make the gate unusable on branches
// it should trivially pass, so the compromise is that it is never
// silent — the same class of mistake that made a cwd-scoped delta look
// like a permanently fresh marker.
func warnEmptyDiffScope(c *gateCtx, errOut io.Writer) {
	d, ok := c.hasher.(hasher.Diff)
	if !ok {
		return
	}
	scope, err := d.Scope(c.repo)
	if err != nil || len(scope) > 0 {
		return
	}
	patterns := "(everything)"
	if len(c.gate.Include) > 0 {
		patterns = strings.Join(c.gate.Include, ", ")
	}
	fmt.Fprintf(errOut,
		"markgate: %s: hash=diff recorded an empty in-scope delta (include: %s); this branch changes nothing the gate covers, so the marker stays fresh until it does\n",
		c.key, patterns)
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
	if g.Base != "" && g.Hash != config.HashDiff {
		return fmt.Errorf("base is only valid with hash=diff (got hash=%q)", g.Hash)
	}
	switch g.Hash {
	case "", config.HashGitTree:
		return nil
	case config.HashFiles:
		if len(g.Include) == 0 {
			return fmt.Errorf("hash=files requires --include or an include list in config")
		}
		return nil
	case config.HashDiff:
		// Mirrors config.validateGate: a deps-only gate never consults a
		// hasher, so hash and base would be silently discarded.
		if !g.HasOwnScope() {
			return fmt.Errorf("hash=diff requires its own scope; add --include, or drop --hash/--base (composes/requires without include makes the gate deps-only)")
		}
		if g.Base == "" {
			return fmt.Errorf("hash=diff requires --base or a base: in config")
		}
		return nil
	default:
		return fmt.Errorf("unknown hash type %q (want %q, %q or %q)", g.Hash, config.HashGitTree, config.HashFiles, config.HashDiff)
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
// HEAD is recorded only for git-tree, and base / merge base only for
// diff, to aid status output. CreatedAt is
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
	if d, ok := c.hasher.(hasher.Diff); ok {
		// Recorded for diagnostics only. Neither field is compared at
		// verify time — a moved merge base is exactly what this hash
		// type is built to tolerate.
		m.Base = d.Base
		if mergeBase, err := d.MergeBase(c.repo); err == nil {
			m.MergeBase = mergeBase
		}
	}
	return m, nil
}

package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/go-to-k/markgate/internal/config"
	"github.com/go-to-k/markgate/internal/gitutil"
	"github.com/go-to-k/markgate/internal/hasher"
	"github.com/go-to-k/markgate/internal/key"
	"github.com/go-to-k/markgate/internal/state"
)

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
// gate on a per-invocation basis.
type gateFlagValues struct {
	hash    string
	include []string
	exclude []string
}

// addGateFlags registers --hash / --include / --exclude on cmd and
// returns a pointer whose fields are populated when RunE fires.
func addGateFlags(cmd *cobra.Command) *gateFlagValues {
	v := &gateFlagValues{}
	cmd.Flags().StringVar(&v.hash, "hash", "",
		"override hash type for this invocation: git-tree or files")
	cmd.Flags().StringArrayVar(&v.include, "include", nil,
		"glob to include (repeatable); overrides config include list")
	cmd.Flags().StringArrayVar(&v.exclude, "exclude", nil,
		"glob to exclude (repeatable); overrides config exclude list")
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
		markerPath: state.Path(gitDir, k),
	}, nil
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

// newMarker computes the current digest and returns a marker ready to save.
// HEAD is recorded only for git-tree, to aid status output.
func newMarker(c *gateCtx) (*state.Marker, error) {
	digest, err := c.hasher.Hash(c.repo)
	if err != nil {
		return nil, err
	}
	m := &state.Marker{
		HashType: c.hasher.Type(),
		Digest:   digest,
	}
	if _, ok := c.hasher.(hasher.GitTree); ok {
		if head, err := c.repo.HeadSHA(); err == nil {
			m.Head = head
		}
	}
	return m, nil
}

package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/go-to-k/markgate/internal/config"
	"github.com/go-to-k/markgate/internal/gitutil"
)

const initSkeleton = `# markgate configuration - https://github.com/go-to-k/markgate
# This file is optional. Zero-config (hash: git-tree) is the default.
# Define a gate here only when you want:
#   - exclude patterns on the default git-tree hash, or
#   - a narrow-scope (hash: files) gate for docs / Docker / coverage, or
#   - a branch-delta (hash: diff) gate that ignores base-branch churn, or
#   - a non-default marker storage directory (state_dir) for sharing
#     markers across machines / CI.

gates:
  # Default gate (used when ` + "`markgate verify`" + ` runs without a key).
  default:
    hash: git-tree
    # exclude:
    #   - "vendor/**"
    #   - "node_modules/**"
    #
    # An include list where no pattern matches any file in the working
    # tree is a run-time error, not an empty scope: the digest would be
    # a constant and the gate could never block. "markgate config lint"
    # warns per pattern, so run it after renaming a directory.
    #
    # state_dir controls where the marker file is written. Prefer
    # relative paths (resolved against the repo top-level) so every
    # machine agrees on the location. Two patterns:
    #
    #   Pattern A: not committed (e.g. restored from CI cache).
    #     state_dir: .markgate-cache
    #     -> gitignore .markgate-cache/ (required if you stay on
    #        hash: git-tree or on an unscoped hash: diff gate,
    #        optional hygiene for hash: files).
    #
    #   Pattern B: committed to git for zero-infra local->CI sharing.
    #     state_dir: .markgate-state
    #     -> requires a scoped hash: files or hash: diff gate whose
    #        include/exclude keeps the state dir out of scope
    #        (git-tree would break: the commit changes HEAD and stales
    #        the marker it just wrote).
    #
    # See README "Sharing markers" for the full picture.

  # Example: narrow-scope gate for PR-time docs checks.
  # pre-pr:
  #   hash: files
  #   include:
  #     - "docs/**"
  #     - "README.md"

  # Example: ignore base-branch changes to files this branch has not
  # touched (hash: diff). The digest covers only the delta against
  # merge-base(base, HEAD), so pulling an unrelated change from the base
  # branch keeps the marker fresh. Requires base:, and errors when the
  # delta is empty (e.g. on the base branch itself).
  #
  # This is the LEAST STRICT hash type: a base-branch change your branch
  # never touched is trusted without re-verification, so a combination
  # that was never verified together can read as fresh. Pair it with
  # ttl:, and only use it where the base branch runs the same gate.
  # See README "Hashing strategies" before adopting it.
  # integ:
  #   hash: diff
  #   base: origin/main
  #   ttl: 14d
  #   include:
  #     - "src/**"

  # Example: wall-clock expiry for gates that verify external state
  # (cloud APIs, vuln DB, ...). Units: s/m/h/d/w (m is minutes, not
  # months; mo and y are intentionally rejected).
  # integ-destroy:
  #   hash: git-tree
  #   ttl: 7d
`

func newInitCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Generate a starter .markgate.yml at the repo root",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			repo := gitutil.New("")
			top, err := repo.TopLevel()
			if err != nil {
				return &ExitError{Code: 2, Err: err}
			}
			path := filepath.Join(top, config.Filename)
			switch _, statErr := os.Stat(path); {
			case statErr == nil:
				if !force {
					return &ExitError{Code: 2, Err: fmt.Errorf("%s already exists (use --force to overwrite)", config.Filename)}
				}
			case errors.Is(statErr, os.ErrNotExist):
				// ok, we will create it
			default:
				return &ExitError{Code: 2, Err: statErr}
			}
			// 0o644 so teammates can also read the config; G306 does not apply here.
			if err := os.WriteFile(path, []byte(initSkeleton), 0o644); err != nil { //nolint:gosec // G306
				return &ExitError{Code: 2, Err: err}
			}
			fmt.Fprintf(cmd.OutOrStdout(), "wrote: %s\n", path)
			return nil
		},
	}
	cmd.Flags().BoolVarP(&force, "force", "f", false, "overwrite existing .markgate.yml")
	return cmd
}

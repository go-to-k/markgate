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
#   - a narrow-scope (hash: files) gate for docs / Docker / coverage.

gates:
  # Default gate (used when ` + "`markgate verify`" + ` runs without a key).
  default:
    hash: git-tree
    # exclude:
    #   - "vendor/**"
    #   - "node_modules/**"

  # Example: narrow-scope gate for PR-time docs checks.
  # pre-pr:
  #   hash: files
  #   include:
  #     - "docs/**"
  #     - "README.md"
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

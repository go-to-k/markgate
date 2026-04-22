// Package cli wires the markgate command tree.
package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// ExitError lets subcommands return a specific process exit code.
type ExitError struct {
	Code int
	Err  error
}

func (e *ExitError) Error() string {
	if e.Err == nil {
		return fmt.Sprintf("exit %d", e.Code)
	}
	return e.Err.Error()
}

// Execute runs the root command. It never returns; the process exits with
// the appropriate code (0 verified, 1 not verified, 2 error).
func Execute(rawVersion string) {
	resolved := resolveVersion(rawVersion)
	root := newRootCmd(resolved)
	err := root.Execute()
	if err == nil {
		os.Exit(0)
	}
	var exitErr *ExitError
	if errors.As(err, &exitErr) {
		if exitErr.Err != nil {
			fmt.Fprintln(os.Stderr, "markgate:", exitErr.Err)
		}
		os.Exit(exitErr.Code)
	}
	fmt.Fprintln(os.Stderr, "markgate:", err)
	os.Exit(2)
}

func newRootCmd(version string) *cobra.Command {
	cmd := &cobra.Command{
		Use:           "markgate",
		Short:         "State-cached gate primitive for hook managers",
		Long:          "markgate records a hash of the repository state after verification steps succeed, then checks it quickly before subsequent actions (for example, git commit). It is a primitive to compose with hook managers, not a hook manager itself.",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.SetVersionTemplate("{{.Version}}\n")
	cmd.AddCommand(
		newSetCmd(),
		newVerifyCmd(),
		newStatusCmd(),
		newClearCmd(),
		newRunCmd(),
		newVersionCmd(),
	)
	return cmd
}

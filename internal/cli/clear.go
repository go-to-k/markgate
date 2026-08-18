package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/go-to-k/markgate/internal/state"
)

func newClearCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "clear [key]",
		Short: "Remove the marker (idempotent)",
		Long: "Remove the marker for [key] (default: \"default\"). Idempotent:\n" +
			"removing a marker that is not there succeeds.\n\n" +
			"clear does not require .markgate.yml to be valid. It only needs to\n" +
			"know where the marker lives, so a config error elsewhere must not\n" +
			"stop you cleaning up — every other command still refuses a bad\n" +
			"config. Errors are reported on stderr so clearing never hides them.",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: gateKeyCompletion,
	}
	overrides := addGateFlags(cmd)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		c, err := newClearTarget(resolveKey(args), overrides, cmd.ErrOrStderr())
		if err != nil {
			return err
		}
		if err := state.Remove(c.markerPath); err != nil {
			return &ExitError{Code: 2, Err: err}
		}
		fmt.Fprintf(cmd.OutOrStdout(), "cleared: %s\n", c.key)
		return nil
	}
	return cmd
}

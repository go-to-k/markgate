package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/go-to-k/markgate/internal/state"
)

func newClearCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "clear <key>",
		Short: "Remove the marker for <key> (idempotent)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newGateCtx(args[0])
			if err != nil {
				return err
			}
			if err := state.Remove(c.markerPath); err != nil {
				return &ExitError{Code: 2, Err: err}
			}
			fmt.Fprintf(cmd.OutOrStdout(), "cleared: %s\n", c.key)
			return nil
		},
	}
}

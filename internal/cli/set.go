package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/go-to-k/markgate/internal/state"
)

func newSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set [key]",
		Short: "Record the current state hash as the marker (default key: \"default\")",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newGateCtx(resolveKey(args))
			if err != nil {
				return err
			}
			m, err := newMarker(c)
			if err != nil {
				return &ExitError{Code: 2, Err: err}
			}
			if err := state.Save(c.markerPath, m); err != nil {
				return &ExitError{Code: 2, Err: err}
			}
			fmt.Fprintf(cmd.OutOrStdout(), "marker saved: %s\n", c.key)
			return nil
		},
	}
}

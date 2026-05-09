package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/go-to-k/markgate/internal/state"
)

func newSetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:               "set [key]",
		Short:             "Record the current state hash as the marker (default key: \"default\")",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: gateKeyCompletion,
	}
	overrides := addGateFlags(cmd)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		c, err := newGateCtx(resolveKey(args), overrides)
		if err != nil {
			return err
		}
		if stale, sErr := c.staleRequiredChild(); sErr != nil {
			return &ExitError{Code: 2, Err: sErr}
		} else if stale != "" {
			return &ExitError{Code: 2, Err: fmt.Errorf("set %s: required child %q is stale", c.key, stale)}
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
	}
	return cmd
}

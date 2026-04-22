package cli

import (
	"errors"

	"github.com/spf13/cobra"

	"github.com/go-to-k/markgate/internal/state"
)

func newVerifyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "verify <key>",
		Short: "Check the current state against the marker (exit 0 match, 1 mismatch, 2 error)",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			c, err := newGateCtx(args[0])
			if err != nil {
				return err
			}
			m, err := state.Load(c.markerPath)
			if err != nil {
				if errors.Is(err, state.ErrNotFound) {
					return &ExitError{Code: 1}
				}
				return &ExitError{Code: 2, Err: err}
			}
			digest, err := c.hasher.Hash(c.repo)
			if err != nil {
				return &ExitError{Code: 2, Err: err}
			}
			if m.HashType != c.hasher.Type() || m.Digest != digest {
				return &ExitError{Code: 1}
			}
			return nil
		},
	}
}

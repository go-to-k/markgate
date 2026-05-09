package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/go-to-k/markgate/internal/state"
)

func newVerifyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:               "verify [key]",
		Short:             "Check current state against the marker (exit 0 match, 1 mismatch, 2 error)",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: gateKeyCompletion,
	}
	overrides := addGateFlags(cmd)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		c, err := newGateCtx(resolveKey(args), overrides)
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
		ttl, err := checkTTL(c.gate, m)
		if err != nil {
			return &ExitError{Code: 2, Err: err}
		}
		if ttl.expired {
			fmt.Fprintf(cmd.ErrOrStderr(),
				"markgate: state mismatch (expired by ttl: %s, marker is %s old)\n",
				c.gate.TTL, formatAge(ttl.age))
			return &ExitError{Code: 1}
		}
		return nil
	}
	return cmd
}

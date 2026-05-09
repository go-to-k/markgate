package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newVerifyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:               "verify [key]",
		Short:             "Check current state against the marker (exit 0 match, 1 mismatch, 2 error)",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: gateKeyCompletion,
	}
	overrides := addGateFlags(cmd)
	explain := addExplainFlags(cmd)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if err := explain.validate(); err != nil {
			return &ExitError{Code: 2, Err: err}
		}
		c, err := newGateCtx(resolveKey(args), overrides)
		if err != nil {
			return err
		}
		label, matched, m, err := explainStateForVerify(c)
		if err != nil {
			return &ExitError{Code: 2, Err: err}
		}
		if matched {
			ttl, ttlErr := checkTTL(c.gate, m)
			if ttlErr != nil {
				return &ExitError{Code: 2, Err: ttlErr}
			}
			if ttl.expired {
				if emitErr := emitExplain(c, explain, cmd.OutOrStdout(), cmd.ErrOrStderr(), stateMismatch); emitErr != nil {
					return &ExitError{Code: 2, Err: emitErr}
				}
				fmt.Fprintf(cmd.ErrOrStderr(),
					"markgate: state mismatch (expired by ttl: %s, marker is %s old)\n",
					c.gate.TTL, formatAge(ttl.age))
				return &ExitError{Code: 1}
			}
		}
		if emitErr := emitExplain(c, explain, cmd.OutOrStdout(), cmd.ErrOrStderr(), label); emitErr != nil {
			return &ExitError{Code: 2, Err: emitErr}
		}
		if !matched {
			return &ExitError{Code: 1}
		}
		return nil
	}
	return cmd
}

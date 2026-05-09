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
	explain := addExplainFlags(cmd)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if err := explain.validate(); err != nil {
			return &ExitError{Code: 2, Err: err}
		}
		c, err := newGateCtx(resolveKey(args), overrides)
		if err != nil {
			return err
		}

		res, evalErr := c.evaluate()
		if evalErr != nil {
			return &ExitError{Code: 2, Err: evalErr}
		}

		// TTL is surfaced only at the parent level: a fresh-by-digest parent
		// can still mismatch when its own marker is older than gate.TTL.
		// TODO(#28+#29): TTL propagation through composes chain.
		var ttlMessage string
		if res.matched {
			m, loadErr := state.Load(c.markerPath)
			if loadErr != nil && !errors.Is(loadErr, state.ErrNotFound) {
				return &ExitError{Code: 2, Err: loadErr}
			}
			if m != nil {
				ttl, ttlErr := checkTTL(c.gate, m)
				if ttlErr != nil {
					return &ExitError{Code: 2, Err: ttlErr}
				}
				if ttl.expired {
					res.matched = false
					ttlMessage = fmt.Sprintf(
						"markgate: state mismatch (expired by ttl: %s, marker is %s old)\n",
						c.gate.TTL, formatAge(ttl.age))
				}
			}
		}

		label := stateLabel(res)
		if emitErr := emitExplain(c, explain, cmd.OutOrStdout(), cmd.ErrOrStderr(), label); emitErr != nil {
			return &ExitError{Code: 2, Err: emitErr}
		}
		if ttlMessage != "" {
			fmt.Fprint(cmd.ErrOrStderr(), ttlMessage)
		}
		if !res.matched {
			return &ExitError{Code: 1}
		}
		return nil
	}
	return cmd
}

// stateLabel maps an evalResult to the explain-vocabulary label.
// Reasons starting with "no marker" are mapped to stateNoMarker so --explain
// reports the same "no marker" string as before; everything else collapses
// to stateMismatch / stateMatch.
func stateLabel(res evalResult) string {
	if res.matched {
		return stateMatch
	}
	if res.reason == "no marker" {
		return stateNoMarker
	}
	return stateMismatch
}

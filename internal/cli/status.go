package cli

import (
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/go-to-k/markgate/internal/state"
)

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status <key>",
		Short: "Show marker information and freshness for <key>",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newGateCtx(args[0])
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()

			m, err := state.Load(c.markerPath)
			if err != nil {
				if errors.Is(err, state.ErrNotFound) {
					fmt.Fprintf(out, "key:        %s\nstate:      no marker\n", c.key)
					return &ExitError{Code: 1}
				}
				return &ExitError{Code: 2, Err: err}
			}

			digest, err := c.hasher.Hash(c.repo)
			if err != nil {
				return &ExitError{Code: 2, Err: err}
			}

			fmt.Fprintf(out, "key:        %s\n", c.key)
			fmt.Fprintf(out, "hash type:  %s\n", m.HashType)
			fmt.Fprintf(out, "created:    %s\n", m.CreatedAt.Format(time.RFC3339))
			if m.Head != "" {
				fmt.Fprintf(out, "head:       %s\n", m.Head)
			}

			switch {
			case m.HashType != c.hasher.Type():
				fmt.Fprintf(out, "state:      mismatch (hash type changed: %s -> %s)\n", m.HashType, c.hasher.Type())
				return &ExitError{Code: 1}
			case m.Digest != digest:
				fmt.Fprintln(out, "state:      mismatch (digest differs)")
				return &ExitError{Code: 1}
			default:
				fmt.Fprintln(out, "state:      match")
				return nil
			}
		},
	}
}

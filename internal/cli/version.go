package cli

import (
	"fmt"
	"runtime/debug"

	"github.com/spf13/cobra"
)

// resolveVersion picks the best available version string.
//
// Order:
//  1. ldflags-injected value (anything other than "dev")
//  2. runtime/debug.ReadBuildInfo Main.Version (set by "go install module@tag")
//  3. "dev"
func resolveVersion(injected string) string {
	if injected != "dev" && injected != "" {
		return injected
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		v := info.Main.Version
		if v != "" && v != "(devel)" {
			return v
		}
	}
	return "dev"
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the markgate version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), cmd.Root().Version)
			return nil
		},
	}
}

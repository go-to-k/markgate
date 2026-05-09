package cli

import (
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/go-to-k/markgate/internal/config"
	"github.com/go-to-k/markgate/internal/gitutil"
)

func newCompletionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "completion [bash|zsh|fish|powershell]",
		Short: "Generate shell completion script",
		Long: "Output a shell completion script for the chosen shell.\n\n" +
			"Bash:\n" +
			"  source <(markgate completion bash)\n" +
			"  # persist by writing to /etc/bash_completion.d/markgate or ~/.local/share/bash-completion/completions/markgate\n\n" +
			"Zsh:\n" +
			"  markgate completion zsh > \"${fpath[1]}/_markgate\"\n" +
			"  # ensure 'autoload -Uz compinit && compinit' runs in your zshrc\n\n" +
			"Fish:\n" +
			"  markgate completion fish > ~/.config/fish/completions/markgate.fish\n\n" +
			"PowerShell:\n" +
			"  markgate completion powershell | Out-String | Invoke-Expression\n\n" +
			"Gate-key positions on set / verify / status / clear / run complete from\n" +
			"the gates: map in .markgate.yml at the repo top-level.",
		Args:                  cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
		ValidArgs:             []string{"bash", "zsh", "fish", "powershell"},
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			root := cmd.Root()
			switch args[0] {
			case "bash":
				return root.GenBashCompletionV2(out, true)
			case "zsh":
				return root.GenZshCompletion(out)
			case "fish":
				return root.GenFishCompletion(out, true)
			case "powershell":
				return root.GenPowerShellCompletionWithDesc(out)
			default:
				return &ExitError{Code: 2, Err: fmt.Errorf("unsupported shell %q", args[0])}
			}
		},
	}
	return cmd
}

// gateKeyCompletion returns a cobra.ValidArgsFunction that completes the
// optional [key] positional from the gates: map in .markgate.yml. Source is
// config-only by design: a TAB press must not poke the marker directory or
// run anything beyond a cheap config read. Missing config silently yields no
// suggestions so completion never breaks zero-config usage.
func gateKeyCompletion(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	top, err := gitutil.New("").TopLevel()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	cfg, err := config.Load(top)
	if err != nil || cfg == nil || len(cfg.Gates) == 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	keys := make([]string, 0, len(cfg.Gates))
	for k := range cfg.Gates {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys, cobra.ShellCompDirectiveNoFileComp
}

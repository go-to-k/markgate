package cli

import (
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/go-to-k/markgate/internal/state"
)

func newRunCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run [key] -- <cmd> [args...]",
		Short: "Sugar for verify + set: verify; on mismatch run <cmd>; on success set the marker",
		Long: "run combines `markgate verify` and `markgate set` into a single invocation,\n" +
			"wedging <cmd> in between. It is sugar for:\n\n" +
			"  markgate verify [key] || ( <cmd> && markgate set [key] )\n\n" +
			"If [key] is omitted, the default key is used. Arguments after `--`\n" +
			"are executed verbatim (no shell interpretation).",
		Args: cobra.MinimumNArgs(1),
	}
	overrides := addGateFlags(cmd)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		return runE(cmd, args, overrides)
	}
	return cmd
}

func runE(cmd *cobra.Command, args []string, overrides *gateFlagValues) error {
	dash := cmd.ArgsLenAtDash()
	if dash < 0 {
		return &ExitError{Code: 2, Err: errors.New("run: '--' separator before the command is required")}
	}
	if dash > 1 {
		return &ExitError{Code: 2, Err: errors.New("run: at most one [key] may precede '--'")}
	}
	keyArg := DefaultKey
	if dash == 1 {
		keyArg = args[0]
	}
	cmdArgs := args[dash:]
	if len(cmdArgs) == 0 {
		return &ExitError{Code: 2, Err: errors.New("run: a command after '--' is required")}
	}

	c, err := newGateCtx(keyArg, overrides)
	if err != nil {
		return err
	}

	// Verify first; on match, skip execution entirely.
	m, loadErr := state.Load(c.markerPath)
	switch {
	case loadErr == nil:
		digest, hashErr := c.hasher.Hash(c.repo)
		if hashErr != nil {
			return &ExitError{Code: 2, Err: hashErr}
		}
		if m.HashType == c.hasher.Type() && m.Digest == digest {
			return nil
		}
	case errors.Is(loadErr, state.ErrNotFound):
		// fall through to execution
	default:
		return &ExitError{Code: 2, Err: loadErr}
	}

	code, execErr := execChild(cmdArgs)
	if execErr != nil {
		return &ExitError{Code: 2, Err: execErr}
	}
	if code != 0 {
		return &ExitError{Code: code}
	}

	// Success: record a fresh marker.
	newM, err := newMarker(c)
	if err != nil {
		return &ExitError{Code: 2, Err: err}
	}
	if err := state.Save(c.markerPath, newM); err != nil {
		return &ExitError{Code: 2, Err: err}
	}
	return nil
}

// execChild runs argv with stdio pass-through and forwards SIGINT/SIGTERM
// to the child. It returns the child's exit code, or a non-nil error if
// the process could not be started.
func execChild(argv []string) (int, error) {
	// User-supplied command is the whole point of `markgate run`;
	// passing it to exec.Command is intentional.
	child := exec.Command(argv[0], argv[1:]...) //nolint:gosec // G204
	child.Stdin = os.Stdin
	child.Stdout = os.Stdout
	child.Stderr = os.Stderr

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	if err := child.Start(); err != nil {
		return 0, err
	}

	done := make(chan struct{})
	go func() {
		for {
			select {
			case sig := <-sigCh:
				if child.Process != nil {
					_ = child.Process.Signal(sig)
				}
			case <-done:
				return
			}
		}
	}()

	waitErr := child.Wait()
	close(done)

	if waitErr == nil {
		return 0, nil
	}
	var ee *exec.ExitError
	if errors.As(waitErr, &ee) {
		return ee.ExitCode(), nil
	}
	return 0, waitErr
}

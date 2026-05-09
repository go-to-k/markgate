package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/go-to-k/markgate/internal/state"
)

// State labels for the --explain output. Match exactly across text and
// JSON modes so callers parsing either form see the same vocabulary.
const (
	stateMatch    = "match"
	stateMismatch = "mismatch"
	stateNoMarker = "no marker"
)

// explainFlags holds the values populated by addExplainFlags. nil-safe:
// commands that did not register the flags pass nil and get default
// (no-op) behavior.
type explainFlags struct {
	enabled bool
	json    bool
}

// addExplainFlags registers --explain/-e and --json on cmd. --json is a
// modifier on --explain; passing --json without --explain is a usage
// error reported at run time.
func addExplainFlags(cmd *cobra.Command) *explainFlags {
	v := &explainFlags{}
	cmd.Flags().BoolVarP(&v.enabled, "explain", "e", false,
		"list files in the current gate scope (to stderr)")
	cmd.Flags().BoolVar(&v.json, "json", false,
		"with --explain: emit a single JSON object on stdout instead of the text form")
	return v
}

// validate enforces the "--json requires --explain" rule.
func (v *explainFlags) validate() error {
	if v == nil {
		return nil
	}
	if v.json && !v.enabled {
		return errors.New("--json requires --explain")
	}
	return nil
}

// explainPayload mirrors the documented JSON shape. Keys are snake_case;
// the field set is locked at {key, scope, hasher, state}.
type explainPayload struct {
	Key    string   `json:"key"`
	Scope  []string `json:"scope"`
	Hasher string   `json:"hasher"`
	State  string   `json:"state"`
}

// emitExplain writes the scope listing for c. In text mode the listing
// goes to errOut (stderr), preserving stdout for downstream composition;
// in --json mode a single object goes to out (stdout) and nothing goes
// to stderr.
//
// markerState is one of stateMatch / stateMismatch / stateNoMarker and
// reflects what verify would return for the same context. Callers that
// already loaded the marker pass the result of explainStateForVerify.
func emitExplain(c *gateCtx, flags *explainFlags, out, errOut io.Writer, markerState string) error {
	if flags == nil || !flags.enabled {
		return nil
	}
	scope, err := c.hasher.Scope(c.repo)
	if err != nil {
		return err
	}
	// Empty scope is a real signal (globs match nothing); render an
	// empty list rather than nil so JSON consumers see []  .
	if scope == nil {
		scope = []string{}
	}

	if flags.json {
		payload := explainPayload{
			Key:    c.key,
			Scope:  scope,
			Hasher: c.hasher.Type(),
			State:  markerState,
		}
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(payload)
	}

	fmt.Fprintln(errOut, "scope:")
	for _, p := range scope {
		fmt.Fprintf(errOut, "  %s\n", p)
	}
	fmt.Fprintf(errOut, "state: %s\n", markerState)
	return nil
}

// explainStateForVerify computes the state label that --explain should
// report for c, using the same rules as `verify`: missing marker yields
// "no marker"; a hash-type or digest mismatch yields "mismatch";
// otherwise "match". The boolean tracks whether the marker matched, so
// `run` can decide whether to skip the child without re-loading.
func explainStateForVerify(c *gateCtx) (label string, matched bool, marker *state.Marker, err error) {
	m, loadErr := state.Load(c.markerPath)
	if loadErr != nil {
		if errors.Is(loadErr, state.ErrNotFound) {
			return stateNoMarker, false, nil, nil
		}
		return "", false, nil, loadErr
	}
	digest, hashErr := c.hasher.Hash(c.repo)
	if hashErr != nil {
		return "", false, nil, hashErr
	}
	if m.HashType != c.hasher.Type() || m.Digest != digest {
		return stateMismatch, false, m, nil
	}
	return stateMatch, true, m, nil
}

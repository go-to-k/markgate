package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/go-to-k/markgate/internal/config"
	"github.com/go-to-k/markgate/internal/gitutil"
	"github.com/go-to-k/markgate/internal/hasher"
	"github.com/go-to-k/markgate/internal/key"
	"github.com/go-to-k/markgate/internal/state"
)

// Note string values are part of the cross-cutting JSON shape (locked
// alongside #24); changing them is a breaking change for consumers
// that branch on `note`. The state vocabulary lives in explain.go and
// is shared across verify / status / run.
const (
	noteDigestDiff = "digest differs"
	noteUnconfig   = "(unconfigured)"
	noteConfigured = "(configured)"
)

func newStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:               "status [key]",
		Short:             "Show marker information and freshness (bare lists every gate)",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: gateKeyCompletion,
	}
	overrides := addGateFlags(cmd)
	explain := addExplainFlags(cmd)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		errOut := cmd.ErrOrStderr()
		if len(args) == 0 {
			// --explain is a single-key affordance (per-row scope listing
			// would multiply stderr output by N gates and confuse pipes).
			if explain != nil && explain.enabled {
				return &ExitError{Code: 2, Err: errors.New("--explain is only valid with a single key")}
			}
			return statusListAll(out, overrides, explain != nil && explain.json)
		}
		return statusSingle(out, errOut, args[0], overrides, explain)
	}
	return cmd
}

type statusMarkerJSON struct {
	CreatedAt string `json:"created_at"`
	Kind      string `json:"kind,omitempty"`
	HashType  string `json:"hash_type,omitempty"`
	Head      string `json:"head,omitempty"`
}

type statusRowJSON struct {
	Key          string            `json:"key"`
	State        string            `json:"state"`
	Marker       *statusMarkerJSON `json:"marker"`
	Configured   bool              `json:"configured"`
	Unconfigured bool              `json:"unconfigured"`
	Note         string            `json:"note"`
}

type statusRow struct {
	key          string
	state        string
	marker       *state.Marker
	configured   bool
	unconfigured bool
	note         string
	now          time.Time
}

func (r statusRow) toJSON() statusRowJSON {
	row := statusRowJSON{
		Key:          r.key,
		State:        r.state,
		Configured:   r.configured,
		Unconfigured: r.unconfigured,
		Note:         r.note,
	}
	if r.marker != nil {
		row.Marker = &statusMarkerJSON{
			CreatedAt: r.marker.CreatedAt.UTC().Format(time.RFC3339),
			Kind:      r.marker.Kind,
			HashType:  r.marker.HashType,
			Head:      r.marker.Head,
		}
	}
	return row
}

func statusSingle(out, errOut io.Writer, k string, overrides *gateFlagValues, explain *explainFlags) error {
	c, err := newGateCtx(k, overrides)
	if err != nil {
		return err
	}

	jsonOnly := explain != nil && explain.json && !explain.enabled

	res, evalErr := c.evaluate()
	if evalErr != nil {
		return &ExitError{Code: 2, Err: evalErr}
	}

	if res.marker == nil {
		// No marker on disk — evaluate already mapped this to
		// reason="no marker"; honor explain/json variants.
		if explain != nil && explain.enabled {
			if emitErr := emitExplain(c, explain, out, errOut, stateNoMarker); emitErr != nil {
				return &ExitError{Code: 2, Err: emitErr}
			}
			if explain.json {
				return &ExitError{Code: 1}
			}
			fmt.Fprintf(out, "key:        %s\nstate:      no marker\n", c.key)
			return &ExitError{Code: 1}
		}
		if jsonOnly {
			row := statusRow{
				key:          c.key,
				state:        stateNoMarker,
				configured:   true,
				unconfigured: false,
				note:         noteConfigured,
			}
			if writeErr := writeJSON(out, row.toJSON()); writeErr != nil {
				return &ExitError{Code: 2, Err: writeErr}
			}
			return &ExitError{Code: 1}
		}
		fmt.Fprintf(out, "key:        %s\nstate:      no marker\n", c.key)
		return &ExitError{Code: 1}
	}

	m := res.marker
	label := stateMatch
	if !res.matched {
		label = stateMismatch
	}

	// JSON --explain replaces the textual status block entirely so
	// stdout stays a single object.
	if explain != nil && explain.json && explain.enabled {
		if emitErr := emitExplain(c, explain, out, errOut, label); emitErr != nil {
			return &ExitError{Code: 2, Err: emitErr}
		}
		if label != stateMatch {
			return &ExitError{Code: 1}
		}
		return nil
	}

	if jsonOnly {
		row := statusRow{
			key:          c.key,
			marker:       m,
			configured:   true,
			unconfigured: false,
		}
		switch {
		case res.hashTypeChanged, res.ownDigestDiff:
			row.state = stateMismatch
			row.note = noteDigestDiff
		case !res.matched:
			row.state = stateMismatch
			row.note = "child " + res.childKey + " is stale"
		default:
			row.state = stateMatch
		}
		if writeErr := writeJSON(out, row.toJSON()); writeErr != nil {
			return &ExitError{Code: 2, Err: writeErr}
		}
		if row.state != stateMatch {
			return &ExitError{Code: 1}
		}
		return nil
	}

	// Text --explain: scope listing on stderr first, then the existing
	// status block on stdout. The label was already computed above so
	// the stderr line agrees with the detailed stdout line.
	if explain != nil && explain.enabled {
		if emitErr := emitExplain(c, explain, out, errOut, label); emitErr != nil {
			return &ExitError{Code: 2, Err: emitErr}
		}
	}

	fmt.Fprintf(out, "key:        %s\n", c.key)
	if m.Kind == state.KindDepsOnly {
		fmt.Fprintln(out, "kind:       deps-only")
	} else {
		fmt.Fprintf(out, "hash type:  %s\n", m.HashType)
	}
	fmt.Fprintf(out, "created:    %s\n", m.CreatedAt.Format(time.RFC3339))
	if m.Head != "" {
		fmt.Fprintf(out, "head:       %s\n", m.Head)
	}
	if c.gate.TTL != "" {
		fmt.Fprintf(out, "ttl:        %s\n", c.gate.TTL)
	}

	switch {
	case res.hashTypeChanged:
		fmt.Fprintf(out, "state:      mismatch (hash type changed: %s -> %s)\n", m.HashType, c.hasher.Type())
		return &ExitError{Code: 1}
	case res.ownDigestDiff:
		fmt.Fprintln(out, "state:      mismatch (digest differs)")
		return &ExitError{Code: 1}
	case res.ttl.expired:
		fmt.Fprintf(out, "state:      mismatch (expired by ttl: %s, marker is %s old)\n", c.gate.TTL, formatAge(res.ttl.age))
		return &ExitError{Code: 1}
	case !res.matched:
		fmt.Fprintf(out, "state:      mismatch (%s)\n", res.reason)
		return &ExitError{Code: 1}
	case res.ttl.configured:
		fmt.Fprintf(out, "state:      match (expires in %s)\n", formatAge(res.ttl.ttl-res.ttl.age))
		return nil
	default:
		fmt.Fprintln(out, "state:      match")
		return nil
	}
}

func statusListAll(out io.Writer, overrides *gateFlagValues, asJSON bool) error {
	repo := gitutil.New("")
	top, err := repo.TopLevel()
	if err != nil {
		return &ExitError{Code: 2, Err: err}
	}
	gitDir, err := repo.GitDir()
	if err != nil {
		return &ExitError{Code: 2, Err: err}
	}
	cfg, err := config.Load(top)
	if err != nil {
		return &ExitError{Code: 2, Err: err}
	}

	// Use the default key's gate to anchor the state-dir resolution: we
	// want the directory the per-key path would walk, and per-gate
	// state_dir is settled later for each row anyway.
	defaultGate := overrides.override(cfg.Gate(DefaultKey))
	stateDir := resolveStateDir(overrides, defaultGate, top, gitDir)

	keys, err := discoverKeys(cfg, stateDir)
	if err != nil {
		return &ExitError{Code: 2, Err: err}
	}

	clock := now()
	rows := make([]statusRow, 0, len(keys))
	configured := configuredSet(cfg)
	for _, k := range keys {
		_, isConfigured := configured[k]
		gate := overrides.override(cfg.Gate(k))
		if vErr := validateGate(gate); vErr != nil {
			return &ExitError{Code: 2, Err: vErr}
		}
		h, hErr := hasher.For(gate)
		if hErr != nil {
			return &ExitError{Code: 2, Err: hErr}
		}
		markerPath := resolveMarkerPath(overrides, gate, top, gitDir, k)
		row, bErr := buildRow(k, gate, h, repo, markerPath, isConfigured, clock)
		if bErr != nil {
			return &ExitError{Code: 2, Err: bErr}
		}
		rows = append(rows, row)
	}

	if asJSON {
		payload := make([]statusRowJSON, 0, len(rows))
		for _, r := range rows {
			payload = append(payload, r.toJSON())
		}
		if err := writeJSON(out, payload); err != nil {
			return &ExitError{Code: 2, Err: err}
		}
	} else {
		writeListPlain(out, rows)
	}

	for _, r := range rows {
		if r.state != stateMatch {
			return &ExitError{Code: 1}
		}
	}
	return nil
}

// discoverKeys silently skips marker files whose names don't pass key
// validation: a stray file in the state dir must not poison the listing.
func discoverKeys(cfg *config.Config, stateDir string) ([]string, error) {
	seen := make(map[string]struct{})
	if cfg != nil {
		for k := range cfg.Gates {
			seen[k] = struct{}{}
		}
	}
	entries, err := os.ReadDir(stateDir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		k := strings.TrimSuffix(name, ".json")
		if err := key.Validate(k); err != nil {
			continue
		}
		seen[k] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, nil
}

func configuredSet(cfg *config.Config) map[string]struct{} {
	out := map[string]struct{}{}
	if cfg != nil {
		for k := range cfg.Gates {
			out[k] = struct{}{}
		}
	}
	return out
}

// buildRow skips the digest computation when no marker is present —
// config-only keys (which dominate "fresh repo" output) shouldn't pay
// the hash cost. TTL is folded into the note column when the gate
// matches and a TTL is configured: "expires in 4d" while fresh,
// "expired 1d ago" once the deadline has passed (the latter also
// flips state to mismatch).
func buildRow(k string, gate config.Gate, h hasher.Hasher, repo *gitutil.Repo, markerPath string, configured bool, clock time.Time) (statusRow, error) {
	row := statusRow{
		key:          k,
		configured:   configured,
		unconfigured: !configured,
		now:          clock,
	}
	m, err := state.Load(markerPath)
	if err != nil {
		if !errors.Is(err, state.ErrNotFound) {
			return row, err
		}
		row.state = stateNoMarker
		if configured {
			row.note = noteConfigured
		}
		return row, nil
	}
	row.marker = m
	mismatch := false
	if m.Kind == state.KindDepsOnly {
		// Deps-only marker: no own scope to hash, so freshness depends
		// on children alone. Bare status doesn't recurse into children
		// (cost / surprise), so report match here and let the user run
		// `markgate verify <key>` for the full propagation if it
		// matters. The presence of the marker proves an explicit set.
	} else {
		digest, hashErr := h.Hash(repo)
		if hashErr != nil {
			return row, hashErr
		}
		mismatch = m.HashType != h.Type() || m.Digest != digest
	}
	if mismatch {
		row.state = stateMismatch
		row.note = noteDigestDiff
	} else {
		row.state = stateMatch
		if gate.TTL != "" {
			ttl, ttlErr := checkTTL(gate, m)
			if ttlErr != nil {
				return row, ttlErr
			}
			switch {
			case ttl.expired:
				row.state = stateMismatch
				row.note = fmt.Sprintf("expired %s ago", formatAge(ttl.age-ttl.ttl))
			case ttl.configured:
				row.note = fmt.Sprintf("expires in %s", formatAge(ttl.ttl-ttl.age))
			}
		}
	}
	if !configured {
		// (unconfigured) wins over digest / ttl notes: a stray marker
		// is the more actionable signal — fix the listing first.
		row.note = noteUnconfig
	}
	return row, nil
}

func writeJSON(out io.Writer, v any) error {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func writeListPlain(out io.Writer, rows []statusRow) {
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "KEY\tSTATE\tAGE\tNOTE")
	for _, r := range rows {
		age := "-"
		if r.marker != nil {
			age = formatAge(r.now.Sub(r.marker.CreatedAt)) + " ago"
		}
		note := r.note
		if note == "" {
			note = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", r.key, r.state, age, note)
	}
	_ = tw.Flush()
}

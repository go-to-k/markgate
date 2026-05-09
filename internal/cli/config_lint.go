package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/go-to-k/markgate/internal/config"
	"github.com/go-to-k/markgate/internal/gitutil"
	"github.com/go-to-k/markgate/internal/hasher"
)

// lintFinding is one warning emitted by `markgate config lint`.
type lintFinding struct {
	Path     string `json:"path"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

// gateFields lists the YAML keys recognized inside a gate. Kept in sync
// with config.Gate's yaml tags; the lint command treats anything else as
// an unknown field.
var gateFields = map[string]struct{}{
	"hash":      {},
	"include":   {},
	"exclude":   {},
	"state_dir": {},
}

// topFields lists the YAML keys recognized at the document root. Mirrors
// config.Config's yaml tags.
var topFields = map[string]struct{}{
	"gates": {},
}

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Inspect or check .markgate.yml",
	}
	cmd.AddCommand(newConfigLintCmd())
	return cmd
}

func newConfigLintCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "lint",
		Short: "Report dead globs and unknown fields in .markgate.yml",
		Long: "Walks .markgate.yml and warns on:\n" +
			"  - include/exclude globs that match zero files in the working tree\n" +
			"  - unknown top-level or per-gate keys (typos, leftovers).\n\n" +
			"Exit codes: 0 clean, 1 warnings, 2 parse / read error.",
		Args: cobra.NoArgs,
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit findings as a JSON array of {path, severity, message}")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		return runConfigLint(cmd.OutOrStdout(), jsonOut)
	}
	return cmd
}

func runConfigLint(out io.Writer, jsonOut bool) error {
	repo := gitutil.New("")
	top, err := repo.TopLevel()
	if err != nil {
		return &ExitError{Code: 2, Err: err}
	}
	path := filepath.Join(top, config.Filename)

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &ExitError{Code: 2, Err: fmt.Errorf("%s not found at repo root", config.Filename)}
		}
		return &ExitError{Code: 2, Err: err}
	}

	var root yaml.Node
	if err = yaml.Unmarshal(data, &root); err != nil {
		return &ExitError{Code: 2, Err: fmt.Errorf("parse %s: %w", config.Filename, err)}
	}

	var cfg config.Config
	if err = yaml.Unmarshal(data, &cfg); err != nil {
		return &ExitError{Code: 2, Err: fmt.Errorf("parse %s: %w", config.Filename, err)}
	}

	findings, err := collectFindings(top, &root, &cfg)
	if err != nil {
		return &ExitError{Code: 2, Err: err}
	}
	sortFindings(findings)

	if jsonOut {
		if err := emitJSON(out, findings); err != nil {
			return &ExitError{Code: 2, Err: err}
		}
	} else {
		emitText(out, findings)
	}

	if len(findings) > 0 {
		return &ExitError{Code: 1}
	}
	return nil
}

// collectFindings walks the YAML tree for unknown keys and the typed
// config for dead include/exclude globs, returning the merged warning
// set. A non-nil error means a glob pattern was malformed (config
// error, not finding) — caller surfaces it as exit 2.
func collectFindings(topLevel string, root *yaml.Node, cfg *config.Config) ([]lintFinding, error) {
	findings := unknownFieldFindings(root)
	dead, err := deadGlobFindings(topLevel, cfg)
	if err != nil {
		return nil, err
	}
	findings = append(findings, dead...)
	return findings, nil
}

// unknownFieldFindings reports keys that are not defined on Config or Gate.
// Walks yaml.Node directly so messages stay user-facing
// ("gates.legacy.legacy_field") rather than leaking Go type names that
// yaml.v3's KnownFields error would otherwise expose.
func unknownFieldFindings(root *yaml.Node) []lintFinding {
	var out []lintFinding
	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 {
		return out
	}
	docRoot := root.Content[0]
	if docRoot.Kind != yaml.MappingNode {
		return out
	}
	for i := 0; i+1 < len(docRoot.Content); i += 2 {
		keyNode := docRoot.Content[i]
		valNode := docRoot.Content[i+1]
		k := keyNode.Value
		if _, ok := topFields[k]; !ok {
			out = append(out, lintFinding{
				Path:     k,
				Severity: "warning",
				Message:  fmt.Sprintf("unknown field: %s", k),
			})
			continue
		}
		if k == "gates" && valNode.Kind == yaml.MappingNode {
			out = append(out, unknownGateFieldFindings(valNode)...)
		}
	}
	return out
}

func unknownGateFieldFindings(gates *yaml.Node) []lintFinding {
	var out []lintFinding
	for i := 0; i+1 < len(gates.Content); i += 2 {
		gateName := gates.Content[i].Value
		gateNode := gates.Content[i+1]
		if gateNode.Kind != yaml.MappingNode {
			continue
		}
		for j := 0; j+1 < len(gateNode.Content); j += 2 {
			fieldName := gateNode.Content[j].Value
			if _, ok := gateFields[fieldName]; ok {
				continue
			}
			dotted := fmt.Sprintf("gates.%s.%s", gateName, fieldName)
			out = append(out, lintFinding{
				Path:     dotted,
				Severity: "warning",
				Message:  fmt.Sprintf("unknown field: %s", dotted),
			})
		}
	}
	return out
}

// deadGlobFindings flags include/exclude globs that match zero files in
// the working tree. The check expands each pattern against the FS so
// typos (`docss/**`) and patterns left over after refactors surface
// immediately rather than waiting for someone to notice the gate has
// stopped invalidating. A glob with malformed syntax (unmatched
// bracket, etc.) is a config error, not a finding — surface it as
// an error so the lint command exits 2 rather than silently passing
// the broken pattern.
func deadGlobFindings(topLevel string, cfg *config.Config) ([]lintFinding, error) {
	var out []lintFinding
	names := make([]string, 0, len(cfg.Gates))
	for name := range cfg.Gates {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		g := cfg.Gates[name]
		for i, pat := range g.Include {
			path := fmt.Sprintf("gates.%s.include[%d]", name, i)
			matches, err := hasher.MatchGlob(topLevel, pat)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", path, err)
			}
			if len(matches) == 0 {
				out = append(out, lintFinding{
					Path:     path,
					Severity: "warning",
					Message:  fmt.Sprintf("%s: '%s' matches 0 files", path, pat),
				})
			}
		}
		for i, pat := range g.Exclude {
			path := fmt.Sprintf("gates.%s.exclude[%d]", name, i)
			matches, err := hasher.MatchGlob(topLevel, pat)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", path, err)
			}
			if len(matches) == 0 {
				out = append(out, lintFinding{
					Path:     path,
					Severity: "warning",
					Message:  fmt.Sprintf("%s: '%s' matches 0 files", path, pat),
				})
			}
		}
	}
	return out, nil
}

func sortFindings(f []lintFinding) {
	sort.SliceStable(f, func(i, j int) bool { return f[i].Path < f[j].Path })
}

func emitText(out io.Writer, findings []lintFinding) {
	for _, f := range findings {
		fmt.Fprintln(out, f.Message)
	}
}

func emitJSON(out io.Writer, findings []lintFinding) error {
	if findings == nil {
		findings = []lintFinding{}
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(findings)
}

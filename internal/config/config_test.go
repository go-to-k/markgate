package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeConfig(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, Filename), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoad_Missing(t *testing.T) {
	c, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if c == nil {
		t.Fatal("want non-nil empty config, got nil")
	}
	if g := c.Gate("anything"); g.Hash != HashGitTree {
		t.Errorf("missing-file default = %q, want %q", g.Hash, HashGitTree)
	}
}

func TestLoad_GitTreeOK(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "gates:\n  pre-commit:\n    hash: git-tree\n")
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if g := c.Gate("pre-commit"); g.Hash != HashGitTree {
		t.Errorf("Gate.Hash = %q, want %q", g.Hash, HashGitTree)
	}
}

func TestLoad_FilesRequiresInclude(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "gates:\n  pre-pr:\n    hash: files\n")
	if _, err := Load(dir); err == nil {
		t.Error("want error when include missing for files")
	}
}

func TestLoad_DiffRequiresBase(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "gates:\n  integ:\n    hash: diff\n")
	if _, err := Load(dir); err == nil {
		t.Error("want error when base missing for diff")
	}
}

func TestLoad_DiffOK(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "gates:\n  integ:\n    hash: diff\n    base: origin/main\n")
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	g := c.Gate("integ")
	if g.Hash != HashDiff || g.Base != "origin/main" {
		t.Errorf("Gate = %+v, want hash=%q base=origin/main", g, HashDiff)
	}
}

// base on any other hash type is a silent no-op, which is exactly how a
// gate ends up trusted for behavior it does not have.
func TestLoad_BaseRejectedOnNonDiffGate(t *testing.T) {
	for _, body := range []string{
		"gates:\n  x:\n    hash: files\n    include:\n      - \"src/**\"\n    base: main\n",
		"gates:\n  x:\n    hash: git-tree\n    base: main\n",
		"gates:\n  x:\n    base: main\n",
	} {
		dir := t.TempDir()
		writeConfig(t, dir, body)
		if _, err := Load(dir); err == nil {
			t.Errorf("want error for base on a non-diff gate: %q", body)
		}
	}
}

// A deps-only gate never consults a hasher, so hash: diff and base:
// on one are discarded — the same silent no-op that
// TestLoad_BaseRejectedOnNonDiffGate exists to prevent, seen from the
// other side.
func TestLoad_DiffRejectedOnDepsOnlyGate(t *testing.T) {
	for _, body := range []string{
		"gates:\n  child:\n    hash: files\n    include:\n      - \"src/**\"\n  x:\n    hash: diff\n    base: main\n    composes: [child]\n",
		"gates:\n  child:\n    hash: files\n    include:\n      - \"src/**\"\n  x:\n    hash: diff\n    base: main\n    requires: [child]\n",
	} {
		dir := t.TempDir()
		writeConfig(t, dir, body)
		if _, err := Load(dir); err == nil {
			t.Errorf("want error for hash=diff on a deps-only gate: %q", body)
		}
	}
}

// The rule is about own scope, not about having children: a diff gate
// that declares include alongside composes keeps its own digest and
// must stay legal.
func TestLoad_DiffWithIncludeAndDepsOK(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir,
		"gates:\n  child:\n    hash: files\n    include:\n      - \"src/**\"\n  x:\n    hash: diff\n    base: main\n    include:\n      - \"src/**\"\n    composes: [child]\n")
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if g := c.Gate("x"); !g.HasOwnScope() {
		t.Error("diff gate with include + composes should keep its own scope")
	}
}

// git-tree is the default and needs no scope config, so writing it
// explicitly on a deps-only gate stays legal — the rule targets config
// that would be discarded, not config that is merely redundant.
func TestLoad_ExplicitGitTreeOnDepsOnlyGateOK(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir,
		"gates:\n  child:\n    hash: files\n    include:\n      - \"src/**\"\n  x:\n    hash: git-tree\n    composes: [child]\n")
	if _, err := Load(dir); err != nil {
		t.Errorf("explicit git-tree on a deps-only gate should load: %v", err)
	}
}

func TestLoad_UnknownHash(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "gates:\n  x:\n    hash: bogus\n")
	if _, err := Load(dir); err == nil {
		t.Error("want error for unknown hash")
	}
}

func TestLoad_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "not: [valid: yaml")
	if _, err := Load(dir); err == nil {
		t.Error("want parse error")
	}
}

func TestLoad_InvalidKey(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "gates:\n  Bad_Key:\n    hash: git-tree\n")
	if _, err := Load(dir); err == nil {
		t.Error("want key validation error")
	}
}

func TestGate_DefaultForNilConfig(t *testing.T) {
	var c *Config
	if g := c.Gate("anything"); g.Hash != HashGitTree {
		t.Errorf("nil-config default = %q, want %q", g.Hash, HashGitTree)
	}
}

func TestLoad_StateDirPreserved(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "gates:\n  pre-pr:\n    hash: git-tree\n    state_dir: .cache/mg\n")
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if g := c.Gate("pre-pr"); g.StateDir != ".cache/mg" {
		t.Errorf("Gate.StateDir = %q, want %q", g.StateDir, ".cache/mg")
	}
}

func TestLoad_DepsBothFields(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir,
		"gates:\n  child:\n    hash: git-tree\n  parent:\n    composes: [child]\n    requires: [child]\n")
	if _, err := Load(dir); err == nil {
		t.Error("want error when composes and requires are both set")
	}
}

func TestLoad_DepsMissingChild(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir,
		"gates:\n  parent:\n    composes: [ghost]\n")
	if _, err := Load(dir); err == nil {
		t.Error("want error when child gate is undeclared")
	}
}

func TestLoad_DepsCycle(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir,
		"gates:\n  a:\n    composes: [b]\n  b:\n    requires: [c]\n  c:\n    composes: [a]\n")
	if _, err := Load(dir); err == nil {
		t.Error("want error for a -> b -> c -> a cycle")
	}
}

func TestLoad_DepsSelfReference(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir,
		"gates:\n  a:\n    composes: [a]\n")
	if _, err := Load(dir); err == nil {
		t.Error("want error for self-reference")
	}
}

func TestLoad_DepsValid(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir,
		"gates:\n"+
			"  parent:\n    composes: [a, b]\n"+
			"  a:\n    hash: git-tree\n"+
			"  b:\n    hash: git-tree\n")
	c, err := Load(dir)
	if err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	if got := c.Gate("parent").Composes; len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("parent.Composes = %v, want [a b]", got)
	}
}

func TestGate_DefaultForMissingKey(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "gates:\n  pre-commit:\n    hash: git-tree\n")
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if g := c.Gate("other"); g.Hash != HashGitTree {
		t.Errorf("missing-key default = %q, want %q", g.Hash, HashGitTree)
	}
}

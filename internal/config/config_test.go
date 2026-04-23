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

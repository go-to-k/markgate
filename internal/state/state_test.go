package state

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSaveLoad_RoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "check.json")
	m := &Marker{
		HashType: "git-tree",
		Digest:   "abcd",
		Head:     "deadbeef",
	}
	if err := Save(p, m); err != nil {
		t.Fatal(err)
	}

	got, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.HashType != "git-tree" || got.Digest != "abcd" || got.Head != "deadbeef" {
		t.Errorf("round-trip mismatch: %+v", got)
	}
	if got.Version != SchemaVersion {
		t.Errorf("Version = %d, want %d", got.Version, SchemaVersion)
	}
	if got.CreatedAt.IsZero() {
		t.Error("CreatedAt not populated")
	}
}

func TestLoad_NotFound(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "missing.json"))
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestLoad_CorruptJSON(t *testing.T) {
	p := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(p, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(p)
	if err == nil || errors.Is(err, ErrNotFound) {
		t.Errorf("want parse error, got %v", err)
	}
}

func TestRemove_Idempotent(t *testing.T) {
	p := filepath.Join(t.TempDir(), "m.json")

	if err := Remove(p); err != nil {
		t.Errorf("Remove on missing: %v", err)
	}

	if err := Save(p, &Marker{HashType: "git-tree", Digest: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := Remove(p); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
		t.Error("marker file still present after Remove")
	}
}

func TestSave_AtomicNoTempLeaks(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "m.json")

	if err := Save(p, &Marker{HashType: "git-tree", Digest: "x"}); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "m.json" {
			t.Errorf("unexpected leftover file: %s", e.Name())
		}
	}
}

package hasher

import (
	"testing"

	"github.com/go-to-k/markgate/internal/config"
)

func TestFiles_IncludeAndExclude(t *testing.T) {
	repo, dir := newTestRepo(t)
	writeFile(t, dir, "src/a.ts", "a")
	writeFile(t, dir, "src/b.md", "b")
	writeFile(t, dir, "README.md", "r")

	h := Files{
		Include: []string{"src/**/*"},
		Exclude: []string{"**/*.md"},
	}

	d1, err := h.Hash(repo)
	if err != nil {
		t.Fatal(err)
	}

	// Excluded file changing must not affect the digest.
	writeFile(t, dir, "src/b.md", "b changed")
	d2, err := h.Hash(repo)
	if err != nil {
		t.Fatal(err)
	}
	if d1 != d2 {
		t.Errorf("exclude did not suppress change: %s -> %s", d1, d2)
	}

	// Included file changing must affect the digest.
	writeFile(t, dir, "src/a.ts", "a changed")
	d3, err := h.Hash(repo)
	if err != nil {
		t.Fatal(err)
	}
	if d1 == d3 {
		t.Error("include did not register change")
	}
}

func TestFiles_DoublestarMatchesNested(t *testing.T) {
	repo, dir := newTestRepo(t)
	writeFile(t, dir, "src/deep/nested/x.ts", "x")

	h := Files{Include: []string{"src/**/*.ts"}}

	d1, err := h.Hash(repo)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "src/deep/nested/x.ts", "y")
	d2, err := h.Hash(repo)
	if err != nil {
		t.Fatal(err)
	}
	if d1 == d2 {
		t.Error("** did not recurse into nested dirs")
	}
}

func TestFiles_OutsideIncludeIgnored(t *testing.T) {
	repo, dir := newTestRepo(t)
	writeFile(t, dir, "src/a.ts", "a")
	writeFile(t, dir, "docs/x.md", "x")

	h := Files{Include: []string{"src/**/*.ts"}}

	d1, err := h.Hash(repo)
	if err != nil {
		t.Fatal(err)
	}
	// Changing something outside include must be a no-op.
	writeFile(t, dir, "docs/x.md", "y")
	d2, err := h.Hash(repo)
	if err != nil {
		t.Fatal(err)
	}
	if d1 != d2 {
		t.Errorf("file outside include affected digest: %s -> %s", d1, d2)
	}
}

func TestFor_UnknownHash(t *testing.T) {
	if _, err := For(config.Gate{Hash: "weird"}); err == nil {
		t.Error("want error for unknown hash type")
	}
}

func TestFor_DefaultsToGitTree(t *testing.T) {
	h, err := For(config.Gate{})
	if err != nil {
		t.Fatal(err)
	}
	if h.Type() != config.HashGitTree {
		t.Errorf("default hasher = %q, want %q", h.Type(), config.HashGitTree)
	}
}

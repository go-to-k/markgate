package hasher

import (
	"errors"
	"strings"
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

// An include list that matches nothing is not an empty scope, it is a
// broken one: the digest degenerates to the SHA-256 of the empty set,
// which is the same value for every such gate, so the marker matches
// forever and the gate can never block.
func TestFiles_DeadIncludeIsAnError(t *testing.T) {
	repo, dir := newTestRepo(t)
	writeFile(t, dir, "src/a.ts", "a")

	h := Files{Include: []string{"scr/**"}}
	if _, err := h.Hash(repo); !errors.Is(err, ErrDeadScope) {
		t.Errorf("Hash err = %v, want ErrDeadScope", err)
	}
	// Scope must stay a truthful description rather than a refusal: it
	// backs --explain, which is a diagnostic and must never change what
	// a command does.
	scope, err := h.Scope(repo)
	if err != nil {
		t.Errorf("Scope on a dead include should not error: %v", err)
	}
	if len(scope) != 0 {
		t.Errorf("Scope = %v, want empty", scope)
	}
	// The message has to name the pattern: the gate's behavior gives the
	// user no way to tell which one is wrong.
	_, hashErr := h.Hash(repo)
	if !strings.Contains(hashErr.Error(), "scr/**") {
		t.Errorf("error does not name the dead pattern: %v", hashErr)
	}
}

// One live pattern is enough. A partially dead include list still has a
// real scope, so it is lint's business (it warns per pattern), not a
// reason to refuse to hash.
func TestFiles_OneLiveIncludePatternIsEnough(t *testing.T) {
	repo, dir := newTestRepo(t)
	writeFile(t, dir, "src/a.ts", "a")

	h := Files{Include: []string{"scr/**", "src/**"}}
	if _, err := h.Hash(repo); err != nil {
		t.Errorf("partially dead include should still hash: %v", err)
	}
}

// Excluding everything an include matched is a deliberate configuration,
// not a broken one, so it must stay distinct from a dead include.
func TestFiles_ExcludeEmptyingTheScopeIsNotDead(t *testing.T) {
	repo, dir := newTestRepo(t)
	writeFile(t, dir, "src/a.ts", "a")

	h := Files{Include: []string{"src/**"}, Exclude: []string{"src/**"}}
	scope, err := h.Scope(repo)
	if err != nil {
		t.Fatalf("exclude-emptied scope should not error: %v", err)
	}
	if len(scope) != 0 {
		t.Errorf("scope = %v, want empty", scope)
	}
}

// A gate with no include at all covers the whole tree and cannot be
// dead, so the check must not fire on it.
func TestFiles_NoIncludeIsNotDead(t *testing.T) {
	repo, _ := newTestRepo(t)
	if _, err := (Files{}).Scope(repo); err != nil {
		t.Errorf("include-less Files should not report a dead scope: %v", err)
	}
}

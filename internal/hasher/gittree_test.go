package hasher

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGitTree_StagingInvariant(t *testing.T) {
	repo, dir := newTestRepo(t)
	writeFile(t, dir, "a.txt", "hello")

	d1, err := GitTree{}.Hash(repo)
	if err != nil {
		t.Fatal(err)
	}

	runGit(t, dir, "add", "a.txt")

	d2, err := GitTree{}.Hash(repo)
	if err != nil {
		t.Fatal(err)
	}
	if d1 != d2 {
		t.Errorf("digest changed across git add: %s -> %s", d1, d2)
	}
}

func TestGitTree_HeadAffectsDigest(t *testing.T) {
	repo, dir := newTestRepo(t)
	writeFile(t, dir, "a.txt", "hello")

	d1, err := GitTree{}.Hash(repo)
	if err != nil {
		t.Fatal(err)
	}

	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-qm", "bump")

	d2, err := GitTree{}.Hash(repo)
	if err != nil {
		t.Fatal(err)
	}
	if d1 == d2 {
		t.Error("HEAD change did not affect digest")
	}
}

func TestGitTree_DetectsDeletion(t *testing.T) {
	repo, dir := newTestRepo(t)
	writeFile(t, dir, "a.txt", "hello")
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-qm", "add a")

	d1, err := GitTree{}.Hash(repo)
	if err != nil {
		t.Fatal(err)
	}

	if rmErr := os.Remove(filepath.Join(dir, "a.txt")); rmErr != nil {
		t.Fatal(rmErr)
	}

	d2, err := GitTree{}.Hash(repo)
	if err != nil {
		t.Fatal(err)
	}
	if d1 == d2 {
		t.Error("deletion did not affect digest")
	}
}

func TestGitTree_ContentChangeAffectsDigest(t *testing.T) {
	repo, dir := newTestRepo(t)
	writeFile(t, dir, "a.txt", "hello")

	d1, err := GitTree{}.Hash(repo)
	if err != nil {
		t.Fatal(err)
	}

	writeFile(t, dir, "a.txt", "hello world")

	d2, err := GitTree{}.Hash(repo)
	if err != nil {
		t.Fatal(err)
	}
	if d1 == d2 {
		t.Error("content edit did not affect digest")
	}
}

func TestGitTree_ExcludeFilter(t *testing.T) {
	repo, dir := newTestRepo(t)
	writeFile(t, dir, "src/a.go", "a")
	writeFile(t, dir, "vendor/lib.go", "lib")

	g := GitTree{Exclude: []string{"vendor/**"}}
	d1, err := g.Hash(repo)
	if err != nil {
		t.Fatal(err)
	}

	// Edit inside excluded path: digest must stay the same.
	writeFile(t, dir, "vendor/lib.go", "lib edited")
	d2, err := g.Hash(repo)
	if err != nil {
		t.Fatal(err)
	}
	if d1 != d2 {
		t.Errorf("edit inside excluded path changed digest: %s -> %s", d1, d2)
	}

	// Edit inside NON-excluded path: digest must change.
	writeFile(t, dir, "src/a.go", "a edited")
	d3, err := g.Hash(repo)
	if err != nil {
		t.Fatal(err)
	}
	if d1 == d3 {
		t.Error("edit outside excluded path did not change digest")
	}
}

func TestGitTree_IncludeFilter(t *testing.T) {
	repo, dir := newTestRepo(t)
	writeFile(t, dir, "src/a.go", "a")
	writeFile(t, dir, "other/b.go", "b")

	g := GitTree{Include: []string{"src/**"}}
	d1, err := g.Hash(repo)
	if err != nil {
		t.Fatal(err)
	}

	// Edit outside include: digest must stay the same.
	writeFile(t, dir, "other/b.go", "b edited")
	d2, err := g.Hash(repo)
	if err != nil {
		t.Fatal(err)
	}
	if d1 != d2 {
		t.Errorf("edit outside include changed digest: %s -> %s", d1, d2)
	}

	// Edit inside include: digest must change.
	writeFile(t, dir, "src/a.go", "a edited")
	d3, err := g.Hash(repo)
	if err != nil {
		t.Fatal(err)
	}
	if d1 == d3 {
		t.Error("edit inside include did not change digest")
	}
}

func TestGitTree_FiltersStillHeadAware(t *testing.T) {
	// With filters, commits still invalidate the marker (HEAD is in the hash).
	repo, dir := newTestRepo(t)
	writeFile(t, dir, "src/a.go", "a")

	g := GitTree{Exclude: []string{"vendor/**"}}
	d1, err := g.Hash(repo)
	if err != nil {
		t.Fatal(err)
	}

	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-qm", "bump")

	d2, err := g.Hash(repo)
	if err != nil {
		t.Fatal(err)
	}
	if d1 == d2 {
		t.Error("HEAD change did not affect digest under GitTree with filters")
	}
}

func writeFile(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

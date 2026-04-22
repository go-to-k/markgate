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

	if err := os.Remove(filepath.Join(dir, "a.txt")); err != nil {
		t.Fatal(err)
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

func writeFile(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

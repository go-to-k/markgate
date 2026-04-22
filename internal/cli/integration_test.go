package cli

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// runCmd drives the root command with the given args and returns the exit
// code (0 match, 1 mismatch, 2 error) plus captured stdout.
func runCmd(t *testing.T, args ...string) (int, string) {
	t.Helper()
	root := newRootCmd("test")
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs(args)

	err := root.Execute()
	if err == nil {
		return 0, stdout.String()
	}
	var ee *ExitError
	if errors.As(err, &ee) {
		return ee.Code, stdout.String()
	}
	t.Fatalf("unexpected error type %T: %v\nstderr: %s", err, err, stderr.String())
	return -1, ""
}

// initRepo creates a fresh repo in a temp dir, chdirs into it (auto-
// restored by t.Chdir), and returns the path.
func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, "seed.txt"), []byte("seed"), 0o600); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-qm", "init")
	t.Chdir(dir)
	return dir
}

func writeRepoFile(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestSetVerify_ZeroConfig(t *testing.T) {
	dir := initRepo(t)

	if code, _ := runCmd(t, "verify", "check"); code != 1 {
		t.Errorf("initial verify: code = %d, want 1 (not found)", code)
	}

	if code, _ := runCmd(t, "set", "check"); code != 0 {
		t.Errorf("set: code = %d, want 0", code)
	}
	if code, _ := runCmd(t, "verify", "check"); code != 0 {
		t.Errorf("verify after set: code = %d, want 0", code)
	}

	writeRepoFile(t, dir, "seed.txt", "seed modified")
	if code, _ := runCmd(t, "verify", "check"); code != 1 {
		t.Errorf("verify after edit: code = %d, want 1", code)
	}
}

func TestClear_Idempotent(t *testing.T) {
	initRepo(t)
	// Clear with no marker is a no-op, exit 0.
	if code, _ := runCmd(t, "clear", "check"); code != 0 {
		t.Errorf("clear on missing: code = %d, want 0", code)
	}
	// Set, clear, verify reports mismatch (exit 1).
	if code, _ := runCmd(t, "set", "check"); code != 0 {
		t.Fatalf("set: code = %d", code)
	}
	if code, _ := runCmd(t, "clear", "check"); code != 0 {
		t.Errorf("clear: code = %d, want 0", code)
	}
	if code, _ := runCmd(t, "verify", "check"); code != 1 {
		t.Errorf("verify after clear: code = %d, want 1", code)
	}
}

func TestStatus_MatchAndMismatch(t *testing.T) {
	dir := initRepo(t)
	if code, _ := runCmd(t, "set", "check"); code != 0 {
		t.Fatalf("set failed: %d", code)
	}

	code, out := runCmd(t, "status", "check")
	if code != 0 {
		t.Errorf("status (match): code = %d, want 0", code)
	}
	if !bytes.Contains([]byte(out), []byte("state:      match")) {
		t.Errorf("status output missing match line:\n%s", out)
	}

	writeRepoFile(t, dir, "seed.txt", "edit")
	code, out = runCmd(t, "status", "check")
	if code != 1 {
		t.Errorf("status (mismatch): code = %d, want 1", code)
	}
	if !bytes.Contains([]byte(out), []byte("mismatch")) {
		t.Errorf("status output missing mismatch line:\n%s", out)
	}
}

func TestFilesHash_RespectsScope(t *testing.T) {
	dir := initRepo(t)
	writeRepoFile(t, dir, "src/a.ts", "a")
	writeRepoFile(t, dir, "docs/x.md", "x")
	writeRepoFile(t, dir, ".markgate.yml",
		"gates:\n  pre-pr:\n    hash: files\n    include:\n      - \"src/**/*.ts\"\n")

	if code, _ := runCmd(t, "set", "pre-pr"); code != 0 {
		t.Fatalf("set: %d", code)
	}

	writeRepoFile(t, dir, "docs/x.md", "edited")
	if code, _ := runCmd(t, "verify", "pre-pr"); code != 0 {
		t.Errorf("verify after docs edit: code = %d, want 0 (out of scope)", code)
	}

	writeRepoFile(t, dir, "src/a.ts", "edited")
	if code, _ := runCmd(t, "verify", "pre-pr"); code != 1 {
		t.Errorf("verify after src edit: code = %d, want 1 (in scope)", code)
	}
}

func TestInvalidKey(t *testing.T) {
	initRepo(t)
	if code, _ := runCmd(t, "set", "Bad_Key"); code != 2 {
		t.Errorf("invalid key: code = %d, want 2", code)
	}
}

func TestNonGitDir(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if code, _ := runCmd(t, "set", "check"); code != 2 {
		t.Errorf("non-git dir: code = %d, want 2", code)
	}
}

func TestRun_SkipOnMatch(t *testing.T) {
	initRepo(t)
	// Run a sentinel command that must not be invoked when verify passes.
	// Using a deliberately bad path ensures any execution fails loudly.
	if code, _ := runCmd(t, "set", "check"); code != 0 {
		t.Fatalf("set: %d", code)
	}
	// After set, marker matches; the command ("false") would exit 1 if run.
	if code, _ := runCmd(t, "run", "check", "--", "false"); code != 0 {
		t.Errorf("run when match: code = %d, want 0 (skipped)", code)
	}
}

func TestRun_ExecuteAndSet(t *testing.T) {
	dir := initRepo(t)
	writeRepoFile(t, dir, "seed.txt", "edited")
	if code, _ := runCmd(t, "run", "check", "--", "true"); code != 0 {
		t.Errorf("run success: code = %d, want 0", code)
	}
	if code, _ := runCmd(t, "verify", "check"); code != 0 {
		t.Errorf("verify after successful run: code = %d, want 0", code)
	}
}

func TestRun_FailureDoesNotSet(t *testing.T) {
	dir := initRepo(t)
	writeRepoFile(t, dir, "seed.txt", "edited")
	if code, _ := runCmd(t, "run", "check", "--", "false"); code != 1 {
		t.Errorf("run fail: code = %d, want 1 (child's exit)", code)
	}
	// Marker must NOT have been written.
	if code, _ := runCmd(t, "verify", "check"); code != 1 {
		t.Errorf("verify after failed run: code = %d, want 1", code)
	}
}

func TestRun_RequiresDashDash(t *testing.T) {
	initRepo(t)
	if code, _ := runCmd(t, "run", "check", "echo", "hi"); code != 2 {
		t.Errorf("run without --: code = %d, want 2", code)
	}
}

func TestSetVerify_DefaultKey(t *testing.T) {
	dir := initRepo(t)

	if code, _ := runCmd(t, "verify"); code != 1 {
		t.Errorf("initial verify (default): code = %d, want 1 (no marker)", code)
	}
	if code, _ := runCmd(t, "set"); code != 0 {
		t.Errorf("set (default): code = %d, want 0", code)
	}
	if code, _ := runCmd(t, "verify"); code != 0 {
		t.Errorf("verify after set (default): code = %d, want 0", code)
	}

	writeRepoFile(t, dir, "seed.txt", "edit")
	if code, _ := runCmd(t, "verify"); code != 1 {
		t.Errorf("verify after edit (default): code = %d, want 1", code)
	}
	if code, _ := runCmd(t, "clear"); code != 0 {
		t.Errorf("clear (default): code = %d, want 0", code)
	}
}

func TestRun_DefaultKey(t *testing.T) {
	dir := initRepo(t)
	writeRepoFile(t, dir, "seed.txt", "edit")

	if code, _ := runCmd(t, "run", "--", "true"); code != 0 {
		t.Errorf("run --default-- success: code = %d, want 0", code)
	}
	if code, _ := runCmd(t, "verify"); code != 0 {
		t.Errorf("verify after default run: code = %d, want 0", code)
	}

	if code, _ := runCmd(t, "run", "--", "true"); code != 0 {
		t.Errorf("re-run (matches): code = %d, want 0", code)
	}
}

func TestRun_TooManyKeys(t *testing.T) {
	initRepo(t)
	if code, _ := runCmd(t, "run", "a", "b", "--", "true"); code != 2 {
		t.Errorf("run with two keys before --: code = %d, want 2", code)
	}
}

func TestExcludeFlag_OnGitTree(t *testing.T) {
	dir := initRepo(t)
	writeRepoFile(t, dir, "vendor/lib.go", "lib")

	if code, _ := runCmd(t, "set", "--exclude", "vendor/**"); code != 0 {
		t.Fatalf("set: %d", code)
	}
	// Edit inside excluded path: verify still matches.
	writeRepoFile(t, dir, "vendor/lib.go", "lib edited")
	if code, _ := runCmd(t, "verify", "--exclude", "vendor/**"); code != 0 {
		t.Errorf("verify after vendor edit: %d, want 0 (excluded)", code)
	}
	// Edit outside excluded path: verify fails.
	writeRepoFile(t, dir, "seed.txt", "touched")
	if code, _ := runCmd(t, "verify", "--exclude", "vendor/**"); code != 1 {
		t.Errorf("verify after seed edit: %d, want 1 (not excluded)", code)
	}
}

func TestIncludeFlag_OnGitTree(t *testing.T) {
	dir := initRepo(t)
	writeRepoFile(t, dir, "src/a.go", "a")

	if code, _ := runCmd(t, "set", "--include", "src/**"); code != 0 {
		t.Fatalf("set: %d", code)
	}
	// Edit outside include: verify still matches.
	writeRepoFile(t, dir, "seed.txt", "touched")
	if code, _ := runCmd(t, "verify", "--include", "src/**"); code != 0 {
		t.Errorf("verify after out-of-scope edit: %d, want 0", code)
	}
	// Edit inside include: verify fails.
	writeRepoFile(t, dir, "src/a.go", "a edited")
	if code, _ := runCmd(t, "verify", "--include", "src/**"); code != 1 {
		t.Errorf("verify after in-scope edit: %d, want 1", code)
	}
}

func TestHashFlag_SwitchesToFiles(t *testing.T) {
	dir := initRepo(t)
	writeRepoFile(t, dir, "src/a.go", "a")

	if code, _ := runCmd(t, "set", "--hash", "files", "--include", "src/**"); code != 0 {
		t.Fatalf("set: %d", code)
	}
	// Commit a new file *outside* include: files hash ignores HEAD so
	// verify still matches.
	writeRepoFile(t, dir, "docs/x.md", "x")
	// (don't actually commit; just touching an out-of-scope file is enough)
	if code, _ := runCmd(t, "verify", "--hash", "files", "--include", "src/**"); code != 0 {
		t.Errorf("verify after out-of-scope change: %d, want 0 (files hash)", code)
	}
}

func TestHashFlag_FilesRequiresInclude(t *testing.T) {
	initRepo(t)
	if code, _ := runCmd(t, "set", "--hash", "files"); code != 2 {
		t.Errorf("hash=files without include: %d, want 2", code)
	}
}

func TestHashFlag_Unknown(t *testing.T) {
	initRepo(t)
	if code, _ := runCmd(t, "set", "--hash", "bogus"); code != 2 {
		t.Errorf("unknown hash: %d, want 2", code)
	}
}

func TestInit_Generates(t *testing.T) {
	dir := initRepo(t)
	if code, _ := runCmd(t, "init"); code != 0 {
		t.Fatalf("init: %d", code)
	}
	p := filepath.Join(dir, ".markgate.yml")
	info, err := os.Stat(p)
	if err != nil {
		t.Fatalf("expected .markgate.yml to exist: %v", err)
	}
	if info.Size() == 0 {
		t.Error("generated .markgate.yml is empty")
	}
}

func TestInit_ExistingBlocks(t *testing.T) {
	dir := initRepo(t)
	writeRepoFile(t, dir, ".markgate.yml", "pre-existing\n")
	if code, _ := runCmd(t, "init"); code != 2 {
		t.Errorf("init over existing: %d, want 2", code)
	}
	if code, _ := runCmd(t, "init", "--force"); code != 0 {
		t.Errorf("init --force: %d, want 0", code)
	}
	// After --force, content should match the skeleton (first line includes the header comment).
	body, err := os.ReadFile(filepath.Join(dir, ".markgate.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(body, []byte("markgate configuration")) {
		t.Errorf("init --force did not overwrite with skeleton, got:\n%s", body)
	}
}

func TestVersion_PrintsInjected(t *testing.T) {
	root := newRootCmd("v1.2.3")
	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"version"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if got := stdout.String(); got != "v1.2.3\n" {
		t.Errorf("version output = %q, want %q", got, "v1.2.3\n")
	}
}

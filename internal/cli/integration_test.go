package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// withClock pins the package's clock to a fixed instant for the duration
// of t and restores the real clock when t finishes.
func withClock(t *testing.T, instant time.Time) {
	t.Helper()
	prev := now
	now = func() time.Time { return instant }
	t.Cleanup(func() { now = prev })
}

// runCmd drives the root command with the given args and returns the exit
// code (0 match, 1 mismatch, 2 error) plus captured stdout.
func runCmd(t *testing.T, args ...string) (int, string) {
	t.Helper()
	code, stdout, _ := runCmdStderr(t, args...)
	return code, stdout
}

// runCmdStderr is the long form of runCmd: same exit-code semantics, but
// also returns the captured stderr. Used by --explain tests where the
// scope listing is written to stderr.
func runCmdStderr(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	root := newRootCmd("test")
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs(args)

	err := root.Execute()
	if err == nil {
		return 0, stdout.String(), stderr.String()
	}
	var ee *ExitError
	if errors.As(err, &ee) {
		return ee.Code, stdout.String(), stderr.String()
	}
	// Mirrors Execute(): unknown errors (e.g. cobra Args validation
	// rejections) map to exit code 2.
	return 2, stdout.String(), stderr.String()
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

func TestStateDir_FlagAbsolutePath(t *testing.T) {
	initRepo(t)
	stateDir := t.TempDir()

	if code, _ := runCmd(t, "set", "check", "--state-dir", stateDir); code != 0 {
		t.Fatalf("set: %d", code)
	}
	// Marker must be at <stateDir>/<key>.json, NOT <stateDir>/markgate/<key>.json.
	if _, err := os.Stat(filepath.Join(stateDir, "check.json")); err != nil {
		t.Errorf("marker not at <dir>/<key>.json: %v", err)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "markgate", "check.json")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("unexpected markgate/ subdir under --state-dir")
	}

	if code, _ := runCmd(t, "verify", "check", "--state-dir", stateDir); code != 0 {
		t.Errorf("verify after set: %d, want 0", code)
	}
	if code, _ := runCmd(t, "clear", "check", "--state-dir", stateDir); code != 0 {
		t.Errorf("clear: %d, want 0", code)
	}
	if code, _ := runCmd(t, "verify", "check", "--state-dir", stateDir); code != 1 {
		t.Errorf("verify after clear: %d, want 1", code)
	}
}

func TestStateDir_FlagRelativeResolvesToRepoRoot(t *testing.T) {
	dir := initRepo(t)
	// Relative path must resolve from repo top-level, not cwd.
	sub := filepath.Join(dir, "nested")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(sub)

	if code, _ := runCmd(t, "set", "check", "--state-dir", ".cache/markgate"); code != 0 {
		t.Fatalf("set: %d", code)
	}
	want := filepath.Join(dir, ".cache", "markgate", "check.json")
	if _, err := os.Stat(want); err != nil {
		t.Errorf("marker not at repo-root-relative path %s: %v", want, err)
	}
}

func TestStateDir_EnvVar(t *testing.T) {
	initRepo(t)
	stateDir := t.TempDir()
	t.Setenv("MARKGATE_STATE_DIR", stateDir)

	if code, _ := runCmd(t, "set", "check"); code != 0 {
		t.Fatalf("set: %d", code)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "check.json")); err != nil {
		t.Errorf("env-var marker not found: %v", err)
	}
	if code, _ := runCmd(t, "verify", "check"); code != 0 {
		t.Errorf("verify via env: %d, want 0", code)
	}
}

func TestStateDir_FlagBeatsEnv(t *testing.T) {
	initRepo(t)
	envDir := t.TempDir()
	flagDir := t.TempDir()
	t.Setenv("MARKGATE_STATE_DIR", envDir)

	if code, _ := runCmd(t, "set", "check", "--state-dir", flagDir); code != 0 {
		t.Fatalf("set: %d", code)
	}
	// Flag wins: file is in flagDir, not envDir.
	if _, err := os.Stat(filepath.Join(flagDir, "check.json")); err != nil {
		t.Errorf("flag path not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(envDir, "check.json")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("env path should not have been written when flag is set")
	}
}

func TestStateDir_DoesNotTouchDefaultLocation(t *testing.T) {
	dir := initRepo(t)
	stateDir := t.TempDir()

	if code, _ := runCmd(t, "set", "check", "--state-dir", stateDir); code != 0 {
		t.Fatalf("set: %d", code)
	}
	// Default .git/markgate/<key>.json must not exist: override is exclusive.
	defaultPath := filepath.Join(dir, ".git", "markgate", "check.json")
	if _, err := os.Stat(defaultPath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("default marker path should not exist when --state-dir is used")
	}
}

func TestStateDir_FromConfigAbsolutePath(t *testing.T) {
	dir := initRepo(t)
	absDir := t.TempDir()
	writeRepoFile(t, dir, ".markgate.yml",
		"gates:\n  check:\n    hash: git-tree\n    state_dir: "+absDir+"\n")

	if code, _ := runCmd(t, "set", "check"); code != 0 {
		t.Fatalf("set: %d", code)
	}
	if _, err := os.Stat(filepath.Join(absDir, "check.json")); err != nil {
		t.Errorf("marker not at config-specified absolute path: %v", err)
	}
	if code, _ := runCmd(t, "verify", "check"); code != 0 {
		t.Errorf("verify: %d, want 0", code)
	}
}

func TestStateDir_ConfigWithFilesHash(t *testing.T) {
	dir := initRepo(t)
	writeRepoFile(t, dir, "src/a.ts", "a")
	writeRepoFile(t, dir, ".markgate.yml",
		"gates:\n  coverage:\n    hash: files\n    include:\n      - \"src/**\"\n    state_dir: .mg\n")

	if code, _ := runCmd(t, "set", "coverage"); code != 0 {
		t.Fatalf("set: %d", code)
	}
	// Marker must be at config state_dir (files hash + state_dir combine correctly).
	if _, err := os.Stat(filepath.Join(dir, ".mg", "coverage.json")); err != nil {
		t.Errorf("marker not at config state_dir for files hash: %v", err)
	}
	// Out-of-scope change does not invalidate (files hash).
	writeRepoFile(t, dir, "docs/x.md", "x")
	if code, _ := runCmd(t, "verify", "coverage"); code != 0 {
		t.Errorf("verify after out-of-scope edit: %d, want 0", code)
	}
	// In-scope change does invalidate (files hash).
	writeRepoFile(t, dir, "src/a.ts", "edited")
	if code, _ := runCmd(t, "verify", "coverage"); code != 1 {
		t.Errorf("verify after in-scope edit: %d, want 1", code)
	}
}

func TestStateDir_FromConfig(t *testing.T) {
	dir := initRepo(t)
	writeRepoFile(t, dir, ".markgate.yml",
		"gates:\n  check:\n    hash: git-tree\n    state_dir: .mg-cache\n")

	if code, _ := runCmd(t, "set", "check"); code != 0 {
		t.Fatalf("set: %d", code)
	}
	want := filepath.Join(dir, ".mg-cache", "check.json")
	if _, err := os.Stat(want); err != nil {
		t.Errorf("marker not at config-specified path %s: %v", want, err)
	}
}

func TestStateDir_EnvBeatsConfig(t *testing.T) {
	dir := initRepo(t)
	envDir := t.TempDir()
	writeRepoFile(t, dir, ".markgate.yml",
		"gates:\n  check:\n    state_dir: .mg-cache\n")
	t.Setenv("MARKGATE_STATE_DIR", envDir)

	if code, _ := runCmd(t, "set", "check"); code != 0 {
		t.Fatalf("set: %d", code)
	}
	if _, err := os.Stat(filepath.Join(envDir, "check.json")); err != nil {
		t.Errorf("env path should win over config: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".mg-cache", "check.json")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("config path should have been shadowed by env")
	}
}

func TestStateDir_FlagBeatsConfig(t *testing.T) {
	dir := initRepo(t)
	flagDir := t.TempDir()
	writeRepoFile(t, dir, ".markgate.yml",
		"gates:\n  check:\n    state_dir: .mg-cache\n")

	if code, _ := runCmd(t, "set", "check", "--state-dir", flagDir); code != 0 {
		t.Fatalf("set: %d", code)
	}
	if _, err := os.Stat(filepath.Join(flagDir, "check.json")); err != nil {
		t.Errorf("flag path should win over config: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".mg-cache", "check.json")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("config path should have been shadowed by flag")
	}
}

func TestStateDir_StatusCmd(t *testing.T) {
	initRepo(t)
	stateDir := t.TempDir()

	code, out := runCmd(t, "status", "check", "--state-dir", stateDir)
	if code != 1 {
		t.Errorf("status with no marker: code = %d, want 1", code)
	}
	if !bytes.Contains([]byte(out), []byte("no marker")) {
		t.Errorf("status output missing 'no marker' line:\n%s", out)
	}

	if setCode, _ := runCmd(t, "set", "check", "--state-dir", stateDir); setCode != 0 {
		t.Fatalf("set: %d", setCode)
	}
	code, out = runCmd(t, "status", "check", "--state-dir", stateDir)
	if code != 0 {
		t.Errorf("status after set: code = %d, want 0", code)
	}
	if !bytes.Contains([]byte(out), []byte("state:      match")) {
		t.Errorf("status output missing match line:\n%s", out)
	}
}

func TestStateDir_RunCmd(t *testing.T) {
	dir := initRepo(t)
	stateDir := t.TempDir()
	writeRepoFile(t, dir, "seed.txt", "edited")

	if code, _ := runCmd(t, "run", "check", "--state-dir", stateDir, "--", "true"); code != 0 {
		t.Errorf("run success: %d, want 0", code)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "check.json")); err != nil {
		t.Errorf("run did not persist marker to --state-dir: %v", err)
	}
	// Second run should skip (match on same state).
	if code, _ := runCmd(t, "run", "check", "--state-dir", stateDir, "--", "false"); code != 0 {
		t.Errorf("run skip: %d, want 0 (should not execute child)", code)
	}
}

func TestConfigLint_CleanConfig(t *testing.T) {
	dir := initRepo(t)
	writeRepoFile(t, dir, "src/a.go", "a")
	writeRepoFile(t, dir, ".markgate.yml",
		"gates:\n  check:\n    hash: files\n    include:\n      - \"src/**\"\n")

	code, out := runCmd(t, "config", "lint")
	if code != 0 {
		t.Errorf("clean config: code = %d, want 0; out: %q", code, out)
	}
	if out != "" {
		t.Errorf("clean config: want empty stdout, got %q", out)
	}
}

func TestConfigLint_DeadIncludeGlob(t *testing.T) {
	dir := initRepo(t)
	writeRepoFile(t, dir, ".markgate.yml",
		"gates:\n  docs:\n    hash: files\n    include:\n      - \"README.md\"\n      - \"docss/**\"\n")
	writeRepoFile(t, dir, "README.md", "x")

	code, out := runCmd(t, "config", "lint")
	if code != 1 {
		t.Errorf("dead include: code = %d, want 1", code)
	}
	if !bytes.Contains([]byte(out), []byte("gates.docs.include[1]: 'docss/**' matches 0 files")) {
		t.Errorf("missing dead-include warning, got:\n%s", out)
	}
	if bytes.Contains([]byte(out), []byte("include[0]")) {
		t.Errorf("unexpected warning on healthy glob:\n%s", out)
	}
}

func TestConfigLint_DeadExcludeGlob(t *testing.T) {
	dir := initRepo(t)
	writeRepoFile(t, dir, "src/a.go", "a")
	writeRepoFile(t, dir, ".markgate.yml",
		"gates:\n  api:\n    hash: files\n    include:\n      - \"src/**\"\n    exclude:\n      - \"*.proto\"\n")

	code, out := runCmd(t, "config", "lint")
	if code != 1 {
		t.Errorf("dead exclude: code = %d, want 1", code)
	}
	if !bytes.Contains([]byte(out), []byte("gates.api.exclude[0]: '*.proto' matches 0 files")) {
		t.Errorf("missing dead-exclude warning, got:\n%s", out)
	}
}

func TestConfigLint_UnknownTopLevelField(t *testing.T) {
	dir := initRepo(t)
	writeRepoFile(t, dir, ".markgate.yml",
		"gates:\n  check: {}\nweird_top: 1\n")

	code, out := runCmd(t, "config", "lint")
	if code != 1 {
		t.Errorf("unknown top: code = %d, want 1", code)
	}
	if !bytes.Contains([]byte(out), []byte("unknown field: weird_top")) {
		t.Errorf("missing unknown-field warning, got:\n%s", out)
	}
}

func TestConfigLint_UnknownGateField(t *testing.T) {
	dir := initRepo(t)
	writeRepoFile(t, dir, ".markgate.yml",
		"gates:\n  legacy:\n    hash: git-tree\n    legacy_field: foo\n")

	code, out := runCmd(t, "config", "lint")
	if code != 1 {
		t.Errorf("unknown gate field: code = %d, want 1", code)
	}
	if !bytes.Contains([]byte(out), []byte("unknown field: gates.legacy.legacy_field")) {
		t.Errorf("missing unknown-gate-field warning, got:\n%s", out)
	}
}

func TestConfigLint_ParseError(t *testing.T) {
	dir := initRepo(t)
	writeRepoFile(t, dir, ".markgate.yml", "gates:\n  - bad\n :::not yaml\n")

	code, _ := runCmd(t, "config", "lint")
	if code != 2 {
		t.Errorf("parse error: code = %d, want 2", code)
	}
}

func TestConfigLint_MissingConfig(t *testing.T) {
	initRepo(t)
	code, _ := runCmd(t, "config", "lint")
	if code != 2 {
		t.Errorf("missing config: code = %d, want 2", code)
	}
}

func TestConfigLint_JSONOutput(t *testing.T) {
	dir := initRepo(t)
	writeRepoFile(t, dir, ".markgate.yml",
		"gates:\n  docs:\n    hash: files\n    include:\n      - \"docss/**\"\n")

	code, out := runCmd(t, "config", "lint", "--json")
	if code != 1 {
		t.Errorf("json dead glob: code = %d, want 1", code)
	}
	var findings []struct {
		Path     string `json:"path"`
		Severity string `json:"severity"`
		Message  string `json:"message"`
	}
	if err := json.Unmarshal([]byte(out), &findings); err != nil {
		t.Fatalf("invalid JSON: %v\nout: %s", err, out)
	}
	if len(findings) != 1 {
		t.Fatalf("findings = %d, want 1; out: %s", len(findings), out)
	}
	if findings[0].Path != "gates.docs.include[0]" {
		t.Errorf("path = %q, want gates.docs.include[0]", findings[0].Path)
	}
	if findings[0].Severity != "warning" {
		t.Errorf("severity = %q, want warning", findings[0].Severity)
	}
}

func TestConfigLint_JSONCleanIsEmptyArray(t *testing.T) {
	dir := initRepo(t)
	writeRepoFile(t, dir, "src/a.go", "a")
	writeRepoFile(t, dir, ".markgate.yml",
		"gates:\n  check:\n    hash: files\n    include:\n      - \"src/**\"\n")

	code, out := runCmd(t, "config", "lint", "--json")
	if code != 0 {
		t.Errorf("clean json: code = %d, want 0", code)
	}
	var findings []any
	if err := json.Unmarshal([]byte(out), &findings); err != nil {
		t.Fatalf("invalid JSON: %v\nout: %s", err, out)
	}
	if len(findings) != 0 {
		t.Errorf("clean json findings = %d, want 0", len(findings))
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

func TestCompletion_GeneratesScripts(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish", "powershell"} {
		t.Run(shell, func(t *testing.T) {
			code, out := runCmd(t, "completion", shell)
			if code != 0 {
				t.Fatalf("completion %s: code = %d, want 0", shell, code)
			}
			if len(out) == 0 {
				t.Errorf("completion %s: empty script", shell)
			}
		})
	}
}

func TestCompletion_UnknownShellErrors(t *testing.T) {
	if code, _ := runCmd(t, "completion", "totallybogus"); code != 2 {
		t.Errorf("unknown shell: code = %d, want 2", code)
	}
}

func TestCompletion_GateKeysFromConfig(t *testing.T) {
	dir := initRepo(t)
	writeRepoFile(t, dir, ".markgate.yml",
		"gates:\n  alpha:\n    hash: git-tree\n  beta:\n    hash: git-tree\n")

	code, out := runCmd(t, "__complete", "verify", "")
	if code != 0 {
		t.Fatalf("__complete: code = %d, want 0\nout: %s", code, out)
	}
	if !bytes.Contains([]byte(out), []byte("alpha")) || !bytes.Contains([]byte(out), []byte("beta")) {
		t.Errorf("expected alpha and beta in completion output, got:\n%s", out)
	}
}

func TestCompletion_NoConfigSilent(t *testing.T) {
	initRepo(t)
	code, out := runCmd(t, "__complete", "verify", "")
	if code != 0 {
		t.Fatalf("__complete with no config: code = %d, want 0\nout: %s", code, out)
	}
	// No suggestions surface as a body that is just the directive line(s),
	// so neither an alpha-style key nor an error string should appear.
	if bytes.Contains([]byte(out), []byte("Error")) {
		t.Errorf("completion errored on missing config:\n%s", out)
	}
}

func TestTTL_ExpiredVerifyFails(t *testing.T) {
	dir := initRepo(t)
	writeRepoFile(t, dir, ".markgate.yml",
		"gates:\n  integ-destroy:\n    hash: git-tree\n    ttl: 7d\n")

	t0 := time.Now().UTC()
	withClock(t, t0)
	if code, _ := runCmd(t, "set", "integ-destroy"); code != 0 {
		t.Fatalf("set: %d", code)
	}

	withClock(t, t0.Add(8*24*time.Hour+3*time.Hour))
	if code, _ := runCmd(t, "verify", "integ-destroy"); code != 1 {
		t.Errorf("verify after ttl expiry: code = %d, want 1", code)
	}
}

func TestTTL_FreshVerifyPasses(t *testing.T) {
	dir := initRepo(t)
	writeRepoFile(t, dir, ".markgate.yml",
		"gates:\n  integ-destroy:\n    hash: git-tree\n    ttl: 7d\n")

	t0 := time.Now().UTC()
	withClock(t, t0)
	if code, _ := runCmd(t, "set", "integ-destroy"); code != 0 {
		t.Fatalf("set: %d", code)
	}

	withClock(t, t0.Add(3*24*time.Hour))
	if code, _ := runCmd(t, "verify", "integ-destroy"); code != 0 {
		t.Errorf("verify within ttl: code = %d, want 0", code)
	}
}

func TestTTL_SetResets(t *testing.T) {
	dir := initRepo(t)
	writeRepoFile(t, dir, ".markgate.yml",
		"gates:\n  integ-destroy:\n    hash: git-tree\n    ttl: 7d\n")

	t0 := time.Now().UTC()
	withClock(t, t0)
	if code, _ := runCmd(t, "set", "integ-destroy"); code != 0 {
		t.Fatalf("set: %d", code)
	}
	// Advance past the TTL...
	t1 := t0.Add(8 * 24 * time.Hour)
	withClock(t, t1)
	// ...re-set restarts the countdown.
	if code, _ := runCmd(t, "set", "integ-destroy"); code != 0 {
		t.Fatalf("re-set: %d", code)
	}
	if code, _ := runCmd(t, "verify", "integ-destroy"); code != 0 {
		t.Errorf("verify just after re-set: code = %d, want 0", code)
	}
	// Still within the new TTL window.
	withClock(t, t1.Add(6*24*time.Hour))
	if code, _ := runCmd(t, "verify", "integ-destroy"); code != 0 {
		t.Errorf("verify within renewed ttl: code = %d, want 0", code)
	}
}

func TestTTL_ParseFormats(t *testing.T) {
	for _, s := range []string{"7d", "2w", "12h", "1h30m", "1d12h"} {
		dir := initRepo(t)
		writeRepoFile(t, dir, ".markgate.yml",
			"gates:\n  g:\n    hash: git-tree\n    ttl: "+s+"\n")
		if code, _ := runCmd(t, "set", "g"); code != 0 {
			t.Errorf("ttl=%q: set: code = %d, want 0", s, code)
		}
		if code, _ := runCmd(t, "verify", "g"); code != 0 {
			t.Errorf("ttl=%q: verify: code = %d, want 0", s, code)
		}
	}
}

func TestTTL_RejectsMonths(t *testing.T) {
	dir := initRepo(t)

	writeRepoFile(t, dir, ".markgate.yml",
		"gates:\n  g:\n    hash: git-tree\n    ttl: 1m\n")
	if code, _ := runCmd(t, "set", "g"); code != 0 {
		t.Errorf("ttl=1m (minutes, Go-standard): code = %d, want 0", code)
	}

	writeRepoFile(t, dir, ".markgate.yml",
		"gates:\n  g:\n    hash: git-tree\n    ttl: 1mo\n")
	if code, _ := runCmd(t, "set", "g"); code != 2 {
		t.Errorf("ttl=1mo (months, unsupported): code = %d, want 2", code)
	}
}

func TestTTL_MalformedRejectedAtConfigLoad(t *testing.T) {
	dir := initRepo(t)
	writeRepoFile(t, dir, ".markgate.yml",
		"gates:\n  g:\n    hash: git-tree\n    ttl: foo\n")
	if code, _ := runCmd(t, "set", "g"); code != 2 {
		t.Errorf("malformed ttl: code = %d, want 2", code)
	}
	if code, _ := runCmd(t, "verify", "g"); code != 2 {
		t.Errorf("malformed ttl on verify: code = %d, want 2", code)
	}
}

func TestExplain_VerifyNoMarkerListsScope(t *testing.T) {
	dir := initRepo(t)
	writeRepoFile(t, dir, "src/a.go", "a")

	code, stdout, stderr := runCmdStderr(t, "verify", "check", "--explain")
	if code != 1 {
		t.Errorf("verify with no marker: code = %d, want 1", code)
	}
	if stdout != "" {
		t.Errorf("--explain text mode wrote to stdout: %q", stdout)
	}
	if !bytes.Contains([]byte(stderr), []byte("scope:")) {
		t.Errorf("stderr missing 'scope:' header:\n%s", stderr)
	}
	if !bytes.Contains([]byte(stderr), []byte("src/a.go")) {
		t.Errorf("stderr missing untracked file in scope:\n%s", stderr)
	}
	if !bytes.Contains([]byte(stderr), []byte("state: no marker")) {
		t.Errorf("stderr missing 'state: no marker':\n%s", stderr)
	}
}

func TestExplain_VerifyMismatchListsScope(t *testing.T) {
	dir := initRepo(t)
	if code, _ := runCmd(t, "set", "check"); code != 0 {
		t.Fatalf("set: %d", code)
	}
	writeRepoFile(t, dir, "seed.txt", "edited")

	code, _, stderr := runCmdStderr(t, "verify", "check", "-e")
	if code != 1 {
		t.Errorf("verify after edit: code = %d, want 1", code)
	}
	if !bytes.Contains([]byte(stderr), []byte("seed.txt")) {
		t.Errorf("stderr missing edited file in scope:\n%s", stderr)
	}
	if !bytes.Contains([]byte(stderr), []byte("state: mismatch")) {
		t.Errorf("stderr missing mismatch state:\n%s", stderr)
	}
}

func TestExplain_VerifyMatchPreservesExitZero(t *testing.T) {
	initRepo(t)
	if code, _ := runCmd(t, "set", "check"); code != 0 {
		t.Fatalf("set: %d", code)
	}
	code, _, stderr := runCmdStderr(t, "verify", "check", "--explain")
	if code != 0 {
		t.Errorf("verify (match) with --explain: code = %d, want 0", code)
	}
	if !bytes.Contains([]byte(stderr), []byte("state: match")) {
		t.Errorf("stderr missing match state:\n%s", stderr)
	}
}

func TestExplain_RespectsIncludeOnly(t *testing.T) {
	dir := initRepo(t)
	writeRepoFile(t, dir, "src/a.go", "a")
	writeRepoFile(t, dir, "docs/x.md", "x")

	_, _, stderr := runCmdStderr(t, "verify", "check", "--include", "src/**", "-e")
	if !bytes.Contains([]byte(stderr), []byte("src/a.go")) {
		t.Errorf("stderr missing included path:\n%s", stderr)
	}
	if bytes.Contains([]byte(stderr), []byte("docs/x.md")) {
		t.Errorf("stderr should not list out-of-include path:\n%s", stderr)
	}
}

func TestExplain_RespectsExcludeFilter(t *testing.T) {
	dir := initRepo(t)
	writeRepoFile(t, dir, "vendor/lib.go", "lib")
	writeRepoFile(t, dir, "src/a.go", "a")

	_, _, stderr := runCmdStderr(t, "verify", "check", "--exclude", "vendor/**", "-e")
	if !bytes.Contains([]byte(stderr), []byte("src/a.go")) {
		t.Errorf("stderr missing non-excluded path:\n%s", stderr)
	}
	if bytes.Contains([]byte(stderr), []byte("vendor/lib.go")) {
		t.Errorf("stderr should not list excluded path:\n%s", stderr)
	}
}

func TestExplain_VerifyJSON(t *testing.T) {
	dir := initRepo(t)
	writeRepoFile(t, dir, "src/a.go", "a")

	code, stdout, stderr := runCmdStderr(t, "verify", "check", "--explain", "--json")
	if code != 1 {
		t.Errorf("verify (no marker) JSON: code = %d, want 1", code)
	}
	if stderr != "" {
		t.Errorf("--explain --json wrote to stderr: %q", stderr)
	}
	var doc struct {
		Key    string   `json:"key"`
		Scope  []string `json:"scope"`
		Hasher string   `json:"hasher"`
		State  string   `json:"state"`
	}
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("stdout not JSON: %v\n%s", err, stdout)
	}
	if doc.Key != "check" {
		t.Errorf("key = %q, want check", doc.Key)
	}
	if doc.Hasher != "git-tree" {
		t.Errorf("hasher = %q, want git-tree", doc.Hasher)
	}
	if doc.State != "no marker" {
		t.Errorf("state = %q, want %q", doc.State, "no marker")
	}
	found := false
	for _, p := range doc.Scope {
		if p == "src/a.go" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("scope missing src/a.go: %v", doc.Scope)
	}
}

func TestExplain_JSONRequiresExplain(t *testing.T) {
	initRepo(t)
	if code, _ := runCmd(t, "verify", "check", "--json"); code != 2 {
		t.Errorf("--json without --explain: code = %d, want 2", code)
	}
}

func TestExplain_StatusListsScope(t *testing.T) {
	dir := initRepo(t)
	if code, _ := runCmd(t, "set", "check"); code != 0 {
		t.Fatalf("set: %d", code)
	}
	writeRepoFile(t, dir, "seed.txt", "edited")

	code, stdout, stderr := runCmdStderr(t, "status", "check", "-e")
	if code != 1 {
		t.Errorf("status mismatch: code = %d, want 1", code)
	}
	if !bytes.Contains([]byte(stderr), []byte("scope:")) {
		t.Errorf("stderr missing scope listing:\n%s", stderr)
	}
	if !bytes.Contains([]byte(stdout), []byte("state:      mismatch")) {
		t.Errorf("stdout missing existing status block:\n%s", stdout)
	}
}

func TestExplain_StatusJSON(t *testing.T) {
	dir := initRepo(t)
	if code, _ := runCmd(t, "set", "check"); code != 0 {
		t.Fatalf("set: %d", code)
	}
	writeRepoFile(t, dir, "seed.txt", "edited")

	code, stdout, _ := runCmdStderr(t, "status", "check", "--explain", "--json")
	if code != 1 {
		t.Errorf("status JSON mismatch: code = %d, want 1", code)
	}
	var doc struct {
		Key    string   `json:"key"`
		Scope  []string `json:"scope"`
		Hasher string   `json:"hasher"`
		State  string   `json:"state"`
	}
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("stdout not JSON: %v\n%s", err, stdout)
	}
	if doc.State != "mismatch" {
		t.Errorf("state = %q, want mismatch", doc.State)
	}
	if doc.Hasher != "git-tree" {
		t.Errorf("hasher = %q", doc.Hasher)
	}
}

func TestExplain_RunListsScopeOnSkip(t *testing.T) {
	initRepo(t)
	if code, _ := runCmd(t, "set", "check"); code != 0 {
		t.Fatalf("set: %d", code)
	}
	// Match → child must not run; --explain still emits scope + state.
	code, _, stderr := runCmdStderr(t, "run", "check", "-e", "--", "false")
	if code != 0 {
		t.Errorf("run skip with --explain: code = %d, want 0", code)
	}
	if !bytes.Contains([]byte(stderr), []byte("state: match")) {
		t.Errorf("stderr missing state line:\n%s", stderr)
	}
}

func TestExplain_RunJSONOnSkip(t *testing.T) {
	initRepo(t)
	if code, _ := runCmd(t, "set", "check"); code != 0 {
		t.Fatalf("set: %d", code)
	}
	code, stdout, _ := runCmdStderr(t, "run", "check", "--explain", "--json", "--", "false")
	if code != 0 {
		t.Errorf("run skip JSON: code = %d, want 0", code)
	}
	var doc struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("stdout not JSON: %v\n%s", err, stdout)
	}
	if doc.State != "match" {
		t.Errorf("state = %q, want match", doc.State)
	}
}

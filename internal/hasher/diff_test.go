package hasher

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"

	"github.com/go-to-k/markgate/internal/gitutil"
)

// newDiffRepo returns a repo whose main branch holds src/a.go, src/b.go
// and docs/x.md, with a feature branch checked out.
func newDiffRepo(t *testing.T) (*gitutil.Repo, string) {
	t.Helper()
	repo, dir := newTestRepo(t)
	writeFile(t, dir, "src/a.go", "a1\na2\na3\na4\na5\n")
	writeFile(t, dir, "src/b.go", "b1\nb2\nb3\nb4\nb5\n")
	writeFile(t, dir, "docs/x.md", "x\n")
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-qm", "seed")
	runGit(t, dir, "checkout", "-q", "-b", "feat")
	return repo, dir
}

func srcDiff() Diff {
	return Diff{Include: []string{"src/**"}, Base: "main"}
}

func mustHash(t *testing.T, h Hasher, repo *gitutil.Repo) string {
	t.Helper()
	d, err := h.Hash(repo)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	return d
}

func TestDiff_UnrelatedBaseBranchChangeStaysFresh(t *testing.T) {
	repo, dir := newDiffRepo(t)
	writeFile(t, dir, "src/a.go", "a1\nMINE\na3\na4\na5\n")
	runGit(t, dir, "commit", "-qam", "my work")

	h := srcDiff()
	before := mustHash(t, h, repo)

	// An in-scope file this branch never touched changes on the base
	// branch, and the branch merges it in.
	runGit(t, dir, "checkout", "-q", "main")
	writeFile(t, dir, "src/b.go", "b1\nb2\nb3\nb4\nBASE\n")
	runGit(t, dir, "commit", "-qam", "unrelated base work")
	runGit(t, dir, "checkout", "-q", "feat")
	runGit(t, dir, "merge", "-q", "--no-edit", "main")

	after := mustHash(t, h, repo)
	if before != after {
		t.Errorf("unrelated base-branch change invalidated the digest: %s -> %s", before, after)
	}
}

func TestDiff_SameFileBaseBranchChangeGoesStale(t *testing.T) {
	repo, dir := newDiffRepo(t)
	writeFile(t, dir, "src/a.go", "MINE\na2\na3\na4\na5\n")
	runGit(t, dir, "commit", "-qam", "my work")

	h := srcDiff()
	before := mustHash(t, h, repo)

	// The base branch touches the same file, far enough away to merge
	// cleanly: the combination is unverified, so the marker must go stale.
	runGit(t, dir, "checkout", "-q", "main")
	writeFile(t, dir, "src/a.go", "a1\na2\na3\na4\nBASE\n")
	runGit(t, dir, "commit", "-qam", "base touches the same file")
	runGit(t, dir, "checkout", "-q", "feat")
	runGit(t, dir, "merge", "-q", "--no-edit", "main")

	after := mustHash(t, h, repo)
	if before == after {
		t.Error("base-branch change to a file this branch also changed did not invalidate the digest")
	}
}

func TestDiff_UncommittedInScopeEditGoesStale(t *testing.T) {
	repo, dir := newDiffRepo(t)
	h := srcDiff()
	writeFile(t, dir, "src/a.go", "a1\nMINE\na3\na4\na5\n")
	runGit(t, dir, "commit", "-qam", "my work")
	before := mustHash(t, h, repo)

	writeFile(t, dir, "src/a.go", "a1\nMINE AGAIN\na3\na4\na5\n")
	if after := mustHash(t, h, repo); before == after {
		t.Error("uncommitted in-scope edit did not invalidate the digest")
	}
}

func TestDiff_OutOfScopeEditStaysFresh(t *testing.T) {
	repo, dir := newDiffRepo(t)
	h := srcDiff()
	writeFile(t, dir, "src/a.go", "a1\nMINE\na3\na4\na5\n")
	runGit(t, dir, "commit", "-qam", "my work")
	before := mustHash(t, h, repo)

	writeFile(t, dir, "docs/x.md", "edited outside the include set\n")
	runGit(t, dir, "commit", "-qam", "docs")
	if after := mustHash(t, h, repo); before != after {
		t.Errorf("out-of-scope edit invalidated the digest: %s -> %s", before, after)
	}
}

func TestDiff_UntrackedInScopeFileGoesStale(t *testing.T) {
	repo, dir := newDiffRepo(t)
	h := srcDiff()
	writeFile(t, dir, "src/a.go", "a1\nMINE\na3\na4\na5\n")
	runGit(t, dir, "commit", "-qam", "my work")
	before := mustHash(t, h, repo)

	// Untracked files are absent from `git diff`; the hasher folds them
	// in explicitly so a brand-new source file is never invisible.
	writeFile(t, dir, "src/new.go", "brand new\n")
	if after := mustHash(t, h, repo); before == after {
		t.Error("untracked in-scope file did not invalidate the digest")
	}
}

func TestDiff_DeletionGoesStale(t *testing.T) {
	repo, dir := newDiffRepo(t)
	h := srcDiff()
	writeFile(t, dir, "src/a.go", "a1\nMINE\na3\na4\na5\n")
	runGit(t, dir, "commit", "-qam", "my work")
	before := mustHash(t, h, repo)

	runGit(t, dir, "rm", "-q", "src/b.go")
	if after := mustHash(t, h, repo); before == after {
		t.Error("in-scope deletion did not invalidate the digest")
	}
}

func TestDiff_StagingInvariant(t *testing.T) {
	repo, dir := newDiffRepo(t)
	h := srcDiff()
	writeFile(t, dir, "src/a.go", "a1\nMINE\na3\na4\na5\n")
	before := mustHash(t, h, repo)

	runGit(t, dir, "add", "src/a.go")
	if after := mustHash(t, h, repo); before != after {
		t.Errorf("digest changed across git add: %s -> %s", before, after)
	}
}

func TestDiff_CommittingOwnWorkIsNotAChange(t *testing.T) {
	repo, dir := newDiffRepo(t)
	h := srcDiff()
	writeFile(t, dir, "src/a.go", "a1\nMINE\na3\na4\na5\n")
	before := mustHash(t, h, repo)

	// Unlike git-tree, committing the very content that was verified
	// leaves the digest alone: the delta against the merge base is the
	// same either way.
	runGit(t, dir, "commit", "-qam", "commit the verified content")
	if after := mustHash(t, h, repo); before != after {
		t.Errorf("committing already-hashed content changed the digest: %s -> %s", before, after)
	}
}

// TestDiff_DigestIsInvariantUnderGitConfig is the reason the digest is
// built from blob identity plus worktree bytes rather than from the text
// `git diff` prints: patch rendering is configurable per user, so a
// text-derived digest would differ between two machines holding the same
// content — turning every shared marker into a permanent miss.
func TestDiff_DigestIsInvariantUnderGitConfig(t *testing.T) {
	repo, dir := newDiffRepo(t)
	writeFile(t, dir, "src/a.go", "a1\nMINE\na3\na4\na5\n")
	runGit(t, dir, "commit", "-qam", "my work")
	writeFile(t, dir, "src/new.go", "brand new\n")

	h := srcDiff()
	want := mustHash(t, h, repo)

	cfg := [][2]string{
		{"diff.algorithm", "patience"},
		{"diff.mnemonicPrefix", "true"},
		{"diff.context", "7"},
		{"diff.noprefix", "true"},
		{"diff.renames", "copies"},
		{"diff.external", "/bin/false"},
		{"color.diff", "always"},
		{"core.abbrev", "12"},
		{"core.autocrlf", "input"},
	}
	t.Setenv("GIT_CONFIG_COUNT", strconv.Itoa(len(cfg)))
	for i, kv := range cfg {
		t.Setenv(fmt.Sprintf("GIT_CONFIG_KEY_%d", i), kv[0])
		t.Setenv(fmt.Sprintf("GIT_CONFIG_VALUE_%d", i), kv[1])
	}
	// GIT_EXTERNAL_DIFF would replace patch generation wholesale; the
	// raw comparison never renders one, so it cannot be reached.
	t.Setenv("GIT_EXTERNAL_DIFF", "/bin/false")

	if got := mustHash(t, h, repo); got != want {
		t.Errorf("digest moved under non-default git config: %s -> %s", want, got)
	}
}

// TestDiff_ModeChangeGoesStale documents how a mode-only change is
// represented: content is identical on both sides, but the path joins the
// delta, so the digest moves. Stricter than hash: files, which never sees
// permission bits at all.
func TestDiff_ModeChangeGoesStale(t *testing.T) {
	repo, dir := newDiffRepo(t)
	writeFile(t, dir, "src/a.go", "a1\nMINE\na3\na4\na5\n")
	runGit(t, dir, "commit", "-qam", "my work")

	h := srcDiff()
	before := mustHash(t, h, repo)

	if err := os.Chmod(filepath.Join(dir, "src/b.go"), 0o755); err != nil {
		t.Fatal(err)
	}
	if after := mustHash(t, h, repo); before == after {
		t.Error("mode-only change did not invalidate the digest")
	}
}

// TestDiff_BinaryContentGoesStale pins that binary payloads are compared
// as bytes: no "Binary files differ" placeholder can hide a change.
func TestDiff_BinaryContentGoesStale(t *testing.T) {
	repo, dir := newDiffRepo(t)
	writeFile(t, dir, "src/blob.bin", "\x00\x01\x02binary\x00")
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-qm", "binary")
	writeFile(t, dir, "src/a.go", "a1\nMINE\na3\na4\na5\n")
	runGit(t, dir, "commit", "-qam", "my work")

	h := srcDiff()
	before := mustHash(t, h, repo)

	writeFile(t, dir, "src/blob.bin", "\x00\x01\x02BINARY\x00")
	if after := mustHash(t, h, repo); before == after {
		t.Error("binary content change did not invalidate the digest")
	}
}

// TestDiff_ResolvingAMergeByKeepingOursStillCounts is the case the
// base-blob half of the digest exists for. Content-only hashing cannot
// see it: the worktree bytes are unchanged, but the branch now carries
// its version of the file over a DIFFERENT base version, and that
// combination was never verified.
func TestDiff_ResolvingAMergeByKeepingOursStillCounts(t *testing.T) {
	repo, dir := newDiffRepo(t)
	writeFile(t, dir, "src/a.go", "MINE\na2\na3\na4\na5\n")
	runGit(t, dir, "commit", "-qam", "my work")

	h := srcDiff()
	before := mustHash(t, h, repo)
	beforeBytes, err := os.ReadFile(filepath.Join(dir, "src/a.go"))
	if err != nil {
		t.Fatal(err)
	}

	// The base branch rewrites the same line, so merging conflicts.
	runGit(t, dir, "checkout", "-q", "main")
	writeFile(t, dir, "src/a.go", "BASE\na2\na3\na4\na5\n")
	runGit(t, dir, "commit", "-qam", "base rewrites the same line")
	runGit(t, dir, "checkout", "-q", "feat")
	if out, mergeErr := runGitAllowFail(dir, "merge", "--no-edit", "main"); mergeErr == nil {
		t.Fatalf("fixture broken: the merge was expected to conflict\n%s", out)
	}
	runGit(t, dir, "checkout", "--ours", "--", "src/a.go")
	runGit(t, dir, "add", "src/a.go")
	runGit(t, dir, "commit", "-qm", "resolve by keeping ours")

	afterBytes, err := os.ReadFile(filepath.Join(dir, "src/a.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(beforeBytes, afterBytes) {
		t.Fatalf("fixture broken: worktree content changed, so the test would pass on content alone\nbefore=%q after=%q", beforeBytes, afterBytes)
	}

	if after := mustHash(t, h, repo); before == after {
		t.Error("keeping ours over a moved base did not invalidate the digest")
	}
}

func TestDiff_OnBaseBranchErrors(t *testing.T) {
	repo, dir := newDiffRepo(t)
	runGit(t, dir, "checkout", "-q", "main")

	_, err := srcDiff().Hash(repo)
	if err == nil {
		t.Fatal("want an error when HEAD is at the merge base, got nil")
	}
	if !errors.Is(err, ErrDiffBase) {
		t.Errorf("error = %v, want one matching ErrDiffBase", err)
	}
}

func TestDiff_BehindBaseBranchErrors(t *testing.T) {
	repo, dir := newDiffRepo(t)
	// feat has no commits of its own, main moves ahead: HEAD is still the
	// merge base, so the delta is empty and the digest would be constant.
	runGit(t, dir, "checkout", "-q", "main")
	writeFile(t, dir, "src/b.go", "moved on\n")
	runGit(t, dir, "commit", "-qam", "base moves ahead")
	runGit(t, dir, "checkout", "-q", "feat")

	if _, err := srcDiff().Hash(repo); !errors.Is(err, ErrDiffBase) {
		t.Errorf("error = %v, want one matching ErrDiffBase", err)
	}
}

func TestDiff_UnresolvableBaseErrors(t *testing.T) {
	repo, dir := newDiffRepo(t)
	writeFile(t, dir, "src/a.go", "mine\n")
	runGit(t, dir, "commit", "-qam", "my work")

	h := Diff{Include: []string{"src/**"}, Base: "origin/never-fetched"}
	_, err := h.Hash(repo)
	if !errors.Is(err, ErrDiffBase) {
		t.Fatalf("error = %v, want one matching ErrDiffBase", err)
	}
	if _, scopeErr := h.Scope(repo); !errors.Is(scopeErr, ErrDiffBase) {
		t.Errorf("Scope error = %v, want one matching ErrDiffBase", scopeErr)
	}
}

func TestDiff_NoMergeBaseErrors(t *testing.T) {
	repo, dir := newDiffRepo(t)
	runGit(t, dir, "checkout", "-q", "--orphan", "unrelated")
	writeFile(t, dir, "src/a.go", "unrelated history\n")
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-qm", "orphan")

	if _, err := srcDiff().Hash(repo); !errors.Is(err, ErrDiffBase) {
		t.Errorf("error = %v, want one matching ErrDiffBase", err)
	}
}

func TestDiff_MissingBaseErrors(t *testing.T) {
	repo, _ := newDiffRepo(t)
	if _, err := (Diff{Include: []string{"src/**"}}).Hash(repo); !errors.Is(err, ErrDiffBase) {
		t.Errorf("error = %v, want one matching ErrDiffBase", err)
	}
}

// TestDiff_ScopeMatchesFilesHasher pins the guarantee that include /
// exclude mean the same thing in both modes. It is the reason the delta
// is filtered with doublestar rather than handed to git as a pathspec:
// git's default pathspec magic reads `src/*.go` as matching across
// directory boundaries and `src/**/*.go` as NOT matching `src/a.go`.
//
// The guarantee is about PATTERN SEMANTICS, and holds for a non-empty
// include list over paths that exist in the worktree. The two modes
// select from different universes — `files` globs the filesystem,
// `diff` filters the branch's delta — so they legitimately differ
// where those universes do; the two cases that produces are pinned by
// the tests immediately below rather than papered over here.
func TestDiff_ScopeMatchesFilesHasher(t *testing.T) {
	repo, dir := newDiffRepo(t)
	writeFile(t, dir, "src/deep/nested/c.go", "c\n")
	writeFile(t, dir, "src/notes.md", "n\n")
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-qm", "more files")

	// Every tracked file differs from the merge base, so the two hashers
	// see the same candidate set and any divergence is glob semantics.
	for _, rel := range []string{"src/a.go", "src/b.go", "src/deep/nested/c.go", "src/notes.md", "docs/x.md"} {
		writeFile(t, dir, rel, "touched by this branch\n")
	}
	runGit(t, dir, "commit", "-qam", "touch everything")

	cases := []struct {
		include []string
		exclude []string
	}{
		{include: []string{"src/**"}},
		{include: []string{"src/**/*.go"}},
		{include: []string{"src/*.go"}},
		{include: []string{"**/*.md"}},
		{include: []string{"src/**"}, exclude: []string{"**/*.md"}},
		{include: []string{"src/**", "docs/**"}, exclude: []string{"src/deep/**"}},
		{include: []string{"docs/x.md"}},
	}
	for _, tc := range cases {
		files := Files{Include: tc.include, Exclude: tc.exclude}
		want, err := files.Scope(repo)
		if err != nil {
			t.Fatalf("files scope %v: %v", tc.include, err)
		}
		diff := Diff{Include: tc.include, Exclude: tc.exclude, Base: "main"}
		got, err := diff.Scope(repo)
		if err != nil {
			t.Fatalf("diff scope %v: %v", tc.include, err)
		}
		if len(want) == 0 {
			t.Fatalf("include %v matched nothing under hash=files; the case proves nothing", tc.include)
		}
		if !slices.Equal(want, got) {
			t.Errorf("include=%v exclude=%v: files scope %v, diff scope %v", tc.include, tc.exclude, want, got)
		}
	}
}

// TestDiff_EmptyIncludeCoversTheWholeDelta pins one of the two
// deliberate divergences: an omitted include is a legal diff config
// meaning "everything I changed", whereas `hash: files` rejects it at
// validation (it would have nothing to glob).
func TestDiff_EmptyIncludeCoversTheWholeDelta(t *testing.T) {
	repo, dir := newDiffRepo(t)
	writeFile(t, dir, "src/a.go", "mine\n")
	writeFile(t, dir, "docs/x.md", "mine too\n")
	runGit(t, dir, "commit", "-qam", "my work")

	scope, err := (Diff{Base: "main"}).Scope(repo)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(scope, []string{"docs/x.md", "src/a.go"}) {
		t.Errorf("unscoped diff scope = %v, want the whole delta", scope)
	}

	// Excluding without including is equally legal and subtracts from
	// that whole-delta default.
	scope, err = (Diff{Exclude: []string{"docs/**"}, Base: "main"}).Scope(repo)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(scope, []string{"src/a.go"}) {
		t.Errorf("exclude-only diff scope = %v, want the delta minus docs", scope)
	}
}

// TestDiff_DeletedPathStaysInScope pins the other divergence: `files`
// globs the filesystem, so a deleted path simply vanishes from its
// scope, while `diff` must keep it — a branch that deletes an in-scope
// file has certainly changed what the gate verified.
func TestDiff_DeletedPathStaysInScope(t *testing.T) {
	repo, dir := newDiffRepo(t)
	runGit(t, dir, "rm", "-q", "src/b.go")
	runGit(t, dir, "commit", "-qm", "delete an in-scope file")

	diffScope, err := (Diff{Include: []string{"src/**"}, Base: "main"}).Scope(repo)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(diffScope, []string{"src/b.go"}) {
		t.Errorf("diff scope = %v, want the deleted path", diffScope)
	}

	filesScope, err := (Files{Include: []string{"src/**"}}).Scope(repo)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range filesScope {
		if p == "src/b.go" {
			t.Error("files scope unexpectedly contains the deleted path")
		}
	}
}

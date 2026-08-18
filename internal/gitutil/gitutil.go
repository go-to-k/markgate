// Package gitutil wraps the git binary for the bits markgate needs:
// repository discovery, HEAD resolution, and the file lists used by the
// git-tree hasher. Output is parsed from -z (NUL-delimited) streams so
// unusual file names round-trip safely.
package gitutil

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// Repo runs git commands scoped to a working directory.
// An empty Dir means the current process working directory.
type Repo struct {
	Dir string
	// root memoizes TopLevel so the path-producing commands can be
	// re-rooted without paying an extra rev-parse per call.
	root string
}

// New returns a Repo bound to dir. Pass "" to use the process cwd.
func New(dir string) *Repo {
	return &Repo{Dir: dir}
}

// ErrNotARepo is returned when git reports the working directory is not
// inside a repository. Callers translate this to exit code 2.
var ErrNotARepo = errors.New("not a git repository")

// ErrRefNotFound is returned by ResolveCommit / MergeBase when the ref
// does not resolve to a commit (typo, or never fetched).
var ErrRefNotFound = errors.New("ref does not resolve to a commit")

// ErrNoMergeBase is returned by MergeBase when the two commits share no
// ancestor (unrelated histories, or a shallow clone that does not reach
// far enough back).
var ErrNoMergeBase = errors.New("no merge base")

func (r *Repo) run(args ...string) ([]byte, error) {
	return r.runIn(r.Dir, args...)
}

// runAtRoot runs git from the worktree root instead of the caller's cwd.
// Every command whose OUTPUT is a set of paths must go through this:
// git scopes `ls-files --others` to the cwd subtree, and under
// `diff.relative` it also prints paths relative to the cwd. Either one
// silently changes which files a gate covers depending on the directory
// a hook happened to run from — and a delta whose paths no longer match
// the repo-relative globs filters down to nothing, which is a marker
// that can never go stale. Re-rooting fixes both without depending on
// a git version for --no-relative.
func (r *Repo) runAtRoot(args ...string) ([]byte, error) {
	root, err := r.TopLevel()
	if err != nil {
		return nil, err
	}
	return r.runIn(root, args...)
}

func (r *Repo) runIn(dir string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if strings.Contains(msg, "not a git repository") {
			return nil, ErrNotARepo
		}
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return stdout.Bytes(), nil
}

// TopLevel returns the absolute path to the working tree root. The
// result is memoized: it is resolved once per Repo and then reused by
// every path-producing command.
func (r *Repo) TopLevel() (string, error) {
	if r.root != "" {
		return r.root, nil
	}
	out, err := r.run("rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	r.root = strings.TrimSpace(string(out))
	return r.root, nil
}

// GitDir returns the absolute path to the .git directory (or worktree
// equivalent). This is where markgate stores its marker files.
func (r *Repo) GitDir() (string, error) {
	out, err := r.run("rev-parse", "--absolute-git-dir")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// HeadSHA returns the full SHA of HEAD. Fails on a repo with no commits.
func (r *Repo) HeadSHA() (string, error) {
	out, err := r.run("rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// DiffHeadNames returns paths (repo-relative) that differ from HEAD in the
// working tree or index, for the whole repository regardless of cwd.
func (r *Repo) DiffHeadNames() ([]string, error) {
	out, err := r.runAtRoot("diff", "HEAD", "--name-only", "-z")
	if err != nil {
		return nil, err
	}
	return splitNUL(out), nil
}

// ResolveCommit returns the full SHA that ref points at, or
// ErrRefNotFound when it does not name a commit.
func (r *Repo) ResolveCommit(ref string) (string, error) {
	out, err := r.run("rev-parse", "--verify", "--quiet", ref+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("%q: %w", ref, ErrRefNotFound)
	}
	sha := strings.TrimSpace(string(out))
	if sha == "" {
		return "", fmt.Errorf("%q: %w", ref, ErrRefNotFound)
	}
	return sha, nil
}

// MergeBase returns the best common ancestor of ref and HEAD. The ref is
// resolved first so an unfetched / mistyped ref reports ErrRefNotFound
// rather than the generic "no merge base".
func (r *Repo) MergeBase(ref string) (string, error) {
	if _, err := r.ResolveCommit(ref); err != nil {
		return "", err
	}
	// git merge-base exits 1 with empty output when no ancestor exists,
	// which run() surfaces as a bare "exit status 1"; both shapes mean
	// the same thing to callers.
	out, err := r.run("merge-base", ref, "HEAD")
	if err != nil {
		return "", fmt.Errorf("%q and HEAD: %w", ref, ErrNoMergeBase)
	}
	sha := strings.TrimSpace(string(out))
	if sha == "" {
		return "", fmt.Errorf("%q and HEAD: %w", ref, ErrNoMergeBase)
	}
	return sha, nil
}

// DiffEntry is one path whose working-tree content differs from a base
// commit. BaseBlob is the blob SHA the base side holds, or "" when the
// path does not exist there (added on this side).
type DiffEntry struct {
	Path     string
	BaseBlob string
}

// zeroBlob is git's "unknown / absent" blob SHA in raw diff output. It
// appears on the destination side of every working-tree comparison, and
// on the source side of an addition.
const zeroBlob = "0000000000000000000000000000000000000000"

// DiffFrom returns the working-tree differences against commit rev
// (index state is irrelevant, matching the git-tree hasher's staging
// invariant). Rename detection is off, SHAs are unabbreviated, and the
// command runs from the worktree root, so the result depends on neither
// the caller's git config nor the directory it was invoked from.
func (r *Repo) DiffFrom(rev string) ([]DiffEntry, error) {
	out, err := r.runAtRoot("diff", "--raw", "--no-abbrev", "--no-renames", "-z", rev)
	if err != nil {
		return nil, err
	}
	fields := splitNUL(out)
	entries := make([]DiffEntry, 0, len(fields)/2)
	for i := 0; i+1 < len(fields); i += 2 {
		// meta is ":<srcmode> <dstmode> <srcsha> <dstsha> <status>".
		meta := strings.Fields(fields[i])
		if len(meta) < 5 {
			return nil, fmt.Errorf("git diff --raw: unexpected record %q", fields[i])
		}
		base := meta[2]
		if base == zeroBlob {
			base = ""
		}
		entries = append(entries, DiffEntry{Path: fields[i+1], BaseBlob: base})
	}
	return entries, nil
}

// CandidateNames returns every path git can report in a diff or as
// untracked: what is in the index, plus what is untracked and not
// ignored. One ls-files invocation covers both, and the two sets are
// disjoint by definition. Repo-relative, whole repository regardless
// of cwd.
func (r *Repo) CandidateNames() ([]string, error) {
	out, err := r.runAtRoot("ls-files", "-z", "--cached", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	return splitNUL(out), nil
}

// UntrackedNames returns paths (repo-relative) that are untracked but not
// ignored, for the whole repository regardless of cwd.
func (r *Repo) UntrackedNames() ([]string, error) {
	out, err := r.runAtRoot("ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil, err
	}
	return splitNUL(out), nil
}

func splitNUL(b []byte) []string {
	if len(b) == 0 {
		return nil
	}
	s := string(b)
	s = strings.TrimRight(s, "\x00")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\x00")
}

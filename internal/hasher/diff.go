package hasher

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/go-to-k/markgate/internal/config"
	"github.com/go-to-k/markgate/internal/gitutil"
)

// ErrDiffBase marks the fail-closed preconditions of the diff hasher: a
// base ref that cannot be resolved, and a working tree that is identical
// to the merge base (where the delta is empty, so the digest is a
// constant the marker could never fall out of). Callers that render many
// gates at once (bare `status`) match on it to degrade one row instead
// of aborting the whole listing; every other command surfaces it as an
// error.
var ErrDiffBase = errors.New("hash=diff")

// Diff hashes the working-tree delta against merge-base(Base, HEAD):
// for every changed path it folds in the blob the base side holds plus
// the current worktree content. Untracked-and-not-ignored files count as
// changes too, so a brand-new in-scope file is never invisible.
//
// Neither HEAD nor the merge-base SHA is part of the digest. That is the
// whole point: when the base branch moves under an unrelated in-scope
// file, that file stops differing from the new merge base and drops out
// of the digest, leaving the marker fresh. A base-branch change to a
// file this branch also touched stays in the delta and still invalidates.
//
// An empty delta (a clean base-branch checkout, a branch with no changes
// yet, a fully reverted branch) is refused: see ErrDiffBase.
type Diff struct {
	Include []string
	Exclude []string
	Base    string
}

// Type implements Hasher.
func (Diff) Type() string { return config.HashDiff }

// Hash implements Hasher.
func (d Diff) Hash(repo *gitutil.Repo) (string, error) {
	top, err := repo.TopLevel()
	if err != nil {
		return "", err
	}
	entries, err := d.entries(repo)
	if err != nil {
		return "", err
	}

	h := sha256.New()
	for _, e := range entries {
		// The base-side blob is framed in so that resolving a merge by
		// keeping this branch's version — same worktree content, new
		// starting point — still counts as a change.
		fmt.Fprintf(h, "base\x00%s\x00%s\x00", e.Path, e.BaseBlob)
		if err := hashEntry(h, filepath.Join(top, e.Path), e.Path); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Scope implements Hasher.
func (d Diff) Scope(repo *gitutil.Repo) ([]string, error) {
	entries, err := d.entries(repo)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Path)
	}
	return out, nil
}

// MergeBase resolves merge-base(Base, HEAD), reporting an unusable base
// ref as ErrDiffBase. Exported so `set` can record the resolved SHA on
// the marker for diagnostics.
func (d Diff) MergeBase(repo *gitutil.Repo) (string, error) {
	if d.Base == "" {
		return "", fmt.Errorf("%w: no base ref configured", ErrDiffBase)
	}
	mergeBase, err := repo.MergeBase(d.Base)
	switch {
	case errors.Is(err, gitutil.ErrRefNotFound):
		return "", fmt.Errorf("%w: base ref %q does not resolve; fetch it first (git fetch origin)", ErrDiffBase, d.Base)
	case errors.Is(err, gitutil.ErrNoMergeBase):
		return "", fmt.Errorf("%w: %q and HEAD share no history; a shallow clone needs git fetch --unshallow", ErrDiffBase, d.Base)
	case err != nil:
		return "", err
	}
	return mergeBase, nil
}

// Preflight reports the same errors Hash would, without computing a
// digest. Callers use it to refuse a misconfigured gate before doing
// expensive work — `markgate run` would otherwise execute its child and
// only then discover it cannot record the result.
func (d Diff) Preflight(repo *gitutil.Repo) error {
	_, err := d.entries(repo)
	return err
}

// entries returns the sorted, include/exclude-filtered delta. Patterns
// are matched with the same doublestar matcher the other hashers use —
// never handed to git as pathspecs, whose default magic gives the same
// pattern a different meaning (`src/*.go` matches across directory
// boundaries there, `src/**/*.go` does not match `src/a.go`).
func (d Diff) entries(repo *gitutil.Repo) ([]gitutil.DiffEntry, error) {
	mergeBase, err := d.MergeBase(repo)
	if err != nil {
		return nil, err
	}
	changed, err := repo.DiffFrom(mergeBase)
	if err != nil {
		return nil, err
	}
	untracked, err := repo.UntrackedNames()
	if err != nil {
		return nil, err
	}

	// An empty delta is the one state this hash type cannot represent:
	// the digest degenerates to a constant, so a marker set here would
	// never go stale again. Refuse it rather than hand back a digest
	// that cannot expire. Checked before include/exclude filtering — a
	// branch whose changes all land outside the gate's scope is exactly
	// the case this hash type exists to keep fresh.
	if len(changed) == 0 && len(untracked) == 0 {
		return nil, fmt.Errorf("%w: no delta against merge-base(%s, HEAD); the digest would be a constant that never goes stale, so run this gate from a branch ahead of the base rather than the base branch itself", ErrDiffBase, d.Base)
	}

	baseBlob := make(map[string]string, len(changed)+len(untracked))
	paths := make([]string, 0, len(changed)+len(untracked))
	for _, e := range changed {
		baseBlob[e.Path] = e.BaseBlob
		paths = append(paths, e.Path)
	}
	for _, p := range untracked {
		if _, ok := baseBlob[p]; ok {
			continue
		}
		baseBlob[p] = ""
		paths = append(paths, p)
	}

	kept, err := filterGlobs(dedupSort(paths), d.Include, d.Exclude)
	if err != nil {
		return nil, err
	}
	out := make([]gitutil.DiffEntry, 0, len(kept))
	for _, p := range kept {
		out = append(out, gitutil.DiffEntry{Path: p, BaseBlob: baseBlob[p]})
	}
	return out, nil
}

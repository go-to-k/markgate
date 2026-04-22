// Package hasher computes the state digest that markgate stores in a
// marker and compares at verify time.
//
// Two strategies are provided:
//
//   - GitTree: hashes HEAD + (diff ∪ untracked), including deletions.
//     Safe default; invalidates automatically when HEAD moves.
//   - Files:   hashes files matching include globs minus exclude globs.
//     Useful when you want narrower invalidation (e.g. docs only).
//
// The digest format (framing, sort order, etc.) is an implementation
// detail; markers written by one version are not guaranteed to validate
// against another.
package hasher

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/go-to-k/markgate/internal/config"
	"github.com/go-to-k/markgate/internal/gitutil"
)

// Hasher produces a hex-encoded SHA-256 digest of the repository state.
type Hasher interface {
	Type() string
	Hash(repo *gitutil.Repo) (string, error)
}

// For returns a Hasher matching the gate configuration.
func For(g config.Gate) (Hasher, error) {
	switch g.Hash {
	case "", config.HashGitTree:
		return GitTree{}, nil
	case config.HashFiles:
		return Files{Include: g.Include, Exclude: g.Exclude}, nil
	default:
		return nil, fmt.Errorf("unknown hash type %q", g.Hash)
	}
}

// GitTree hashes HEAD plus every file that differs from HEAD or is
// untracked-and-not-ignored. Deleted files contribute a deletion record.
type GitTree struct{}

// Type implements Hasher.
func (GitTree) Type() string { return config.HashGitTree }

// Hash implements Hasher.
func (GitTree) Hash(repo *gitutil.Repo) (string, error) {
	head, err := repo.HeadSHA()
	if err != nil {
		return "", err
	}
	top, err := repo.TopLevel()
	if err != nil {
		return "", err
	}
	diffs, err := repo.DiffHeadNames()
	if err != nil {
		return "", err
	}
	untracked, err := repo.UntrackedNames()
	if err != nil {
		return "", err
	}
	files := dedupSort(append(diffs, untracked...))

	h := sha256.New()
	fmt.Fprintf(h, "head\x00%s\x00", head)
	for _, rel := range files {
		if err := hashEntry(h, filepath.Join(top, rel), rel); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// hashEntry feeds one path into h, framed so that "F a\nbody" and
// "F a\n" + "body" on the next file can't collide.
func hashEntry(h io.Writer, abs, rel string) error {
	info, err := os.Lstat(abs)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(h, "D\x00%s\x00", rel)
			return nil
		}
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, readErr := os.Readlink(abs)
		if readErr != nil {
			return readErr
		}
		fmt.Fprintf(h, "L\x00%s\x00%s\x00", rel, target)
		return nil
	}
	if info.IsDir() {
		// git-tree pathspec should never yield a directory, but guard anyway.
		return nil
	}
	f, err := os.Open(abs)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	fmt.Fprintf(h, "F\x00%s\x00%d\x00", rel, info.Size())
	if _, copyErr := io.Copy(h, f); copyErr != nil {
		return copyErr
	}
	// Terminator so the next entry's header starts cleanly.
	_, err = h.Write([]byte{0})
	return err
}

func dedupSort(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	for _, s := range in {
		if s != "" {
			seen[s] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

package hasher

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/go-to-k/markgate/internal/config"
	"github.com/go-to-k/markgate/internal/gitutil"
)

// Files hashes the contents of every path matched by Include and not
// excluded by Exclude. HEAD is intentionally omitted so that commits
// unrelated to the tracked paths do not invalidate the marker.
type Files struct {
	Include []string
	Exclude []string
}

// Type implements Hasher.
func (Files) Type() string { return config.HashFiles }

// Hash implements Hasher.
func (f Files) Hash(repo *gitutil.Repo) (string, error) {
	top, err := repo.TopLevel()
	if err != nil {
		return "", err
	}
	matches, err := f.resolve(top)
	if err != nil {
		return "", err
	}

	h := sha256.New()
	for _, rel := range matches {
		if err := hashEntry(h, filepath.Join(top, rel), rel); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// resolve returns the sorted, deduplicated, repo-relative paths that match
// include minus exclude. Directories and matches that disappear between
// glob and stat are filtered out.
func (f Files) resolve(topLevel string) ([]string, error) {
	fsys := os.DirFS(topLevel)
	seen := make(map[string]struct{})
	for _, pat := range f.Include {
		matches, err := doublestar.Glob(fsys, pat)
		if err != nil {
			return nil, fmt.Errorf("include glob %q: %w", pat, err)
		}
		for _, p := range matches {
			seen[p] = struct{}{}
		}
	}
	if len(f.Exclude) > 0 {
		for p := range seen {
			excluded, err := matchesAny(f.Exclude, p)
			if err != nil {
				return nil, err
			}
			if excluded {
				delete(seen, p)
			}
		}
	}

	out := make([]string, 0, len(seen))
	for p := range seen {
		info, err := os.Stat(filepath.Join(topLevel, p))
		if err != nil {
			// Path vanished between glob and stat — skip.
			continue
		}
		if info.IsDir() {
			continue
		}
		out = append(out, p)
	}
	sort.Strings(out)
	return out, nil
}

func matchesAny(patterns []string, path string) (bool, error) {
	for _, pat := range patterns {
		ok, err := doublestar.Match(pat, path)
		if err != nil {
			return false, fmt.Errorf("invalid glob %q: %w", pat, err)
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

// filterGlobs applies optional Include/Exclude scoping to a file list.
// Include empty means "match all"; Exclude removes matching entries.
// When both are empty the input is returned unchanged.
func filterGlobs(paths, include, exclude []string) ([]string, error) {
	if len(include) == 0 && len(exclude) == 0 {
		return paths, nil
	}
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if len(include) > 0 {
			ok, err := matchesAny(include, p)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
		}
		if len(exclude) > 0 {
			ok, err := matchesAny(exclude, p)
			if err != nil {
				return nil, err
			}
			if ok {
				continue
			}
		}
		out = append(out, p)
	}
	return out, nil
}

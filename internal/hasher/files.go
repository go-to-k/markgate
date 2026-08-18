package hasher

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

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
	if err := refuseDeadScope(top, f.Include, len(matches)); err != nil {
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

// Scope implements Hasher.
func (f Files) Scope(repo *gitutil.Repo) ([]string, error) {
	top, err := repo.TopLevel()
	if err != nil {
		return nil, err
	}
	return f.resolve(top)
}

// resolve returns the sorted, deduplicated, repo-relative paths that match
// include minus exclude. Directories and matches that disappear between
// glob and stat are filtered out.
func (f Files) resolve(topLevel string) ([]string, error) {
	seen := make(map[string]struct{})
	for _, pat := range f.Include {
		matches, err := MatchGlob(topLevel, pat)
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
		out = append(out, p)
	}
	sort.Strings(out)
	return out, nil
}

// ErrDeadScope marks an include list where no pattern matches any path
// in the working tree — a typo, a renamed directory, a path that moved.
// The scope is then empty for a reason that has nothing to do with the
// repository's state, so the digest is the constant SHA-256 of the empty
// set and the gate reports "match" forever. Callers that render many
// gates at once (bare `status`) match on it to degrade one row instead
// of aborting the listing; every other command surfaces it as an error.
var ErrDeadScope = errors.New("dead scope")

// deadScopeErr builds the ErrDeadScope message. The patterns are named
// because the whole point is that the user cannot see which one is
// wrong from the gate's behavior — it simply keeps passing.
func deadScopeErr(include []string) error {
	return fmt.Errorf("%w: include matches no file in the working tree (%s); the digest would be a constant that never goes stale, so fix the pattern or drop the gate",
		ErrDeadScope, strings.Join(include, ", "))
}

// refuseDeadScope reports ErrDeadScope when an include list produced an
// empty scope and no pattern matches anything in the working tree.
//
// Called from Hash and never from Scope: an empty scope is a truthful
// description of the gate, and Scope backs the diagnostic paths
// (--explain, the empty-delta warning), which must not change what a
// command does. Digesting that scope is the part that cannot be allowed
// — it yields a constant no change can ever move.
//
// The extra glob is only paid when the scope came out empty, which also
// keeps "every pattern is dead" (a broken config) distinct from
// "exclude removed everything" (a deliberate one).
func refuseDeadScope(topLevel string, include []string, scopeLen int) error {
	if scopeLen > 0 || len(include) == 0 {
		return nil
	}
	return liveIncludes(topLevel, include)
}

// liveIncludes reports ErrDeadScope when include is non-empty and no
// pattern matches any path in the working tree. Diff needs this as a
// separate pass because its scope comes from the branch delta rather
// than from globbing the tree; Files gets the same answer for free
// inside resolve.
func liveIncludes(topLevel string, include []string) error {
	for _, pat := range include {
		matches, err := MatchGlob(topLevel, pat)
		if err != nil {
			return fmt.Errorf("include glob %q: %w", pat, err)
		}
		if len(matches) > 0 {
			return nil
		}
	}
	return deadScopeErr(include)
}

// MatchGlob expands pat against topLevel as a doublestar glob and returns
// the sorted repo-relative paths of matching regular files. Directories
// and entries that disappear between glob and stat are filtered out.
func MatchGlob(topLevel, pat string) ([]string, error) {
	fsys := os.DirFS(topLevel)
	matches, err := doublestar.Glob(fsys, pat)
	if err != nil {
		return nil, fmt.Errorf("invalid glob %q: %w", pat, err)
	}
	out := make([]string, 0, len(matches))
	for _, p := range matches {
		info, statErr := os.Stat(filepath.Join(topLevel, p))
		if statErr != nil {
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

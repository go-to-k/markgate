// Package state reads and writes marker files. The default layout is
// <gitDir>/markgate/<key>.json; callers may also supply a custom
// directory (e.g. for CI cache integration), in which case the marker
// is written at <dir>/<key>.json without the extra "markgate" subdir.
//
// The on-disk JSON schema is an implementation detail; no compatibility
// guarantees are made across markgate versions. Writes are atomic: data
// lands in a sibling temp file, is fsynced, and is renamed into place.
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ErrNotFound signals that no marker exists for the requested key.
// Callers treat this as "not verified" (exit 1), not an error.
var ErrNotFound = errors.New("marker not found")

// SchemaVersion tags the on-disk format. Bump on breaking changes.
const SchemaVersion = 1

// Marker is the serialized form of a recorded state hash.
type Marker struct {
	Version   int       `json:"version"`
	HashType  string    `json:"hash_type"`
	Digest    string    `json:"digest"`
	Head      string    `json:"head,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// Path returns the marker file path for a given git directory and key.
// The default layout places markers under <gitDir>/markgate/<key>.json so
// they live inside .git and need no gitignore entry.
func Path(gitDir, key string) string {
	return PathIn(filepath.Join(gitDir, "markgate"), key)
}

// PathIn returns the marker file path directly under dir, without the
// extra "markgate" subdirectory that Path adds. Callers use this when the
// user has explicitly chosen a storage directory (e.g. --state-dir or
// MARKGATE_STATE_DIR): the directory is already theirs, so there is no
// reason to nest another folder inside it.
func PathIn(dir, key string) string {
	return filepath.Join(dir, key+".json")
}

// Load reads a marker. Returns ErrNotFound when the file does not exist.
func Load(path string) (*Marker, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	var m Marker
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse marker %s: %w", path, err)
	}
	// Forward-compat: a marker written by a different schema version is
	// treated as absent so the caller re-runs and rewrites with the
	// current schema. Prevents cross-version digest comparisons.
	if m.Version != SchemaVersion {
		return nil, ErrNotFound
	}
	return &m, nil
}

// Save writes a marker atomically: CreateTemp in the destination directory,
// Sync, Close, Rename. A crash mid-write leaves either the old marker or
// nothing, never a truncated file.
func Save(path string, m *Marker) error {
	if m.Version == 0 {
		m.Version = SchemaVersion
	}
	if m.CreatedAt.IsZero() {
		m.CreatedAt = time.Now().UTC()
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	dir := filepath.Dir(path)
	if mkErr := os.MkdirAll(dir, 0o755); mkErr != nil {
		return mkErr
	}

	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	committed = true
	return nil
}

// Remove deletes the marker if it exists. Missing markers are not an error.
func Remove(path string) error {
	err := os.Remove(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

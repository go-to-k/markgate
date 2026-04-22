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
}

// New returns a Repo bound to dir. Pass "" to use the process cwd.
func New(dir string) *Repo {
	return &Repo{Dir: dir}
}

// ErrNotARepo is returned when git reports the working directory is not
// inside a repository. Callers translate this to exit code 2.
var ErrNotARepo = errors.New("not a git repository")

func (r *Repo) run(args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = r.Dir
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

// TopLevel returns the absolute path to the working tree root.
func (r *Repo) TopLevel() (string, error) {
	out, err := r.run("rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
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
// working tree or index.
func (r *Repo) DiffHeadNames() ([]string, error) {
	out, err := r.run("diff", "HEAD", "--name-only", "-z")
	if err != nil {
		return nil, err
	}
	return splitNUL(out), nil
}

// UntrackedNames returns paths (repo-relative) that are untracked but not
// ignored.
func (r *Repo) UntrackedNames() ([]string, error) {
	out, err := r.run("ls-files", "--others", "--exclude-standard", "-z")
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

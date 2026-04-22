// Package key validates gate identifiers.
//
// Keys are used both on the CLI (markgate set <key>) and in state file
// names, so the allowed character set is intentionally narrow:
// kebab-case ASCII, starting with an alphanumeric.
package key

import (
	"fmt"
	"regexp"
)

var pattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// Validate reports whether k is a legal gate key.
func Validate(k string) error {
	if k == "" {
		return fmt.Errorf("key must not be empty")
	}
	if !pattern.MatchString(k) {
		return fmt.Errorf("invalid key %q: must match [a-z0-9][a-z0-9-]*", k)
	}
	return nil
}

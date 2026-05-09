// Package duration parses TTL strings on top of time.ParseDuration with
// added support for d (24h) and w (168h) units.
//
// Months and years are intentionally not supported: month length is
// ambiguous (28-31 days) and a "year" varies with leap years, so neither
// rounds to a fixed number of hours. Use d/w/h for stable durations.
package duration

import (
	"fmt"
	"strconv"
	"time"
	"unicode"
)

// Parse accepts every form time.ParseDuration accepts plus d (=24h) and
// w (=168h) units. Mixed units are allowed (e.g. "1d12h", "2w3d").
//
// Each segment is parsed independently and summed, so day/week segments
// are folded to time.Duration directly; the remaining segments are passed
// through to time.ParseDuration, which handles every other unit including
// fractional and signed mantissas.
func Parse(s string) (time.Duration, error) {
	if s == "" {
		return 0, fmt.Errorf("empty duration")
	}

	var total time.Duration
	i := 0
	for i < len(s) {
		j := i
		if j < len(s) && (s[j] == '+' || s[j] == '-') {
			j++
		}
		for j < len(s) && (unicode.IsDigit(rune(s[j])) || s[j] == '.') {
			j++
		}
		if j == i || (j == i+1 && (s[i] == '+' || s[i] == '-')) {
			return 0, fmt.Errorf("invalid duration %q: missing number before unit", s)
		}
		num := s[i:j]

		k := j
		for k < len(s) && unicode.IsLetter(rune(s[k])) {
			k++
		}
		if k == j {
			return 0, fmt.Errorf("invalid duration %q: missing unit after %q", s, num)
		}
		unit := s[j:k]

		switch unit {
		case "d", "w":
			n, err := strconv.ParseFloat(num, 64)
			if err != nil {
				return 0, fmt.Errorf("invalid duration %q: %w", s, err)
			}
			factor := 24 * time.Hour
			if unit == "w" {
				factor = 168 * time.Hour
			}
			total += time.Duration(n * float64(factor))
		default:
			d, err := time.ParseDuration(num + unit)
			if err != nil {
				return 0, fmt.Errorf("invalid duration %q: %w", s, err)
			}
			total += d
		}
		i = k
	}
	return total, nil
}

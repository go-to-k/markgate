package duration

import (
	"testing"
	"time"
)

func TestParse_StandardUnits(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"90s", 90 * time.Second},
		{"30m", 30 * time.Minute},
		{"12h", 12 * time.Hour},
		{"1h30m", time.Hour + 30*time.Minute},
	}
	for _, c := range cases {
		got, err := Parse(c.in)
		if err != nil {
			t.Errorf("Parse(%q): unexpected error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("Parse(%q) = %s, want %s", c.in, got, c.want)
		}
	}
}

func TestParse_DaysAndWeeks(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"7d", 7 * 24 * time.Hour},
		{"2w", 2 * 168 * time.Hour},
		{"1d12h", 24*time.Hour + 12*time.Hour},
		{"1w1d", 168*time.Hour + 24*time.Hour},
	}
	for _, c := range cases {
		got, err := Parse(c.in)
		if err != nil {
			t.Errorf("Parse(%q): unexpected error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("Parse(%q) = %s, want %s", c.in, got, c.want)
		}
	}
}

func TestParse_MIsMinutesNotMonths(t *testing.T) {
	got, err := Parse("1m")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != time.Minute {
		t.Errorf("Parse(\"1m\") = %s, want 1m (Go-standard minutes)", got)
	}
}

func TestParse_MonthsRejected(t *testing.T) {
	for _, s := range []string{"1mo", "1mon", "2months", "1y", "1yr"} {
		if _, err := Parse(s); err == nil {
			t.Errorf("Parse(%q): expected error, got nil", s)
		}
	}
}

func TestParse_Empty(t *testing.T) {
	if _, err := Parse(""); err == nil {
		t.Error("Parse(\"\"): expected error")
	}
}

func TestParse_Garbage(t *testing.T) {
	for _, s := range []string{"abc", "1", "d", "1.2.3h"} {
		if _, err := Parse(s); err == nil {
			t.Errorf("Parse(%q): expected error, got nil", s)
		}
	}
}

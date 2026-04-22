package key

import "testing"

func TestValidate_Accepts(t *testing.T) {
	ok := []string{
		"a",
		"0",
		"pre-commit",
		"pre-pr",
		"check",
		"a0-b-1",
	}
	for _, k := range ok {
		if err := Validate(k); err != nil {
			t.Errorf("Validate(%q) = %v, want nil", k, err)
		}
	}
}

func TestValidate_Rejects(t *testing.T) {
	bad := []string{
		"",
		"-leading-dash",
		"Upper",
		"snake_case",
		"dotted.key",
		"with space",
		"unicodé",
	}
	for _, k := range bad {
		if err := Validate(k); err == nil {
			t.Errorf("Validate(%q) = nil, want error", k)
		}
	}
}

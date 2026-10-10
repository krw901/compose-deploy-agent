package tagfmt

import "testing"

func TestValidate(t *testing.T) {
	valid := []string{"a1b2c3d", "build-123", "0", "v1-0-0", "latest", "dev", "release"}
	for _, tag := range valid {
		if err := Validate(tag); err != nil {
			t.Errorf("expected %q to be valid, got error: %v", tag, err)
		}
	}

	invalid := []string{"", "-leading-dash", "has space", "has_underscore", "has/slash", "1.0.0"}
	for _, tag := range invalid {
		if err := Validate(tag); err == nil {
			t.Errorf("expected %q to be invalid", tag)
		}
	}
}

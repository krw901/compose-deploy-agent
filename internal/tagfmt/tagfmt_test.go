package tagfmt

import (
	"strings"
	"testing"
)

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

func TestValidateDigest(t *testing.T) {
	valid := "sha256:" + strings.Repeat("a1", 32)
	err := ValidateDigest(valid)
	if err != nil {
		t.Errorf("expected %q to be valid, got error: %v", valid, err)
	}

	invalid := []string{
		"",
		"a1b2c3d",
		"sha256:" + strings.Repeat("a", 63),
		"sha256:" + strings.Repeat("a", 65),
		"sha256:" + strings.Repeat("A", 64),
		"sha512:" + strings.Repeat("a", 64),
		"repo@sha256:" + strings.Repeat("a", 64),
		"sha256:" + strings.Repeat("a", 64) + "\nOTHER=1",
		" sha256:" + strings.Repeat("a", 64),
	}
	for _, digest := range invalid {
		if err := ValidateDigest(digest); err == nil {
			t.Errorf("expected %q to be invalid", digest)
		}
	}
}

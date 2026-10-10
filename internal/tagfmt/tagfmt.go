// Package tagfmt validates deploy image references (tags and digests)
// against the allowed character formats.
package tagfmt

import (
	"fmt"
	"regexp"
)

var pattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,127}$`)

// digestPattern accepts only lowercase sha256 content digests. Anything else
// (other algorithms, uppercase hex, a "repo@" prefix) is rejected, so a
// value that passes can be written into .env and interpolated into
// "repo@${VAR}" without any chance of injecting another line or reference.
var digestPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

// Validate returns an error if tag does not satisfy the format constraint.
func Validate(tag string) error {
	if !pattern.MatchString(tag) {
		return fmt.Errorf("tag %q does not match required format %s", tag, pattern.String())
	}
	return nil
}

// ValidateDigest returns an error if digest is not "sha256:<64 lowercase hex>".
func ValidateDigest(digest string) error {
	if !digestPattern.MatchString(digest) {
		return fmt.Errorf("digest %q does not match required format %s", digest, digestPattern.String())
	}
	return nil
}

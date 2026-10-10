// Package tagfmt validates deploy image tags against the allowed character format.
package tagfmt

import (
	"fmt"
	"regexp"
)

var pattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,127}$`)

// Validate returns an error if tag does not satisfy the format constraint.
func Validate(tag string) error {
	if !pattern.MatchString(tag) {
		return fmt.Errorf("tag %q does not match required format %s", tag, pattern.String())
	}
	return nil
}

// Package selfupdate implements `deploy-agent self-check` and
// `deploy-agent self-upgrade`: discovering the latest published agent
// version from GitHub Releases, downloading and verifying it, and
// atomically swapping it in for the installed binary.
package selfupdate

import (
	"cmp"
	"fmt"
	"strconv"
	"strings"
)

// Version is a parsed X.Y.Z semantic version, the only format the release
// workflow publishes (.github/workflows/release.yml rejects every other tag).
type Version struct {
	Major int
	Minor int
	Patch int
}

// ParseVersion parses s as X.Y.Z. Prefixes such as "v" and suffixes such as
// "-rc1" are rejected, matching the release workflow's tag validation.
func ParseVersion(s string) (Version, error) {
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return Version{}, fmt.Errorf("invalid version %q: expected X.Y.Z", s)
	}
	var nums [3]int
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return Version{}, fmt.Errorf("invalid version %q: expected X.Y.Z", s)
		}
		if n < 0 || p != strconv.Itoa(n) {
			return Version{}, fmt.Errorf("invalid version %q: expected X.Y.Z", s)
		}
		nums[i] = n
	}
	return Version{
		Major: nums[0],
		Minor: nums[1],
		Patch: nums[2],
	}, nil
}

// Compare returns -1, 0 or +1 depending on whether v is older than, equal
// to, or newer than other.
func (v Version) Compare(other Version) int {
	return cmp.Or(
		cmp.Compare(v.Major, other.Major),
		cmp.Compare(v.Minor, other.Minor),
		cmp.Compare(v.Patch, other.Patch),
	)
}

func (v Version) String() string {
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
}

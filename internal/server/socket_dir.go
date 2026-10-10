package server

import (
	"errors"
	"fmt"
	"os"
)

// ensurePrivateDir makes sure dir exists as a real directory (not a
// symlink) owned by the running user with no group or other permission
// bits (mode & 0077 == 0). A missing dir is created with mode 0700 (only
// the last path element): systemd normally creates the default
// /run/deploy-agent via RuntimeDirectory=, but older unit files or a
// manual `serve` do not. An existing dir that fails the check is never
// fixed up silently; serve refuses to start instead.
func ensurePrivateDir(dir string) error {
	err := os.Mkdir(dir, 0o700)
	if err != nil {
		if !errors.Is(err, os.ErrExist) {
			return fmt.Errorf("create socket directory: %w", err)
		}
	}
	return checkPrivateDir(dir, os.Geteuid())
}

// checkPrivateDir verifies dir is a directory owned by uid with
// mode & 0077 == 0.
func checkPrivateDir(dir string, uid int) error {
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("socket directory: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("socket directory %s is not a directory (symlinks are not accepted)", dir)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("socket directory %s has mode %04o; it must not be accessible to group or others "+
			"(run: chmod 0700 %s)", dir, info.Mode().Perm(), dir)
	}
	owner, ok := fileOwner(info)
	if ok && owner != uid {
		return fmt.Errorf("socket directory %s is owned by uid %d, not the running uid %d", dir, owner, uid)
	}
	return nil
}

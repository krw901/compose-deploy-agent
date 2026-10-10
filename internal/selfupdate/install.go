package selfupdate

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// BackupPath returns where Replace keeps the previous binary.
func BackupPath(target string) string {
	return target + ".prev"
}

// Replace swaps newPath in for target and keeps the previous binary at
// BackupPath(target). newPath must live in the same directory as target so
// the final rename is atomic: target always names a complete binary, never
// a missing or partially written one.
func Replace(target, newPath string) error {
	backup := BackupPath(target)
	err := os.Remove(backup)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("remove old backup %s: %w", backup, err)
		}
	}
	if err := os.Link(target, backup); err != nil {
		return fmt.Errorf("back up %s to %s: %w", target, backup, err)
	}
	if err := os.Rename(newPath, target); err != nil {
		return fmt.Errorf("rename %s over %s: %w", newPath, target, err)
	}
	return nil
}

// Restore moves the backup kept by Replace back over target.
func Restore(target string) error {
	backup := BackupPath(target)
	if err := os.Rename(backup, target); err != nil {
		return fmt.Errorf("restore %s from %s: %w", target, backup, err)
	}
	return nil
}

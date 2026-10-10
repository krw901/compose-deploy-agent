// Package singleton provides a process-level exclusivity guard so two
// `deploy-agent serve` processes can never run against the same data
// directory at once.
package singleton

import (
	"fmt"
	"os"
	"syscall"
)

// Lock acquires an exclusive, non-blocking flock on path (created if
// necessary) and returns a release function. It fails immediately if
// another process already holds the lock.
func Lock(path string) (release func(), err error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open lock file %s: %w", path, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("acquire lock on %s: another deploy-agent instance is running: %w", path, err)
	}
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}

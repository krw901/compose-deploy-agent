//go:build unix

package dockerx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestExecRunner_TimeoutKillsProcessGroup starts a shell that forks a
// long-running child holding stdout open. Killing only the shell would
// leave the child alive; the runner must kill the whole process group and
// return promptly.
func TestExecRunner_TimeoutKillsProcessGroup(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := execRunner{}.Run(ctx, "", nil, "sh", "-c", `sleep 60 & echo $! > "$0"; wait`, pidFile)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatalf("expected an error from a killed command")
	}
	if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatalf("expected the context deadline to have fired, got %v", ctx.Err())
	}
	if elapsed > execWaitDelay {
		t.Fatalf("Run took %s; the child kept the pipes open, so the process group was not killed", elapsed)
	}

	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("read child pid: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("parse child pid: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for processAlive(pid) {
		if time.Now().After(deadline) {
			t.Fatalf("child process %d still alive after timeout", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestExecRunner_NonZeroExitCode(t *testing.T) {
	out, err := execRunner{}.Run(t.Context(), "", nil, "sh", "-c", "exit 7")
	if err == nil {
		t.Fatalf("expected an error for a non-zero exit")
	}
	if out.ExitCode != 7 {
		t.Fatalf("expected exit code 7, got %d", out.ExitCode)
	}
}

// processAlive reports whether pid exists and is not a zombie (a killed
// child reparented to a non-reaping init may linger as a zombie).
func processAlive(pid int) bool {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false
	}
	_, rest, ok := strings.Cut(string(data), ") ")
	if !ok {
		return true
	}
	return !strings.HasPrefix(rest, "Z") && !strings.HasPrefix(rest, "X")
}

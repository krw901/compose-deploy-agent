package dockerx

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"time"
)

// execWaitDelay bounds how long Run waits for output pipes to close after
// the context is done and the process group has been killed.
const execWaitDelay = 5 * time.Second

// execRunner is the production Runner, shelling out to the real docker CLI.
type execRunner struct{}

// Run shells out to name with args and returns captured stdout, stderr, and exit status.
// When ctx is done the whole process group is killed (not just the direct
// child), so a timed-out command cannot leave helper processes behind.
func (execRunner) Run(ctx context.Context, dir string, extraEnv []string, name string, args ...string) (Output, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	if len(extraEnv) > 0 {
		cmd.Env = append(os.Environ(), extraEnv...)
	}
	killProcessGroupOnCancel(cmd)
	cmd.WaitDelay = execWaitDelay

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	out := Output{
		Stdout: stdout.String(),
		Stderr: stderr.String(),
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		out.ExitCode = exitErr.ExitCode()
	} else if err == nil {
		out.ExitCode = 0
	}
	return out, err
}

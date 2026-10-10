package dockerx

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
)

// execRunner is the production Runner, shelling out to the real docker CLI.
type execRunner struct{}

// Run shells out to name with args and returns captured stdout, stderr, and exit status.
func (execRunner) Run(ctx context.Context, dir string, extraEnv []string, name string, args ...string) (Output, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	if len(extraEnv) > 0 {
		cmd.Env = append(os.Environ(), extraEnv...)
	}

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

// Package dockerx wraps `docker` and `docker compose` invocations behind a
// small interface so the deployer state machine (internal/deployer) can be
// unit tested with a fake, without a real docker daemon. All commands are
// invoked via exec.CommandContext with argument arrays - never through a
// shell - so that a tag value can never be interpreted as shell syntax.
package dockerx

import "context"

// Runner executes a single command and captures its output. The production
// implementation (execRunner) shells out to the real docker CLI; tests
// substitute a fake.
type Runner interface {
	// Run executes name with args in dir (or the current directory if dir
	// is empty), with extraEnv appended to the process environment, and
	// returns combined stdout/stderr and any execution error (including a
	// non-zero exit code, wrapped as *exec.ExitError).
	Run(ctx context.Context, dir string, extraEnv []string, name string, args ...string) (Output, error)
}

// Output captures what a command produced.
type Output struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Client is the high-level docker/compose API used by the deployer.
type Client struct {
	runner Runner
}

// NewClient returns a Client that shells out to the real docker CLI.
func NewClient() *Client {
	return &Client{runner: execRunner{}}
}

// NewClientWithRunner returns a Client backed by a custom Runner, for tests.
func NewClientWithRunner(r Runner) *Client {
	return &Client{runner: r}
}

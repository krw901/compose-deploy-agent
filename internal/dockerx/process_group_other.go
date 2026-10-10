//go:build !unix

package dockerx

import "os/exec"

// killProcessGroupOnCancel keeps exec.CommandContext's default behavior
// (kill the direct child only) on platforms without process groups.
func killProcessGroupOnCancel(cmd *exec.Cmd) {}

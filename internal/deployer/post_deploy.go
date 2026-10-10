package deployer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/krwu/compose-deploy-agent/internal/config"
	"github.com/krwu/compose-deploy-agent/internal/dockerx"
)

// postDeployOutputLimit caps how much command stderr is kept in the deploy
// record message.
const postDeployOutputLimit = 512

// errPostDeployTimeout is the cancellation cause when post_deploy.timeout
// expires, distinguishing it from the overall deploy deadline.
var errPostDeployTimeout = errors.New("post_deploy timeout")

// runPostDeploy runs svc's fixed post_deploy command, if configured. The
// user, service and argv come only from services.yaml. It returns "" on
// success (or when nothing is configured), otherwise a description of the
// failure (exit code or timeout) for the deploy record.
func (deployer *Deployer) runPostDeploy(ctx context.Context, log *slog.Logger, svc config.Service) string {
	pd := svc.PostDeploy
	if pd == nil {
		return ""
	}
	pdCtx, cancel := context.WithTimeoutCause(ctx, pd.Timeout, errPostDeployTimeout)
	defer cancel()

	out, err := deployer.docker.Exec(pdCtx, dockerx.ExecSpec{
		Dir:            svc.Dir,
		File:           svc.File,
		ComposeService: pd.Service,
		User:           pd.User,
		Args:           pd.Args,
	})
	if err == nil {
		log.Info("post_deploy succeeded", "exec_service", pd.Service, "args", pd.Args)
		return ""
	}

	var failure string
	switch {
	case errors.Is(context.Cause(pdCtx), errPostDeployTimeout):
		failure = fmt.Sprintf("post_deploy timed out after %s (process killed)", pd.Timeout)
	case ctx.Err() != nil:
		failure = fmt.Sprintf("post_deploy interrupted: %v", context.Cause(ctx))
	case out.ExitCode != 0:
		failure = fmt.Sprintf("post_deploy failed with exit code %d", out.ExitCode)
	default:
		failure = fmt.Sprintf("post_deploy failed: %v", err)
	}
	stderr := tailOutput(out.Stderr)
	log.Error("post_deploy failed", "exec_service", pd.Service, "args", pd.Args,
		"exit_code", out.ExitCode, "error", err, "stderr", stderr)
	if stderr != "" {
		failure += ": " + stderr
	}
	return failure
}

// tailOutput keeps the last postDeployOutputLimit bytes of s, trimmed.
func tailOutput(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > postDeployOutputLimit {
		s = "..." + s[len(s)-postDeployOutputLimit:]
	}
	return s
}

package deployer

import (
	"context"
	"strings"

	"github.com/krwu/compose-deploy-agent/internal/config"
	"github.com/krwu/compose-deploy-agent/internal/dockerx"
	"github.com/krwu/compose-deploy-agent/internal/dotenv"
)

// calibrationTagPlaceholder is injected as the tag_env process
// environment variable when a service has never been deployed and its
// compose file requires the variable to be non-empty (the conventional
// ${TAG:?...} guard). It only needs to be a
// non-empty string so that `docker compose` can interpolate the file at
// all; ps/inspect-driven reads never resolve an image from it, they read
// the real image off the already-created container.
const calibrationTagPlaceholder = "unconfigured"

// composeEnvFallback returns the process env override to pass to
// read-only `docker compose ps` calls (status queries, startup
// calibration, readiness polling). If svc's tag_env already has a value in
// .env, it returns nil so that real value is used as-is; otherwise it
// supplies calibrationTagPlaceholder purely to satisfy compose's
// variable-interpolation requirement.
func composeEnvFallback(svc config.Service) []string {
	_, found, err := dotenv.GetLine(svc.EnvFile(), svc.EffectiveTagEnv())
	if err != nil {
		return []string{svc.EffectiveTagEnv() + "=" + calibrationTagPlaceholder}
	}
	if !found {
		return []string{svc.EffectiveTagEnv() + "=" + calibrationTagPlaceholder}
	}
	return nil
}

// runningSnapshot is what the deployer knows about a service's currently
// running container, read fresh from docker (never from sqlite).
type runningSnapshot struct {
	// Present is false if the compose service has no running container
	// at all (e.g. first-ever deploy).
	Present bool
	Tag     string
	ImageID string
	Healthy bool
}

// currentSnapshot reads the live state of svc's compose service from
// docker. Healthy reflects an instantaneous read (healthcheck status, or
// running-with-no-healthcheck), not a windowed readiness check.
func currentSnapshot(ctx context.Context, docker *dockerx.Client, svc config.Service) (runningSnapshot, error) {
	containerID, err := docker.ContainerID(ctx, svc.Dir, svc.File, svc.ComposeService, composeEnvFallback(svc))
	if err != nil {
		return runningSnapshot{}, err
	}
	if containerID == "" {
		return runningSnapshot{}, nil
	}

	state, err := docker.Inspect(ctx, containerID)
	if err != nil {
		return runningSnapshot{}, err
	}

	healthy := state.Running
	if state.HasHealthcheck {
		healthy = state.Healthy
	}

	return runningSnapshot{
		Present: true,
		Tag:     tagFromImageRef(state.ImageRef),
		ImageID: state.ImageID,
		Healthy: healthy,
	}, nil
}

// tagFromImageRef extracts the tag component from an image reference like
// "repo:tag" or "registry.example.com:5000/ns/repo:tag". The tag can only
// appear after the final "/", so a colon before that (e.g. a registry
// port) is never mistaken for a tag separator.
func tagFromImageRef(ref string) string {
	tail := ref
	if idx := strings.LastIndex(ref, "/"); idx >= 0 {
		tail = ref[idx+1:]
	}
	if idx := strings.LastIndex(tail, ":"); idx >= 0 {
		return tail[idx+1:]
	}
	return ""
}

package deployer

import (
	"context"
	"time"

	"github.com/krwu/compose-deploy-agent/internal/config"
)

type readinessStability struct {
	since            time.Time
	lastRestartCount int
	haveBaseline     bool
}

// waitReady polls docker until svc's compose service is confirmed running
// the expected image and passing the readiness check:
//   - if the container defines a HEALTHCHECK, wait for it to report healthy
//   - otherwise require Running with an unchanged RestartCount for
//     StableWindow, to filter out crash-restart loops
//
// It always also requires the running image ID to match wantImageID, to
// catch "container is up but still on the old image" (recreate not
// triggered, wrong project name, etc).
func (d *Deployer) waitReady(ctx context.Context, svc config.Service, wantImageID string) bool {
	deadline := time.Now().Add(d.readinessWindow)
	var stable readinessStability

	for {
		if ok := d.probeOnce(ctx, svc, wantImageID, &stable); ok {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(d.readinessInterval):
		}
	}
}

func (deployer *Deployer) probeOnce(ctx context.Context, svc config.Service, wantImageID string, stable *readinessStability) bool {
	docker := deployer.docker
	containerID, err := docker.ContainerID(ctx, svc.Dir, svc.File, svc.ComposeService, composeEnvFallback(svc))
	if err != nil {
		resetStability(stable)
		return false
	}
	if containerID == "" {
		resetStability(stable)
		return false
	}
	state, err := docker.Inspect(ctx, containerID)
	if err != nil {
		resetStability(stable)
		return false
	}
	if state.ImageID != wantImageID {
		resetStability(stable)
		return false
	}

	if state.HasHealthcheck {
		return state.Healthy
	}
	if !state.Running {
		resetStability(stable)
		return false
	}
	if !stable.haveBaseline || state.RestartCount != stable.lastRestartCount {
		stable.since = time.Now()
		stable.lastRestartCount = state.RestartCount
		stable.haveBaseline = true
		return false
	}
	return time.Since(stable.since) >= deployer.stableWindow
}

func resetStability(stable *readinessStability) {
	stable.since = time.Time{}
	stable.haveBaseline = false
}

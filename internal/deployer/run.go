package deployer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/krwu/compose-deploy-agent/internal/api"
	"github.com/krwu/compose-deploy-agent/internal/config"
	"github.com/krwu/compose-deploy-agent/internal/dockerx"
	"github.com/krwu/compose-deploy-agent/internal/dotenv"
	"github.com/krwu/compose-deploy-agent/internal/store"
)

// run executes the full deploy state machine for one deploy_id. It always
// terminates by writing a terminal result via FinishDeploy, and
// never panics on a docker/store error - every failure path maps to one of
// the api.Result values.
func (deployer *Deployer) run(ctx context.Context, service string, svc config.Service, targetTag, deployID string) {
	log := slog.With("deploy_id", deployID, "service", service, "target_tag", targetTag)

	snapshot, err := currentSnapshot(ctx, deployer.docker, svc)
	if err != nil {
		deployer.finish(deployID, deployOutcome(api.ResultPullFailed, api.RollbackNone, "", "",
			fmt.Sprintf("failed to read current running state: %v", err)))
		return
	}
	if err := deployer.store.RecordSnapshot(deployID, snapshot.Tag, snapshot.ImageID, snapshot.Healthy); err != nil {
		log.Error("record snapshot failed", "error", err)
	}
	deployer.seedLastHealthy(log, service, snapshot)

	expectedImage := svc.Image + ":" + targetTag
	if err := deployer.preflight(ctx, svc, expectedImage, targetTag); err != nil {
		deployer.finish(deployID, deployOutcome(api.ResultConfigInvalid, api.RollbackNone, "", "", err.Error()))
		return
	}

	if err := deployer.docker.Pull(ctx, composeTarget(svc, targetTag)); err != nil {
		deployer.finish(deployID, deployOutcome(api.ResultPullFailed, api.RollbackNone, "", "",
			fmt.Sprintf("pull failed: %v", err)))
		return
	}

	targetImageID, ok, err := deployer.docker.ImageID(ctx, expectedImage)
	if imageUnavailable(err, ok) {
		deployer.finish(deployID, deployOutcome(api.ResultPullFailed, api.RollbackNone, "", "",
			fmt.Sprintf("image %s not present locally after pull: %v", expectedImage, err)))
		return
	}

	// The tag must match too: compose recreates the container whenever the
	// resolved image reference changes, even if both tags share an image ID.
	if snapshot.Present && snapshot.Tag == targetTag && targetImageID == snapshot.ImageID {
		// No-op: still make sure .env reflects reality for anyone
		// operating on this directory by hand later.
		if err := dotenv.SetLine(svc.EnvFile(), svc.EffectiveTagEnv(), targetTag); err != nil {
			log.Error("noop .env correction failed", "error", err)
		}
		deployer.finish(deployID, deployOutcome(api.ResultNoop, api.RollbackNone, targetImageID, "", ""))
		return
	}

	if err := dotenv.SetLine(svc.EnvFile(), svc.EffectiveTagEnv(), targetTag); err != nil {
		deployer.finish(deployID, deployOutcome(api.ResultStartFailed, api.RollbackNone, targetImageID, "",
			fmt.Sprintf("failed to write .env: %v", err)))
		return
	}

	upErr := deployer.docker.Up(ctx, svc.Dir, svc.File, svc.ComposeService)
	ready := upErr == nil && deployer.waitReady(ctx, svc, targetImageID)
	if upErr == nil && ready {
		if err := deployer.store.SetLastHealthy(service, targetTag, targetImageID, time.Now()); err != nil {
			log.Error("update last_healthy failed", "error", err)
		}
		deployer.finish(deployID, deployOutcome(api.ResultSuccess, api.RollbackNone, targetImageID, "", ""))
		return
	}

	upMsg := ""
	if upErr != nil {
		upMsg = fmt.Sprintf("up failed: %v; ", upErr)
	} else {
		upMsg = "readiness check timed out; "
	}
	deployer.attemptRollback(ctx, log, rollbackInput{
		service:       service,
		svc:           svc,
		deployID:      deployID,
		snapshot:      snapshot,
		targetImageID: targetImageID,
		msgPrefix:     upMsg,
	})
}

// seedLastHealthy records the current healthy version the first time a
// service is deployed, so a failed first attempt still has a rollback
// target.
func (d *Deployer) seedLastHealthy(log *slog.Logger, service string, snapshot runningSnapshot) {
	if !snapshot.Present || !snapshot.Healthy || snapshot.Tag == "" {
		return
	}
	_, err := d.store.GetLastHealthy(service)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			if err := d.store.SetLastHealthy(service, snapshot.Tag, snapshot.ImageID, time.Now()); err != nil {
				log.Error("seed last_healthy failed", "error", err)
			}
		}
	}
}

// preflight checks, without touching .env, that compose resolves svc's image
// to expectedImage and that its pull_policy lets the agent deploy the
// registry image for targetTag. Any error maps to config_invalid.
func (d *Deployer) preflight(ctx context.Context, svc config.Service, expectedImage, targetTag string) error {
	resolved, err := d.docker.ResolveService(ctx, composeTarget(svc, targetTag))
	if err != nil {
		return fmt.Errorf("compose config failed: %w", err)
	}
	if resolved.Image != expectedImage {
		return fmt.Errorf("compose config did not resolve %s.image to %q (got %q)",
			svc.ComposeService, expectedImage, resolved.Image)
	}
	switch resolved.PullPolicy {
	case dockerx.PullPolicyNever, dockerx.PullPolicyBuild:
		// never means the image is managed outside the registry, and build
		// makes `up` rebuild locally instead of running the pulled image.
		return fmt.Errorf("%s.pull_policy %q is not supported by deploy-agent; remove it or use missing/always",
			svc.ComposeService, resolved.PullPolicy)
	default:
		return nil
	}
}

type rollbackInput struct {
	service       string
	svc           config.Service
	deployID      string
	snapshot      runningSnapshot
	targetImageID string
	msgPrefix     string
}

func (deployer *Deployer) attemptRollback(ctx context.Context, log *slog.Logger, in rollbackInput) {
	lastHealthy, err := deployer.store.GetLastHealthy(in.service)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			log.Error("read last_healthy failed", "error", err)
		}
	}
	target := chooseRollbackTarget(in.snapshot, lastHealthy)
	if !target.Available {
		deployer.finish(in.deployID, deployOutcome(api.ResultStartFailed, api.RollbackNone, in.targetImageID, "",
			in.msgPrefix+"no rollback target available"))
		return
	}

	rollbackImageID, ok, err := deployer.docker.ImageID(ctx, in.svc.Image+":"+target.Tag)
	if imageUnavailable(err, ok) {
		if pullErr := deployer.docker.Pull(ctx, composeTarget(in.svc, target.Tag)); pullErr != nil {
			deployer.finish(in.deployID, deployOutcome(
				api.ResultStartFailed, api.RollbackRollbackFailed, in.targetImageID, target.Tag,
				fmt.Sprintf("%srollback image %s unavailable: %v", in.msgPrefix, target.Tag, pullErr)))
			return
		}
		rollbackImageID, ok, err = deployer.docker.ImageID(ctx, in.svc.Image+":"+target.Tag)
		if imageUnavailable(err, ok) {
			deployer.finish(in.deployID, deployOutcome(
				api.ResultStartFailed, api.RollbackRollbackFailed, in.targetImageID, target.Tag,
				fmt.Sprintf("%srollback image %s still unavailable after pull", in.msgPrefix, target.Tag)))
			return
		}
	}

	if err := dotenv.SetLine(in.svc.EnvFile(), in.svc.EffectiveTagEnv(), target.Tag); err != nil {
		deployer.finish(in.deployID, deployOutcome(
			api.ResultStartFailed, api.RollbackRollbackFailed, in.targetImageID, target.Tag,
			fmt.Sprintf("%sfailed to write .env for rollback: %v", in.msgPrefix, err)))
		return
	}

	upErr := deployer.docker.Up(ctx, in.svc.Dir, in.svc.File, in.svc.ComposeService)
	ready := upErr == nil && deployer.waitReady(ctx, in.svc, rollbackImageID)
	if upErr == nil && ready {
		deployer.finish(in.deployID, deployOutcome(
			api.ResultStartFailed, api.RollbackRolledBack, in.targetImageID, target.Tag,
			in.msgPrefix+fmt.Sprintf("rolled back to %s", target.Tag)))
		return
	}

	rollbackMsg := ""
	if upErr != nil {
		rollbackMsg = fmt.Sprintf("rollback up failed: %v", upErr)
	} else {
		rollbackMsg = "rollback readiness check timed out"
	}
	deployer.finish(in.deployID, deployOutcome(
		api.ResultStartFailed, api.RollbackRollbackFailed, in.targetImageID, target.Tag,
		in.msgPrefix+rollbackMsg))
}

func (d *Deployer) finish(deployID string, outcome store.DeployOutcome) {
	outcome.FinishedAt = time.Now()
	if err := d.store.FinishDeploy(deployID, outcome); err != nil {
		slog.Error("finish deploy failed", "deploy_id", deployID, "error", err)
	}
}

func composeTarget(svc config.Service, tag string) dockerx.ComposeTarget {
	return dockerx.ComposeTarget{
		Dir:            svc.Dir,
		File:           svc.File,
		ComposeService: svc.ComposeService,
		TagEnvKey:      svc.EffectiveTagEnv(),
		Tag:            tag,
	}
}

func deployOutcome(result api.Result, rollback api.Rollback, targetImageID, rolledBackTo, message string) store.DeployOutcome {
	return store.DeployOutcome{
		Result:        result,
		Rollback:      rollback,
		TargetImageID: targetImageID,
		RolledBackTo:  rolledBackTo,
		Message:       message,
	}
}

func imageUnavailable(err error, ok bool) bool {
	if err != nil {
		return true
	}
	return !ok
}

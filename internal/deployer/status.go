package deployer

import (
	"context"
	"errors"
	"fmt"

	"github.com/krwu/compose-deploy-agent/internal/api"
	"github.com/krwu/compose-deploy-agent/internal/dotenv"
	"github.com/krwu/compose-deploy-agent/internal/store"
)

// GetDeploy returns the API view of a single deploy record, for
// GET /v1/deploys/{deploy_id}.
func (d *Deployer) GetDeploy(deployID string) (api.DeployStatus, error) {
	rec, err := d.store.GetDeploy(deployID)
	if err != nil {
		return api.DeployStatus{}, err
	}
	return toDeployStatus(rec), nil
}

func toDeployStatus(rec *store.DeployRecord) api.DeployStatus {
	out := api.DeployStatus{
		DeployID:   rec.DeployID,
		Service:    rec.Service,
		State:      rec.State,
		Result:     rec.Result,
		Rollback:   rec.Rollback,
		TargetTag:  rec.TargetTag,
		Message:    rec.Message,
		StartedAt:  rec.StartedAt,
		FinishedAt: rec.FinishedAt,
	}
	if rec.RunningTag.Valid {
		out.RunningTag = rec.RunningTag.String
	}
	if rec.TargetImageID.Valid {
		out.ImageID = rec.TargetImageID.String
	}
	if rec.RolledBackTo.Valid {
		out.RolledBackTo = rec.RolledBackTo.String
	}
	return out
}

// GetServiceStatus returns the human-facing view of a managed service's
// current state, for GET /v1/status/{service}.
func (deployer *Deployer) GetServiceStatus(ctx context.Context, service string) (api.ServiceStatus, error) {
	svc, ok := deployer.manifest.Lookup(service)
	if !ok {
		return api.ServiceStatus{}, ErrServiceNotFound{Service: service}
	}

	out := api.ServiceStatus{Service: service}

	deployer.mu.Lock()
	_, deploying := deployer.active[service]
	deployer.mu.Unlock()
	out.Deploying = deploying

	snapshot, err := currentSnapshot(ctx, deployer.docker, svc)
	if err != nil {
		return api.ServiceStatus{}, fmt.Errorf("read running state: %w", err)
	}
	if snapshot.Present {
		out.Running = api.RunningInfo{
			Tag:     snapshot.Tag,
			ImageID: snapshot.ImageID,
		}
	}

	envTag, found, err := dotenv.GetLine(svc.EnvFile(), svc.EffectiveTagEnv())
	if err != nil {
		return api.ServiceStatus{}, fmt.Errorf("read .env: %w", err)
	}
	if found {
		out.EnvTag = envTag
	}

	if lh, err := deployer.store.GetLastHealthy(service); err == nil {
		out.LastHealthy = &api.LastHealthy{
			Tag:       lh.Tag,
			ImageID:   lh.ImageID,
			UpdatedAt: lh.UpdatedAt,
		}
	} else if !errors.Is(err, store.ErrNotFound) {
		return api.ServiceStatus{}, fmt.Errorf("read last_healthy: %w", err)
	}

	if last, err := deployer.store.LatestDeploy(service); err == nil {
		out.LastDeploy = &api.LastDeploySummary{
			DeployID:   last.DeployID,
			TargetTag:  last.TargetTag,
			Result:     last.Result,
			Rollback:   last.Rollback,
			StartedAt:  last.StartedAt,
			FinishedAt: last.FinishedAt,
		}
	} else if !errors.Is(err, store.ErrNotFound) {
		return api.ServiceStatus{}, fmt.Errorf("read latest deploy: %w", err)
	}

	return out, nil
}

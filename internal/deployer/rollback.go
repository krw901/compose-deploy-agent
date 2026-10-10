package deployer

import "github.com/krwu/compose-deploy-agent/internal/store"

// rollbackTarget is the version to roll back to, if any.
type rollbackTarget struct {
	Available bool
	Tag       string
	ImageID   string
}

// chooseRollbackTarget picks a rollback version: prefer this deploy's own
// pre-deploy snapshot if it was
// healthy (it is the most specific, most recent known-good state); fall
// back to the service's last_healthy record; otherwise there is nothing
// safe to roll back to.
func chooseRollbackTarget(snapshot runningSnapshot, lastHealthy *store.LastHealthyRecord) rollbackTarget {
	if snapshot.Present && snapshot.Healthy && snapshot.Tag != "" {
		return rollbackTarget{
			Available: true,
			Tag:       snapshot.Tag,
			ImageID:   snapshot.ImageID,
		}
	}
	if lastHealthy != nil {
		return rollbackTarget{
			Available: true,
			Tag:       lastHealthy.Tag,
			ImageID:   lastHealthy.ImageID,
		}
	}
	return rollbackTarget{Available: false}
}

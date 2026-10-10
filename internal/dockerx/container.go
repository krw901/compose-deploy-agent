package dockerx

import (
	"context"
	"encoding/json"
	"fmt"
)

// ContainerState is the subset of `docker inspect` output the readiness
// checker needs: whether a healthcheck
// is defined and its status, whether the container is running, how many
// times it has restarted, and which image ID it is actually running.
type ContainerState struct {
	// ImageID is the content-addressed image ID actually running,
	// compared against the target tag's resolved image ID to catch
	// "container is up but still on the old image".
	ImageID string
	// ImageRef is the image reference ("repo:tag") the container was
	// created with, i.e. the "running tag".
	ImageRef string
	// HasHealthcheck reports whether the container defines a HEALTHCHECK.
	HasHealthcheck bool
	// Healthy reports the healthcheck status when HasHealthcheck is true.
	Healthy bool
	// Running reports whether the container's State.Running is true.
	Running bool
	// RestartCount is the number of times docker has restarted this
	// container since creation.
	RestartCount int
}

type inspectResult struct {
	Image  string `json:"Image"`
	Config struct {
		Image string `json:"Image"`
	} `json:"Config"`
	State struct {
		Running bool `json:"Running"`
		Health  *struct {
			Status string `json:"Status"`
		} `json:"Health"`
	} `json:"State"`
	RestartCount int `json:"RestartCount"`
}

// Inspect returns the current state of a container by id.
func (client *Client) Inspect(ctx context.Context, containerID string) (ContainerState, error) {
	out, err := client.runner.Run(ctx, "", nil, "docker", "inspect", containerID)
	if err != nil {
		return ContainerState{}, fmt.Errorf("docker inspect %s: %w: %s", containerID, err, out.Stderr)
	}

	var results []inspectResult
	if err := json.Unmarshal([]byte(out.Stdout), &results); err != nil {
		return ContainerState{}, fmt.Errorf("parse docker inspect json: %w", err)
	}
	if len(results) == 0 {
		return ContainerState{}, fmt.Errorf("docker inspect %s returned no results", containerID)
	}
	r := results[0]

	state := ContainerState{
		ImageID:      r.Image,
		ImageRef:     r.Config.Image,
		Running:      r.State.Running,
		RestartCount: r.RestartCount,
	}
	if r.State.Health != nil {
		state.HasHealthcheck = true
		state.Healthy = r.State.Health.Status == "healthy"
	}
	return state, nil
}

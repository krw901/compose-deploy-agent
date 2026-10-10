package dockerx

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"strings"
)

// ComposeProject is one entry of `docker compose ls --format json`.
type ComposeProject struct {
	Name   string `json:"Name"`
	Status string `json:"Status"`
	// ConfigFiles is a comma-separated list of absolute compose file paths.
	ConfigFiles string `json:"ConfigFiles"`
}

// ConfigFileList splits ConfigFiles into individual paths.
func (p ComposeProject) ConfigFileList() []string {
	var files []string
	for f := range strings.SplitSeq(p.ConfigFiles, ",") {
		if f = strings.TrimSpace(f); f != "" {
			files = append(files, f)
		}
	}
	return files
}

// ListProjects returns every compose project docker knows about, including
// stopped ones.
func (c *Client) ListProjects(ctx context.Context) ([]ComposeProject, error) {
	out, err := c.runner.Run(ctx, "", nil, "docker", "compose", "ls", "--all", "--format", "json")
	if err != nil {
		return nil, fmt.Errorf("docker compose ls: %w: %s", err, out.Stderr)
	}
	var projects []ComposeProject
	if err := json.Unmarshal([]byte(out.Stdout), &projects); err != nil {
		return nil, fmt.Errorf("parse compose ls json: %w", err)
	}
	return projects, nil
}

// RawServiceImages returns each service's image as written in the compose
// file, before variable interpolation, keyed by compose service name.
// Services without an image (build-only) map to "".
func (c *Client) RawServiceImages(ctx context.Context, dir, file string) (map[string]string, error) {
	return c.serviceImages(ctx, dir, file, "--no-interpolate")
}

// ResolvedServiceImages returns each service's image after interpolation
// with the project's .env file and nothing else injected, i.e. what a human
// running `docker compose up` in dir would get.
func (c *Client) ResolvedServiceImages(ctx context.Context, dir, file string) (map[string]string, error) {
	return c.serviceImages(ctx, dir, file)
}

func (c *Client) serviceImages(ctx context.Context, dir, file string, extraArgs ...string) (map[string]string, error) {
	args := append([]string{"compose", "-f", file, "config", "--format", "json"}, extraArgs...)
	out, err := c.runner.Run(ctx, dir, nil, "docker", args...)
	if err != nil {
		return nil, fmt.Errorf("docker compose config: %w: %s", err, out.Stderr)
	}
	var cfg struct {
		Services map[string]struct {
			Image string `json:"image"`
		} `json:"services"`
	}
	if err := json.Unmarshal([]byte(out.Stdout), &cfg); err != nil {
		return nil, fmt.Errorf("parse compose config json: %w", err)
	}
	images := make(map[string]string, len(cfg.Services))
	for name, svc := range cfg.Services {
		images[name] = svc.Image
	}
	return images, nil
}

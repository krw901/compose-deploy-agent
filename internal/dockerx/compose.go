package dockerx

import (
	"context"
	"encoding/json"
	"fmt"
)

// PullPolicy is a compose service's pull_policy value.
type PullPolicy string

// Pull policies the deployer must recognise; see
// https://docs.docker.com/reference/compose-file/services/#pull_policy
const (
	// PullPolicyAlways always pulls from the registry.
	PullPolicyAlways PullPolicy = "always"
	// PullPolicyNever never pulls; the image must already be local.
	PullPolicyNever PullPolicy = "never"
	// PullPolicyBuild builds the image locally instead of pulling it.
	PullPolicyBuild PullPolicy = "build"
)

// ResolvedService is the subset of a service's resolved compose config that
// the deployer's pre-flight check inspects.
type ResolvedService struct {
	// Image is the fully interpolated image reference ("repo:tag").
	Image string `json:"image"`
	// PullPolicy is empty when the compose file does not set one.
	PullPolicy PullPolicy `json:"pull_policy"`
}

// ComposeTarget names one compose service and the tag variable injected
// into a single docker compose invocation.
type ComposeTarget struct {
	Dir            string
	File           string
	ComposeService string
	TagEnvKey      string
	Tag            string
}

// composeConfig is the subset of `docker compose config --format json`
// output that we need: each service's resolved image and pull policy.
type composeConfig struct {
	Services map[string]ResolvedService `json:"services"`
}

// ResolveService runs `docker compose config` with tagEnv=tag injected as a
// process environment variable (not written to .env yet) and returns what
// compose would use for composeService. Used for the pre-flight check
// (config_invalid).
func (c *Client) ResolveService(ctx context.Context, target ComposeTarget) (ResolvedService, error) {
	out, err := c.runner.Run(ctx, target.Dir, []string{target.TagEnvKey + "=" + target.Tag},
		"docker", "compose", "-f", target.File, "config", "--format", "json")
	if err != nil {
		return ResolvedService{}, fmt.Errorf("docker compose config: %w: %s", err, out.Stderr)
	}
	var cfg composeConfig
	if err := json.Unmarshal([]byte(out.Stdout), &cfg); err != nil {
		return ResolvedService{}, fmt.Errorf("parse compose config json: %w", err)
	}
	svc, ok := cfg.Services[target.ComposeService]
	if !ok {
		return ResolvedService{}, fmt.Errorf("compose service %q not found in resolved config", target.ComposeService)
	}
	return svc, nil
}

// Pull runs `docker compose pull --policy always <service>` with tagEnv=tag
// injected as a process environment variable. It does not touch .env and
// does not change any running container.
//
// --policy always overrides the compose file's pull_policy: under
// missing/if_not_present compose skips any tag already present locally, so
// a tag re-pushed to the registry would never be refreshed.
func (c *Client) Pull(ctx context.Context, target ComposeTarget) error {
	out, err := c.runner.Run(ctx, target.Dir, []string{target.TagEnvKey + "=" + target.Tag},
		"docker", "compose", "-f", target.File, "pull", "--policy", string(PullPolicyAlways), target.ComposeService)
	if err != nil {
		return fmt.Errorf("docker compose pull: %w: %s", err, out.Stderr)
	}
	return nil
}

// Up runs `docker compose up -d <service>`. The target tag must already be
// written into the directory's .env file before calling this. Up
// intentionally does not pass -p or --remove-orphans.
func (c *Client) Up(ctx context.Context, dir, file, composeService string) error {
	out, err := c.runner.Run(ctx, dir, nil, "docker", "compose", "-f", file, "up", "-d", composeService)
	if err != nil {
		return fmt.Errorf("docker compose up: %w: %s", err, out.Stderr)
	}
	return nil
}

// ContainerID returns the container id for composeService in the compose
// project rooted at dir/file. Returns "" if the service has no running
// container.
//
// extraEnv is passed through to `docker compose ps`, which - like every
// other compose subcommand - still fully interpolates the compose file
// before doing anything else. For a service that has never been deployed,
// its tag_env variable has no value anywhere (not in .env, not in the
// process environment) and the compose file's ${TAG:?...} guard makes
// interpolation fail outright, so callers that only need to know whether a
// container exists yet (status queries, startup calibration, readiness
// polling) should pass a placeholder value here rather than nil - see
// deployer.composeEnvFallback.
func (c *Client) ContainerID(ctx context.Context, dir, file, composeService string, extraEnv []string) (string, error) {
	out, err := c.runner.Run(ctx, dir, extraEnv, "docker", "compose", "-f", file, "ps", "-q", composeService)
	if err != nil {
		return "", fmt.Errorf("docker compose ps: %w: %s", err, out.Stderr)
	}
	id := firstLine(out.Stdout)
	return id, nil
}

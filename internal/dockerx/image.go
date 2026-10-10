package dockerx

import (
	"context"
	"fmt"
	"strings"
)

// ImageID returns the local image ID for ref ("repo:tag"). ok is false if
// the image is not present locally (e.g. never pulled), which is not
// treated as an error by callers.
func (c *Client) ImageID(ctx context.Context, ref string) (id string, ok bool, err error) {
	out, runErr := c.runner.Run(ctx, "", nil, "docker", "image", "inspect", ref, "--format", "{{.Id}}")
	if runErr != nil {
		if strings.Contains(out.Stderr, "No such image") {
			return "", false, nil
		}
		return "", false, fmt.Errorf("docker image inspect %s: %w: %s", ref, runErr, out.Stderr)
	}
	return firstLine(out.Stdout), true, nil
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		s = s[:idx]
	}
	return strings.TrimSpace(s)
}

package dockerx

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"
)

// scriptedRunner answers each command line (joined with spaces) with a
// canned stdout, and records the directory it was run in.
type scriptedRunner struct {
	stdout map[string]string
	dirs   []string
}

func (r *scriptedRunner) Run(_ context.Context, dir string, _ []string, name string, args ...string) (Output, error) {
	r.dirs = append(r.dirs, dir)
	cmd := name + " " + strings.Join(args, " ")
	out, ok := r.stdout[cmd]
	if !ok {
		return Output{
			Stderr:   "unexpected command",
			ExitCode: 1,
		}, errors.New("unexpected command: " + cmd)
	}
	return Output{Stdout: out}, nil
}

func TestClient_ListProjects(t *testing.T) {
	r := &scriptedRunner{stdout: map[string]string{
		"docker compose ls --all --format json": `[{"Name":"lss","Status":"running(2)","ConfigFiles":"/srv/lss/compose.yml"},` +
			`{"Name":"multi","Status":"exited(1)","ConfigFiles":"/srv/m/a.yml,/srv/m/b.yml"}]`,
	}}
	projects, err := NewClientWithRunner(r).ListProjects(t.Context())
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(projects) != 2 || projects[0].Name != "lss" {
		t.Fatalf("unexpected projects %+v", projects)
	}
	if got, want := projects[1].ConfigFileList(), []string{"/srv/m/a.yml", "/srv/m/b.yml"}; !slices.Equal(got, want) {
		t.Fatalf("ConfigFileList = %v, want %v", got, want)
	}
}

func TestClient_ServiceImages(t *testing.T) {
	config := `{"name":"lss","services":{"web":{"image":"repo/web:${WEB_TAG:?}"},"worker":{"build":{"context":"."}}}}`
	r := &scriptedRunner{stdout: map[string]string{
		"docker compose -f compose.yml config --format json --no-interpolate": config,
		"docker compose -f compose.yml config --format json":                  `{"services":{"web":{"image":"repo/web:1.0"}}}`,
	}}
	c := NewClientWithRunner(r)

	raw, err := c.RawServiceImages(t.Context(), "/srv/lss", "compose.yml")
	if err != nil {
		t.Fatalf("RawServiceImages: %v", err)
	}
	if want := map[string]string{"web": "repo/web:${WEB_TAG:?}", "worker": ""}; !maps.Equal(raw, want) {
		t.Fatalf("RawServiceImages = %v, want %v", raw, want)
	}

	resolved, err := c.ResolvedServiceImages(t.Context(), "/srv/lss", "compose.yml")
	if err != nil {
		t.Fatalf("ResolvedServiceImages: %v", err)
	}
	if resolved["web"] != "repo/web:1.0" {
		t.Fatalf("ResolvedServiceImages = %v", resolved)
	}
	if !slices.Equal(r.dirs, []string{"/srv/lss", "/srv/lss"}) {
		t.Fatalf("commands ran in %v, want the project dir", r.dirs)
	}
}

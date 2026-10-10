package setup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/krwu/compose-deploy-agent/internal/config"
	"github.com/krwu/compose-deploy-agent/internal/dockerx"
)

// fakeInspector serves compose projects and images from memory. resolve,
// when set, computes ResolvedServiceImages from the files on disk.
type fakeInspector struct {
	projects []dockerx.ComposeProject
	raw      map[string]map[string]string
	resolve  func(dir, file string) (map[string]string, error)
}

func (f *fakeInspector) ListProjects(context.Context) ([]dockerx.ComposeProject, error) {
	if f.projects == nil {
		return nil, errors.New("docker not available")
	}
	return f.projects, nil
}

func (f *fakeInspector) RawServiceImages(_ context.Context, dir, file string) (map[string]string, error) {
	images, ok := f.raw[filepath.Join(dir, file)]
	if !ok {
		return nil, errors.New("no such project")
	}
	return images, nil
}

func (f *fakeInspector) ResolvedServiceImages(_ context.Context, dir, file string) (map[string]string, error) {
	return f.resolve(dir, file)
}

func TestTagEnvName(t *testing.T) {
	tests := map[string]string{
		"lss-web":  "LSS_WEB_TAG",
		"api":      "API_TAG",
		"web.v2":   "WEB_V2_TAG",
		"2fa":      "_2FA_TAG",
		"my_svc_1": "MY_SVC_1_TAG",
	}
	for in, want := range tests {
		if got := TagEnvName(in); got != want {
			t.Errorf("TagEnvName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseImageRef(t *testing.T) {
	tests := []struct {
		in   string
		want ImageRef
	}{
		{"repo/web:1.2.3", ImageRef{
			Repo: "repo/web",
			Tag:  "1.2.3",
		}},
		{"repo/web", ImageRef{Repo: "repo/web"}},
		{"registry:5000/web", ImageRef{Repo: "registry:5000/web"}},
		{"registry:5000/web:v1", ImageRef{
			Repo: "registry:5000/web",
			Tag:  "v1",
		}},
		{"repo/web:${WEB_TAG:?WEB_TAG is required}", ImageRef{
			Repo: "repo/web",
			Var:  "WEB_TAG",
			Op:   ":?",
			Arg:  "WEB_TAG is required",
		}},
		{"repo/web:${TAG:-latest}", ImageRef{
			Repo: "repo/web",
			Var:  "TAG",
			Op:   ":-",
			Arg:  "latest",
		}},
		{"repo/web:${TAG}", ImageRef{
			Repo: "repo/web",
			Var:  "TAG",
		}},
		{"repo/web:$TAG", ImageRef{
			Repo: "repo/web",
			Var:  "TAG",
		}},
	}
	for _, tt := range tests {
		got, err := ParseImageRef(tt.in, english)
		if err != nil {
			t.Errorf("ParseImageRef(%q): %v", tt.in, err)
			continue
		}
		if got != tt.want {
			t.Errorf("ParseImageRef(%q) = %+v, want %+v", tt.in, got, tt.want)
		}
	}

	for _, bad := range []string{"", "repo/web@sha256:abc", "${REGISTRY}/web:1", "repo/web:v-${TAG}", "repo/web:${A}${B}"} {
		if _, err := ParseImageRef(bad, english); err == nil {
			t.Errorf("ParseImageRef(%q): expected error", bad)
		}
	}
}

func TestDiscoverCandidates(t *testing.T) {
	dir := t.TempDir()
	compose := `services:
  web:
    image: repo/web:${WEB_TAG:?WEB_TAG is required}
  worker:
    image: repo/worker:1.0
  db:
    image: postgres@sha256:abc
  base: &base
    image: &img repo/base:1
  child:
    <<: *base
  a-b:
    image: repo/ab:1
  a_b:
    image: repo/ab2:1
`
	path := filepath.Join(dir, "compose.yml")
	if err := os.WriteFile(path, []byte(compose), 0o644); err != nil {
		t.Fatal(err)
	}
	docker := &fakeInspector{
		projects: []dockerx.ComposeProject{
			{
				Name:        "zeta",
				ConfigFiles: "/srv/z/a.yml,/srv/z/b.yml",
			},
			{
				Name:        "app",
				ConfigFiles: path,
			},
		},
		raw: map[string]map[string]string{path: {
			"web":    "repo/web:${WEB_TAG:?WEB_TAG is required}",
			"worker": "repo/worker:1.0",
			"db":     "postgres@sha256:abc",
			"base":   "repo/base:1",
			"child":  "repo/base:1",
			"a-b":    "repo/ab:1",
			"a_b":    "repo/ab2:1",
		}},
	}

	got, err := DiscoverCandidates(t.Context(), docker, english)
	if err != nil {
		t.Fatalf("DiscoverCandidates: %v", err)
	}
	status := map[string]string{}
	for _, c := range got {
		switch {
		case c.Unsupported != "":
			status[c.Label()] = "unsupported"
		case c.NeedsRewrite():
			status[c.Label()] = "rewrite"
		default:
			status[c.Label()] = "ready"
		}
	}
	want := map[string]string{
		"app/a-b":    "rewrite",
		"app/a_b":    "unsupported",
		"app/base":   "unsupported",
		"app/child":  "unsupported",
		"app/db":     "unsupported",
		"app/web":    "ready",
		"app/worker": "rewrite",
		"zeta":       "unsupported",
	}
	for label, s := range want {
		if status[label] != s {
			t.Errorf("%s: got %q, want %q (all: %v)", label, status[label], s, status)
		}
	}
	if got[0].Project != "app" {
		t.Errorf("expected projects sorted by name, first is %q", got[0].Project)
	}
}

func TestLogicalNamesAndBuildManifest(t *testing.T) {
	root := t.TempDir()
	selected := []Candidate{
		{Project: "shop", Dir: filepath.Join(root, "shop"), File: "compose.yml", ComposeService: "web",
			Ref: ImageRef{Repo: "repo/shop-web"}, TagEnv: "WEB_TAG"},
		{Project: "blog", Dir: filepath.Join(root, "blog"), File: "compose.yml", ComposeService: "web",
			Ref: ImageRef{Repo: "repo/blog-web"}, TagEnv: "WEB_TAG"},
		{Project: "blog", Dir: filepath.Join(root, "blog"), File: "compose.yml", ComposeService: "api",
			Ref: ImageRef{Repo: "repo/blog-api"}, TagEnv: "API_TAG"},
	}
	names := LogicalNames(selected, nil)
	if want := []string{"shop-web", "blog-web", "api"}; !slices.Equal(names, want) {
		t.Fatalf("LogicalNames = %v, want %v", names, want)
	}

	data, err := BuildManifest(selected, names, nil)
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	if !strings.HasPrefix(string(data), "# Generated by") {
		t.Errorf("expected header comment, got:\n%s", data)
	}
	m, err := config.Parse(data)
	if err != nil {
		t.Fatalf("parse generated manifest: %v", err)
	}
	svc, ok := m.Lookup("blog-web")
	if !ok || svc.Image != "repo/blog-web" || svc.TagEnv != "WEB_TAG" || svc.ComposeService != "web" {
		t.Fatalf("unexpected blog-web entry %+v", svc)
	}

	if _, err := BuildManifest(selected[:2], []string{"web", "web"}, nil); err == nil {
		t.Fatalf("expected duplicate name error")
	}
}

func TestExistingManifest(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "krweb")
	manifest := "version: 1\nservices:\n" +
		"  lss-api:\n    dir: " + dir + "\n    file: compose.yml\n    service: lss\n" +
		"    image: repo/lss\n    tag_env: LSS_API_IMAGE_TAG\n" +
		"  api:\n    dir: " + filepath.Join(root, "gone") + "\n    file: compose.yml\n    service: api\n    image: repo/api\n"
	path := filepath.Join(root, "services.yaml")
	if err := os.WriteFile(path, []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	existing, err := LoadExistingManifest(path)
	if err != nil {
		t.Fatalf("LoadExistingManifest: %v", err)
	}

	lss := Candidate{Project: "krweb", Dir: dir + "/", File: "compose.yml", ComposeService: "lss",
		Ref: ImageRef{Repo: "repo/lss"}, TagEnv: "LSS_TAG"}
	newAPI := Candidate{Project: "shop", Dir: filepath.Join(root, "shop"), File: "compose.yml", ComposeService: "api",
		Ref: ImageRef{Repo: "repo/shop-api"}, TagEnv: "API_TAG"}
	if name, ok := existing.NameOf(lss); !ok || name != "lss-api" {
		t.Fatalf("NameOf(lss) = %q, %v", name, ok)
	}

	selected := []Candidate{lss, newAPI}
	// The managed service keeps its name; the new one avoids the name the
	// existing manifest already uses.
	names := LogicalNames(selected, existing)
	if want := []string{"lss-api", "shop-api"}; !slices.Equal(names, want) {
		t.Fatalf("LogicalNames = %v, want %v", names, want)
	}

	kept := existing.Unmatched(selected)
	if len(kept) != 1 || kept["api"].ComposeService != "api" {
		t.Fatalf("Unmatched = %v, want only api", kept)
	}
	data, err := BuildManifest(selected, names, kept)
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	m, err := config.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if svc, _ := m.Lookup("lss-api"); svc.TagEnv != "LSS_TAG" {
		t.Fatalf("managed service must switch to the generated tag_env, got %q", svc.TagEnv)
	}
	if _, ok := m.Lookup("api"); !ok {
		t.Fatalf("kept entry missing:\n%s", data)
	}
}

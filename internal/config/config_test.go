package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParse_Valid(t *testing.T) {
	data := []byte(`
version: 1
services:
  lss-web:
    dir: /srv/lss-web
    file: compose.yml
    service: lss-web
    image: registry.example.com/web
`)
	m, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	svc, ok := m.Lookup("lss-web")
	if !ok {
		t.Fatalf("expected lss-web to be present")
	}
	if svc.EffectiveTagEnv() != "IMAGE_TAG" {
		t.Fatalf("expected default tag_env IMAGE_TAG, got %s", svc.EffectiveTagEnv())
	}
	if svc.ComposeFile() != "/srv/lss-web/compose.yml" {
		t.Fatalf("unexpected compose file path: %s", svc.ComposeFile())
	}
	if svc.EnvFile() != "/srv/lss-web/.env" {
		t.Fatalf("unexpected env file path: %s", svc.EnvFile())
	}

	if _, ok := m.Lookup("unknown"); ok {
		t.Fatalf("expected unknown service to be absent (allowlist)")
	}
}

func TestParse_CustomTagEnv(t *testing.T) {
	data := []byte(`
version: 1
services:
  svc:
    dir: /srv/svc
    file: compose.yml
    service: app
    image: example.com/app
    tag_env: MIGRATE_TAG
`)
	m, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	svc, _ := m.Lookup("svc")
	if svc.EffectiveTagEnv() != "MIGRATE_TAG" {
		t.Fatalf("expected MIGRATE_TAG, got %s", svc.EffectiveTagEnv())
	}
}

func TestParse_RejectsWrongVersion(t *testing.T) {
	_, err := Parse([]byte("version: 2\nservices: {}\n"))
	if err == nil {
		t.Fatalf("expected error for unsupported version")
	}
}

func TestParse_RejectsRelativeDir(t *testing.T) {
	data := []byte(`
version: 1
services:
  svc:
    dir: srv/svc
    file: compose.yml
    service: app
    image: example.com/app
`)
	if _, err := Parse(data); err == nil {
		t.Fatalf("expected error for relative dir")
	}
}

func TestParse_RejectsMissingRequiredFields(t *testing.T) {
	cases := []string{
		"version: 1\nservices:\n  svc:\n    file: compose.yml\n    service: app\n    image: example.com/app\n",
		"version: 1\nservices:\n  svc:\n    dir: /srv/svc\n    service: app\n    image: example.com/app\n",
		"version: 1\nservices:\n  svc:\n    dir: /srv/svc\n    file: compose.yml\n    image: example.com/app\n",
		"version: 1\nservices:\n  svc:\n    dir: /srv/svc\n    file: compose.yml\n    service: app\n",
	}
	for i, data := range cases {
		if _, err := Parse([]byte(data)); err == nil {
			t.Errorf("case %d: expected validation error", i)
		}
	}
}

func TestParse_RejectsInvalidTagEnv(t *testing.T) {
	data := []byte(`
version: 1
services:
  svc:
    dir: /srv/svc
    file: compose.yml
    service: app
    image: example.com/app
    tag_env: "not valid"
`)
	if _, err := Parse(data); err == nil {
		t.Fatalf("expected error for invalid tag_env")
	}
}

func TestParse_RejectsDuplicateTagEnvInSameDir(t *testing.T) {
	data := []byte(`
version: 1
services:
  app:
    dir: /srv/shared
    file: compose.yml
    service: app
    image: example.com/app
  migrate:
    dir: /srv/shared
    file: compose.yml
    service: migrate
    image: example.com/migrate
`)
	if _, err := Parse(data); err == nil {
		t.Fatalf("expected error for duplicate tag_env (both default to IMAGE_TAG) in the same dir")
	}
}

func TestParse_AllowsDistinctTagEnvInSameDir(t *testing.T) {
	data := []byte(`
version: 1
services:
  app:
    dir: /srv/shared
    file: compose.yml
    service: app
    image: example.com/app
    tag_env: APP_TAG
  migrate:
    dir: /srv/shared
    file: compose.yml
    service: migrate
    image: example.com/migrate
    tag_env: MIGRATE_TAG
`)
	if _, err := Parse(data); err != nil {
		t.Fatalf("expected distinct tag_env values to be allowed: %v", err)
	}
}

func TestParse_RejectsDuplicateComposeServiceInSameDir(t *testing.T) {
	data := []byte(`
version: 1
services:
  app:
    dir: /srv/shared
    file: compose.yml
    service: app
    image: example.com/app
    tag_env: APP_TAG
  app2:
    dir: /srv/shared
    file: compose.yml
    service: app
    image: example.com/app2
    tag_env: APP2_TAG
`)
	if _, err := Parse(data); err == nil {
		t.Fatalf("expected error for duplicate compose service name in the same dir")
	}
}

func TestValidatePaths(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "compose.yml"), []byte("services: {}\n"), 0o644); err != nil {
		t.Fatalf("write compose file: %v", err)
	}

	m := &Manifest{Version: 1, Services: map[string]Service{
		"svc": {
			Dir:            dir,
			File:           "compose.yml",
			ComposeService: "app",
			Image:          "example.com/app",
			TagEnv:         "IMAGE_TAG",
		},
	}}
	if err := m.ValidatePaths(); err != nil {
		t.Fatalf("ValidatePaths: %v", err)
	}

	m.Services["svc"] = Service{
		Dir:            dir,
		File:           "missing.yml",
		ComposeService: "app",
		Image:          "example.com/app",
		TagEnv:         "IMAGE_TAG",
	}
	if err := m.ValidatePaths(); err == nil {
		t.Fatalf("expected error for missing compose file")
	}
}

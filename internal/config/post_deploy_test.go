package config

import (
	"slices"
	"strings"
	"testing"
	"time"
)

const postDeployBase = `
version: 1
services:
  svc:
    dir: /srv/svc
    file: compose.yml
    service: app
    image: example.com/app
`

func TestParse_PostDeployDefaults(t *testing.T) {
	data := postDeployBase + `
    post_deploy:
      user: www-data
      args: [php, artisan, "lsky:preview-init"]
`
	m, err := Parse([]byte(data))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	pd := m.Services["svc"].PostDeploy
	if pd == nil {
		t.Fatalf("expected post_deploy to be parsed")
	}
	if pd.Service != "app" {
		t.Fatalf("expected post_deploy.service to default to the compose service, got %q", pd.Service)
	}
	if pd.Timeout != DefaultPostDeployTimeout {
		t.Fatalf("expected default timeout %s, got %s", DefaultPostDeployTimeout, pd.Timeout)
	}
	if !slices.Equal(pd.Args, []string{"php", "artisan", "lsky:preview-init"}) {
		t.Fatalf("unexpected args %v", pd.Args)
	}
}

func TestParse_PostDeployExplicitFields(t *testing.T) {
	data := postDeployBase + `
    post_deploy:
      service: worker
      user: "33:33"
      args: [php, artisan, migrate, --force]
      timeout: 90s
`
	m, err := Parse([]byte(data))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	pd := m.Services["svc"].PostDeploy
	if pd.Service != "worker" || pd.User != "33:33" || pd.Timeout != 90*time.Second {
		t.Fatalf("unexpected post_deploy %+v", pd)
	}
}

func TestParse_PostDeployRejectsMissingOrRootUser(t *testing.T) {
	users := []string{
		"",
		"root",
		"0",
		"00",
		"0:0",
		"0:www-data",
		"root:root",
		"root:www-data",
		"www-data:root",
		"www-data:0",
	}
	for _, user := range users {
		userLine := "\n      user: \"" + user + "\""
		if user == "" {
			userLine = ""
		}
		data := postDeployBase + "    post_deploy:" + userLine + "\n      args: [php, artisan, x]\n"
		_, err := Parse([]byte(data))
		if err == nil {
			t.Errorf("user %q: expected post_deploy to be rejected", user)
			continue
		}
		if !strings.Contains(err.Error(), "post_deploy") {
			t.Errorf("user %q: expected a post_deploy error, got %v", user, err)
		}
	}
}

func TestParse_PostDeployRejectsInvalidFields(t *testing.T) {
	cases := map[string]string{
		"flag user":      "user: \"-uroot\"\n      args: [php]",
		"three parts":    "user: \"a:b:c\"\n      args: [php]",
		"spaces in user": "user: \"www data\"\n      args: [php]",
		"no args":        "user: www-data",
		"empty arg":      "user: www-data\n      args: [php, \"\"]",
		"flag argv0":     "user: www-data\n      args: [\"--privileged\", php]",
		"newline arg":    "user: www-data\n      args: [\"php\\nid\"]",
		"bad service":    "service: \"-it\"\n      user: www-data\n      args: [php]",
		"zero timeout":   "user: www-data\n      args: [php]\n      timeout: -1s",
		"huge timeout":   "user: www-data\n      args: [php]\n      timeout: 1h",
	}
	for name, body := range cases {
		data := postDeployBase + "    post_deploy:\n      " + body + "\n"
		_, err := Parse([]byte(data))
		if err == nil {
			t.Errorf("%s: expected post_deploy to be rejected", name)
		}
	}
}

func TestParse_ImageRef(t *testing.T) {
	m, err := Parse([]byte(postDeployBase + "    image_ref: digest\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	svc := m.Services["svc"]
	if !svc.UsesDigest() {
		t.Fatalf("expected digest mode")
	}
	digest := "sha256:" + strings.Repeat("a", 64)
	if got := svc.ImageReference(digest); got != "example.com/app@"+digest {
		t.Fatalf("unexpected digest reference %q", got)
	}

	m, err = Parse([]byte(postDeployBase))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if m.Services["svc"].UsesDigest() {
		t.Fatalf("expected tag mode by default")
	}
	if got := m.Services["svc"].ImageReference("abc"); got != "example.com/app:abc" {
		t.Fatalf("unexpected tag reference %q", got)
	}

	_, err = Parse([]byte(postDeployBase + "    image_ref: latest\n"))
	if err == nil {
		t.Fatalf("expected unknown image_ref to be rejected")
	}
}

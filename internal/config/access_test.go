package config

import "testing"

const accessBase = `
version: 1
services:
  main:
    dir: /srv/main
    file: compose.yml
    service: app
    image: example.com/app
  pr-1:
    dir: /srv/pr-1
    file: compose.yml
    service: app
    image: example.com/app
`

func TestManifest_ClientAllowedWithoutAccess(t *testing.T) {
	m, err := Parse([]byte(accessBase))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !m.ClientAllowed("any", "fp", "main") {
		t.Fatalf("without access config every registered cert keeps full access")
	}
	if len(m.AccessWarnings()) != 1 {
		t.Fatalf("expected a startup warning, got %v", m.AccessWarnings())
	}
}

func TestManifest_ClientAllowedBindings(t *testing.T) {
	m, err := Parse([]byte(accessBase + `
access:
  require_binding: true
  clients:
    ci-main:
      services: [main]
    ci-preview:
      services: [pr-1]
    abcdef:
      services: [main, pr-1]
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	cases := []struct {
		name, fingerprint, service string
		want                       bool
	}{
		{"ci-main", "fp1", "main", true},
		{"ci-main", "fp1", "pr-1", false},
		{"ci-preview", "fp2", "pr-1", true},
		{"ci-preview", "fp2", "main", false},
		{"unlisted", "fp3", "main", false},
		{"ci-preview", "abcdef", "main", true},
	}
	for _, c := range cases {
		got := m.ClientAllowed(c.name, c.fingerprint, c.service)
		if got != c.want {
			t.Errorf("ClientAllowed(%q, %q, %q) = %v, want %v", c.name, c.fingerprint, c.service, got, c.want)
		}
	}
	if w := m.AccessWarnings(); len(w) != 0 {
		t.Fatalf("expected no warnings in strict mode, got %v", w)
	}
}

func TestManifest_ClientAllowedNonStrict(t *testing.T) {
	m, err := Parse([]byte(accessBase + `
access:
  clients:
    ci-preview:
      services: [pr-1]
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if m.ClientAllowed("ci-preview", "fp", "main") {
		t.Fatalf("a bound certificate must be limited to its services")
	}
	if !m.ClientAllowed("legacy", "fp", "main") {
		t.Fatalf("an unbound certificate keeps full access when require_binding is false")
	}
	if len(m.AccessWarnings()) != 1 {
		t.Fatalf("expected a warning for non-strict bindings")
	}
}

func TestParse_RejectsInvalidAccess(t *testing.T) {
	cases := map[string]string{
		"unknown service": "access:\n  clients:\n    ci:\n      services: [nope]\n",
		"empty services":  "access:\n  clients:\n    ci:\n      services: []\n",
	}
	for name, extra := range cases {
		_, err := Parse([]byte(accessBase + extra))
		if err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

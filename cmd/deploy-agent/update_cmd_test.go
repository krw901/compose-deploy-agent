package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/krwu/compose-deploy-agent/internal/paths"
)

func Test_resolveUpdateSocket(t *testing.T) {
	if got := resolveUpdateSocket("/x/y.sock", paths.DefaultDataDir); got != "/x/y.sock" {
		t.Fatalf("explicit --socket must win, got %q", got)
	}

	dataDir := t.TempDir()
	if got := resolveUpdateSocket("", dataDir); got != filepath.Join(dataDir, "agent.sock") {
		t.Fatalf("non-default data dir keeps the legacy path, got %q", got)
	}

	// With the default data dir, an absent /run socket falls back to a
	// legacy socket only if that one exists; otherwise the new default is
	// reported (so the error message names the current path).
	if got := resolveUpdateSocket("", paths.DefaultDataDir); got != paths.DefaultSocketPath {
		_, err := os.Stat(paths.LegacySocketPath(paths.DefaultDataDir))
		if err != nil {
			t.Fatalf("expected %s, got %q", paths.DefaultSocketPath, got)
		}
	}
}

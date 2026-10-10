// Package paths centralizes the on-disk layout conventions.
package paths

import "path/filepath"

const (
	// DefaultManifestPath is the services manifest path when no flag overrides it.
	DefaultManifestPath = "/etc/deploy-agent/services.yaml"
	// DefaultDataDir is the state directory when no flag overrides it.
	DefaultDataDir = "/var/lib/deploy-agent"
)

// DBPath returns the sqlite database path under dataDir.
func DBPath(dataDir string) string { return filepath.Join(dataDir, "agent.db") }

// LockPath returns the single-instance lock file path under dataDir.
func LockPath(dataDir string) string { return filepath.Join(dataDir, "agent.lock") }

// SocketPath returns the local-only unix socket path under dataDir, used by
// `deploy-agent update` to reach a running `serve` process without mTLS
// (the human/host channel does not depend on mTLS).
func SocketPath(dataDir string) string { return filepath.Join(dataDir, "agent.sock") }

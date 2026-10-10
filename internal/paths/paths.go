// Package paths centralizes the on-disk layout conventions.
package paths

import "path/filepath"

const (
	// DefaultManifestPath is the services manifest path when no flag overrides it.
	DefaultManifestPath = "/etc/deploy-agent/services.yaml"
	// DefaultDataDir is the state directory when no flag overrides it.
	DefaultDataDir = "/var/lib/deploy-agent"
	// DefaultSocketPath is the local unix socket used with the default data
	// directory. Its parent is created by systemd (RuntimeDirectory=
	// deploy-agent, RuntimeDirectoryMode=0700).
	DefaultSocketPath = "/run/deploy-agent/deploy-agent.sock"
)

// DBPath returns the sqlite database path under dataDir.
func DBPath(dataDir string) string { return filepath.Join(dataDir, "agent.db") }

// LockPath returns the single-instance lock file path under dataDir.
func LockPath(dataDir string) string { return filepath.Join(dataDir, "agent.lock") }

// SocketPath returns the local-only unix socket path used when no explicit
// --socket is given; `deploy-agent update` uses it to reach a running
// `serve` process without mTLS (the human/host channel does not depend on
// mTLS). The default data directory maps to DefaultSocketPath; an
// explicitly configured data directory keeps the historical
// <data-dir>/agent.sock so existing setups behave as before.
func SocketPath(dataDir string) string {
	if filepath.Clean(dataDir) == DefaultDataDir {
		return DefaultSocketPath
	}
	return LegacySocketPath(dataDir)
}

// LegacySocketPath is where versions before the /run move put the socket.
func LegacySocketPath(dataDir string) string { return filepath.Join(dataDir, "agent.sock") }

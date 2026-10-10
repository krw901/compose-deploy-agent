package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/krwu/compose-deploy-agent/internal/client"
	"github.com/krwu/compose-deploy-agent/internal/paths"
)

// runUpdate implements `deploy-agent update`, the VPS-side operator
// command: a developer already SSH'd
// onto the host can trigger a service update without an endpoint or an
// mTLS credential. It reaches the running `deploy-agent serve` process
// through its local unix socket, reusing the same submit/poll flow and
// exit codes as `deploy-agent deploy`.
func runUpdate(args []string) error {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	service := stringFlag(fs, "service", "s", "", "logical service name (must match services.yaml)")
	tag := stringFlag(fs, "tag", "t", "", "target image tag")
	digest := stringFlag(fs, "digest", "d", "", "target image digest sha256:<64 hex> (services with image_ref: digest)")
	socketFlag := fs.String("socket", "", socketFlagUsage)
	dataDir := fs.String("data-dir", paths.DefaultDataDir, "data directory of the running serve process (used to locate its unix socket)")
	pollTimeout := fs.Duration("poll-timeout", client.DefaultPollTimeout, "total time budget for submit retries and status polling")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *service == "" {
		return fmt.Errorf("--service is required")
	}
	err := requireOneRef(*tag, *digest)
	if err != nil {
		return err
	}

	summary := client.RunLocal(context.Background(), resolveUpdateSocket(*socketFlag, *dataDir), client.Options{
		Service:     *service,
		Tag:         *tag,
		Digest:      *digest,
		PollTimeout: *pollTimeout,
	})

	return emitSummary(summary)
}

// resolveUpdateSocket picks the socket `update` connects to: --socket if
// given, else the default for dataDir. When that default does not exist
// but the pre-/run legacy <data-dir>/agent.sock does (a serve process
// started by an older binary and not yet restarted), the legacy socket is
// used.
func resolveUpdateSocket(explicit, dataDir string) string {
	if explicit != "" {
		return explicit
	}
	socketPath := paths.SocketPath(dataDir)
	_, err := os.Stat(socketPath)
	if err == nil {
		return socketPath
	}
	legacy := paths.LegacySocketPath(dataDir)
	_, err = os.Stat(legacy)
	if err == nil {
		return legacy
	}
	return socketPath
}

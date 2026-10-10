package main

import (
	"context"
	"flag"
	"fmt"

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
	dataDir := fs.String("data-dir", paths.DefaultDataDir, "data directory of the running serve process (used to locate its unix socket)")
	pollTimeout := fs.Duration("poll-timeout", client.DefaultPollTimeout, "total time budget for submit retries and status polling")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *service == "" || *tag == "" {
		return fmt.Errorf("--service and --tag are both required")
	}

	summary := client.RunLocal(context.Background(), paths.SocketPath(*dataDir), client.Options{
		Service:     *service,
		Tag:         *tag,
		PollTimeout: *pollTimeout,
	})

	return emitSummary(summary)
}

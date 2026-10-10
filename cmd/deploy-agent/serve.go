package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/krwu/compose-deploy-agent/internal/ca"
	"github.com/krwu/compose-deploy-agent/internal/config"
	"github.com/krwu/compose-deploy-agent/internal/deployer"
	"github.com/krwu/compose-deploy-agent/internal/dockerx"
	"github.com/krwu/compose-deploy-agent/internal/paths"
	"github.com/krwu/compose-deploy-agent/internal/server"
	"github.com/krwu/compose-deploy-agent/internal/singleton"
	"github.com/krwu/compose-deploy-agent/internal/store"
	"github.com/krwu/compose-deploy-agent/internal/tlsconf"
)

// expiryCheckInterval is how often the running agent re-checks for
// soon-to-expire client certificates ("每日定时检查").
const expiryCheckInterval = 24 * time.Hour

// defaultHealthzAddr is serve's default --healthz-bind, also polled by
// `deploy-agent self-upgrade` after restarting the service.
const defaultHealthzAddr = "127.0.0.1:8867"

func runServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	manifestPath := fs.String("manifest", paths.DefaultManifestPath, "path to services.yaml")
	dataDir := fs.String("data-dir", paths.DefaultDataDir, "data directory (sqlite db, CA material)")
	bindAddr := fs.String("bind", "", "address:port for the mTLS listener, e.g. 10.0.0.5:8866")
	healthzAddr := fs.String("healthz-bind", defaultHealthzAddr, "address:port for the plain-HTTP healthz listener")
	testOnly := fs.Bool("t", false, "test the configuration and exit, without taking the instance lock or opening the database")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *bindAddr == "" {
		return fmt.Errorf("--bind is required, e.g. --bind 10.0.0.5:8866")
	}

	cfg, err := loadServeConfig(*manifestPath, *dataDir)
	if err != nil {
		return err
	}
	if *testOnly {
		notAfter, err := testServeConfig(cfg, *bindAddr)
		if err != nil {
			return err
		}
		fmt.Printf("deploy-agent: configuration test is successful (%d services, server certificate valid until %s)\n",
			len(cfg.manifest.Services), notAfter.Format(time.DateTime))
		return nil
	}

	release, err := singleton.Lock(paths.LockPath(*dataDir))
	if err != nil {
		return err
	}
	defer release()

	st, err := store.Open(paths.DBPath(*dataDir))
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close()

	tlsConfig := tlsconf.ServerConfig(cfg.serverCert, cfg.clientCAPool)
	dep := deployer.New(cfg.manifest, dockerx.NewClient(), st)

	if n, err := dep.MarkInterruptedOnStartup(); err != nil {
		slog.Error("mark interrupted deploys failed", "error", err)
	} else if n > 0 {
		slog.Warn("marked in-progress deploys as interrupted after restart", "count", n)
	}

	runReverseCalibration(context.Background(), dep, cfg.manifest)
	logExpiringCerts(st)

	srv := server.New(dep, st, version)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		ticker := time.NewTicker(expiryCheckInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				logExpiringCerts(st)
			}
		}
	}()

	socketPath := paths.SocketPath(*dataDir)
	errCh := make(chan error, 3)
	go func() { errCh <- srv.MainListener(ctx, *bindAddr, tlsConfig) }()
	go func() { errCh <- srv.HealthzListener(ctx, *healthzAddr) }()
	go func() { errCh <- srv.LocalListener(ctx, socketPath) }()

	slog.Info("deploy-agent serve started", "bind", *bindAddr, "healthz", *healthzAddr, "socket", socketPath, "version", version)

	select {
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("listener error: %w", err)
		}
	case <-ctx.Done():
	}
	return nil
}

// runReverseCalibration compares .env, last_healthy and docker's actual
// running state for every managed service, logging discrepancies without
// mutating any persisted state.
func runReverseCalibration(ctx context.Context, dep *deployer.Deployer, manifest *config.Manifest) {
	for name := range manifest.Services {
		status, err := dep.GetServiceStatus(ctx, name)
		if err != nil {
			slog.Warn("reverse calibration: read status failed", "service", name, "error", err)
			continue
		}
		if status.EnvTag != "" && status.Running.Tag != "" && status.EnvTag != status.Running.Tag {
			slog.Warn("reverse calibration: .env tag differs from running tag",
				"service", name, "env_tag", status.EnvTag, "running_tag", status.Running.Tag)
		}
		if status.LastHealthy != nil && status.Running.ImageID != "" && status.LastHealthy.ImageID != status.Running.ImageID {
			slog.Warn("reverse calibration: last_healthy image differs from running image",
				"service", name, "last_healthy_image_id", status.LastHealthy.ImageID, "running_image_id", status.Running.ImageID)
		}
	}
}

func logExpiringCerts(st *store.Store) {
	expiring, err := ca.ExpiringCerts(st)
	if err != nil {
		slog.Error("check expiring certificates failed", "error", err)
		return
	}
	for _, c := range expiring {
		slog.Warn("client certificate expiring soon", "fingerprint", c.Fingerprint, "name", c.Name, "expires_at", c.ExpiresAt)
	}
}

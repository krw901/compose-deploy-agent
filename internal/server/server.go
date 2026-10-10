// Package server implements the agent's three HTTP listeners: the mTLS
// main listener on :8866 serving /v1/*, the plain-HTTP healthz listener on
// 127.0.0.1:8867, and a local-only unix socket listener serving the same
// /v1/* routes for `deploy-agent update`. The unix socket carries no mTLS:
// its trust root is host/root access to the socket file, the same trust
// root the host already has via docker.sock.
package server

import (
	"context"
	"crypto/tls"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/krwu/compose-deploy-agent/internal/deployer"
	"github.com/krwu/compose-deploy-agent/internal/store"
)

// Server bundles everything the HTTP handlers need.
type Server struct {
	deployer  *deployer.Deployer
	store     *store.Store
	version   string
	startedAt time.Time
}

// New returns a Server ready to build handlers for.
func New(dep *deployer.Deployer, s *store.Store, version string) *Server {
	return &Server{
		deployer:  dep,
		store:     s,
		version:   version,
		startedAt: time.Now(),
	}
}

// MainListener starts the mTLS listener on addr (e.g. "10.0.0.5:8866") and
// blocks until ctx is cancelled or the listener errors.
func (s *Server) MainListener(ctx context.Context, addr string, tlsConfig *tls.Config) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return s.ServeMainOn(ctx, ln, tlsConfig)
}

// ServeMainOn serves the mTLS router and the unauthenticated /v1/version
// router on an already-bound listener, splitting the two protocols with
// protoDemux. Split out from MainListener so
// tests can bind an ephemeral port ("127.0.0.1:0") and learn the actual
// port before connecting.
func (server *Server) ServeMainOn(ctx context.Context, ln net.Listener, tlsConfig *tls.Config) error {
	demux := newProtoDemux(ln)

	tlsSrv := &http.Server{
		Handler:   fingerprintMiddleware(server.store)(server.mainRouter()),
		TLSConfig: tlsConfig,
	}
	plainSrv := &http.Server{Handler: server.publicRouter()}

	errCh := make(chan error, 2)
	go func() { errCh <- tlsSrv.Serve(tls.NewListener(demux.tls, tlsConfig)) }()
	go func() { errCh <- plainSrv.Serve(demux.plain) }()

	select {
	case err := <-errCh:
		ln.Close()
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := tlsSrv.Shutdown(shutdownCtx); err != nil {
			slog.Warn("mTLS listener shutdown error", "error", err)
		}
		if err := plainSrv.Shutdown(shutdownCtx); err != nil {
			slog.Warn("plain version listener shutdown error", "error", err)
		}
		ln.Close()
		return nil
	}
}

// HealthzListener starts the plain-HTTP healthz listener, always bound to
// loopback so that monitoring never needs a client certificate.
func (s *Server) HealthzListener(ctx context.Context, addr string) error {
	srv := &http.Server{
		Addr:    addr,
		Handler: s.healthzRouter(),
	}
	return serveUntilDone(ctx, srv, srv.ListenAndServe)
}

// LocalListener starts a unix socket listener at socketPath serving the
// same /v1/* routes as the mTLS listener, but without mTLS or the
// fingerprint middleware: whoever can reach the socket file has already
// proven host access, which this system treats as the real trust root.
// Any stale socket file left behind by a
// previous unclean shutdown is removed first; this is safe because the
// caller already holds the single-instance lock (internal/singleton). The
// socket is chmod'd to 0600 so only the owning user (root, per the systemd
// unit) can connect.
func (s *Server) LocalListener(ctx context.Context, socketPath string) error {
	err := os.Remove(socketPath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		return err
	}
	if err := os.Chmod(socketPath, 0o600); err != nil {
		ln.Close()
		return err
	}
	srv := &http.Server{Handler: s.mainRouter()}
	return serveUntilDone(ctx, srv, func() error {
		return srv.Serve(ln)
	})
}

func serveUntilDone(ctx context.Context, srv *http.Server, listenAndServe func() error) error {
	errCh := make(chan error, 1)
	go func() { errCh <- listenAndServe() }()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			slog.Warn("server shutdown error", "addr", srv.Addr, "error", err)
		}
		return nil
	}
}

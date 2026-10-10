package server

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/krwu/compose-deploy-agent/internal/api"
)

// authorizeService enforces services.yaml access.clients: it reports
// whether the request may act on service, and otherwise logs the denial
// and writes 403 cert_not_allowed. Requests without a client identity come
// from the local unix socket, whose trust root is host access, and are
// always allowed.
func (server *Server) authorizeService(w http.ResponseWriter, r *http.Request, service string) bool {
	id, ok := r.Context().Value(clientIdentityKey{}).(clientIdentity)
	if !ok {
		return true
	}
	if server.deployer.Manifest().ClientAllowed(id.name, id.fingerprint, service) {
		return true
	}
	slog.Warn("client certificate not allowed for service",
		"cert_name", id.name, "fingerprint", id.fingerprint, "service", service,
		"method", r.Method, "path", r.URL.Path)
	writeError(w, http.StatusForbidden, api.ErrCertNotAllowed,
		fmt.Sprintf("client certificate is not allowed to access service %q", service))
	return false
}

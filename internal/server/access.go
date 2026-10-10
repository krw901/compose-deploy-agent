package server

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/krwu/compose-deploy-agent/internal/api"
)

// authorizeService enforces services.yaml access.clients: it reports
// whether the request may act on service, and otherwise logs the denial
// and writes 403. It fails closed: a request without an identity in its
// context (set by fingerprintMiddleware or localIdentityMiddleware) is
// denied. The local identity, whose trust root is host access to the unix
// socket, keeps full access.
func (server *Server) authorizeService(w http.ResponseWriter, r *http.Request, service string) bool {
	id, ok := r.Context().Value(clientIdentityKey{}).(clientIdentity)
	if !ok {
		slog.Error("request without client identity denied", "service", service,
			"method", r.Method, "path", r.URL.Path)
		writeError(w, http.StatusForbidden, api.ErrCertNotAllowed, "no client identity")
		return false
	}
	if id.local {
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

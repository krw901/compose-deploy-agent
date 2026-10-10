package server

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/krwu/compose-deploy-agent/internal/api"
	"github.com/krwu/compose-deploy-agent/internal/credential"
	"github.com/krwu/compose-deploy-agent/internal/store"
)

// fingerprintMiddleware runs after the TLS handshake has already verified
// the certificate chain and expiry (tls.RequireAndVerifyClientCert in
// internal/tlsconf). It performs the second authentication layer by
// checking the leaf certificate's fingerprint against the client_certs
// registration table, so that individual certificates can be revoked
// without invalidating the whole CA.
func fingerprintMiddleware(s *store.Store) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
				// Should be unreachable given RequireAndVerifyClientCert,
				// but fail closed if it ever happens.
				writeError(w, http.StatusForbidden, api.ErrCertNotRegistered, "no client certificate presented")
				return
			}
			leaf := r.TLS.PeerCertificates[0]
			fingerprint := credential.Fingerprint(leaf)

			rec, err := s.GetCert(fingerprint)
			if errors.Is(err, store.ErrNotFound) {
				writeError(w, http.StatusForbidden, api.ErrCertNotRegistered, "client certificate not registered")
				return
			}
			if err != nil {
				slog.Error("lookup client certificate failed", "error", err)
				writeError(w, http.StatusInternalServerError, api.ErrInternal, "internal error")
				return
			}
			if rec.Revoked() {
				writeError(w, http.StatusForbidden, api.ErrCertRevoked, "client certificate revoked")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

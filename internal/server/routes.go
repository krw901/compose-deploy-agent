package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/krwu/compose-deploy-agent/internal/api"
	"github.com/krwu/compose-deploy-agent/internal/deployer"
	"github.com/krwu/compose-deploy-agent/internal/store"
)

// mainRouter builds the /v1/* mux served over mTLS on :8866, and over the
// local unix socket for `deploy-agent update`. /v1/version is also
// reachable here for callers that already hold a valid client
// certificate, even though its primary entry point is publicRouter.
func (s *Server) mainRouter() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/deploy", s.handleDeploy)
	mux.HandleFunc("GET /v1/deploys/{id}", s.handleGetDeploy)
	mux.HandleFunc("GET /v1/status/{service}", s.handleGetStatus)
	mux.HandleFunc("GET /v1/version", s.handleVersion)
	return mux
}

// publicRouter builds the mux served over the plain-HTTP sub-stream of
// :8866 (see protoDemux): intentionally just /v1/version, so a pipeline
// can discover the running agent version - and thus which client binary
// to download - before it has any mTLS credential. Every other /v1/*
// route must stay unreachable without a client certificate; do not add
// routes here.
func (s *Server) publicRouter() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/version", s.handleVersion)
	return mux
}

// healthzRouter builds the plain-HTTP mux served on 127.0.0.1:8867.
func (s *Server) healthzRouter() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	return mux
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, api.HealthzResponse{
		Status:  "ok",
		Version: s.version,
		UptimeS: int64(time.Since(s.startedAt).Seconds()),
	})
}

func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if _, err := fmt.Fprintln(w, s.version); err != nil {
		slog.Error("write version response failed", "error", err)
	}
}

func (server *Server) handleDeploy(w http.ResponseWriter, r *http.Request) {
	var req api.DeployRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, api.ErrInvalidParam, "invalid JSON body")
		return
	}
	if req.Service == "" || req.Tag == "" {
		writeError(w, http.StatusBadRequest, api.ErrInvalidParam, "service and tag are required")
		return
	}

	deployID, _, err := server.deployer.StartDeploy(r.Context(), req.Service, req.Tag)
	if err != nil {
		server.writeStartDeployError(w, err)
		return
	}

	writeJSON(w, http.StatusAccepted, api.DeployAccepted{
		DeployID: deployID,
		Service:  req.Service,
		Tag:      req.Tag,
		State:    api.StateDeploying,
	})
}

func (s *Server) writeStartDeployError(w http.ResponseWriter, err error) {
	var notFound deployer.ErrServiceNotFound
	var invalidTag deployer.ErrInvalidTag
	var conflict deployer.ErrAlreadyDeploying

	switch {
	case errors.As(err, &notFound):
		writeError(w, http.StatusNotFound, api.ErrServiceNotFound, notFound.Error())
	case errors.As(err, &invalidTag):
		writeError(w, http.StatusBadRequest, api.ErrInvalidParam, invalidTag.Error())
	case errors.As(err, &conflict):
		writeError(w, http.StatusConflict, api.ErrAlreadyDeploying, conflict.Error())
	default:
		slog.Error("start deploy failed", "error", err)
		writeError(w, http.StatusInternalServerError, api.ErrInternal, "internal error")
	}
}

func (s *Server) handleGetDeploy(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	status, err := s.deployer.GetDeploy(id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, api.ErrServiceNotFound, "deploy not found")
			return
		}
		slog.Error("get deploy failed", "error", err)
		writeError(w, http.StatusInternalServerError, api.ErrInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleGetStatus(w http.ResponseWriter, r *http.Request) {
	service := r.PathValue("service")
	status, err := s.deployer.GetServiceStatus(r.Context(), service)
	if err != nil {
		var notFound deployer.ErrServiceNotFound
		if errors.As(err, &notFound) {
			writeError(w, http.StatusNotFound, api.ErrServiceNotFound, notFound.Error())
			return
		}
		slog.Error("get status failed", "error", err)
		writeError(w, http.StatusInternalServerError, api.ErrInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, status)
}

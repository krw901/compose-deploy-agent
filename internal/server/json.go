package server

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/krwu/compose-deploy-agent/internal/api"
)

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		slog.Error("write json response failed", "error", err)
	}
}

func writeError(w http.ResponseWriter, status int, code api.ErrorCode, message string) {
	writeJSON(w, status, api.ErrorResponse{
		Code:    code,
		Message: message,
	})
}

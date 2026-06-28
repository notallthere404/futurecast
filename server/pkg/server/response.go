package server

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/notallthere404/futurecast/server/api/v1"
)

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if payload != nil {
		_ = json.NewEncoder(w).Encode(payload)
	}
}

func writeError(log *slog.Logger, w http.ResponseWriter, status int, code, message string, err error) {
	if err != nil {
		log.Error(message, "error", err)
	}
	writeJSON(w, status, v1.ErrorResponse{
		Error: v1.ErrorDetail{
			Code:    code,
			Message: message,
		},
	})
}

func decodeJSON[T any](w http.ResponseWriter, r *http.Request, log *slog.Logger) (T, bool) {
	var payload T
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeError(log, w, http.StatusBadRequest, "invalid_json", "invalid request body", err)
		return payload, false
	}
	return payload, true
}

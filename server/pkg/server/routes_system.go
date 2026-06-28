package server

import (
	"fmt"
	"net/http"

	v1 "github.com/notallthere404/futurecast/server/api/v1"
)

func (s *Server) systemRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/system/status", s.handleStatus)
	mux.HandleFunc("GET /api/v1/system/config", s.handleConfig)
	mux.HandleFunc("PUT /api/v1/system/config", s.handleConfigUpdate)
	mux.HandleFunc("POST /api/v1/system/restart", s.handleRestart)
	mux.HandleFunc("GET /api/v1/system/logs", s.handleLogs)
	mux.HandleFunc("POST /api/v1/system/uptime", s.handleUptimeTotal)
	mux.HandleFunc("POST /api/v1/system/uptime/rate", s.handleUptimeSegment)
}

func (s *Server) handleStatus(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleConfig(w http.ResponseWriter, _ *http.Request) {
	cc, err := s.config.ClientConfig()
	if err != nil {
		writeError(s.log, w, http.StatusInternalServerError, "config_read_failed", "failed to read config", err)
		return
	}
	writeJSON(w, http.StatusOK, cc)
}

func (s *Server) handleConfigUpdate(w http.ResponseWriter, r *http.Request) {
	payload, ok := decodeJSON[v1.ConfigUpdateRequest](w, r, s.log)
	if !ok {
		return
	}

	if err := s.system.UpdateConfig(payload.Config); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"error": ""})
}

func (s *Server) handleRestart(w http.ResponseWriter, _ *http.Request) {
	if err := s.system.Restart(); err != nil {
		writeError(s.log, w, http.StatusInternalServerError, "restart_failed", "restart failed", err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(s.log, w, http.StatusInternalServerError, "streaming_unsupported", "streaming not supported", nil)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch := s.logs.Subscribe()
	defer s.logs.Unsubscribe(ch)

	for {
		select {
		case msg := <-ch:
			_, _ = fmt.Fprintf(w, "data: %s\n\n", msg)
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func (s *Server) handleUptimeTotal(w http.ResponseWriter, r *http.Request) {
	payload, ok := decodeJSON[v1.UptimeRequest](w, r, s.log)
	if !ok {
		return
	}

	res, err := s.system.UptimeTotal(r.Context(), payload.Start, payload.End)
	if err != nil {
		writeError(s.log, w, http.StatusInternalServerError, "query_failed", "no query results", err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleUptimeSegment(w http.ResponseWriter, r *http.Request) {
	payload, ok := decodeJSON[v1.RateRequest](w, r, s.log)
	if !ok {
		return
	}

	res, err := s.system.UptimeSegment(r.Context(), payload.Format)
	if err != nil {
		writeError(s.log, w, http.StatusInternalServerError, "query_failed", "no query results", err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

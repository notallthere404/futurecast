package server

import (
	"errors"
	"net/http"

	v1 "github.com/notallthere404/futurecast/server/api/v1"
	viewstore "github.com/notallthere404/futurecast/server/pkg/registry/view"
)

func (s *Server) viewRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/views", s.handleViewList)
	mux.HandleFunc("POST /api/v1/views", s.handleViewUpsert)
	mux.HandleFunc("GET /api/v1/views/{slug}", s.handleViewGet)
	mux.HandleFunc("DELETE /api/v1/views/{slug}", s.handleViewDelete)
}

func (s *Server) handleViewList(w http.ResponseWriter, r *http.Request) {
	var userId *string
	if u := r.URL.Query().Get("user_id"); u != "" {
		userId = &u
	}
	views, err := s.views.List(r.Context(), userId)
	if err != nil {
		writeError(s.log, w, http.StatusInternalServerError, "view_list_failed", "failed to list views", err)
		return
	}
	writeJSON(w, http.StatusOK, views)
}

func (s *Server) handleViewGet(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	rendered, err := s.views.Get(r.Context(), slug)
	if err != nil {
		if errors.Is(err, viewstore.ErrNotFound) {
			writeError(s.log, w, http.StatusNotFound, "view_not_found", "view not found", err)
			return
		}
		writeError(s.log, w, http.StatusInternalServerError, "view_get_failed", "failed to render view", err)
		return
	}
	writeJSON(w, http.StatusOK, rendered)
}

func (s *Server) handleViewUpsert(w http.ResponseWriter, r *http.Request) {
	payload, ok := decodeJSON[v1.View](w, r, s.log)
	if !ok {
		return
	}
	if err := s.views.Upsert(r.Context(), &payload); err != nil {
		writeError(s.log, w, http.StatusInternalServerError, "view_upsert_failed", "failed to save view", err)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (s *Server) handleViewDelete(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if err := s.views.Delete(r.Context(), slug); err != nil {
		writeError(s.log, w, http.StatusInternalServerError, "view_delete_failed", "failed to delete view", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

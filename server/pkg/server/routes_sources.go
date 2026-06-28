package server

import (
	"context"
	"net/http"

	v1 "github.com/notallthere404/futurecast/server/api/v1"
)

func (s *Server) sourceRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/sources", s.handleSourceList)
	mux.HandleFunc("POST /api/v1/sources", s.handleSourceUpsert)
	mux.HandleFunc("POST /api/v1/sources/batch", s.handleSourceUpsertBatch)
	mux.HandleFunc("POST /api/v1/sources/sync", s.handleSourceSync)
	mux.HandleFunc("POST /api/v1/sources/rss/run", s.handleReaderRun)
	// mux.HandleFunc("POST /api/v1/sources/scraper/run", s.handleScraperRun)
	mux.HandleFunc("GET /api/v1/articles/recent", s.handleArticleRecent)
	mux.HandleFunc("POST /api/v1/articles/rate", s.handleArticleRate)
}

func (s *Server) sourceList(w http.ResponseWriter, r *http.Request) {
	data, err := s.sources.List(r.Context())
	if err != nil {
		writeError(s.log, w, http.StatusInternalServerError, "source_list_failed", "failed to retrieve sources", err)
		return
	}
	writeJSON(w, http.StatusOK, data)
}

func (s *Server) handleSourceList(w http.ResponseWriter, r *http.Request) {
	s.sourceList(w, r)
}

func (s *Server) handleSourceUpsert(w http.ResponseWriter, r *http.Request) {
	payload, ok := decodeJSON[v1.Source](w, r, s.log)
	if !ok {
		return
	}

	if err := s.sources.Upsert(r.Context(), &payload); err != nil {
		writeError(s.log, w, http.StatusInternalServerError, "source_upsert_failed", "failed to insert source", err)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (s *Server) handleSourceUpsertBatch(w http.ResponseWriter, r *http.Request) {
	payload, ok := decodeJSON[[]*v1.Source](w, r, s.log)
	if !ok {
		return
	}

	if err := s.sources.UpsertBatch(r.Context(), payload); err != nil {
		writeError(s.log, w, http.StatusInternalServerError, "source_batch_upsert_failed", "failed to insert sources", err)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (s *Server) handleArticleRecent(w http.ResponseWriter, r *http.Request) {
	articles, err := s.sources.Recent(r.Context())
	if err != nil {
		writeError(s.log, w, http.StatusInternalServerError, "article_recent_failed", "failed to select articles", err)
		return
	}
	writeJSON(w, http.StatusOK, articles)
}

func (s *Server) handleArticleRate(w http.ResponseWriter, r *http.Request) {
	payload, ok := decodeJSON[v1.RateRequest](w, r, s.log)
	if !ok {
		return
	}

	rate, err := s.sources.Rate(r.Context(), payload.Format)
	if err != nil {
		writeError(s.log, w, http.StatusInternalServerError, "article_rate_failed", "failed to select rate", err)
		return
	}
	writeJSON(w, http.StatusOK, rate)
}

func (s *Server) handleSourceSync(w http.ResponseWriter, r *http.Request) {
	if err := s.sources.RunRSS(r.Context()); err != nil {
		writeError(s.log, w, http.StatusInternalServerError, "source_sync_failed", "update sources failed", err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleReaderRun(w http.ResponseWriter, _ *http.Request) {
	// Manual immediate run. Per-source cron entries (rss:<id>) keep
	// ticking on their own schedule; an overlap with the next tick is
	// harmless because the inserter dedupes on article id.
	if err := s.sources.RunRSS(context.Background()); err != nil {
		writeError(s.log, w, http.StatusInternalServerError, "reader_run_failed", "start reader failed", err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

package server

import (
	"net/http"

	v1 "github.com/notallthere404/futurecast/server/api/v1"
	classificationcontroller "github.com/notallthere404/futurecast/server/pkg/controller/classification"
)

func (s *Server) classificationRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/classifications", s.handleClassificationSearch)
	mux.HandleFunc("POST /api/v1/classifications/count", s.handleClassificationCount)
	mux.HandleFunc("POST /api/v1/classifications/run", s.handleClassificationRun)
	mux.HandleFunc("POST /api/v1/classifications/upload", s.handleClassificationInsert)
	mux.HandleFunc("GET /api/v1/classifications/metrics", s.handleClassificationMetrics)
	mux.HandleFunc("POST /api/v1/visualizations/heatmap", s.handleHeatmap)
	mux.HandleFunc("POST /api/v1/visualizations/treemap", s.handleTreemap)
	mux.HandleFunc("POST /api/v1/visualizations/quadrant", s.handleQuadrant)
	mux.HandleFunc("POST /api/v1/visualizations/plot", s.handlePlot)
	mux.HandleFunc("POST /api/v1/visualizations/scatter", s.handleScatter)
}

func (s *Server) handleClassificationSearch(w http.ResponseWriter, r *http.Request) {
	query := classificationcontroller.ParseQuery(r.URL.Query())
	res, err := s.classifications.Search(r.Context(), query)
	if err != nil {
		writeError(s.log, w, http.StatusInternalServerError, "query_failed", "no query results", err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleClassificationCount(w http.ResponseWriter, r *http.Request) {
	payload, ok := decodeJSON[v1.ClassificationCountRequest](w, r, s.log)
	if !ok {
		return
	}

	res, err := s.classifications.Count(r.Context(), payload)
	if err != nil {
		writeError(s.log, w, http.StatusInternalServerError, "query_failed", "no query results", err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// handleClassificationRun manual trigger for the inference worker.
// The worker is level-triggered: Kick starts the loop if it is not
// already running, otherwise the call is a no-op. Returns 200
// immediately; classification happens in the background.
func (s *Server) handleClassificationRun(w http.ResponseWriter, _ *http.Request) {
	s.inference.Kick()
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleClassificationMetrics(w http.ResponseWriter, r *http.Request) {
	params := r.URL.Query()
	res, err := s.classifications.Metrics(r.Context(), params.Get("classification"), params["label"], params.Get("start"), params.Get("end"))
	if err != nil {
		writeError(s.log, w, http.StatusInternalServerError, "query_failed", "no query results", err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleHeatmap(w http.ResponseWriter, r *http.Request) {
	payload, ok := decodeJSON[v1.HeatmapRequest](w, r, s.log)
	if !ok {
		return
	}

	res, err := s.classifications.Heatmap(r.Context(), payload)
	if err != nil {
		writeError(s.log, w, http.StatusInternalServerError, "query_failed", "no query results", err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleTreemap(w http.ResponseWriter, r *http.Request) {
	payload, ok := decodeJSON[v1.TreemapRequest](w, r, s.log)
	if !ok {
		return
	}

	res, err := s.classifications.Treemap(r.Context(), payload)
	if err != nil {
		writeError(s.log, w, http.StatusInternalServerError, "query_failed", "no query results", err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handlePlot(w http.ResponseWriter, r *http.Request) {
	payload, ok := decodeJSON[v1.PlotRequest](w, r, s.log)
	if !ok {
		return
	}

	res, err := s.classifications.Plot(r.Context(), payload)
	if err != nil {
		writeError(s.log, w, http.StatusInternalServerError, "query_failed", "no query results", err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleScatter(w http.ResponseWriter, r *http.Request) {
	payload, ok := decodeJSON[v1.ScatterRequest](w, r, s.log)
	if !ok {
		return
	}

	res, err := s.classifications.Scatter(r.Context(), payload)
	if err != nil {
		writeError(s.log, w, http.StatusInternalServerError, "query_failed", "no query results", err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleQuadrant(w http.ResponseWriter, r *http.Request) {
	payload, ok := decodeJSON[v1.QuadrantRequest](w, r, s.log)
	if !ok {
		return
	}

	res, err := s.classifications.Quadrant(r.Context(), payload)
	if err != nil {
		writeError(s.log, w, http.StatusInternalServerError, "query_failed", "no query results", err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleClassificationInsert(w http.ResponseWriter, r *http.Request) {
	payload, ok := decodeJSON[[]v1.ClassificationInsertItem](w, r, s.log)
	if !ok {
		return
	}

	if err := s.classifications.InsertBatch(r.Context(), payload); err != nil {
		writeError(s.log, w, http.StatusInternalServerError, "classification_insert_failed", "failed to insert classification", err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) testRoutes(_ *http.ServeMux) {}

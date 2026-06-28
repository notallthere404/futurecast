package server

import (
	"net/http"
)

func (s *Server) schedulerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/scheduler/run", s.handleSchedulerRun)
	mux.HandleFunc("POST /api/v1/scheduler/stop", s.handleSchedulerStop)
}

func (s *Server) handleSchedulerRun(w http.ResponseWriter, _ *http.Request) {
	s.scheduler.Run()
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleSchedulerStop(w http.ResponseWriter, _ *http.Request) {
	s.scheduler.Stop()
	w.WriteHeader(http.StatusOK)
}

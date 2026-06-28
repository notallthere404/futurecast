package server

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/notallthere404/futurecast/server/pkg/config"
	"github.com/notallthere404/futurecast/server/pkg/logger"

	v1 "github.com/notallthere404/futurecast/server/api/v1"

	classificationcontroller "github.com/notallthere404/futurecast/server/pkg/controller/classification"
)

// Server depends on small per-controller interfaces rather than the
// concrete *Controller types. Production wiring (cmd/server/main.go)
// passes the real controllers; each satisfies its interface
// structurally while tests substitute lightweight fakes.

type configDeps interface {
	ClientConfig() (config.ClientConfig, error)
}

type systemDeps interface {
	UpdateConfig(raw string) error
	Restart() error
	UptimeTotal(ctx context.Context, start, end string) (float64, error)
	UptimeSegment(ctx context.Context, format v1.RateFormat) ([]float64, error)
}

type sourcesDeps interface {
	List(ctx context.Context) ([]*v1.Source, error)
	Upsert(ctx context.Context, src *v1.Source) error
	UpsertBatch(ctx context.Context, srcs []*v1.Source) error
	Recent(ctx context.Context) ([]*v1.Article, error)
	Rate(ctx context.Context, format v1.RateFormat) ([]int, error)
	RunRSS(ctx context.Context) error
	WebhookHandler() http.Handler
}

type viewsDeps interface {
	List(ctx context.Context, userId *string) ([]*v1.View, error)
	Get(ctx context.Context, slug string) (*v1.RenderedView, error)
	Upsert(ctx context.Context, view *v1.View) error
	Delete(ctx context.Context, slug string) error
}

type classificationsDeps interface {
	Search(ctx context.Context, q classificationcontroller.Query) ([]*v1.LinkedClassification, error)
	Count(ctx context.Context, req v1.ClassificationCountRequest) (int, error)
	InsertBatch(ctx context.Context, payload []v1.ClassificationInsertItem) error
	Metrics(ctx context.Context, classification string, labels []string, start, end string) (map[string]*v1.Signal, error)
	Heatmap(ctx context.Context, req v1.HeatmapRequest) ([]*v1.LabelWeight, error)
	Treemap(ctx context.Context, req v1.TreemapRequest) ([]*v1.LabelCount, error)
	Plot(ctx context.Context, req v1.PlotRequest) ([]*v1.PlotPoint, error)
	Scatter(ctx context.Context, req v1.ScatterRequest) ([]*v1.ScatterPoint, error)
	Quadrant(ctx context.Context, req v1.QuadrantRequest) ([]*v1.LabelFrequencyAverage, error)
}

// inferenceDeps the manual-classify endpoint and any future inference
// admin surface lives here. Kick is the only entry point the dashboard
// needs today; under continuous mode sources kick the worker
// automatically, so the manual endpoint is only useful in manual mode.
type inferenceDeps interface {
	Kick()
}

type schedulerDeps interface {
	Run()
	Stop()
	Remove(label string)
}

// Server is the HTTP front of the application. It holds one slice
// per controller (declared as local *Deps interfaces in this package
// so the route handlers depend on a minimal contract) plus the live
// log broadcaster the SSE endpoint reads from.
type Server struct {
	log             *slog.Logger
	logs            *logger.Broadcaster
	config          configDeps
	system          systemDeps
	sources         sourcesDeps
	views           viewsDeps
	classifications classificationsDeps
	inference       inferenceDeps
	scheduler       schedulerDeps
	addr            string
}

// New wires the HTTP server. Each *Ctrl arg is the concrete controller
// satisfying the matching local *Deps interface; the server holds
// only what its route handlers need, not the full controller surface.
func New(
	log *slog.Logger,
	logs *logger.Broadcaster,
	addr string,
	configCtrl configDeps,
	systemCtrl systemDeps,
	sourcesCtrl sourcesDeps,
	viewsCtrl viewsDeps,
	classificationsCtrl classificationsDeps,
	inferenceCtrl inferenceDeps,
	schedulerCtrl schedulerDeps,
) *Server {
	return &Server{
		log:             log.With(slog.String("mod", "server")),
		logs:            logs,
		addr:            addr,
		config:          configCtrl,
		system:          systemCtrl,
		sources:         sourcesCtrl,
		views:           viewsCtrl,
		classifications: classificationsCtrl,
		inference:       inferenceCtrl,
		scheduler:       schedulerCtrl,
	}
}

func (s *Server) ListenAndServe() error {
	s.log.Info("starting server", "address", s.addr)
	srv := &http.Server{
		Addr:              s.addr,
		Handler:           s.loggerMiddleware(s.Routes()),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	return srv.ListenAndServe()
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	s.systemRoutes(mux)
	s.sourceRoutes(mux)
	s.viewRoutes(mux)
	s.schedulerRoutes(mux)
	s.classificationRoutes(mux)
	s.testRoutes(mux)
	if wh := s.sources.WebhookHandler(); wh != nil {
		mux.Handle("/webhooks/", wh)
	}
	return mux
}

func (s *Server) loggerMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.log.Info(
			"request",
			"method", r.Method,
			"path", r.URL.String(),
			"proto", r.Proto,
			"source", r.RemoteAddr,
		)
		next.ServeHTTP(w, r)
	})
}

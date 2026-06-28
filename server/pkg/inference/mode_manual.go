package inference

import (
	"context"
	"fmt"
	"log/slog"

	v1 "github.com/notallthere404/futurecast/server/api/v1"
)

// ManualMode is the per-request classifier: articles arrive via an
// explicit route (POST /classify), the controller runs them inline
// against the Client, and the persist hook writes the resulting rows
// into the classification table the same way ContinuousMode does.
//
// Refill returns nil so the background loop (if Kick fires
// accidentally from elsewhere) exits immediately. The classify route
// drives ManualMode directly via Controller.ClassifyInline.
type ManualMode struct {
	log             *slog.Logger
	classifications ClassificationStore
	guard           Guard
}

// NewManualMode wires the persist-only side of the manual flow.
// No article store is needed: in manual mode articles are supplied
// by the caller and never round-trip through the unprocessed table.
func NewManualMode(log *slog.Logger, classifications ClassificationStore, guard Guard) *ManualMode {
	return &ManualMode{
		log:             log.With(slog.String("mode", "manual")),
		classifications: classifications,
		guard:           guard,
	}
}

// Refill is a no-op; manual mode never pulls from a queue.
func (m *ManualMode) Refill(_ context.Context, _ int) ([]*v1.ClassifyArticle, error) {
	return nil, nil
}

// Persist writes classification rows under the schema guard. Unlike
// ContinuousMode it does NOT mark articles processed — manual
// articles aren't necessarily rows in the articles table (they may
// have come from a one-off file upload).
func (m *ManualMode) Persist(ctx context.Context, results v1.MappedClassArray) error {
	m.guard.RLock()
	defer m.guard.RUnlock()

	for name, classes := range results {
		if err := m.classifications.InsertClassificationBatch(ctx, name, classes); err != nil {
			return fmt.Errorf("insert classifications into %s: %w", name, err)
		}
		m.log.Debug("manual classifications inserted", "table", name, "count", len(classes))
	}
	return nil
}

// AutoDrive returns false so the controller's Kick is a no-op in
// manual mode. The classify route is the only entry point.
func (m *ManualMode) AutoDrive() bool { return false }

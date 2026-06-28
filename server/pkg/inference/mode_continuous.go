package inference

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	v1 "github.com/notallthere404/futurecast/server/api/v1"
)

// ContinuousMode is the steady-state background classifier: refills
// from the unprocessed-articles table and marks the source rows
// processed once results land in the classification table. Sources
// kick the controller's loop after every insert; the loop drives
// this mode until the article table is empty, then exits.
type ContinuousMode struct {
	log             *slog.Logger
	articles        ArticleStore
	classifications ClassificationStore
	guard           Guard
}

// NewContinuousMode wires the DB-backed refill + persist pair.
func NewContinuousMode(log *slog.Logger, articles ArticleStore, classifications ClassificationStore, guard Guard) *ContinuousMode {
	return &ContinuousMode{
		log:             log.With(slog.String("mode", "continuous")),
		articles:        articles,
		classifications: classifications,
		guard:           guard,
	}
}

// Refill pulls the next batch of unprocessed articles from the store.
// Held under the schema guard's RLock so a concurrent config reload's
// syncTables can't drop the articles table FK while the SELECT is in
// flight.
func (m *ContinuousMode) Refill(ctx context.Context, capacity int) ([]*v1.ClassifyArticle, error) {
	m.guard.RLock()
	defer m.guard.RUnlock()
	return m.articles.SelectArticleBatch(ctx, capacity)
}

// Persist writes classification rows then marks the source articles
// processed. Both steps share the guard's RLock so a config reload
// can't race the writes against a classification-table create/drop.
// A failure here aborts the loop; the article stays processed=false
// and the next kick retries.
func (m *ContinuousMode) Persist(ctx context.Context, results v1.MappedClassArray) error {
	m.guard.RLock()
	defer m.guard.RUnlock()

	var ids []string
	for name, classes := range results {
		if err := m.classifications.InsertClassificationBatch(ctx, name, classes); err != nil {
			return fmt.Errorf("insert classifications into %s: %w", name, err)
		}
		for _, cls := range classes {
			ids = append(ids, cls.ArticleID)
		}
		m.log.Debug("classified events inserted", "table", name, "count", len(classes))
	}
	if len(ids) == 0 {
		return nil
	}
	return m.articles.UpdateArticleProcessed(ctx, ids)
}

// AutoDrive returns true so Kick spawns the level-triggered loop.
func (m *ContinuousMode) AutoDrive() bool { return true }

// ParseClassifyResponses turns the wire-level responses into the
// MappedClassArray the persist callback consumes. Kept here so the
// continuous loop and any future ad-hoc caller share one path.
func ParseClassifyResponses(responses []*ClassifyResponse) (v1.MappedClassArray, error) {
	out := make(v1.MappedClassArray, len(responses))
	for _, r := range responses {
		ts, err := time.Parse(time.RFC3339, r.Timestamp)
		if err != nil {
			ts = time.Now()
		}
		out[r.Classification] = append(out[r.Classification], &v1.Classification{
			ID:        r.ID,
			ArticleID: r.ArticleID,
			Timestamp: ts,
			Data:      r.Data,
		})
	}
	return out, nil
}

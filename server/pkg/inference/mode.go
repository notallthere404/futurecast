package inference

import (
	"context"

	v1 "github.com/notallthere404/futurecast/server/api/v1"
)

// Mode is the article-fetching policy. The controller's event loop
// calls Refill to get the next batch and Persist to commit results;
// concrete modes decide where articles come from (DB, in-memory
// queue, request body) and whether to mark them processed.
//
// AutoDrive reports whether the controller's Kick should spawn the
// loop in this mode. ContinuousMode returns true; ManualMode false —
// manual classification fires only via an explicit route call.
type Mode interface {
	Refill(ctx context.Context, capacity int) ([]*v1.ClassifyArticle, error)
	Persist(ctx context.Context, results v1.MappedClassArray) error
	AutoDrive() bool
}

// ArticleStore is the slice ContinuousMode needs for refill + the
// mark-processed step after persist. Defined here (not in pkg/registry)
// so the mode files own their dependency surface.
type ArticleStore interface {
	SelectArticleBatch(ctx context.Context, limit int) ([]*v1.ClassifyArticle, error)
	UpdateArticleProcessed(ctx context.Context, ids []string) error
}

// ClassificationStore is the slice both modes need to write
// classification rows after each batch.
type ClassificationStore interface {
	InsertClassificationBatch(ctx context.Context, name string, classes []*v1.Classification) error
}

// Guard is the schema-mutation lock both modes hold (RLock) around
// inserts so concurrent CREATE/DROP on the classification tables
// can't race the writes. See pkg/schema for the deadlock it prevents.
type Guard interface {
	RLock()
	RUnlock()
}

// Package schema exports Guard, which serialises classification-schema mutations
// against concurrent data-plane traffic.
//
// Classification tables carry an FK to `articles`. PostgreSQL's
// DROP TABLE (and CREATE TABLE with FK) therefore briefly needs an
// AccessExclusiveLock on `articles` to add/remove the FK trigger.
// That conflicts with every concurrent INSERT or SELECT that touches
// `articles` or the classification table; when the conflict forms a
// cycle, PG breaks it with a deadlock (SQLSTATE 40P01).
//
// The Guard parks in-process callers before they reach the wire:
//
//   - Schema mutators (system controller's syncTables) take Lock.
//   - Data-plane callers (classifier inserts + dashboard reads) take
//     RLock around each unit of work.
//
// While a writer holds the lock, readers queue; once it releases, all
// queued readers proceed concurrently. The SQL itself never gets a
// chance to deadlock because the in-process schedule is enforced
// before the queries fire.
package schema

import "sync"

// Guard is a process-wide RWMutex shared between schema mutators
// (writers) and data-plane callers (readers) to break the FK-driven
// deadlock chain described in the package doc.
type Guard struct {
	mu sync.RWMutex
}

// New returns an unlocked Guard.
func New() *Guard { return &Guard{} }

// Lock acquires the writer lock — held by syncTables around
// CREATE / DROP statements.
func (g *Guard) Lock() { g.mu.Lock() }

// Unlock releases the writer lock.
func (g *Guard) Unlock() { g.mu.Unlock() }

// RLock acquires a reader lock — held by classifier inserts and
// dashboard reads so they queue while syncTables is running.
func (g *Guard) RLock() { g.mu.RLock() }

// RUnlock releases a reader lock.
func (g *Guard) RUnlock() { g.mu.RUnlock() }

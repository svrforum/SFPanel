package db

import (
	"context"
	"database/sql"
	"log/slog"
	"sync"
)

// AsyncWriter serializes background INSERTs onto a single goroutine
// draining a bounded queue. Audit middleware and the auth feature module
// used to each spawn a fresh `go func()` per state-changing request; under
// a burst that fans out unbounded goroutine creation (each holding a
// SQLite handle) and starves the connection pool. The bounded queue means
// a flood drops the oldest entries instead of locking the panel up, and
// shutdown can drain pending writes deterministically.
//
// Submit is non-blocking. On queue overflow the closure is dropped and a
// structured warning is logged — losing an audit row beats losing the
// request that caused it.
type AsyncWriter struct {
	db    *sql.DB
	name  string // identifies the writer in dropped-row warnings
	queue chan func(*sql.DB)
	wg    sync.WaitGroup
}

// NewAsyncWriter starts the drain goroutine. The writer stops when ctx is
// cancelled and drains the remaining queue before exiting so a clean
// shutdown still persists in-flight rows. Capacity should be sized for
// burst traffic, not steady state — 256 covers a 5-second spike at 50
// rps which is well above sustained sfpanel write load.
func NewAsyncWriter(ctx context.Context, db *sql.DB, name string, capacity int) *AsyncWriter {
	aw := &AsyncWriter{
		db:    db,
		name:  name,
		queue: make(chan func(*sql.DB), capacity),
	}
	aw.wg.Add(1)
	go aw.run(ctx)
	return aw
}

// Submit non-blockingly enqueues a write closure. Returns true if accepted,
// false if the queue is full (the closure is dropped and a warning is
// logged at most once per drop).
func (aw *AsyncWriter) Submit(fn func(*sql.DB)) bool {
	if aw == nil || fn == nil {
		return false
	}
	select {
	case aw.queue <- fn:
		return true
	default:
		slog.Warn("async writer queue full, dropping row",
			"component", "db", "writer", aw.name, "capacity", cap(aw.queue))
		return false
	}
}

// Wait blocks until the drain goroutine has finished. Intended for tests
// and graceful shutdown — production callers connect ctx instead.
func (aw *AsyncWriter) Wait() {
	if aw == nil {
		return
	}
	aw.wg.Wait()
}

func (aw *AsyncWriter) run(ctx context.Context) {
	defer aw.wg.Done()
	for {
		select {
		case fn := <-aw.queue:
			aw.execute(fn)
		case <-ctx.Done():
			// Drain remaining queue so a graceful shutdown still
			// persists in-flight rows.
			for {
				select {
				case fn := <-aw.queue:
					aw.execute(fn)
				default:
					return
				}
			}
		}
	}
}

func (aw *AsyncWriter) execute(fn func(*sql.DB)) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("async writer closure panicked",
				"component", "db", "writer", aw.name, "panic", r)
		}
	}()
	fn(aw.db)
}

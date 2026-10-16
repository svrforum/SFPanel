package middleware

import (
	"context"
	"net/http"
	"sync"

	"github.com/labstack/echo/v4"
)

// Streams ends the event streams still open when the server shuts down.
//
// http.Server.Shutdown waits for every response in progress, and an event
// stream ends only when its client leaves. One progress view left open — a
// cluster update's, served here or relayed through here to the leader — held
// every restart for the whole grace period, and the stop then failed. The
// streaming endpoints (isStreamingEndpoint, the routes the cluster proxy
// relays) run under a context End cancels; their handlers return as they do
// when the client goes away, and ordinary requests keep the grace period to
// finish in.
//
// A stream ended this way is aborted, not closed: its client sees the stream
// break, as it did when the process went away under it, and does not take an
// install or a compose run cut off half way for one that finished.
type Streams struct {
	mu      sync.Mutex
	next    uint64
	cancels map[uint64]context.CancelFunc
	ended   bool
}

// NewStreams returns a Streams with nothing in flight.
func NewStreams() *Streams {
	return &Streams{cancels: make(map[uint64]context.CancelFunc)}
}

// Middleware runs each streaming request under a context End cancels.
func (s *Streams) Middleware() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if !isStreamingEndpoint(c.Request().URL.Path) {
				return next(c)
			}
			ctx, cancel := context.WithCancel(c.Request().Context())
			defer cancel()
			id := s.add(cancel)
			defer s.remove(id)
			c.SetRequest(c.Request().WithContext(ctx))
			err := next(c)
			if ctx.Err() != nil && s.shuttingDown() {
				// net/http drops the connection without ending the response.
				panic(http.ErrAbortHandler)
			}
			return err
		}
	}
}

func (s *Streams) add(cancel context.CancelFunc) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended {
		// Shutdown has begun: a stream that slips in now ends at once.
		cancel()
	}
	s.next++
	s.cancels[s.next] = cancel
	return s.next
}

func (s *Streams) remove(id uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.cancels, id)
}

func (s *Streams) shuttingDown() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ended
}

// End cancels every stream in flight, and every one that starts after it.
func (s *Streams) End() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ended = true
	for _, cancel := range s.cancels {
		cancel()
	}
}

// EndOnShutdown has each server call End as its Shutdown begins.
func (s *Streams) EndOnShutdown(servers ...*http.Server) {
	for _, srv := range servers {
		srv.RegisterOnShutdown(s.End)
	}
}

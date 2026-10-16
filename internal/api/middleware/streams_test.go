package middleware

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	echoMw "github.com/labstack/echo/v4/middleware"
)

// streamServer serves e with s ending its streams on Shutdown, the way the
// router wires them: Recover outside, Streams right inside it.
func streamServer(t *testing.T, s *Streams, routes func(e *echo.Echo)) *httptest.Server {
	return streamServerOver(t, s, routes, false)
}

// streamServerOver serves over TLS with HTTP/2 when h2 is set, as a browser
// reaches a panel with server.tls on.
func streamServerOver(t *testing.T, s *Streams, routes func(e *echo.Echo), h2 bool) *httptest.Server {
	t.Helper()
	e := echo.New()
	e.Use(echoMw.Recover())
	e.Use(s.Middleware())
	routes(e)
	srv := httptest.NewUnstartedServer(e)
	s.EndOnShutdown(srv.Config)
	if h2 {
		srv.EnableHTTP2 = true
		srv.StartTLS()
	} else {
		srv.Start()
	}
	t.Cleanup(srv.Close)
	return srv
}

// holdStream sends one event and then holds the stream open until its
// request context ends, as the cluster update's does while nodes restart.
func holdStream(c echo.Context) error {
	c.Response().Header().Set("Content-Type", "text/event-stream")
	c.Response().WriteHeader(http.StatusOK)
	fmt.Fprint(c.Response(), "data: {\"step\":\"started\"}\n\n")
	c.Response().Flush()
	<-c.Request().Context().Done()
	return nil
}

func TestShutdownEndsAnOpenEventStream(t *testing.T) {
	for _, h2 := range []bool{false, true} {
		t.Run(map[bool]string{false: "http1", true: "tls-http2"}[h2], func(t *testing.T) {
			shutdownEndsAnOpenEventStream(t, h2)
		})
	}
}

func shutdownEndsAnOpenEventStream(t *testing.T, h2 bool) {
	srv := streamServerOver(t, NewStreams(), func(e *echo.Echo) {
		e.POST("/api/v1/cluster/update", holdStream)
	}, h2)
	resp, err := srv.Client().Post(srv.URL+"/api/v1/cluster/update", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if want := map[bool]int{false: 1, true: 2}[h2]; resp.ProtoMajor != want {
		t.Fatalf("served over HTTP/%d, want HTTP/%d", resp.ProtoMajor, want)
	}
	body := bufio.NewReader(resp.Body)
	if line, err := body.ReadString('\n'); err != nil || !strings.HasPrefix(line, "data: ") {
		t.Fatalf("first event = %q, %v", line, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	start := time.Now()
	if err := srv.Config.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown with a stream open: %v after %v; it waited for the stream instead of ending it", err, time.Since(start).Round(time.Millisecond))
	}
	// Broken, not finished: a page reading it must not take it for done.
	if rest, err := io.ReadAll(body); err == nil {
		t.Fatalf("the stream read to a clean end (%q); one cut off by a shutdown must break", rest)
	}
}

func TestShutdownLetsAnOrdinaryRequestFinish(t *testing.T) {
	running := make(chan struct{})
	srv := streamServer(t, NewStreams(), func(e *echo.Echo) {
		e.GET("/api/v1/system/info", func(c echo.Context) error {
			close(running)
			time.Sleep(300 * time.Millisecond)
			if err := c.Request().Context().Err(); err != nil {
				return c.String(http.StatusServiceUnavailable, "cancelled: "+err.Error())
			}
			return c.String(http.StatusOK, "finished")
		})
	})
	type result struct {
		status int
		body   string
		err    error
	}
	got := make(chan result, 1)
	go func() {
		resp, err := http.Get(srv.URL + "/api/v1/system/info")
		if err != nil {
			got <- result{err: err}
			return
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(resp.Body)
		got <- result{resp.StatusCode, string(b), err}
	}()
	<-running

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := srv.Config.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	r := <-got
	if r.err != nil || r.status != http.StatusOK || r.body != "finished" {
		t.Fatalf("request in flight at shutdown = %d %q (%v), want 200 \"finished\" with its context still alive", r.status, r.body, r.err)
	}
}

func TestStreamStartedAfterEndEndsAtOnce(t *testing.T) {
	s := NewStreams()
	srv := streamServer(t, s, func(e *echo.Echo) {
		e.POST("/api/v1/cluster/update", holdStream)
	})
	s.End()

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Post(srv.URL+"/api/v1/cluster/update", "application/json", nil)
	if err == nil {
		_, err = io.ReadAll(resp.Body)
		resp.Body.Close()
	}
	if err == nil {
		t.Fatal("a stream started after End read to a clean end; it should break at once")
	}
	if strings.Contains(err.Error(), "Client.Timeout") {
		t.Fatalf("a stream started after End stayed open: %v", err)
	}
}

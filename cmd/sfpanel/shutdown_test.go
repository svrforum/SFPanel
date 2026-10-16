package main

import (
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
)

// A request that outlasts the grace period is cut off, and shutdownServer
// returns: what follows it in main drains the audit queue and closes the
// database. It used to exit the process there, status 1, and skip them.
func TestShutdownServerReturnsWhenARequestOutlastsTheGrace(t *testing.T) {
	e := echo.New()
	e.HideBanner, e.HidePort = true, true
	running, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	e.GET("/slow", func(c echo.Context) error {
		close(running)
		<-release // does not watch its context, as a long command run does not
		return c.String(http.StatusOK, "late")
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	e.Listener = ln
	go func() { _ = e.Start("") }()
	clientDone := make(chan error, 1)
	go func() {
		resp, err := http.Get("http://" + ln.Addr().String() + "/slow")
		if err == nil {
			resp.Body.Close()
		}
		clientDone <- err
	}()
	<-running

	start := time.Now()
	shutdownServer(e, 200*time.Millisecond)
	if took := time.Since(start); took > 2*time.Second {
		t.Fatalf("shutdownServer took %v with a grace of 200ms", took)
	}
	select {
	case err := <-clientDone:
		if err == nil {
			t.Fatal("the request outlasting the grace got its response; its connection should have been closed")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the request outlasting the grace still had its connection after shutdownServer returned")
	}
}

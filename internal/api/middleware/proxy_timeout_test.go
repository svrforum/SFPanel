package middleware

import (
	"testing"
	"time"
)

// The proxy capped every ?node= request at thirty seconds while the handlers
// on the other side deliberately allow five minutes, so work that succeeds on
// the local node was cut off partway when aimed at another one — and the
// subprocess dies with the proxied request, leaving the remote host with a
// half-finished operation and nobody told.
func TestProxyAllowsTheOperationsTheHandlersAllow(t *testing.T) {
	long := []string{
		"/api/v1/docker/compose/myapp/up",
		"/api/v1/appstore/apps/immich/install",
		"/api/v1/packages/install",
		"/api/v1/packages/remove",
		"/api/v1/filesystems/format",
		"/api/v1/filesystems/resize",
		"/api/v1/swap",
		"/api/v1/system/restore",
		"/api/v1/system/restore/file",
		"/api/v1/disks/network-shares/tools/install",
	}
	for _, p := range long {
		if got := proxyTimeoutFor(p); got != 5*time.Minute {
			t.Errorf("proxyTimeoutFor(%q) = %v, want 5m", p, got)
		}
	}
}

// And the default stays short. A stuck request must not hold a proxy
// connection for five minutes because the list grew careless.
func TestProxyKeepsOrdinaryRequestsShort(t *testing.T) {
	short := []string{
		"/api/v1/system/info",
		"/api/v1/files?path=/etc",
		"/api/v1/docker/containers",
		"/api/v1/network/interfaces",
		"/api/v1/logs/read",
		"/api/v1/system/processes",
	}
	for _, p := range short {
		if got := proxyTimeoutFor(p); got != 30*time.Second {
			t.Errorf("proxyTimeoutFor(%q) = %v, want 30s", p, got)
		}
	}
}

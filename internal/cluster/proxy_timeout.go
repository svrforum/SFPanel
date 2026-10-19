package cluster

import (
	"strings"
	"time"
)

// longProxyPaths are the operations the target node deliberately allows five
// minutes for.
//
// The proxy capped every ?node= request at thirty seconds and exempted only
// /docker/compose/, so the same work that succeeds locally — mkfs on a large
// volume, an apt install, writing a swap file with dd, tearing down a stack —
// was cut off partway when it was aimed at another node. The subprocess is
// bound to the proxied request, so it died with it: the operator got a
// timeout, the remote host got a half-finished operation, and neither said so.
//
// Matched on substrings rather than exact paths because these carry ids and
// names, and kept as a list rather than "anything that might be slow" so the
// default stays short — a genuinely stuck request should not hold a proxy
// connection for five minutes.
var longProxyPaths = []string{
	"/docker/compose/",
	"/appstore/apps/",
	"/packages/install",
	"/packages/remove",
	"/filesystems/format",
	"/filesystems/resize",
	"/swap",
	"/system/restore",
	"/disks/network-shares/tools/install",
}

// ProxyTimeoutFor is how long a proxied request may take, on the sending
// node's gRPC call and on the receiving node's loopback hop alike. One list
// for both: the receiver used to keep its own copy holding only
// /docker/compose/, so everything else here was cut off at thirty seconds on
// the far side while the sender waited five minutes.
func ProxyTimeoutFor(path string) time.Duration {
	for _, p := range longProxyPaths {
		if strings.Contains(path, p) {
			return localHTTPLongTimeout
		}
	}
	return localHTTPTimeout
}

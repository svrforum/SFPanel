package disk

import (
	"os"
	"sync"
)

// ownMountNamespace reports whether the panel runs in a mount namespace of
// its own rather than PID 1's. The unit install.sh wrote from v0.13 to
// v0.78.11 set PrivateTmp=true, which gives the service exactly that: a mount
// made there stays invisible to the host, its shells and Docker, so a
// container bound to the mountpoint wrote into the empty directory on the
// root disk. In a container PID 1 shares the panel's namespace, and nothing
// changes there. Overridable in tests.
var ownMountNamespace = sync.OnceValue(func() bool {
	self, err := os.Readlink("/proc/self/ns/mnt")
	if err != nil {
		return false
	}
	host, err := os.Readlink("/proc/1/ns/mnt")
	return err == nil && self != host
})

// inHostMountNS returns the command that runs name with args in the host's
// mount namespace: unchanged when the panel already shares it, otherwise
// through nsenter. A mount made in the host namespace also propagates back
// into the panel's, so the panel's own view of it stays right.
//
// Only for mounts meant to outlive the request. A probe that mounts into a
// directory from os.MkdirTemp must stay in the panel's namespace: under
// PrivateTmp that directory does not exist in the host's /tmp.
func inHostMountNS(name string, args ...string) (string, []string) {
	if !ownMountNamespace() {
		return name, args
	}
	return "nsenter", append([]string{"--mount=/proc/1/ns/mnt", "--", name}, args...)
}

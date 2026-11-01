package cluster

import (
	"fmt"
	"regexp"
)

// ValidateLabels enforces a small bounded schema on user-supplied node
// labels — without this, the FSM Apply path persisted whatever the caller
// sent (including multi-MB strings) into the Raft log forever. Following
// k8s conventions: 63 chars max per key/value, key starts with alnum,
// allowed chars [A-Za-z0-9._-]. Caps total label count at 32 per node. The
// label route checks a node's labels with it, and an agent install the
// group it will give each server, before it touches the server.
func ValidateLabels(labels map[string]string) error {
	if len(labels) > 32 {
		return fmt.Errorf("too many labels (max 32)")
	}
	for k, v := range labels {
		if len(k) == 0 || len(k) > 63 {
			return fmt.Errorf("label key %q must be 1..63 chars", k)
		}
		if !labelKeyRE.MatchString(k) {
			return fmt.Errorf("label key %q has invalid characters (allowed: alnum + .-_)", k)
		}
		if len(v) > 63 {
			return fmt.Errorf("label value for %q exceeds 63 chars", k)
		}
		if !labelValueRE.MatchString(v) {
			return fmt.Errorf("label value for %q has invalid characters", k)
		}
	}
	return nil
}

var (
	labelKeyRE   = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)
	labelValueRE = regexp.MustCompile(`^[a-zA-Z0-9._-]*$`)
)

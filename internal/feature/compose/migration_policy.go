package compose

import (
	"net/http"
	"strconv"
)

// importPolicy is what the manager that sends an import decides about it,
// never the bundle: whether to replace a stack or volume that exists on the
// target (the operator's acknowledgement), whether the operator acknowledged
// a risky compose file, and whether the bundle came from an agent — whose
// root must not reach the target through it. Only a manager can send an
// import: it is signed with the target's key, and an agent holds no other
// node's key.
type importPolicy struct {
	Overwrite, AckRisks, AgentSource bool
}

const (
	migrationPolicyHeader     = "X-SFPanel-Migration-Policy"
	migrationOverwriteHeader  = "X-SFPanel-Migration-Overwrite"
	migrationAckRisksHeader   = "X-SFPanel-Migration-Ack-Risks"
	migrationSourceKindHeader = "X-SFPanel-Migration-Source-Kind"
)

func (p importPolicy) setHeaders(h http.Header) {
	h.Set(migrationPolicyHeader, "1")
	h.Set(migrationOverwriteHeader, strconv.FormatBool(p.Overwrite))
	h.Set(migrationAckRisksHeader, strconv.FormatBool(p.AckRisks))
	kind := "manager"
	if p.AgentSource {
		kind = "agent"
	}
	h.Set(migrationSourceKindHeader, kind)
}

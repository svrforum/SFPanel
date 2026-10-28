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

// importPolicyFrom reads the policy the sending manager stated. A sender from
// before policies states none; it is a manager — agents existed only once
// every manager knew them — and its bundle's word on overwrite stands, with
// risks taken as answered when the stack was created, as they were.
func importPolicyFrom(h http.Header, m MigrationManifest) importPolicy {
	if h.Get(migrationPolicyHeader) == "" {
		return importPolicy{Overwrite: m.Overwrite, AckRisks: true}
	}
	return importPolicy{
		Overwrite:   h.Get(migrationOverwriteHeader) == "true",
		AckRisks:    h.Get(migrationAckRisksHeader) == "true",
		AgentSource: h.Get(migrationSourceKindHeader) != "manager", // anything else is the stricter case
	}
}

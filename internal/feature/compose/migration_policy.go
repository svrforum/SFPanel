package compose

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
)

// importPolicy is what the manager that sends an import decides about it,
// never the bundle: whether to replace a stack or volume that exists on the
// target (the operator's acknowledgement), which risky findings the operator
// was shown and acknowledged, and whether the bundle came from an agent —
// whose root must not reach the target through it. Only a manager can send an
// import: it is signed with the target's key, and an agent holds no other
// node's key.
//
// AckedRisks is the findings' sentences, not a yes: the lines the dialog
// listed came from the agent, and a yes to the one line an agent chose to
// show must not cover the docker.sock bind it left out.
type importPolicy struct {
	Overwrite, AgentSource bool
	AckedRisks             []string
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
	h.Set(migrationAckRisksHeader, encodeAckedRisks(p.AckedRisks))
	kind := "manager"
	if p.AgentSource {
		kind = "agent"
	}
	h.Set(migrationSourceKindHeader, kind)
}

// encodeAckedRisks is the header form of the acknowledged findings: base64
// of a JSON array, so a path with quotes, commas or non-ASCII survives.
func encodeAckedRisks(risks []string) string {
	if risks == nil {
		risks = []string{}
	}
	b, _ := json.Marshal(risks) // a []string always marshals
	return base64.StdEncoding.EncodeToString(b)
}

// decodeAckedRisks reads that header. Anything that does not decode
// acknowledges nothing.
func decodeAckedRisks(v string) []string {
	b, err := base64.StdEncoding.DecodeString(v)
	if err != nil {
		return nil
	}
	var risks []string
	if json.Unmarshal(b, &risks) != nil {
		return nil
	}
	return risks
}

// importPolicyFrom reads the policy the sending manager stated. A sender from
// before policies states none; it is a manager — agents existed only once
// every manager knew them — and its bundle's word on overwrite stands, with
// risks taken as answered when the stack was created, as they were (a
// manager's source lifts the risky tier whatever the list says).
func importPolicyFrom(h http.Header, m MigrationManifest) importPolicy {
	if h.Get(migrationPolicyHeader) == "" {
		return importPolicy{Overwrite: m.Overwrite}
	}
	return importPolicy{
		Overwrite:   h.Get(migrationOverwriteHeader) == "true",
		AckedRisks:  decodeAckedRisks(h.Get(migrationAckRisksHeader)),
		AgentSource: h.Get(migrationSourceKindHeader) != "manager", // anything else is the stricter case
	}
}

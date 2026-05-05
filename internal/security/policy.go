package security

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/svrforum/SFPanel/internal/cluster"
)

// Mode is the global policy mode. Empty string means off (back-compat for
// pre-Theme-C clusters where state.Config["security.policy"] is absent).
type Mode string

const (
	ModeOff     Mode = "off"
	ModeWarn    Mode = "warn"
	ModeRequire Mode = "require"
)

// Policy is the cluster-replicated security policy. Stored as JSON in the
// Raft FSM at Config["security.policy"].
type Policy struct {
	Mode  Mode   `json:"mode"`
	Rules []Rule `json:"rules"`
}

// IsOff returns true when verification is disabled (mode = off OR empty).
// Hot path for the verifier — used to short-circuit before any DB or
// cosign work.
func (p Policy) IsOff() bool {
	return p.Mode == "" || p.Mode == ModeOff
}

// Rule maps a glob pattern to a required signing identity.
type Rule struct {
	Pattern  string   `json:"pattern"`
	Identity Identity `json:"identity"`
}

// Identity is a Sigstore keyless identity (cert SAN URI prefix + OIDC issuer).
type Identity struct {
	SubjectPrefix string `json:"subject_prefix"`
	Issuer        string `json:"issuer"`
}

// configKey is the Raft FSM Config key. Stable forever.
const configKey = "security.policy"

// LoadPolicy reads the current cluster-wide policy. Returns an off policy
// (no error) when the FSM Config has no entry — this is the case for
// clusters upgrading from pre-Theme-C without explicit opt-in.
func LoadPolicy(c *cluster.Manager) (Policy, error) {
	if c == nil {
		return Policy{Mode: ModeOff}, nil
	}
	raw := c.GetConfigValue(configKey)
	if raw == "" {
		return Policy{Mode: ModeOff}, nil
	}
	var p Policy
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return Policy{}, fmt.Errorf("decode policy: %w", err)
	}
	return p, nil
}

// SavePolicy writes the policy via cluster.SetConfig (Raft Apply, leader
// only). Validates Mode + Rule fields before writing — bad input never
// reaches the FSM.
func SavePolicy(c *cluster.Manager, p Policy) error {
	if err := p.Validate(); err != nil {
		return err
	}
	data, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("encode policy: %w", err)
	}
	return c.SetConfig(configKey, string(data))
}

// Validate reports input mistakes that would corrupt the FSM if applied.
func (p Policy) Validate() error {
	switch p.Mode {
	case ModeOff, ModeWarn, ModeRequire:
	default:
		return fmt.Errorf("invalid mode %q (want off|warn|require)", p.Mode)
	}
	for i, r := range p.Rules {
		if strings.TrimSpace(r.Pattern) == "" {
			return fmt.Errorf("rule[%d]: pattern empty", i)
		}
		if strings.TrimSpace(r.Identity.SubjectPrefix) == "" {
			return fmt.Errorf("rule[%d]: subject_prefix empty", i)
		}
		if strings.TrimSpace(r.Identity.Issuer) == "" {
			return fmt.Errorf("rule[%d]: issuer empty", i)
		}
	}
	return nil
}

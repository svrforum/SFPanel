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

// MatchRule returns the first Rule matching ref, or (Rule{}, false). Pattern
// uses glob: `*` matches a single segment, `**` matches multiple. Implicit
// docker.io/library/ prefix is normalized before matching so that "postgres"
// matches "docker.io/library/postgres:*".
func (p Policy) MatchRule(ref string) (Rule, bool) {
	full := normalizeRef(ref)
	for _, r := range p.Rules {
		if matchGlob(r.Pattern, full) || matchGlob(normalizeRef(r.Pattern), full) {
			return r, true
		}
	}
	return Rule{}, false
}

// normalizeRef expands shorthand to a fully-qualified reference:
//
//	"postgres"          → "docker.io/library/postgres:latest"
//	"myuser/img:1"      → "docker.io/myuser/img:1"
//	"ghcr.io/x/y:tag"   → unchanged
//	"ghcr.io/x/y"       → "ghcr.io/x/y:latest"
//	"x:tag@sha256:..."  → "x:tag@sha256:..." (digest preserved)
func normalizeRef(ref string) string {
	atIdx := strings.Index(ref, "@")
	digest := ""
	if atIdx >= 0 {
		digest = ref[atIdx:]
		ref = ref[:atIdx]
	}
	hasRegistry := false
	if i := strings.Index(ref, "/"); i >= 0 {
		first := ref[:i]
		if first == "localhost" || strings.ContainsAny(first, ".:") {
			hasRegistry = true
		}
	}
	if !hasRegistry {
		if strings.Contains(ref, "/") {
			ref = "docker.io/" + ref
		} else {
			ref = "docker.io/library/" + ref
		}
	}
	lastSlash := strings.LastIndex(ref, "/")
	tagPart := ref[lastSlash+1:]
	if !strings.Contains(tagPart, ":") {
		ref += ":latest"
	}
	return ref + digest
}

// matchGlob splits pattern + s into path/tag halves and compares each.
// Pattern segments are separated by `/`; `*` matches one segment, `**`
// matches zero or more. Within a segment, `*` is a wildcard for any run
// of non-`/` characters.
func matchGlob(pattern, s string) bool {
	pPath, pTag := splitTag(pattern)
	sPath, sTag := splitTag(s)
	if !globPath(pPath, sPath) {
		return false
	}
	if pTag == "" || pTag == "*" {
		return true
	}
	return globSegment(pTag, sTag)
}

// splitTag returns (path, tag-or-digest). Tag includes everything after the
// LAST colon AFTER the last slash (so registry:port doesn't split).
func splitTag(s string) (string, string) {
	lastSlash := strings.LastIndex(s, "/")
	rest := s[lastSlash+1:]
	colon := strings.Index(rest, ":")
	if colon < 0 {
		return s, ""
	}
	return s[:lastSlash+1] + rest[:colon], rest[colon+1:]
}

func globPath(pattern, s string) bool {
	return globSegments(strings.Split(pattern, "/"), strings.Split(s, "/"))
}

func globSegments(p, s []string) bool {
	for i, seg := range p {
		if seg == "**" {
			rest := p[i+1:]
			for j := 0; j <= len(s); j++ {
				if globSegments(rest, s[j:]) {
					return true
				}
			}
			return false
		}
		if i >= len(s) {
			return false
		}
		if !globSegment(seg, s[i]) {
			return false
		}
	}
	return len(p) == len(s)
}

// globSegment — `*` wildcard, no slashes. Greedy substring split.
func globSegment(pattern, s string) bool {
	if pattern == "*" {
		return true
	}
	if !strings.Contains(pattern, "*") {
		return pattern == s
	}
	parts := strings.Split(pattern, "*")
	idx := 0
	if !strings.HasPrefix(s, parts[0]) {
		return false
	}
	idx += len(parts[0])
	for i := 1; i < len(parts)-1; i++ {
		j := strings.Index(s[idx:], parts[i])
		if j < 0 {
			return false
		}
		idx += j + len(parts[i])
	}
	last := parts[len(parts)-1]
	return strings.HasSuffix(s[idx:], last)
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

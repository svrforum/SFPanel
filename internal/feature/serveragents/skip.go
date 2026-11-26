package featureserveragents

import (
	"fmt"
	"strings"

	"github.com/svrforum/SFPanel/internal/serveragents"
)

// Why a node leaves an agent alone.
const (
	WhyOS        = "os"         // not a Debian or Ubuntu system
	WhyLegacy    = "legacy"     // the old Zabbix agent (agentd) is there
	WhyUnmanaged = "unmanaged"  // the agent is there, but not from its package
	WhyNoPackage = "no_package" // neither the vendor nor the OS has zabbix-agent2
	WhyNewer     = "newer"      // the Wazuh agent is newer than the version set
)

// Skip is why a node leaves an agent alone. The run records the step as
// skipped with it, and moves on.
type Skip struct {
	Why string
	// Message is for the run record, in English.
	Message string
	// Details are agent and why; installed and version for newer; os for
	// no_package and os.
	Details map[string]string
}

// Skip says why this node leaves the agent alone, or nil when it deploys
// it. Every reason is about the host as it is, never about s's values,
// except the Wazuh version s sets.
func (h *Host) Skip(agent string, s *serveragents.Settings) *Skip {
	o := h.osRelease()
	switch agent {
	case serveragents.Zabbix:
		return h.skipZabbix(o)
	case serveragents.Wazuh:
		return h.skipWazuh(o, s)
	}
	return nil
}

func (h *Host) skipZabbix(o serveragents.OS) *Skip {
	const agent = serveragents.Zabbix
	if !o.Debianish() {
		return skipOS(agent, o)
	}
	f := h.readZabbix()
	switch {
	case f.state == serveragents.StateLegacy:
		return skip(agent, WhyLegacy, "the old Zabbix agent (zabbix-agent) is here; only Zabbix Agent 2 is set up, and not beside it", nil)
	case f.state == serveragents.StateUnmanaged:
		return skip(agent, WhyUnmanaged, "Zabbix Agent 2 is here but not from the "+zabbixPackage+" package; it is left as it is", nil)
	case f.agent2.Installed && !f.hasConf:
		return skip(agent, WhyUnmanaged, "the "+zabbixPackage+" package is here without "+zabbixConfPath+"; it is left as it is", nil)
	case f.state == serveragents.StateInstalled:
		return nil
	}
	if _, vendor := serveragents.ZabbixRepoLine(o, ""); vendor {
		return nil
	}
	if h.aptCandidate(zabbixPackage) != "" {
		return nil
	}
	name := osName(o)
	return skip(agent, WhyNoPackage, fmt.Sprintf("neither the Zabbix repository nor %s's own archive has %s", name, zabbixPackage), map[string]string{"os": name})
}

func (h *Host) skipWazuh(o serveragents.OS, s *serveragents.Settings) *Skip {
	const agent = serveragents.Wazuh
	if !o.Debianish() {
		return skipOS(agent, o)
	}
	f := h.readWazuh()
	if f.state == serveragents.StateUnmanaged {
		return skip(agent, WhyUnmanaged, "a Wazuh agent is here ("+ossecConfPath+") but not from the "+wazuhPackage+" package; it is left as it is", nil)
	}
	version := serveragents.DefaultWazuhVersion
	if s != nil && s.Wazuh.Version != "" {
		version = s.Wazuh.Version
	}
	if f.pkg.Installed && serveragents.CompareVersions(serveragents.UpstreamVersion(f.pkg.Version), version) > 0 {
		return skip(agent, WhyNewer, fmt.Sprintf("the Wazuh agent here, %s, is newer than %s, the version set for the cluster; it is left as it is", f.pkg.Version, version),
			map[string]string{"installed": f.pkg.Version, "version": version})
	}
	return nil
}

func skipOS(agent string, o serveragents.OS) *Skip {
	name := osName(o)
	return skip(agent, WhyOS, name+" is not Debian or Ubuntu, which the agents are installed on with apt", map[string]string{"os": name})
}

func skip(agent, why, reason string, details map[string]string) *Skip {
	d := map[string]string{"agent": agent, "why": why}
	for k, v := range details {
		d[k] = v
	}
	return &Skip{Why: why, Message: "skipped: " + reason, Details: d}
}

// osName is how os-release names the system.
func osName(o serveragents.OS) string {
	if o.Pretty != "" {
		return o.Pretty
	}
	if name := strings.TrimSpace(o.ID + " " + o.VersionID); name != "" {
		return name
	}
	return "this system"
}

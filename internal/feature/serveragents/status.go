package featureserveragents

import (
	"context"
	"errors"
	"net"
	"os"
	"strconv"
	"time"

	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/serveragents"
)

// agentdStale is how long wazuh-agentd.state may go unchanged before it no
// longer says how the agent is: a running agent rewrites it every 5 seconds.
const agentdStale = 2 * time.Minute

// Status reads the node's agents: how each is installed, what its config
// file says, and whether it reaches its server. It changes nothing.
func (h *Host) Status(ctx context.Context) serveragents.Status {
	return serveragents.Status{
		OS:        h.osRelease(),
		Zabbix:    h.zabbixStatus(ctx),
		Wazuh:     h.wazuhStatus(),
		CheckedAt: h.Now(),
	}
}

func (h *Host) zabbixStatus(ctx context.Context) serveragents.ZabbixStatus {
	f := h.readZabbix()
	st := serveragents.ZabbixStatus{State: f.state}
	switch f.state {
	case serveragents.StateNone:
		return st
	case serveragents.StateLegacy:
		// Out of this feature: what it is, and nothing about how it runs.
		st.Version = f.legacy.Version
		st.Source = serveragents.ZabbixSource(f.legacy.Version)
		return st
	}
	st.Version = f.agent2.Version
	st.Source = serveragents.ZabbixSource(f.agent2.Version)
	var conf serveragents.ZabbixConf
	if f.hasConf {
		conf = serveragents.ReadZabbixConf(f.conf)
		st.Config = &conf
	}
	st.Service = h.service(zabbixUnit)
	st.Connection = h.zabbixConnection(ctx, st.Service, conf)
	return st
}

// zabbixConnection judges whether an agent with this config reaches its
// server: the service has to be active, ServerActive has to accept a TCP
// connection, and the agent's own log since it last started must not end in
// a failure.
func (h *Host) zabbixConnection(ctx context.Context, service string, conf serveragents.ZabbixConf) *serveragents.Connection {
	if service != "active" {
		return &serveragents.Connection{State: serveragents.ConnNotRunning}
	}
	if conf.ServerActive == "" {
		return &serveragents.Connection{State: serveragents.ConnNoServer}
	}
	host, port := serveragents.ZabbixSettings{ServerActive: conf.ServerActive}.ActiveAddress()
	if host == "" {
		// Several servers, or a value that is not an address: which one the
		// agent is meant to reach is not something to guess.
		return &serveragents.Connection{State: serveragents.ConnUnknown, Detail: response.SanitizeOutput("ServerActive is not one address: " + conf.ServerActive)}
	}
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	if err := h.dial(ctx, addr); err != nil {
		return &serveragents.Connection{State: serveragents.ConnUnreachable, Detail: response.SanitizeOutput(err.Error())}
	}
	switch class, line := serveragents.ClassifyZabbixLog(h.readTail(zabbixLogPath, logTail)); class {
	case serveragents.LogNotFound:
		return &serveragents.Connection{State: serveragents.ConnNotRegistered, Detail: response.SanitizeOutput(line)}
	case serveragents.LogUnreachable:
		return &serveragents.Connection{State: serveragents.ConnFailing, Detail: response.SanitizeOutput(line)}
	}
	return &serveragents.Connection{State: serveragents.ConnConnected}
}

func (h *Host) wazuhStatus() serveragents.WazuhStatus {
	f := h.readWazuh()
	st := serveragents.WazuhStatus{State: f.state}
	if f.state == serveragents.StateNone {
		return st
	}
	st.Version = f.pkg.Version
	st.Held = f.pkg.Held
	if f.hasConf {
		switch conf, err := serveragents.ReadOssecConf(f.conf); {
		case errors.Is(err, serveragents.ErrOssecUnreadable):
			st.ConfigError = serveragents.ErrOssecUnreadable.Error()
		case err != nil:
			st.ConfigError = err.Error()
		default:
			st.Config = &conf
		}
	}
	st.Service = h.service(wazuhUnit)
	st.Connection = h.wazuhConnection(st.Service)
	return st
}

// wazuhConnection reads what the agent says about its own connection to the
// manager, in a state file it rewrites while it runs.
func (h *Host) wazuhConnection(service string) *serveragents.Connection {
	if service != "active" {
		return &serveragents.Connection{State: serveragents.ConnNotRunning}
	}
	fi, err := os.Stat(h.path(agentdStatePath))
	if err != nil {
		return &serveragents.Connection{State: serveragents.ConnNever}
	}
	b, _ := h.readFile(agentdStatePath)
	s := serveragents.ParseAgentdState(b)
	c := &serveragents.Connection{LastKeepalive: response.SanitizeOutput(s.LastKeepalive), LastAck: response.SanitizeOutput(s.LastAck)}
	switch {
	case h.Now().Sub(fi.ModTime()) > agentdStale:
		c.State = serveragents.ConnStale
	case s.Status == serveragents.ConnConnected, s.Status == serveragents.ConnPending, s.Status == serveragents.ConnDisconnected:
		c.State = s.Status
	default:
		c.State = serveragents.ConnUnknown
		c.Detail = response.SanitizeOutput(s.Status)
	}
	return c
}

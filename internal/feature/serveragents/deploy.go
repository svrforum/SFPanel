package featureserveragents

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/serveragents"
	"github.com/svrforum/SFPanel/internal/tasks"
)

func logger() *slog.Logger { return slog.With("component", "serveragents") }

// restartTimeout bounds systemctl enable and restart of an agent.
const restartTimeout = 60 * time.Second

// JobIO is what a deploy needs from the job running it.
type JobIO interface {
	Logf(format string, args ...any)
	// Apt runs one package transaction to its end, its output in the job's
	// log; nil when apt succeeded.
	Apt(ctx context.Context, kind string, names []string, update bool) error
}

// Outcome is how a deploy ended.
type Outcome struct {
	Status  string // tasks.JobSucceeded | tasks.JobFailed | tasks.JobCancelled
	Changed bool
	Summary string
}

// Deployer installs an agent on the node, points it at the cluster's
// servers, and waits until it really connects.
type Deployer struct {
	*Host
	// Wait is how long an agent has to connect; it is checked every Poll.
	// RegisterWait is how long in all a Zabbix agent whose server answers
	// that it does not know the host is waited for: agent 7.0.22 asks again
	// only a minute after a refusal, so a host its server registers at the
	// first request says it is taken just after Wait.
	Wait         time.Duration
	RegisterWait time.Duration
	Poll         time.Duration
	// Settle is how long a restarted Zabbix agent runs before it can count
	// as connected: until its first active check, its log has nothing to
	// say against it.
	Settle time.Duration
	Sleep  func(ctx context.Context, d time.Duration) error
}

// NewDeployer is the deployer of the agents on h.
func NewDeployer(h *Host) *Deployer {
	return &Deployer{Host: h, Wait: 60 * time.Second, RegisterWait: 150 * time.Second, Poll: 5 * time.Second, Settle: 10 * time.Second, Sleep: sleep}
}

// sleep waits for d, or until ctx is done.
func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Deploy runs step on this node: Skip first (a skip found here is a failure
// with its message), then the agent's half.
func (d *Deployer) Deploy(ctx context.Context, step tasks.AgentStep, io JobIO) Outcome {
	if ctx.Err() != nil {
		return cancelled(false, "reading the host")
	}
	if !slices.Contains(serveragents.Names, step.Name) {
		return failed(false, fmt.Sprintf("%q is not an agent this panel deploys", step.Name))
	}
	if step.Settings == nil || !step.Settings.Configured(step.Name) {
		return failed(false, "the cluster has no "+step.Name+" connection values")
	}
	if s := d.Skip(step.Name, step.Settings); s != nil {
		io.Logf("%s", s.Message)
		return failed(false, s.Message)
	}
	switch step.Name {
	case serveragents.Zabbix:
		return d.deployZabbix(ctx, step, io)
	case serveragents.Wazuh:
		return d.deployWazuh(ctx, step, io)
	}
	return failed(false, "deploying "+step.Name+" is not supported by this node")
}

func failed(changed bool, summary string) Outcome {
	return Outcome{Status: tasks.JobFailed, Changed: changed, Summary: summary}
}

// cancelled is the outcome of a deploy stopped before stage.
func cancelled(changed bool, stage string) Outcome {
	return Outcome{Status: tasks.JobCancelled, Changed: changed, Summary: "cancelled before " + stage}
}

// cancelledWhile is the outcome of a deploy stopped in the middle of stage.
func cancelledWhile(changed bool, stage string) Outcome {
	return Outcome{Status: tasks.JobCancelled, Changed: changed, Summary: "cancelled while " + stage}
}

// restart enables an agent's unit and restarts it. Neither command takes the
// job's context: a cancel that lands after the config is written must not
// leave the agent running with its old values.
func (d *Deployer) restart(unit string, io JobIO) error {
	for _, verb := range []string{"enable", "restart"} {
		io.Logf("systemctl %s %s", verb, unit)
		if out, err := d.Cmd.RunWithTimeout(restartTimeout, "systemctl", verb, unit); err != nil {
			detail := response.SanitizeOutput(out)
			if detail == "" {
				detail = response.SanitizeOutput(err.Error())
			}
			return fmt.Errorf("systemctl %s %s failed: %s", verb, unit, detail)
		}
	}
	return nil
}

// mustRestart reports whether a deploy restarts unit: when it changed
// anything, when the service is not active, or when the agent is not
// connected now — a run that died between writing the config and the
// restart leaves an agent that is active, already set, and still talking to
// the server it had before. A service starting or reloading is waited for,
// not restarted. When nothing changed it logs which it is.
func (d *Deployer) mustRestart(unit string, changed bool, io JobIO, connection func(service string) *serveragents.Connection) bool {
	if changed {
		return true
	}
	service := d.service(unit)
	if transitional(service) {
		io.Logf("%s is %s; it is not restarted, but waited for", unit, service)
		return false
	}
	if service != "active" {
		io.Logf("%s is %s; it is restarted", unit, service)
		return true
	}
	if c := connection(service); c.State != serveragents.ConnConnected {
		io.Logf("%s is active but not connected (%s); it is restarted", unit, describeConnection(c))
		return true
	}
	io.Logf("%s is active, connected, and nothing changed; it is not restarted", unit)
	return false
}

// transitionalGrace is how long past Wait an agent whose service is still
// starting or reloading is waited for.
const transitionalGrace = 30 * time.Second

// waitConnected checks the agent every Poll until check says connected or
// Wait has gone by, and returns the last answer and how long it waited.
// After a restart it waits a Poll before the first check, and a connected
// answer counts only once the agent has run settle. Past Wait, an agent
// whose service is starting or reloading gets up to transitionalGrace
// more, and one whose server answered that it does not know the host is
// waited for until RegisterWait in all, for the agent's next retry. err is
// the context's, when it ended the wait.
func (d *Deployer) waitConnected(ctx context.Context, io JobIO, restarted bool, settle time.Duration, check func(context.Context) *serveragents.Connection) (last *serveragents.Connection, waited time.Duration, ok bool, err error) {
	start := d.Now()
	if restarted {
		if err := d.Sleep(ctx, d.Poll); err != nil {
			return nil, d.Now().Sub(start), false, err
		}
	}
	var toldRetry bool
	for {
		c := check(ctx)
		elapsed := d.Now().Sub(start)
		if c.State == serveragents.ConnConnected && (!restarted || elapsed >= settle) {
			return c, elapsed, true, nil
		}
		if c.State == serveragents.ConnConnected {
			io.Logf("%s: connected; it counts once the agent has run %s", elapsed, settle)
		} else {
			io.Logf("%s: %s", elapsed, describeConnection(c))
		}
		if elapsed >= d.Wait {
			switch {
			case c.Transitional && elapsed < d.Wait+transitionalGrace:
			case c.State == serveragents.ConnNotRegistered && elapsed < d.RegisterWait:
				if !toldRetry {
					io.Logf("the server has not registered this host yet — waiting for the agent's next retry")
					toldRetry = true
				}
			default:
				return c, elapsed, false, nil
			}
		}
		if err := d.Sleep(ctx, d.Poll); err != nil {
			return c, d.Now().Sub(start), false, err
		}
	}
}

// notConnected is the summary of a deploy whose agent did not connect in
// the time it was waited for.
func notConnected(waited time.Duration, c *serveragents.Connection) string {
	return fmt.Sprintf("not connected after %s: %s", waited.Round(time.Second), describeConnection(c))
}

// describeConnection is a connection's state, and its detail in brackets.
func describeConnection(c *serveragents.Connection) string {
	if c.Detail == "" {
		return c.State
	}
	return c.State + " (" + c.Detail + ")"
}

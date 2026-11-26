package taskjob

// An agent step is the server-agents module's deploy of Zabbix Agent 2 or
// the Wazuh agent, with the connection values the leader filled in. Its apt
// is the packages module's transaction, as a packages step's is. The job
// records no unit: a panel restart ends it interrupted, and every part of a
// deploy is safe to run again.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/feature/packages"
	featureserveragents "github.com/svrforum/SFPanel/internal/feature/serveragents"
	"github.com/svrforum/SFPanel/internal/serveragents"
	"github.com/svrforum/SFPanel/internal/tasks"
)

// AgentDeployer is the server-agents module (*featureserveragents.Deployer).
type AgentDeployer interface {
	Skip(agent string, s *serveragents.Settings) *featureserveragents.Skip
	Deploy(ctx context.Context, step tasks.AgentStep, io featureserveragents.JobIO) featureserveragents.Outcome
}

// AgentRunner runs agent steps.
type AgentRunner struct {
	Agents   AgentDeployer
	Packages PackageTransactions
	// poll is how often a running package transaction is asked after.
	poll time.Duration
}

// NewAgentRunner returns the runner of agent steps.
func NewAgentRunner(a AgentDeployer, p PackageTransactions) *AgentRunner {
	return &AgentRunner{Agents: a, Packages: p, poll: time.Second}
}

// Check is the verdict Refuse gives, before a run.
func (r *AgentRunner) Check(spec tasks.JobSpec) tasks.CheckVerdict {
	if ref := r.Refuse(spec); ref != nil {
		return tasks.CheckVerdict{Refused: true, Code: ref.Code, Reason: ref.Message, Details: ref.Details}
	}
	return tasks.CheckVerdict{}
}

// Refuse turns a step away before it is recorded: 400 for one this node
// cannot deploy as sent — an agent it does not know, no values for it, or a
// value that is not one, which could add a line to the agent's config file —
// and 409 SERVER_AGENT_SKIPPED, with why in the details, when the node leaves
// the agent alone.
func (r *AgentRunner) Refuse(spec tasks.JobSpec) *Refusal {
	if msg := invalidAgentStep(spec.Agent); msg != "" {
		return &Refusal{Status: http.StatusBadRequest, Code: response.ErrInvalidRequest, Message: msg}
	}
	step := spec.Agent
	if s := r.Agents.Skip(step.Name, step.Settings); s != nil {
		return &Refusal{Status: http.StatusConflict, Code: response.ErrServerAgentSkipped, Message: s.Message, Details: s.Details}
	}
	return nil
}

// invalidAgentStep says what makes step one this node cannot deploy, or "".
func invalidAgentStep(step *tasks.AgentStep) string {
	switch {
	case step == nil:
		return "The job has no agent settings"
	case !slices.Contains(serveragents.Names, step.Name):
		return strconv.Quote(step.Name) + " is not an agent this panel deploys"
	case step.Settings == nil:
		return "The step carries no connection values"
	}
	if err := step.Settings.Validate(); err != nil {
		return "Invalid connection values: " + err.Error()
	}
	if !step.Settings.Configured(step.Name) {
		return "The cluster has no " + step.Name + " connection values"
	}
	return ""
}

// Start runs the deploy and records how it ended.
func (r *AgentRunner) Start(ctx context.Context, job *Job) error {
	step := *job.Spec.Agent
	out := r.Agents.Deploy(ctx, step, &agentJobIO{r: r, job: job, agent: step.Name})
	return job.Finish(Result{Status: out.Status, Changed: out.Changed, Summary: out.Summary})
}

// agentJobIO is the job, as a deploy uses it.
type agentJobIO struct {
	r     *AgentRunner
	job   *Job
	agent string
}

// Logf writes a line to the job's output under the agent's name.
func (a *agentJobIO) Logf(format string, args ...any) {
	appendLog(a.job, "["+a.agent+"] "+fmt.Sprintf(format, args...))
}

// Apt runs one package transaction to its end, its output copied to the
// job's output. A forced cancel does not cut it off — apt stopped half way
// can leave dpkg half-configured — but once it has ended, it is an error.
func (a *agentJobIO) Apt(ctx context.Context, kind string, names []string, update bool) error {
	tx, err := a.r.Packages.StartJobTransaction(kind, names, update)
	if err != nil {
		var refusal *packages.Refusal
		if errors.As(err, &refusal) {
			return fmt.Errorf("%s: %s", refusal.Code, refusal.Message)
		}
		return err
	}
	follow := &PackageRunner{Packages: a.r.Packages, poll: a.r.poll}
	copied := int64(0)
	result, code, cancelled, err := follow.wait(ctx, a.job, pkgPhase{kind: kind, names: names}, tx, &copied)
	switch {
	case err != nil:
		return fmt.Errorf("the %s transaction's record is gone", kind)
	case cancelled:
		return fmt.Errorf("cancelled; the %s transaction ran to its end (%s)", kind, result)
	case result == "success":
		return nil
	case result == "failed":
		return fmt.Errorf("%s failed with exit status %d", kind, code)
	}
	return fmt.Errorf("the %s transaction stopped without saying how it ended", kind)
}

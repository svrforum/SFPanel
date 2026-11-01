package featuretasks

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/svrforum/SFPanel/internal/cluster"
	"github.com/svrforum/SFPanel/internal/tasks"
)

// MaxTargets caps the nodes one run takes, counted before any is left out.
const MaxTargets = 200

// ErrTooManyTargets is a run whose targets come to more than MaxTargets
// nodes (TASK_TOO_MANY_TARGETS).
var ErrTooManyTargets = fmt.Errorf("a task run takes at most %d nodes", MaxTargets)

// groupLabel is the node label that puts a node in a group: one group per
// node, as the node list shows it.
const groupLabel = "group"

// Why a target is left out of a run.
const (
	LeftUnknown     = "unknown"     // no node has the ID the run names
	LeftOffline     = "offline"     // offline by the leader's live health
	LeftOutdated    = "outdated"    // its code does not run task jobs (no CapTasks): update it
	LeftUnreachable = "unreachable" // an agent this leader's probe does not reach
	LeftBusy        = "busy"        // another run under way has it; RunID names that run
)

// checkTimeout is how long the plan waits on one node's verdicts, and
// checkParallel how many nodes it asks at once.
var (
	checkTimeout  = 10 * time.Second
	checkParallel = 16
)

// PlanNode is one target of a run as the plan sees it.
type PlanNode struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Kind   string `json:"kind"` // manager | agent
	Leader bool   `json:"leader,omitempty"`
	// Reason says why the node is left out (Left*), and RunID the run that
	// holds a busy one.
	Reason string `json:"reason,omitempty"`
	RunID  string `json:"run_id,omitempty"`
	// Verdicts are the node's own verdicts on the steps — refused outright,
	// or allowed once confirmed — and CheckError why it gave none.
	Verdicts   []tasks.CheckVerdict `json:"verdicts,omitempty"`
	CheckError string               `json:"check_error,omitempty"`
}

// Plan is the order a run takes its targets in, and the targets it leaves
// out with why.
type Plan struct {
	// Waves run one after another: the agents, batch at a time; then each
	// other manager alone; then the leader alone.
	Waves [][]PlanNode `json:"waves"`
	Left  []PlanNode   `json:"left,omitempty"`
	// PlanID names what the preview showed: the document, the inputs and
	// the targets (planID). A start that confirms steps sends it back.
	PlanID string `json:"plan_id"`
	// ManagersNotReady are the managers a start would wait for, each with
	// why (TasksReady): a start is refused until there are none.
	ManagersNotReady []string `json:"managers_not_ready,omitempty"`
	// Steps are the steps of the document previewed, which the verdicts
	// name by index: the task may have changed since the screen read it.
	Steps []runStep `json:"steps"`
}

// WaveIDs is the plan's waves as node IDs, as a run record keeps them.
func (p Plan) WaveIDs() [][]string {
	out := make([][]string, 0, len(p.Waves))
	for _, w := range p.Waves {
		ids := make([]string, len(w))
		for i, n := range w {
			ids[i] = n.ID
		}
		out = append(out, ids)
	}
	return out
}

// NodeIDs is every node the plan runs, in order.
func (p Plan) NodeIDs() []string {
	var out []string
	for _, w := range p.WaveIDs() {
		out = append(out, w...)
	}
	return out
}

// planRun orders a run of t: the targets are the union of the nodes in
// targets' groups, of its kinds and the nodes it names — at most MaxTargets.
// A target is left out when it is offline by live health, runs code without
// task jobs, is an agent this leader cannot reach, or is held by another run
// under way. The rest go agents first, batch at a time, so a bad step stops
// on agents before any manager; then the other managers one at a time; then
// this leader. Each group goes by name, then ID, so a run takes the same
// order every time. Every node is asked for its verdicts on the steps,
// filled in for it, under username, the operator who asked; a required
// input left out is an error (tasks.ErrInputMissing).
func planRun(env runEnv, t *tasks.Task, targets tasks.Targets, inputs map[string]string, batch int, username string) (Plan, error) {
	plan, byID, err := orderRun(env, targets, runBatch(t, batch))
	if err != nil {
		return Plan{}, err
	}
	if err := checkPlan(env, t, inputs, byID, plan, username); err != nil {
		return Plan{}, err
	}
	return plan, nil
}

// runBatch is how many agents a run of t takes at once: batch when the
// operator gave one, else the task's, else 1.
func runBatch(t *tasks.Task, batch int) int {
	if batch < 1 {
		batch = t.Batch
	}
	if batch < 1 {
		batch = 1
	}
	return batch
}

// orderRun is planRun's order without the nodes' verdicts: the targets in
// their waves, and the ones left out with why. It asks no node, so starting
// a run stays well inside the thirty seconds a follower's forward of it
// gets; only the preview asks. byID is every node of the cluster.
func orderRun(env runEnv, targets tasks.Targets, batch int) (plan Plan, byID map[string]cluster.Node, err error) {
	localID := env.LocalID()
	byID = map[string]cluster.Node{}
	var chosen []cluster.Node
	for _, n := range env.Nodes() {
		byID[n.ID] = n
		if targeted(n, targets) {
			chosen = append(chosen, n)
		}
	}
	for _, id := range targets.Nodes {
		if _, ok := byID[id]; !ok {
			plan.Left = append(plan.Left, PlanNode{ID: id, Name: id, Reason: LeftUnknown})
		}
	}
	if len(chosen) > MaxTargets {
		return Plan{}, nil, ErrTooManyTargets
	}

	held, err := heldNodes(env.Store())
	if err != nil {
		return Plan{}, nil, err
	}
	health := env.Health()
	var agents, managers []PlanNode
	var leader *PlanNode
	for _, n := range chosen {
		pn := PlanNode{ID: n.ID, Name: n.Name, Kind: kindOf(n), Leader: n.ID == localID}
		switch {
		case health[n.ID] != cluster.StatusOnline:
			pn.Reason = LeftOffline
		case !env.Capable(n.ID):
			pn.Reason = LeftOutdated
		case env.Unreachable(n.ID):
			pn.Reason = LeftUnreachable
		case held[n.ID] != "":
			pn.Reason, pn.RunID = LeftBusy, held[n.ID]
		}
		switch {
		case pn.Reason != "":
			plan.Left = append(plan.Left, pn)
		case pn.Leader:
			leader = &pn
		case n.IsManager():
			managers = append(managers, pn)
		default:
			agents = append(agents, pn)
		}
	}
	sortPlanNodes(agents)
	sortPlanNodes(managers)
	sortPlanNodes(plan.Left)

	for len(agents) > 0 {
		k := min(batch, len(agents))
		plan.Waves = append(plan.Waves, agents[:k])
		agents = agents[k:]
	}
	for _, m := range managers {
		plan.Waves = append(plan.Waves, []PlanNode{m})
	}
	if leader != nil {
		plan.Waves = append(plan.Waves, []PlanNode{*leader})
	}
	return plan, byID, nil
}

// targeted reports whether n is in one of the targets' groups, of its
// kinds, or among the nodes it names.
func targeted(n cluster.Node, t tasks.Targets) bool {
	if g := n.Labels[groupLabel]; g != "" && slices.Contains(t.Groups, g) {
		return true
	}
	return slices.Contains(t.Kinds, kindOf(n)) || slices.Contains(t.Nodes, n.ID)
}

func kindOf(n cluster.Node) string {
	if n.IsManager() {
		return string(cluster.KindManager)
	}
	return string(cluster.KindAgent)
}

// heldNodes maps each node a run under way takes to that run's ID.
func heldNodes(st runStore) (map[string]string, error) {
	runs, err := st.ListRuns()
	if err != nil {
		return nil, err
	}
	held := map[string]string{}
	for _, r := range runs {
		if !r.Active() {
			continue
		}
		for _, id := range r.Nodes {
			held[id] = r.ID
		}
	}
	return held, nil
}

func sortPlanNodes(s []PlanNode) {
	sort.Slice(s, func(i, j int) bool {
		if s[i].Name != s[j].Name {
			return s[i].Name < s[j].Name
		}
		return s[i].ID < s[j].ID
	})
}

// checkPlan fills in every planned node's verdicts, asking up to
// checkParallel nodes at once, under the name of the operator who asked for
// the preview. The steps are rendered for each node first:
// a missing input fails the whole plan, before any node is asked.
func checkPlan(env runEnv, t *tasks.Task, inputs map[string]string, byID map[string]cluster.Node, plan Plan, username string) error {
	jobs, err := renderPlan(t, inputs, byID, plan)
	if err != nil {
		return err
	}
	sem := make(chan struct{}, checkParallel)
	var wg sync.WaitGroup
	for _, j := range jobs {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			ctx, cancel := context.WithTimeout(context.Background(), checkTimeout)
			defer cancel()
			v, err := env.Check(ctx, j.node.ID, j.specs, username)
			if err != nil {
				j.node.CheckError = err.Error()
				return
			}
			j.node.Verdicts = v
		}()
	}
	wg.Wait()
	return nil
}

// planJob is one planned node with its steps filled in for it.
type planJob struct {
	node  *PlanNode
	specs []tasks.JobSpec
}

// renderPlan fills the steps in for every node the plan runs: a required
// input left out fails it (tasks.ErrInputMissing) before any node is asked
// or any run recorded.
func renderPlan(t *tasks.Task, inputs map[string]string, byID map[string]cluster.Node, plan Plan) ([]planJob, error) {
	var jobs []planJob
	for _, w := range plan.Waves {
		for i := range w {
			specs, err := tasks.Render(t, inputs, nodeVars(byID[w[i].ID]))
			if err != nil {
				return nil, err
			}
			jobs = append(jobs, planJob{node: &w[i], specs: specs})
		}
	}
	return jobs, nil
}

// nodeVars are the values {{ node.name }} and {{ node.address }} take on n:
// its name, and the address the cluster reaches it at first.
func nodeVars(n cluster.Node) tasks.NodeVars {
	v := tasks.NodeVars{Name: n.Name}
	if addrs := n.AllAddresses(); len(addrs) > 0 {
		v.Address = addrs[0]
	}
	return v
}

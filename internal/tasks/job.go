package tasks

import "time"

// Job statuses, as a node reports them.
const (
	JobRunning     = "running"
	JobSucceeded   = "succeeded"
	JobFailed      = "failed"
	JobInterrupted = "interrupted" // the panel stopped and the result is not known
	JobCancelled   = "cancelled"
	JobRebooting   = "rebooting" // a reboot is scheduled; done once the boot ID changes
)

// JobSpec is one step of one run, filled in for one node: what the leader
// sends to POST /api/v1/task-jobs. Exactly the pointer Kind names is set.
type JobSpec struct {
	ID    string `json:"id"`
	RunID string `json:"run_id"`
	Task  string `json:"task"`
	// Step is the step's index in Task.Steps, counted from 0.
	Step int    `json:"step"`
	Kind string `json:"kind"`
	// Confirmed is set when the operator ticked this step in the preview,
	// letting a guard that asks for confirmation through.
	Confirmed bool          `json:"confirmed,omitempty"`
	Packages  *PackagesStep `json:"packages,omitempty"`
	Service   *ServiceStep  `json:"service,omitempty"`
	Command   *CommandStep  `json:"command,omitempty"`
	File      *FileStep     `json:"file,omitempty"`
	Stack     *StackStep    `json:"stack,omitempty"`
	Reboot    *RebootStep   `json:"reboot,omitempty"`
}

// JobView is a node's answer about one job: its state and a piece of its
// output starting at the requested offset.
type JobView struct {
	ID       string `json:"id"`
	RunID    string `json:"run_id"`
	Kind     string `json:"kind"`
	Status   string `json:"status"`
	Changed  bool   `json:"changed"`
	ExitCode *int   `json:"exit_code,omitempty"`
	Summary  string `json:"summary,omitempty"`
	Output   string `json:"output,omitempty"`
	// NextSkip is the offset to ask from next; Done says the output is
	// complete and NextSkip is its end.
	NextSkip  int64      `json:"next_skip"`
	Done      bool       `json:"done"`
	StartedAt time.Time  `json:"started_at"`
	EndedAt   time.Time  `json:"ended_at,omitzero"`
	Holder    *JobHolder `json:"holder,omitempty"`
}

// JobHolder names the job a node is busy with when it turns another away.
type JobHolder struct {
	JobID string `json:"job_id"`
	RunID string `json:"run_id"`
	Task  string `json:"task"`
}

// CheckVerdict is a node's cheap verdict on one step before a run: refused
// outright, or allowed only once the operator confirms it.
type CheckVerdict struct {
	Step         int    `json:"step"`
	Refused      bool   `json:"refused,omitempty"`
	Code         string `json:"code,omitempty"`
	Reason       string `json:"reason,omitempty"`
	NeedsConfirm bool   `json:"needs_confirm,omitempty"`
}

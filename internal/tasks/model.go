// Package tasks is the group-task model: a task is one YAML document of
// steps, read with line numbers, checked in full, and filled in for each node
// it runs on. It is pure code — no commands, no cluster — so the leader that
// plans a run and the node that executes one step share a single definition.
package tasks

import "time"

// Limits a task document is held to.
const (
	// MaxDocumentBytes caps a task document. Task definitions live in the
	// replicated config map, so this is also what one task costs every
	// manager.
	MaxDocumentBytes = 64 << 10
	// MaxSteps caps the steps in one task.
	MaxSteps = 50
)

// Defaults for values a step may leave out.
const (
	DefaultCommandTimeout = 10 * time.Minute
	DefaultRebootWait     = 10 * time.Minute
)

// Step kinds. A step has exactly one.
const (
	StepPackages = "packages"
	StepService  = "service"
	StepCommand  = "command"
	StepFile     = "file"
	StepStack    = "stack"
	StepReboot   = "reboot"
)

// stepKinds is the order kinds are listed in messages.
var stepKinds = []string{StepPackages, StepService, StepCommand, StepFile, StepStack, StepReboot}

// Service actions a service step may take.
var serviceActions = []string{"start", "stop", "restart", "enable", "disable"}

// Node kinds a task may target, as the cluster names them.
var nodeKinds = []string{"manager", "agent"}

// Task is a parsed, checked task document.
type Task struct {
	Name        string  `json:"name"`
	Description string  `json:"description,omitempty"`
	Inputs      []Input `json:"inputs,omitempty"` // in document order
	Targets     Targets `json:"targets"`
	// Batch is how many agents run at once. Managers always run one at a
	// time. Defaults to 1.
	Batch int `json:"batch"`
	// StopOnFailure stops a run from starting new batches once a node has
	// failed. Defaults to true.
	StopOnFailure bool   `json:"stop_on_failure"`
	Steps         []Step `json:"steps"`
}

// Input is a value asked for when the task is run, referenced in steps as
// {{ name }}.
type Input struct {
	Name     string `json:"name"`
	Label    string `json:"label,omitempty"`
	Default  string `json:"default,omitempty"`
	Required bool   `json:"required,omitempty"`
	// Raw inputs go into commands as typed, not shell-quoted. The built-in
	// "run a command" task needs this to take a whole command line.
	Raw bool `json:"raw,omitempty"`
}

// Targets are a task's default targets, which a run may change.
type Targets struct {
	Groups []string `json:"groups,omitempty"`
	Kinds  []string `json:"kinds,omitempty"`
	Nodes  []string `json:"nodes,omitempty"`
}

// Step is one step of a task. Kind names the one pointer that is set.
type Step struct {
	Name            string        `json:"name,omitempty"`
	ContinueOnError bool          `json:"continue_on_error,omitempty"`
	Kind            string        `json:"kind"`
	Packages        *PackagesStep `json:"packages,omitempty"`
	Service         *ServiceStep  `json:"service,omitempty"`
	Command         *CommandStep  `json:"command,omitempty"`
	File            *FileStep     `json:"file,omitempty"`
	Stack           *StackStep    `json:"stack,omitempty"`
	Reboot          *RebootStep   `json:"reboot,omitempty"`
}

// PackagesStep refreshes the package lists and/or makes one change: upgrade,
// install or remove. When Update is set it runs first.
type PackagesStep struct {
	Update bool `json:"update,omitempty"`
	// UpgradeAll is `upgrade: all`; Upgrade is `upgrade: [names…]`.
	UpgradeAll bool     `json:"upgrade_all,omitempty"`
	Upgrade    []string `json:"upgrade,omitempty"`
	Install    []string `json:"install,omitempty"`
	Remove     []string `json:"remove,omitempty"`
}

// ServiceStep runs one systemctl action on a unit.
type ServiceStep struct {
	Name   string `json:"name"`
	Action string `json:"action"` // start|stop|restart|enable|disable
}

// CommandStep runs a one-line command or a script, as root, under a time
// limit. Exactly one of Run and Script is set.
type CommandStep struct {
	Run     string        `json:"run,omitempty"`
	Script  string        `json:"script,omitempty"`
	Timeout time.Duration `json:"timeout"`
}

// FileStep writes a file. Mode and Owner are optional; empty leaves the
// node's choice.
type FileStep struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Mode    string `json:"mode,omitempty"`  // three or four octal digits
	Owner   string `json:"owner,omitempty"` // user or user:group
}

// StackStep creates or updates a compose stack and, with Up, brings it up.
type StackStep struct {
	Name    string `json:"name"`
	Compose string `json:"compose"`
	Up      bool   `json:"up,omitempty"`
}

// RebootStep reboots the node and waits for it to come back.
type RebootStep struct {
	OnlyIfRequired bool          `json:"only_if_required"`
	Wait           time.Duration `json:"wait"`
}

// Problem is one thing wrong with a task document. Line counts from 1; 0
// means the document as a whole.
type Problem struct {
	Line    int    `json:"line"`
	Message string `json:"message"`
}

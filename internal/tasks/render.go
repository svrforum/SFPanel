package tasks

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/svrforum/SFPanel/internal/serveragents"
)

// ErrInputMissing is a required input that was neither given nor defaulted.
// The error Render returns wraps it in an *InputMissingError naming the input.
var ErrInputMissing = errors.New("a required input is missing")

// InputMissingError names the required input a run did not supply.
type InputMissingError struct {
	Name string
}

func (e *InputMissingError) Error() string {
	return fmt.Sprintf("input %q is required", e.Name)
}

// Is makes errors.Is(err, ErrInputMissing) hold.
func (e *InputMissingError) Is(target error) bool { return target == ErrInputMissing }

// NodeVars are the per-node values a step may reference as {{ node.name }}
// and {{ node.address }}.
type NodeVars struct {
	Name    string
	Address string
}

// ShellQuote makes s one POSIX shell word: wrapped in single quotes, with
// each single quote inside closed, escaped and reopened:
//
//	x'y  becomes  'x'\''y'
//
// Nothing inside single quotes is special to the shell, so this holds for
// any input.
func ShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Render fills a parsed task in for one node: inputs with their defaults
// applied, then node variables. A value going into a command step is
// shell-quoted unless its input is raw; every other step gets the value as
// typed, because the node's own guards — the same ones its endpoints use —
// are what judge a path, a unit or a package name. An agent step gets the
// node's name and no connection values; FillAgentSettings adds those. The
// returned specs carry no job or run ID; the leader assigns those.
func Render(t *Task, inputs map[string]string, node NodeVars) ([]JobSpec, error) {
	r := renderer{values: map[string]string{}, raw: map[string]bool{}}
	for _, in := range t.Inputs {
		v := inputs[in.Name]
		if v == "" {
			v = in.Default
		}
		if v == "" && in.Required {
			return nil, &InputMissingError{Name: in.Name}
		}
		r.values[in.Name] = v
		r.raw[in.Name] = in.Raw
	}
	r.values[refNodeName] = node.Name
	r.values[refNodeAddress] = node.Address

	specs := make([]JobSpec, 0, len(t.Steps))
	for i, s := range t.Steps {
		spec := JobSpec{Task: t.Name, Step: i, Kind: s.Kind}
		switch s.Kind {
		case StepPackages:
			p := *s.Packages
			p.Upgrade = r.packageList(p.Upgrade)
			p.Install = r.packageList(p.Install)
			p.Remove = r.packageList(p.Remove)
			spec.Packages = &p
		case StepService:
			sv := *s.Service
			sv.Name = r.fill(sv.Name, false)
			spec.Service = &sv
		case StepCommand:
			c := *s.Command
			c.Run = r.fill(c.Run, true)
			c.Script = r.fill(c.Script, true)
			spec.Command = &c
		case StepFile:
			f := *s.File
			f.Path = r.fill(f.Path, false)
			f.Content = r.fill(f.Content, false)
			f.Owner = r.fill(f.Owner, false)
			spec.File = &f
		case StepStack:
			st := *s.Stack
			st.Name = r.fill(st.Name, false)
			st.Compose = r.fill(st.Compose, false)
			spec.Stack = &st
		case StepReboot:
			rb := *s.Reboot
			spec.Reboot = &rb
		case StepAgent:
			a := *s.Agent
			a.NodeName = node.Name
			a.Settings = nil
			spec.Agent = &a
		}
		if r.err != nil {
			return nil, r.err
		}
		specs = append(specs, spec)
	}
	return specs, nil
}

// ErrAgentSettingsMissing is an agent step whose agent has no saved
// connection values. FillAgentSettings returns it wrapped in an
// *AgentSettingsMissingError naming the agent.
var ErrAgentSettingsMissing = errors.New("the agent's connection values are not set")

// AgentSettingsMissingError names the agent a run would deploy with nothing
// to point it at.
type AgentSettingsMissingError struct {
	Agent string
}

func (e *AgentSettingsMissingError) Error() string {
	return fmt.Sprintf("the %s agent's connection values are not set", e.Agent)
}

// Is makes errors.Is(err, ErrAgentSettingsMissing) hold.
func (e *AgentSettingsMissingError) Is(target error) bool { return target == ErrAgentSettingsMissing }

// HasAgentSteps says whether any of a task's steps deploys an agent.
func HasAgentSteps(t *Task) bool {
	return t != nil && slices.ContainsFunc(t.Steps, func(s Step) bool { return s.Kind == StepAgent })
}

// FillAgentSettings gives each agent step in specs, as Render left them, the
// cluster's connection values s. Every agent is checked before any step is
// filled: one whose values are not set (or s nil, nothing saved) is refused,
// since an agent deployed without them points at nothing. Each step gets its
// own copy of *s, so no two specs, and not the caller, share one.
func FillAgentSettings(specs []JobSpec, s *serveragents.Settings) error {
	for _, spec := range specs {
		if spec.Agent != nil && (s == nil || !s.Configured(spec.Agent.Name)) {
			return &AgentSettingsMissingError{Agent: spec.Agent.Name}
		}
	}
	for i := range specs {
		if specs[i].Agent == nil {
			continue
		}
		a := *specs[i].Agent
		values := *s
		a.Settings = &values
		specs[i].Agent = &a
	}
	return nil
}

type renderer struct {
	values map[string]string
	raw    map[string]bool
	err    error
}

// fill replaces every reference in s. Parse has already refused references
// to anything undefined; one that turns up here means the task was not
// parsed, and is an error rather than silently empty.
func (r *renderer) fill(s string, quote bool) string {
	return referencePattern.ReplaceAllStringFunc(s, func(m string) string {
		name := referencePattern.FindStringSubmatch(m)[1]
		v, ok := r.values[name]
		if !ok {
			if r.err == nil {
				r.err = fmt.Errorf("the task references %q, which is not defined; parse the task before rendering it", name)
			}
			return m
		}
		if quote && !r.raw[name] {
			return ShellQuote(v)
		}
		return v
	})
}

// packageList fills package names in. An item that was a reference may
// hold several names — "htop curl" or "htop, curl" from one input — so it
// is split on spaces and commas, neither of which a package name can
// contain. Items that were literal names stay as they are.
func (r *renderer) packageList(items []string) []string {
	if items == nil {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, it := range items {
		if !hasRef(it) {
			out = append(out, it)
			continue
		}
		out = append(out, strings.FieldsFunc(r.fill(it, false), func(c rune) bool {
			return c == ',' || c == ' ' || c == '\t' || c == '\n'
		})...)
	}
	return out
}

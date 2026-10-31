package tasks

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// validPackageName is the packages module's rule, copied rather than
// imported so this package stays free of feature modules: an alphanumeric
// first character, so a name can never read as an apt-get flag.
var validPackageName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._+\-]*$`)

// validInputName is what an input may be called: something a reference can
// spell without ambiguity.
var validInputName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// referencePattern finds {{ name }} and {{ node.name }} references. Only an
// identifier, optionally dotted, counts: `{{.Names}}` and `{{json .}}` are Go
// templates for docker and similar tools and are left alone, so a command
// using them is not refused.
var referencePattern = regexp.MustCompile(`\{\{\s*([A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*)\s*\}\}`)

// Node variables a step may reference besides inputs.
const (
	refNodeName    = "node.name"
	refNodeAddress = "node.address"
)

// yamlLine pulls the line out of a yaml.v3 syntax error ("yaml: line 3: …").
var yamlLine = regexp.MustCompile(`line (\d+)`)

// Parse reads a task document and checks all of it, returning every problem
// it finds with its line rather than stopping at the first, so an editor can
// mark them all at once. When there are problems the task is nil.
func Parse(doc []byte) (*Task, []Problem) {
	if len(doc) > MaxDocumentBytes {
		return nil, []Problem{{Line: 0, Message: fmt.Sprintf("the document is %d bytes; a task is at most 64 KiB", len(doc))}}
	}

	// yaml.Node rather than a struct with KnownFields: decoding into a struct
	// stops at the first unknown key, and the node tree carries a line for
	// every value.
	var root yaml.Node
	dec := yaml.NewDecoder(bytes.NewReader(doc))
	if err := dec.Decode(&root); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, []Problem{{Line: 1, Message: "the document is empty"}}
		}
		return nil, []Problem{syntaxProblem(err)}
	}
	var extra yaml.Node
	switch err := dec.Decode(&extra); {
	case errors.Is(err, io.EOF):
	case err != nil:
		return nil, []Problem{syntaxProblem(err)}
	default:
		return nil, []Problem{{Line: extra.Line, Message: "a task is one YAML document; remove the second one"}}
	}

	p := &parser{inputs: map[string]bool{}}
	task := p.task(&root)
	if len(p.problems) > 0 {
		sort.SliceStable(p.problems, func(i, j int) bool { return p.problems[i].Line < p.problems[j].Line })
		return nil, p.problems
	}
	return task, nil
}

func syntaxProblem(err error) Problem {
	msg := strings.TrimPrefix(err.Error(), "yaml: ")
	line := 1
	if m := yamlLine.FindStringSubmatch(msg); m != nil {
		if n, convErr := strconv.Atoi(m[1]); convErr == nil {
			line = n
		}
	}
	return Problem{Line: line, Message: "not valid YAML: " + msg}
}

type parser struct {
	problems []Problem
	inputs   map[string]bool // defined input names
}

func (p *parser) addf(line int, format string, args ...any) {
	p.problems = append(p.problems, Problem{Line: line, Message: fmt.Sprintf(format, args...)})
}

// entry is one key of a mapping with its value.
type entry struct {
	key, val *yaml.Node
}

// mapping checks that n is a mapping and returns its keys in document order.
// Duplicate keys and aliases are reported; what to do with unknown keys is
// the caller's, since a step treats them differently from everything else.
func (p *parser) mapping(n *yaml.Node, what string) ([]entry, bool) {
	if n.Kind == yaml.AliasNode {
		p.addf(n.Line, "%s: aliases are not supported in a task", what)
		return nil, false
	}
	if n.Kind != yaml.MappingNode {
		p.addf(n.Line, "%s must be a mapping of keys to values", what)
		return nil, false
	}
	seen := map[string]bool{}
	var out []entry
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		if seen[k.Value] {
			p.addf(k.Line, "%s: %q appears twice", what, k.Value)
			continue
		}
		seen[k.Value] = true
		out = append(out, entry{key: k, val: v})
	}
	return out, true
}

// fields reads a mapping whose keys must all be in allowed, reporting the
// rest, and returns the known ones by name.
func (p *parser) fields(n *yaml.Node, what string, allowed ...string) (map[string]entry, bool) {
	entries, ok := p.mapping(n, what)
	if !ok {
		return nil, false
	}
	out := map[string]entry{}
	for _, e := range entries {
		if !slices.Contains(allowed, e.key.Value) {
			p.addf(e.key.Line, "unknown key %q in %s; expected one of %s", e.key.Value, what, strings.Join(allowed, ", "))
			continue
		}
		out[e.key.Value] = e
	}
	return out, true
}

// present says whether a key was given a value; `key:` with nothing after it
// counts as left out.
func present(f map[string]entry, key string) bool {
	e, ok := f[key]
	return ok && !isNull(e.val)
}

func isNull(n *yaml.Node) bool {
	return n.Kind == yaml.ScalarNode && n.Tag == "!!null"
}

// str reads a scalar as text. A missing or empty-null value reports
// "is required" when required.
func (p *parser) str(f map[string]entry, key, what string, required bool, parent *yaml.Node) (string, *yaml.Node, bool) {
	if !present(f, key) {
		if required {
			line := parent.Line
			if e, ok := f[key]; ok {
				line = e.key.Line
			}
			p.addf(line, "%q is required in %s", key, what)
		}
		return "", nil, false
	}
	v := f[key].val
	if v.Kind != yaml.ScalarNode {
		p.addf(v.Line, "%s %q must be a single value", what, key)
		return "", nil, false
	}
	return v.Value, v, true
}

func (p *parser) boolean(f map[string]entry, key, what string, def bool) bool {
	if !present(f, key) {
		return def
	}
	v := f[key].val
	var b bool
	if v.Kind != yaml.ScalarNode || v.Decode(&b) != nil {
		p.addf(v.Line, "%s %q must be true or false", what, key)
		return def
	}
	return b
}

func (p *parser) duration(f map[string]entry, key, what string, def time.Duration) time.Duration {
	if !present(f, key) {
		return def
	}
	v := f[key].val
	d, err := time.ParseDuration(v.Value)
	if v.Kind != yaml.ScalarNode || err != nil || d <= 0 {
		p.addf(v.Line, "%s %s %q is not a duration; write it like 30s, 10m or 1h", what, key, v.Value)
		return def
	}
	return d
}

// list reads a sequence of scalars.
func (p *parser) list(n *yaml.Node, what string) ([]*yaml.Node, bool) {
	if n.Kind != yaml.SequenceNode {
		p.addf(n.Line, "%s must be a list", what)
		return nil, false
	}
	ok := true
	for _, item := range n.Content {
		if item.Kind != yaml.ScalarNode || isNull(item) {
			p.addf(item.Line, "%s: each item must be a single value", what)
			ok = false
		}
	}
	return n.Content, ok
}

func values(items []*yaml.Node) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.Value)
	}
	return out
}

// refs checks every {{ … }} reference in a value against the defined inputs
// and node variables. In a literal block (script: |) each reference is
// reported on its own line rather than the block's first.
func (p *parser) refs(v *yaml.Node) {
	if v == nil {
		return
	}
	for _, m := range referencePattern.FindAllStringSubmatchIndex(v.Value, -1) {
		name := v.Value[m[2]:m[3]]
		if p.inputs[name] || name == refNodeName || name == refNodeAddress {
			continue
		}
		line := v.Line
		if v.Style&yaml.LiteralStyle != 0 {
			line = v.Line + 1 + strings.Count(v.Value[:m[0]], "\n")
		}
		if strings.HasPrefix(name, "node.") {
			p.addf(line, "%q is not a node variable; use node.name or node.address", name)
			continue
		}
		p.addf(line, "%q is not an input of this task; define it under inputs", name)
	}
}

func hasRef(s string) bool { return referencePattern.MatchString(s) }

func (p *parser) task(root *yaml.Node) *Task {
	doc := root
	if doc.Kind == yaml.DocumentNode && len(doc.Content) == 1 {
		doc = doc.Content[0]
	}
	f, ok := p.fields(doc, "the task", "name", "description", "inputs", "targets", "batch", "stop_on_failure", "steps")
	if !ok {
		return nil
	}
	t := &Task{Batch: 1, StopOnFailure: true}

	t.Name, _, _ = p.str(f, "name", "the task", true, doc)
	if t.Name != "" && strings.TrimSpace(t.Name) == "" {
		p.addf(f["name"].val.Line, "the task's name is blank")
	}
	t.Description, _, _ = p.str(f, "description", "the task", false, doc)

	// Inputs first, wherever they sit in the document: the steps' references
	// are checked against them.
	if present(f, "inputs") {
		t.Inputs = p.inputList(f["inputs"].val)
	}
	if present(f, "targets") {
		t.Targets = p.targets(f["targets"].val)
	}
	if present(f, "batch") {
		v := f["batch"].val
		if v.Kind != yaml.ScalarNode || v.Decode(&t.Batch) != nil || t.Batch < 1 {
			p.addf(v.Line, "batch must be a whole number of 1 or more")
			t.Batch = 1
		}
	}
	t.StopOnFailure = p.boolean(f, "stop_on_failure", "the task", true)

	if !present(f, "steps") {
		line := doc.Line
		if e, ok := f["steps"]; ok {
			line = e.key.Line
		}
		p.addf(line, "%q is required in the task", "steps")
		return t
	}
	steps := f["steps"]
	if steps.val.Kind != yaml.SequenceNode {
		p.addf(steps.val.Line, "steps must be a list")
		return t
	}
	if len(steps.val.Content) == 0 {
		p.addf(steps.key.Line, "a task needs at least one step")
	}
	if len(steps.val.Content) > MaxSteps {
		p.addf(steps.key.Line, "the task has %d steps; a task has at most %d steps", len(steps.val.Content), MaxSteps)
	}
	for i, n := range steps.val.Content {
		if s, ok := p.step(n, i); ok {
			t.Steps = append(t.Steps, s)
		}
	}
	return t
}

func (p *parser) inputList(n *yaml.Node) []Input {
	entries, ok := p.mapping(n, "inputs")
	if !ok {
		return nil
	}
	var out []Input
	for _, e := range entries {
		name := e.key.Value
		what := fmt.Sprintf("input %q", name)
		switch {
		case !validInputName.MatchString(name):
			p.addf(e.key.Line, "input name %q must be letters, digits and _ and not start with a digit", name)
			continue
		case name == "node":
			p.addf(e.key.Line, "an input cannot be called %q; node.name and node.address are already taken", name)
			continue
		}
		p.inputs[name] = true
		in := Input{Name: name}
		if isNull(e.val) {
			out = append(out, in)
			continue
		}
		f, ok := p.fields(e.val, what, "label", "default", "required", "raw")
		if !ok {
			continue
		}
		in.Label, _, _ = p.str(f, "label", what, false, e.val)
		in.Default, _, _ = p.str(f, "default", what, false, e.val)
		in.Required = p.boolean(f, "required", what, false)
		in.Raw = p.boolean(f, "raw", what, false)
		out = append(out, in)
	}
	return out
}

func (p *parser) targets(n *yaml.Node) Targets {
	var t Targets
	f, ok := p.fields(n, "targets", "groups", "kinds", "nodes")
	if !ok {
		return t
	}
	read := func(key string) []string {
		if !present(f, key) {
			return nil
		}
		items, _ := p.list(f[key].val, "targets "+key)
		return values(items)
	}
	t.Groups = read("groups")
	t.Nodes = read("nodes")
	if present(f, "kinds") {
		items, _ := p.list(f["kinds"].val, "targets kinds")
		for _, it := range items {
			if !slices.Contains(nodeKinds, it.Value) {
				p.addf(it.Line, "node kind %q is not one of %s", it.Value, strings.Join(nodeKinds, ", "))
			}
		}
		t.Kinds = values(items)
	}
	return t
}

// step reads one step: the common keys plus exactly one kind.
func (p *parser) step(n *yaml.Node, index int) (Step, bool) {
	what := fmt.Sprintf("step %d", index+1)
	entries, ok := p.mapping(n, what)
	if !ok {
		return Step{}, false
	}
	var s Step
	common := map[string]entry{}
	var kind *entry
	for i, e := range entries {
		switch k := e.key.Value; {
		case k == "name" || k == "continue_on_error":
			common[k] = e
		case slices.Contains(stepKinds, k):
			if kind != nil {
				p.addf(e.key.Line, "a step takes exactly one kind; this one has %q and %q", kind.key.Value, k)
				continue
			}
			kind = &entries[i]
		case e.val.Kind == yaml.MappingNode:
			p.addf(e.key.Line, "unknown step kind %q; a step is one of %s", k, strings.Join(stepKinds, ", "))
		default:
			p.addf(e.key.Line, "unknown key %q in %s; expected name, continue_on_error or a step kind (%s)", k, what, strings.Join(stepKinds, ", "))
		}
	}
	s.Name, _, _ = p.str(common, "name", what, false, n)
	s.ContinueOnError = p.boolean(common, "continue_on_error", what, false)
	if kind == nil {
		// An unknown kind has been reported already; only a step with no
		// kind-shaped key at all needs saying.
		reported := false
		for _, e := range entries {
			if _, isCommon := common[e.key.Value]; !isCommon {
				reported = true
			}
		}
		if !reported {
			p.addf(n.Line, "%s has no kind; a step is one of %s", what, strings.Join(stepKinds, ", "))
		}
		return Step{}, false
	}

	s.Kind = kind.key.Value
	body := kind.val
	if isNull(body) {
		// `reboot:` with nothing after it means all the defaults.
		body = &yaml.Node{Kind: yaml.MappingNode, Line: kind.key.Line}
	}
	what = fmt.Sprintf("the %s step", s.Kind)
	switch s.Kind {
	case StepPackages:
		s.Packages = p.packages(body, what)
	case StepService:
		s.Service = p.service(body, what)
	case StepCommand:
		s.Command = p.command(body, what)
	case StepFile:
		s.File = p.file(body, what)
	case StepStack:
		s.Stack = p.stack(body, what)
	case StepReboot:
		s.Reboot = p.reboot(body, what)
	}
	return s, true
}

// packageNames checks a list of package names. A name that is a reference
// is filled in at run time and judged by the node then.
func (p *parser) packageNames(items []*yaml.Node) {
	for _, it := range items {
		if hasRef(it.Value) {
			p.refs(it)
			continue
		}
		if !validPackageName.MatchString(it.Value) {
			p.addf(it.Line, "package name %q is not valid; names are letters, digits and . _ + - and start with a letter or digit", it.Value)
		}
	}
}

func (p *parser) packages(n *yaml.Node, what string) *PackagesStep {
	f, ok := p.fields(n, what, "update", "upgrade", "install", "remove")
	if !ok {
		return nil
	}
	s := &PackagesStep{Update: p.boolean(f, "update", what, false)}
	var actions []string
	if present(f, "upgrade") {
		actions = append(actions, "upgrade")
		v := f["upgrade"].val
		switch {
		case v.Kind == yaml.ScalarNode && v.Value == "all":
			s.UpgradeAll = true
		case v.Kind == yaml.SequenceNode:
			items, _ := p.list(v, what+" upgrade")
			if len(items) == 0 {
				p.addf(v.Line, "upgrade needs at least one package, or all")
			}
			p.packageNames(items)
			s.Upgrade = values(items)
		default:
			p.addf(v.Line, `upgrade takes "all" or a list of package names`)
		}
	}
	for _, key := range []string{"install", "remove"} {
		if !present(f, key) {
			continue
		}
		actions = append(actions, key)
		items, _ := p.list(f[key].val, what+" "+key)
		if len(items) == 0 {
			p.addf(f[key].val.Line, "%s needs at least one package", key)
		}
		p.packageNames(items)
		if key == "install" {
			s.Install = values(items)
		} else {
			s.Remove = values(items)
		}
	}
	switch {
	case len(actions) > 1:
		p.addf(f[actions[1]].key.Line, "a packages step takes one of upgrade, install or remove (update may go with it); split %s and %s into two steps", actions[0], actions[1])
	case len(actions) == 0 && !s.Update:
		p.addf(n.Line, "a packages step needs update, upgrade, install or remove")
	}
	return s
}

func (p *parser) service(n *yaml.Node, what string) *ServiceStep {
	f, ok := p.fields(n, what, "name", "action")
	if !ok {
		return nil
	}
	s := &ServiceStep{}
	var v *yaml.Node
	s.Name, v, _ = p.str(f, "name", what, true, n)
	p.refs(v)
	if action, av, ok := p.str(f, "action", what, true, n); ok {
		if !slices.Contains(serviceActions, action) {
			p.addf(av.Line, "service action %q is not one of %s", action, strings.Join(serviceActions, ", "))
		}
		s.Action = action
	}
	return s
}

func (p *parser) command(n *yaml.Node, what string) *CommandStep {
	f, ok := p.fields(n, what, "run", "script", "timeout")
	if !ok {
		return nil
	}
	s := &CommandStep{}
	var rv, sv *yaml.Node
	s.Run, rv, _ = p.str(f, "run", what, false, n)
	s.Script, sv, _ = p.str(f, "script", what, false, n)
	switch {
	case rv != nil && sv != nil:
		p.addf(f["script"].key.Line, "a command step takes run or script, not both")
	case rv == nil && sv == nil:
		p.addf(n.Line, "a command step needs run or script")
	case strings.TrimSpace(s.Run+s.Script) == "":
		p.addf(n.Line, "the command is empty")
	}
	p.refs(rv)
	p.refs(sv)
	s.Timeout = p.duration(f, "timeout", "command", DefaultCommandTimeout)
	return s
}

func (p *parser) file(n *yaml.Node, what string) *FileStep {
	f, ok := p.fields(n, what, "path", "content", "mode", "owner")
	if !ok {
		return nil
	}
	s := &FileStep{}
	var v *yaml.Node
	if s.Path, v, ok = p.str(f, "path", what, true, n); ok {
		if !hasRef(s.Path) && !path.IsAbs(s.Path) {
			p.addf(v.Line, "file path %q must be absolute", s.Path)
		}
		p.refs(v)
	}
	s.Content, v, _ = p.str(f, "content", what, true, n)
	p.refs(v)
	if s.Mode, v, ok = p.str(f, "mode", what, false, n); ok {
		if err := checkFileMode(s.Mode); err != nil {
			p.addf(v.Line, "file mode %q: %v", s.Mode, err)
		}
	}
	s.Owner, v, _ = p.str(f, "owner", what, false, n)
	p.refs(v)
	return s
}

// checkFileMode is the files module's parseFileMode rule: three or four
// octal digits, permission bits only.
func checkFileMode(raw string) error {
	if raw == "" || len(raw) > 4 {
		return errors.New("must be three or four octal digits")
	}
	parsed, err := strconv.ParseUint(raw, 8, 32)
	if err != nil {
		return errors.New("must be octal, for example 0644")
	}
	if os.FileMode(parsed)&^os.ModePerm != 0 {
		return errors.New("only permission bits may be set; setuid, setgid and sticky are not settable here")
	}
	return nil
}

func (p *parser) stack(n *yaml.Node, what string) *StackStep {
	f, ok := p.fields(n, what, "name", "compose", "up")
	if !ok {
		return nil
	}
	s := &StackStep{}
	var v *yaml.Node
	s.Name, v, _ = p.str(f, "name", what, true, n)
	p.refs(v)
	s.Compose, v, _ = p.str(f, "compose", what, true, n)
	p.refs(v)
	s.Up = p.boolean(f, "up", what, false)
	return s
}

func (p *parser) reboot(n *yaml.Node, what string) *RebootStep {
	f, ok := p.fields(n, what, "only_if_required", "wait")
	if !ok {
		return nil
	}
	return &RebootStep{
		OnlyIfRequired: p.boolean(f, "only_if_required", what, true),
		Wait:           p.duration(f, "wait", "reboot", DefaultRebootWait),
	}
}

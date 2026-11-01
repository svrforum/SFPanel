package tasks

import (
	"bytes"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// Format writes a task back into a document, the inverse of Parse: for a
// task Parse returned, Parse(Format(t)) is equal to t, up to the size limit.
// Format re-indents, so a document just under MaxDocumentBytes can come out
// over it, and that one reads back with the size problem. The editor's card
// view edits the parsed model and its YAML view the document, and the web
// carries no YAML library, so the panel turns one into the other here.
//
// It leaves out every value Parse fills in when a key is missing (batch 1,
// stop_on_failure true, a command's 10m timeout, a reboot's 10m wait and
// only_if_required true), so a document written from cards reads like one
// written by hand. A zero duration or batch counts as left out too: that is
// what a model built in the editor holds for a value nobody set.
//
// Format takes any model, not only a valid one: a step with no body or an
// unknown kind still makes a document, which Parse then answers with its
// problems.
func Format(t *Task) ([]byte, error) {
	if t == nil {
		return nil, errors.New("tasks: no task to format")
	}
	doc := &yaml.Node{Kind: yaml.MappingNode}
	add(doc, "name", text(t.Name))
	if t.Description != "" {
		add(doc, "description", text(t.Description))
	}
	if len(t.Inputs) > 0 {
		inputs := &yaml.Node{Kind: yaml.MappingNode}
		for _, in := range t.Inputs {
			add(inputs, in.Name, formatInput(in))
		}
		add(doc, "inputs", inputs)
	}
	if t.Targets.Groups != nil || t.Targets.Kinds != nil || t.Targets.Nodes != nil {
		targets := &yaml.Node{Kind: yaml.MappingNode}
		addList(targets, "groups", t.Targets.Groups)
		addList(targets, "kinds", t.Targets.Kinds)
		addList(targets, "nodes", t.Targets.Nodes)
		add(doc, "targets", targets)
	}
	if t.Batch != 0 && t.Batch != 1 {
		add(doc, "batch", number(t.Batch))
	}
	if !t.StopOnFailure {
		add(doc, "stop_on_failure", boolean(false))
	}
	steps := &yaml.Node{Kind: yaml.SequenceNode}
	for _, s := range t.Steps {
		steps.Content = append(steps.Content, formatStep(s))
	}
	add(doc, "steps", steps)

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func formatInput(in Input) *yaml.Node {
	n := &yaml.Node{Kind: yaml.MappingNode, Style: yaml.FlowStyle}
	if in.Label != "" {
		add(n, "label", text(in.Label))
	}
	if in.Default != "" {
		add(n, "default", text(in.Default))
	}
	if in.Required {
		add(n, "required", boolean(true))
	}
	if in.Raw {
		add(n, "raw", boolean(true))
	}
	return n
}

// formatStep writes a step's common keys, then its kind. A kind with no body
// is written as an empty mapping, which Parse reads as the kind's defaults.
func formatStep(s Step) *yaml.Node {
	n := &yaml.Node{Kind: yaml.MappingNode}
	if s.Name != "" {
		add(n, "name", text(s.Name))
	}
	if s.ContinueOnError {
		add(n, "continue_on_error", boolean(true))
	}
	if s.Kind == "" {
		return n
	}
	body := &yaml.Node{Kind: yaml.MappingNode}
	switch s.Kind {
	case StepPackages:
		if p := s.Packages; p != nil {
			if p.Update {
				add(body, "update", boolean(true))
			}
			if p.UpgradeAll {
				add(body, "upgrade", text("all"))
			} else {
				addList(body, "upgrade", p.Upgrade)
			}
			addList(body, "install", p.Install)
			addList(body, "remove", p.Remove)
		}
	case StepService:
		if sv := s.Service; sv != nil {
			add(body, "name", text(sv.Name))
			add(body, "action", text(sv.Action))
		}
	case StepCommand:
		if c := s.Command; c != nil {
			if c.Run != "" {
				add(body, "run", text(c.Run))
			}
			if c.Script != "" {
				add(body, "script", text(c.Script))
			}
			addDuration(body, "timeout", c.Timeout, DefaultCommandTimeout)
		}
	case StepFile:
		if f := s.File; f != nil {
			add(body, "path", text(f.Path))
			add(body, "content", text(f.Content))
			if f.Mode != "" {
				add(body, "mode", text(f.Mode))
			}
			if f.Owner != "" {
				add(body, "owner", text(f.Owner))
			}
		}
	case StepStack:
		if st := s.Stack; st != nil {
			add(body, "name", text(st.Name))
			add(body, "compose", text(st.Compose))
			if st.Up {
				add(body, "up", boolean(true))
			}
		}
	case StepReboot:
		if r := s.Reboot; r != nil {
			if !r.OnlyIfRequired {
				add(body, "only_if_required", boolean(false))
			}
			addDuration(body, "wait", r.Wait, DefaultRebootWait)
		}
	}
	add(n, s.Kind, body)
	return n
}

func add(m *yaml.Node, key string, val *yaml.Node) {
	m.Content = append(m.Content, text(key), val)
}

// addList writes a list of names, on one line as the built-ins do. A nil
// list is left out; an empty one is written, as Parse tells the two apart.
func addList(m *yaml.Node, key string, items []string) {
	if items == nil {
		return
	}
	seq := &yaml.Node{Kind: yaml.SequenceNode, Style: yaml.FlowStyle}
	for _, it := range items {
		seq.Content = append(seq.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: it})
	}
	add(m, key, seq)
}

func addDuration(m *yaml.Node, key string, d, def time.Duration) {
	if d != 0 && d != def {
		add(m, key, &yaml.Node{Kind: yaml.ScalarNode, Value: shortDuration(d)})
	}
}

// text is a string value. Tagged as a string, the encoder quotes whatever
// would read back as something else (true, 123, null, a leading { or *);
// a value of several lines is written as a literal block, as a script is
// by hand. The encoder falls back to a quoted string when a block cannot
// hold the value exactly (trailing spaces on a line, a carriage return),
// but not for two it gets wrong: a leading line break, which it drops, and
// a leading tab, which it writes with no indentation indicator, so the
// document no longer parses. Those are double-quoted here.
func text(s string) *yaml.Node {
	n := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s}
	if strings.Contains(s, "\n") {
		n.Style = yaml.LiteralStyle
		if r, _ := utf8.DecodeRuneInString(s); r == '\t' || isLineBreak(r) {
			n.Style = yaml.DoubleQuotedStyle
		}
	}
	return n
}

// isLineBreak is a rune YAML reads as a line break.
func isLineBreak(r rune) bool {
	switch r {
	case '\n', '\r', '\u0085', '\u2028', '\u2029':
		return true
	}
	return false
}

func number(i int) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.Itoa(i)}
}

func boolean(b bool) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: strconv.FormatBool(b)}
}

// shortDuration writes a duration as a person would: 10m and 1h30m rather
// than Go's 10m0s and 1h30m0s. Only whole trailing units are dropped, so
// the result still reads back as d.
func shortDuration(d time.Duration) string {
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}

package tasks

import (
	"embed"
	"fmt"
)

// builtinFS holds the built-in tasks. They are read-only and never stored
// in the cluster: every manager carries the same set in its binary.
//
//go:embed builtin/*.yaml
var builtinFS embed.FS

// builtinOrder is the order the built-ins are listed in, simplest first.
var builtinOrder = []string{"package-update", "package-install", "service-restart", "command", "reboot"}

// BuiltinTask is a built-in task: its ID ("builtin/<name>") and document.
type BuiltinTask struct {
	ID   string
	YAML []byte
}

// Builtins returns the built-in tasks in display order.
func Builtins() []BuiltinTask {
	out := make([]BuiltinTask, 0, len(builtinOrder))
	for _, name := range builtinOrder {
		doc, err := builtinFS.ReadFile("builtin/" + name + ".yaml")
		if err != nil {
			// The files are compiled in; a missing one is a build mistake
			// TestEveryBuiltinParses catches.
			panic(fmt.Sprintf("tasks: built-in %s is not embedded: %v", name, err))
		}
		out = append(out, BuiltinTask{ID: "builtin/" + name, YAML: doc})
	}
	return out
}

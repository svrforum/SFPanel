package ai

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// profileEnv is the variable each tool reads for its configuration and
// credential directory. A profile is one such directory, so selecting a
// profile is setting this variable — verified on the reference host: with
// CODEX_HOME or CLAUDE_CONFIG_DIR pointed at an empty directory, both CLIs
// report "not logged in" while the account's default directory keeps its
// login. A tool absent from this map does not support profiles: Gemini has
// no override the project has verified, and a shell session runs no tool.
var profileEnv = map[string]string{
	ToolClaude: "CLAUDE_CONFIG_DIR",
	ToolCodex:  "CODEX_HOME",
}

// profileLoginFile is what the tool writes into that directory once it has
// been logged in. Existence is a hint for the UI, never a gate — an expired
// token leaves the file behind, exactly as loginFiles already documents.
var profileLoginFile = map[string]string{
	ToolClaude: ".credentials.json",
	ToolCodex:  "auth.json",
}

// profileRootName is the directory the panel owns inside an account's home.
// Under the account's home rather than /tmp because Codex refuses to create
// its helper binaries in a temporary directory.
const profileRootName = ".sfpanel-ai"

func toolSupportsProfiles(tool string) bool {
	_, ok := profileEnv[tool]
	return ok
}

// profileNameRe is deliberately narrow: the name becomes a directory name, so
// it may not start with a dot (no hidden directories, no "." or ".."), may
// not contain a separator, and is capped so a path cannot be pushed past a
// filesystem limit.
var profileNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,31}$`)

func validProfileName(name string) bool {
	return profileNameRe.MatchString(name) && !strings.Contains(name, "..")
}

// profileRoot is where an account's profiles for one tool live.
func profileRoot(acct Account, tool string) string {
	return filepath.Join(acct.Home, profileRootName, tool)
}

var errProfileName = errors.New("ai: invalid profile name")

// profileDir resolves one profile's directory. It validates the name, joins
// it, and then re-checks the joined path against the expected prefix: the
// name alone is not the thing that reaches the filesystem, and this module's
// own history (and the file manager's) says to check the result.
func profileDir(acct Account, tool, name string) (string, error) {
	if !toolSupportsProfiles(tool) {
		return "", fmt.Errorf("ai: %s has no profile support", tool)
	}
	if !filepath.IsAbs(acct.Home) {
		return "", fmt.Errorf("ai: account home %q is not absolute", acct.Home)
	}
	if !validProfileName(name) {
		return "", errProfileName
	}
	root := profileRoot(acct, tool)
	dir := filepath.Clean(filepath.Join(root, name))
	if filepath.Dir(dir) != root {
		return "", errProfileName
	}
	return dir, nil
}

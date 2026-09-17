package ai

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// LaunchOptions is what the operator chose for one session (spec §1). The
// zero value means "start the tool bare", which is what every session created
// before this feature means too — and what an empty ai_sessions.launch says.
//
// The row keeps the *choices*, never a built argv: the argv is derived at
// spawn time by toolArgv, so 다시 실행 and 다시 시작 reproduce the launch, the
// info action can show it in words, and a later change to a flag's spelling
// cannot turn an old row into an unrunnable command.
type LaunchOptions struct {
	Continue   string   `json:"continue,omitempty"`   // "" | "last" | "pick"
	Permission string   `json:"permission,omitempty"` // per-tool allowlist, see below
	Sandbox    string   `json:"sandbox,omitempty"`    // codex only
	Dangerous  bool     `json:"dangerous,omitempty"`  // the tool's own bypass flag
	Model      string   `json:"model,omitempty"`
	Extra      []string `json:"extra,omitempty"` // advanced, validated per token
}

// The two continue choices. "last" picks up the most recent conversation
// without asking; "pick" opens the CLI's own picker.
const (
	continueLast = "last"
	continuePick = "pick"
)

// claudePermissionModes is `claude --help`'s own list for --permission-mode,
// read from the installed CLI (Claude Code 2.1.271) rather than from
// documentation. Claude also refuses --dangerously-skip-permissions when it
// runs as root, which is a rule about the account and therefore not checked
// here — see validateLaunch.
var claudePermissionModes = []string{"auto", "manual", "plan", "acceptEdits", "dontAsk", "bypassPermissions"}

// codexApprovalPolicies and codexSandboxModes are `codex --help`'s own lists
// for -a/--ask-for-approval and -s/--sandbox, read from the installed CLI
// (Codex 0.154.0). Codex's bypass flag has no account rule of its own.
var (
	codexApprovalPolicies = []string{"on-request", "never"}
	codexSandboxModes     = []string{"read-only", "workspace-write", "danger-full-access"}
)

// launchModelRe and launchExtraRe are narrow on purpose. Every token here
// becomes one argv element and no shell re-parses it (toolArgv explains the
// wrapper), so these patterns are belt to that brace and not the only
// defence — but a model name or a flag with whitespace in it is a mistake
// worth naming before a session spawns rather than after it dies.
//
// launchExtraRe allows an optional one or two leading dashes, then an
// alphanumeric, then the characters a flag or its value actually uses. There
// is no whitespace in the set, so a value that would need quoting cannot be
// expressed at all; the refusal says so instead of mangling it.
var (
	launchModelRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,63}$`)
	launchExtraRe = regexp.MustCompile(`^-{0,2}[A-Za-z0-9][A-Za-z0-9=._,:/@-]*$`)
)

const (
	maxLaunchExtra    = 8
	maxLaunchExtraLen = 64
)

// IsZero is "the operator touched nothing", the one representation of "no
// options" everywhere: it is what Encode writes as the empty string and what
// decodeLaunch reads the empty string back as.
func (o LaunchOptions) IsZero() bool {
	return o.Continue == "" && o.Permission == "" && o.Sandbox == "" &&
		!o.Dangerous && o.Model == "" && len(o.Extra) == 0
}

// Encode is the stored column: the empty string for the zero value rather
// than "{}", so a bare session reads identically to every row written before
// migration 38.
func (o LaunchOptions) Encode() (string, error) {
	if o.IsZero() {
		return "", nil
	}
	b, err := json.Marshal(o)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// decodeLaunch reads that column back. Unknown fields are ignored — that is
// encoding/json's default and it is deliberately not switched off: a row
// written by a newer panel that has learned another option must not fail this
// panel's restart of that session (spec §7).
func decodeLaunch(s string) (LaunchOptions, error) {
	var o LaunchOptions
	if strings.TrimSpace(s) == "" {
		return o, nil
	}
	if err := json.Unmarshal([]byte(s), &o); err != nil {
		return LaunchOptions{}, fmt.Errorf("ai: stored launch options: %w", err)
	}
	return o, nil
}

// toolSupportsLaunch is which tools take launch options at all. Gemini has no
// launch preferences this project has verified, and a shell session runs no
// tool; for both, options are refused rather than silently dropped, because
// dropping them would start a session that is not the one that was asked for.
func toolSupportsLaunch(tool string) bool {
	return tool == ToolClaude || tool == ToolCodex
}

// validateLaunch checks every value against the chosen tool before anything
// spawns (spec §3). Each refusal names the rule it broke — continue,
// permission, approval, sandbox, model, argument — because the dialog shows
// the message beside the control that produced it, and "invalid" beside a
// select box says nothing.
//
// A value from the other tool's list is refused, not translated: Claude's
// acceptEdits and Codex's on-request are not two spellings of one idea, and
// guessing which the operator meant would launch the CLI with a permission
// posture nobody chose.
//
// One rule from §3 is not here: Claude refuses --dangerously-skip-permissions
// when it runs as root, which depends on the resolved account rather than on
// the options. It belongs to the handler that has one (ErrLaunchRootDanger).
func validateLaunch(tool string, o LaunchOptions) error {
	if !toolSupportsLaunch(tool) {
		if o.IsZero() {
			return nil
		}
		return fmt.Errorf("ai: %s takes no launch options", tool)
	}
	switch o.Continue {
	case "", continueLast, continuePick:
	default:
		return fmt.Errorf("ai: continue must be %q, %q or empty", continueLast, continuePick)
	}
	if o.Permission != "" {
		switch tool {
		case ToolClaude:
			if !slices.Contains(claudePermissionModes, o.Permission) {
				return fmt.Errorf("ai: permission mode %q is not one of %s",
					o.Permission, strings.Join(claudePermissionModes, ", "))
			}
		case ToolCodex:
			if !slices.Contains(codexApprovalPolicies, o.Permission) {
				return fmt.Errorf("ai: approval policy %q is not one of %s",
					o.Permission, strings.Join(codexApprovalPolicies, ", "))
			}
		}
	}
	if o.Sandbox != "" {
		if tool != ToolCodex {
			return fmt.Errorf("ai: sandbox is a codex option; %s has none", tool)
		}
		if !slices.Contains(codexSandboxModes, o.Sandbox) {
			return fmt.Errorf("ai: sandbox %q is not one of %s",
				o.Sandbox, strings.Join(codexSandboxModes, ", "))
		}
	}
	if o.Model != "" && !launchModelRe.MatchString(o.Model) {
		return fmt.Errorf("ai: model %q must be 1-64 characters of letters, digits, dot, dash, underscore or colon, starting with a letter or digit", o.Model)
	}
	if len(o.Extra) > maxLaunchExtra {
		return fmt.Errorf("ai: at most %d extra arguments, got %d", maxLaunchExtra, len(o.Extra))
	}
	for _, tok := range o.Extra {
		if len(tok) > maxLaunchExtraLen {
			return fmt.Errorf("ai: extra argument %q is longer than %d characters", tok, maxLaunchExtraLen)
		}
		if !launchExtraRe.MatchString(tok) {
			return fmt.Errorf("ai: extra argument %q carries a character an argument may not; arguments are space-separated with no quoting, so a value containing a space cannot be expressed", tok)
		}
	}
	return nil
}

// toolArgv turns the chosen options into the arguments that follow the tool
// name (spec §2). It is the only place either CLI's flag spelling appears,
// and it validates first so no unvalidated argv can be built.
//
// What it returns is appended, element by element, to the wrapper the module
// already spawns (sessionCommands):
//
//	bash -lic 'command "$0" "$@"; exec bash -l' <tool> <argv…>
//
// `"$@"` expands each element as its own word, so no token is ever re-parsed
// by a shell. That is what makes 추가 인자 safe, and it is why nothing here
// may ever be joined into the -c script string instead: doing so would hand
// operator text to a shell to parse. TestToolArgv_Claude and
// TestToolArgv_CodexResumeIsASubcommandAndComesFirst assert the elements
// separately, so a concatenating "simplification" fails them.
func toolArgv(tool string, o LaunchOptions) ([]string, error) {
	if err := validateLaunch(tool, o); err != nil {
		return nil, err
	}
	var argv []string
	switch tool {
	case ToolClaude:
		switch o.Continue {
		case continueLast:
			argv = append(argv, "--continue")
		case continuePick:
			// --resume with no id: the CLI opens its own picker.
			argv = append(argv, "--resume")
		}
		if o.Permission != "" {
			argv = append(argv, "--permission-mode", o.Permission)
		}
		if o.Dangerous {
			argv = append(argv, "--dangerously-skip-permissions")
		}
		if o.Model != "" {
			argv = append(argv, "--model", o.Model)
		}
	case ToolCodex:
		// `resume` is a SUBCOMMAND, not a flag, so it comes first and ahead
		// of every option: `codex --continue` does not exist, and codex reads
		// the subcommand before the flags that qualify it.
		switch o.Continue {
		case continueLast:
			argv = append(argv, "resume", "--last")
		case continuePick:
			argv = append(argv, "resume")
		}
		if o.Permission != "" {
			argv = append(argv, "--ask-for-approval", o.Permission)
		}
		if o.Sandbox != "" {
			argv = append(argv, "--sandbox", o.Sandbox)
		}
		if o.Dangerous {
			argv = append(argv, "--dangerously-bypass-approvals-and-sandbox")
		}
		if o.Model != "" {
			argv = append(argv, "-m", o.Model)
		}
	default:
		// validateLaunch already refused a non-zero set for a tool that takes
		// none; an empty set adds nothing, which is how a shell or gemini
		// session keeps spawning exactly as it does today.
		return nil, nil
	}
	return append(argv, o.Extra...), nil
}

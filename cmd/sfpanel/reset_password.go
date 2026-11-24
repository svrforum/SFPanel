package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"

	"golang.org/x/term"
)

// resetPasswordCommand is `sfpanel reset-password [--user NAME] [--config PATH]`:
// a new password for an account, without the current one, for an operator who
// has forgotten it. It asks the running panel, which writes the account where
// logins read it — through the cluster leader when the node is in a cluster.
// The password is typed twice at a terminal, or read as one line from stdin.
func resetPasswordCommand(args []string) {
	if os.Getuid() != 0 {
		log.Fatal("Run as root: sudo sfpanel reset-password")
	}
	cfgPath, rest := parseCfgFlag(args)
	user, err := parseResetPasswordArgs(rest)
	if err != nil {
		log.Fatalf("%v\nUsage: sfpanel reset-password [--user NAME] [--config PATH]", err)
	}
	cfg := loadCfgForCLI(cfgPath)
	if cfg.Cluster.IsAgent() {
		log.Fatal("This server is an agent: it has no accounts of its own, and signs in through its managers. Run sudo sfpanel reset-password on a manager.")
	}

	var password string
	if term.IsTerminal(int(os.Stdin.Fd())) {
		password, err = promptNewPassword(func(prompt string) (string, error) {
			fmt.Print(prompt)
			b, err := term.ReadPassword(int(os.Stdin.Fd()))
			fmt.Println()
			return string(b), err
		})
	} else {
		password, err = readPasswordLine(os.Stdin)
	}
	if err != nil {
		log.Fatal(err)
	}

	raw, err := callLocalAPI(cfg, http.MethodPost, "/api/v1/auth/reset-password", map[string]string{
		"username":     user,
		"new_password": password,
	})
	if err != nil {
		log.Fatal(resetFailure(err))
	}
	var envelope struct {
		Data struct {
			Username string `json:"username"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil || envelope.Data.Username == "" {
		log.Fatalf("Password reset, but the answer was not understood: %s", string(raw))
	}
	fmt.Printf("Password reset for %s. Every manager has it within a few seconds.\n", envelope.Data.Username)
}

// resetFailure says why the panel refused, from callLocalAPI's error: the
// panel's own message and code when it sent them.
func resetFailure(err error) string {
	msg := err.Error()
	_, body, ok := strings.Cut(strings.TrimPrefix(msg, "server returned "), ": ")
	if !ok || !strings.HasPrefix(msg, "server returned ") {
		return "Password not reset: " + msg
	}
	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal([]byte(body), &envelope) != nil || envelope.Error.Code == "" {
		return "Password not reset: " + msg
	}
	return fmt.Sprintf("Password not reset: %s (%s)", envelope.Error.Message, envelope.Error.Code)
}

// parseResetPasswordArgs reads --user NAME; nothing else is accepted.
func parseResetPasswordArgs(args []string) (string, error) {
	user := ""
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--user" && i+1 < len(args):
			user = args[i+1]
			i++
		case strings.HasPrefix(args[i], "--user="):
			user = strings.TrimPrefix(args[i], "--user=")
		default:
			return "", fmt.Errorf("unknown argument %q", args[i])
		}
	}
	return user, nil
}

// promptNewPassword asks for the password twice and returns it when both
// answers are the same and not empty.
func promptNewPassword(ask func(prompt string) (string, error)) (string, error) {
	first, err := ask("New password: ")
	if err != nil {
		return "", fmt.Errorf("read password: %w", err)
	}
	if first == "" {
		return "", errors.New("no password entered")
	}
	second, err := ask("Again: ")
	if err != nil {
		return "", fmt.Errorf("read password: %w", err)
	}
	if first != second {
		return "", errors.New("the two passwords differ; nothing was changed")
	}
	return first, nil
}

// readPasswordLine is the first line of r without its line ending, for a
// password piped in rather than typed.
func readPasswordLine(r io.Reader) (string, error) {
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("read password: %w", err)
	}
	line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
	if line == "" {
		return "", errors.New("no password on stdin")
	}
	return line, nil
}

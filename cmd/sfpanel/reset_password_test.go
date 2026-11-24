package main

import (
	"errors"
	"strings"
	"testing"
)

func TestParseResetPasswordArgs(t *testing.T) {
	for _, tc := range []struct {
		args []string
		user string
		bad  bool
	}{
		{nil, "", false},
		{[]string{"--user", "alice"}, "alice", false},
		{[]string{"--user=bob"}, "bob", false},
		{[]string{"alice"}, "", true},
		{[]string{"--user"}, "", true},
	} {
		user, err := parseResetPasswordArgs(tc.args)
		if (err != nil) != tc.bad || user != tc.user {
			t.Errorf("%v: user=%q err=%v, want user=%q bad=%v", tc.args, user, err, tc.user, tc.bad)
		}
	}
}

func TestPromptNewPasswordWantsTheSameTwice(t *testing.T) {
	answers := func(a ...string) func(string) (string, error) {
		return func(string) (string, error) {
			v := a[0]
			a = a[1:]
			return v, nil
		}
	}
	if pw, err := promptNewPassword(answers("same-password-1", "same-password-1")); err != nil || pw != "same-password-1" {
		t.Errorf("matching answers: %q %v", pw, err)
	}
	if _, err := promptNewPassword(answers("one-password-123", "other-password-1")); err == nil || !strings.Contains(err.Error(), "differ") {
		t.Errorf("differing answers: err=%v, want it to say they differ", err)
	}
	if _, err := promptNewPassword(answers("")); err == nil || !strings.Contains(err.Error(), "no password") {
		t.Errorf("empty answer: err=%v, want no password", err)
	}
	failing := func(string) (string, error) { return "", errors.New("tty gone") }
	if _, err := promptNewPassword(failing); err == nil || !strings.Contains(err.Error(), "tty gone") {
		t.Errorf("read failure: err=%v", err)
	}
}

func TestReadPasswordLineTakesTheFirstLine(t *testing.T) {
	for in, want := range map[string]string{
		"piped-password-1\n":         "piped-password-1",
		"piped-password-1\r\nmore\n": "piped-password-1",
		"no-newline-password":        "no-newline-password",
		" spaces kept \n":            " spaces kept ",
	} {
		if got, err := readPasswordLine(strings.NewReader(in)); err != nil || got != want {
			t.Errorf("%q: got %q %v, want %q", in, got, err, want)
		}
	}
	if _, err := readPasswordLine(strings.NewReader("\n")); err == nil {
		t.Error("an empty line was taken as a password")
	}
}

func TestResetFailureSaysWhatThePanelSaid(t *testing.T) {
	for _, tc := range []struct {
		err  string
		want string
	}{
		{`server returned 404 Not Found: {"success":false,"error":{"code":"USER_NOT_FOUND","message":"User not found"}}`,
			"Password not reset: User not found (USER_NOT_FOUND)"},
		{`server returned 404 Not Found: {"message":"Not Found"}`, `Password not reset: server returned 404 Not Found: {"message":"Not Found"}`},
		{`server returned 400 Bad Request: {"success":false,"error":{"code":"WEAK_PASSWORD","message":"Password must be at least 12 characters."}}`,
			"Password not reset: Password must be at least 12 characters. (WEAK_PASSWORD)"},
		{`reach local sfpanel at http://127.0.0.1:3628/x: connection refused (is the server running?)`, "is the server running?"},
	} {
		if got := resetFailure(errors.New(tc.err)); !strings.Contains(got, tc.want) {
			t.Errorf("%s\n got %q\nwant it to contain %q", tc.err, got, tc.want)
		}
	}
}

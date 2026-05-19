package packages

import "testing"

func TestValidateSearchQuery_AllowsMultiWord(t *testing.T) {
	ok := []string{
		"nginx",
		"redis server",
		"libssl-dev",
		"node.js",
		"python3.11",
		"foo bar baz",
		"a+b",
	}
	for _, q := range ok {
		if !validateSearchQuery(q) {
			t.Errorf("validateSearchQuery(%q) should be accepted", q)
		}
	}
}

func TestValidateSearchQuery_RejectsShellMetacharsAndControl(t *testing.T) {
	bad := []string{
		"",          // empty
		"redis; ls", // semicolon
		"a|b",       // pipe
		"a&b",       // ampersand
		"a`b`",      // backtick
		"$(whoami)", // command substitution
		"a\nb",      // newline
		"a\tb",      // tab (we keep apt-cache happy by disallowing whitespace other than space)
		"a/b",       // slash — apt-cache doesn't search paths
		"a*",        // glob
		"a?",        // glob
		"a<b",       // redirection
		"a>b",       // redirection
		"a\"b",      // quote (operator probably typed accidentally; cleaner to reject)
		"a'b",       // quote
	}
	for _, q := range bad {
		if validateSearchQuery(q) {
			t.Errorf("validateSearchQuery(%q) should be rejected", q)
		}
	}
}

func TestValidatePackageName_StillStrict(t *testing.T) {
	// Search query rule must NOT replace the package-name rule. Package
	// names are passed to apt-get install and must remain conservative.
	if validatePackageName("redis server") {
		t.Error("validatePackageName should still reject spaces — package names cannot contain whitespace")
	}
	if !validatePackageName("redis-server") {
		t.Error("validatePackageName(redis-server) should accept")
	}
}

func TestValidatePackageName_RejectsFlagShape(t *testing.T) {
	// Regression: a leading hyphen turns the "package name" into an apt-get
	// flag (e.g. `--reinstall`, `-y`). Even though every install/remove path
	// now passes `--` before the package list, the validator is the first
	// guard and must not accept the flag shape on its own.
	flagShape := []string{
		"--reinstall",
		"-y",
		"--allow-downgrades",
		"-",
		".hidden",  // dpkg names start with [a-z0-9]
		"+plus",    // + is allowed mid-name but not leading
	}
	for _, name := range flagShape {
		if validatePackageName(name) {
			t.Errorf("validatePackageName(%q) should be rejected — leading punctuation is flag-shaped", name)
		}
	}

	// Confirm well-formed names still pass.
	good := []string{
		"nginx",
		"redis-server",
		"libc6",
		"python3.11",
		"g++",
		"lib32stdc++6",
	}
	for _, name := range good {
		if !validatePackageName(name) {
			t.Errorf("validatePackageName(%q) should be accepted", name)
		}
	}
}

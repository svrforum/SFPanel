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

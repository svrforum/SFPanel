package exec_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every apt invocation goes through the helpers in this package.
//
// Six handlers used to shell out to apt-get directly and no two agreed: three
// omitted DEBIAN_FRONTEND, so a package with a debconf prompt blocked on a
// terminal that does not exist; two let the subprocess outlive the request;
// the firewall module kept its own copy of the environment helper; and only
// the packages module checked the dpkg lock first. None of that was decided —
// the Commander simply had no method taking both an environment and a context,
// so each caller gave one of them up.
//
// This walks the tree rather than asserting on behaviour because the failure
// mode is a new caller written the old way, which no unit test would see.
func TestNoHandlerShellsOutToAptDirectly(t *testing.T) {
	root := filepath.Join("..", "..", "..", "internal")

	// The streaming installer is the documented exception: it needs
	// os/exec directly to pipe apt's output to an SSE client line by line,
	// and it already carries AptEnv and the request context.
	allowed := map[string]string{
		filepath.Join("feature", "packages", "handler.go"): "streams apt output to SSE",
	}

	var offenders []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr == nil {
			if _, ok := allowed[rel]; ok {
				return nil
			}
		}
		// This package defines the helpers.
		if strings.HasPrefix(rel, filepath.Join("common", "exec")) {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for i, line := range strings.Split(string(data), "\n") {
			if !strings.Contains(line, `"apt-get"`) {
				continue
			}
			offenders = append(offenders, rel+":"+itoa(i+1)+"  "+strings.TrimSpace(line))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(offenders) > 0 {
		t.Errorf("apt-get is invoked directly outside the shared helpers:\n  %s\n\nUse exec.AptInstall / AptRemove / AptUpdate, which carry the "+
			"non-interactive environment, the request context, the dpkg lock check and the -- terminator together.",
			strings.Join(offenders, "\n  "))
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

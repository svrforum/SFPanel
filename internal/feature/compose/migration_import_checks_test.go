package compose

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/auth"
	"github.com/svrforum/SFPanel/internal/docker"
)

// importWith runs one MigrateImport of a bundle holding compose under policy,
// with script as docker on PATH, and returns the answer and docker's log.
func importWith(t *testing.T, compose string, policy importPolicy, script string) (*httptest.ResponseRecorder, string) {
	t.Helper()
	bin := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "docker.log")
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	t.Setenv("FAKE_DOCKER_LOG", logPath)
	auth.SetClusterProxySecret("test-secret")
	t.Cleanup(func() { auth.SetClusterProxySecret("") })

	var buf bytes.Buffer
	bw := NewBundleWriter(&buf)
	if err := bw.WriteManifest(MigrationManifest{SchemaVersion: 1, StackID: "demo", ComposeFile: "docker-compose.yml"}); err != nil {
		t.Fatal(err)
	}
	if err := bw.WriteFile("compose/docker-compose.yml", []byte(compose), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := bw.Close(); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(buf.Bytes())
	hdr := http.Header{}
	policy.setHeaders(hdr)
	headers := map[string]string{
		auth.InternalProxyHeaderV2: auth.SignProxyRequestV2(http.MethodPost, "/api/v1/docker/compose/migrate-import"),
		migrationShaHeader:         hex.EncodeToString(sum[:]),
	}
	for k := range hdr {
		headers[k] = hdr.Get(k)
	}
	stacks := t.TempDir()
	h := &Handler{Compose: docker.NewComposeManager(stacks, nil), ComposePath: stacks}
	c, rec := newImportCtx(echo.New(), buf.String(), headers)
	if err := h.MigrateImport(c); err != nil {
		t.Fatal(err)
	}
	return rec, readFile(t, logPath)
}

// twoRisks asks for two risky findings; an agent may list only one of them.
const twoRisks = "services:\n  web:\n    image: nginx:fake\n    privileged: true\n    volumes:\n      - /var/run/docker.sock:/var/run/docker.sock\n"

const (
	sockFinding = `service "web" binds sensitive host path "/var/run/docker.sock" at "/var/run/docker.sock"`
	privFinding = `service "web" sets privileged: true`
)

// An operator who acknowledged the lines an agent listed acknowledged those
// lines: a risky finding the agent left out is refused by the target, named,
// in the bundle's compose and in the config the target resolves.
func TestAnAgentImportTakesOnlyTheAcknowledgedFindings(t *testing.T) {
	rec, log := importWith(t, twoRisks, importPolicy{AgentSource: true, AckedRisks: []string{sockFinding}}, diskComposeDockerScript)
	body := rec.Body.String()
	if rec.Code != http.StatusBadRequest || !strings.Contains(body, `"code":"`+response.ErrComposeRisky+`"`) ||
		!strings.Contains(body, "privileged") || strings.Contains(body, "docker.sock") {
		t.Fatalf("%d %s, want 400 %s naming privileged and only it", rec.Code, body, response.ErrComposeRisky)
	}
	if strings.Contains(log, " up ") {
		t.Fatalf("the stack went up:\n%s", log)
	}

	// Every finding acknowledged: the checks pass and the import reaches up
	// (which fails, there being no daemon in a test).
	rec, log = importWith(t, twoRisks, importPolicy{AgentSource: true, AckedRisks: []string{privFinding, sockFinding}}, diskComposeDockerScript)
	if !strings.Contains(log, " up -d") {
		t.Fatalf("%d %s: every finding acknowledged, yet the import stopped before up\n%s", rec.Code, rec.Body, log)
	}

	// A manager's stack needs no list, as before.
	rec, log = importWith(t, twoRisks, importPolicy{}, diskComposeDockerScript)
	if !strings.Contains(log, " up -d") {
		t.Fatalf("%d %s: a manager's stack stopped before up\n%s", rec.Code, rec.Body, log)
	}
}

// flakyConfigDocker is docker whose `config` fails on call $FAKE_FAIL_ON. It
// resolves the stack as it is for the import's two checks and, from the third
// call — `up`'s own, which holds the forbidden tier only — as a privileged
// one, as the bundle's .env could make it. `up` is logged (and fails, there
// being no daemon in a test).
const flakyConfigDocker = `#!/bin/sh
echo "$*" >> "$FAKE_DOCKER_LOG"
case "$*" in
  *" up -d") echo "no daemon in a test" >&2; exit 1 ;;
  "compose -f "*" config")
    n=$(cat "$FAKE_DOCKER_LOG.n" 2>/dev/null || echo 0); n=$((n+1)); echo $n > "$FAKE_DOCKER_LOG.n"
    if [ "$n" = "$FAKE_FAIL_ON" ]; then echo "transient resolver failure" >&2; exit 1; fi
    if [ "$n" -ge 3 ]; then printf 'services:\n  web:\n    image: nginx:fake\n    privileged: true\n'; else cat "$3"; fi ;;
esac
exit 0
`

// An import's resolved re-checks were skipped when the config would not
// resolve, and `up` checks the forbidden tier only: an agent's stack went up
// privileged, unacknowledged, when either check's resolve failed. Each
// refuses now, with the resolver's reason.
func TestAnImportRefusesWhatItCannotResolve(t *testing.T) {
	for _, failOn := range []string{"1", "2"} {
		t.Run("config call "+failOn+" fails", func(t *testing.T) {
			t.Setenv("FAKE_FAIL_ON", failOn)
			rec, log := importWith(t, "services:\n  web:\n    image: nginx:fake\n", importPolicy{AgentSource: true}, flakyConfigDocker)
			body := rec.Body.String()
			if rec.Code != http.StatusBadRequest || !strings.Contains(body, `"code":"`+response.ErrComposeError+`"`) || !strings.Contains(body, "transient resolver failure") {
				t.Fatalf("%d %s, want 400 %s with the resolver's reason\n%s", rec.Code, body, response.ErrComposeError, log)
			}
			if strings.Contains(log, " up -d") {
				t.Fatalf("the stack went up:\n%s", log)
			}
		})
	}
}

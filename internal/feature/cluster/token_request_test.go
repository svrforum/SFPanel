package featurecluster

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/cluster"
	"github.com/svrforum/SFPanel/internal/config"
)

func TestParseTokenRequest(t *testing.T) {
	ttl, kind, err := parseTokenRequest(tokenRequest{})
	if err != nil || kind != cluster.KindManager || ttl != cluster.DefaultTokenTTL {
		t.Fatalf("defaults: %v %q %v", ttl, kind, err)
	}
	if ttl, kind, err = parseTokenRequest(tokenRequest{Kind: "agent", TTL: "2h"}); err != nil || kind != cluster.KindAgent || ttl != 2*time.Hour {
		t.Fatalf("agent: %v %q %v", ttl, kind, err)
	}
	if _, kind, err = parseTokenRequest(tokenRequest{Kind: "manager"}); err != nil || kind != cluster.KindManager {
		t.Fatalf("manager: %q %v", kind, err)
	}
	// A kind nobody mints is refused rather than read as the default: a typo
	// for agent must not come back as a manager token.
	if _, _, err = parseTokenRequest(tokenRequest{Kind: "observer"}); err == nil || !strings.Contains(err.Error(), "kind") {
		t.Fatalf("unknown kind: %v", err)
	}
	// The TTL rules are the ones CreateToken had: a value that does not parse
	// is the default, one that parses must be positive and within the cap.
	if ttl, _, err = parseTokenRequest(tokenRequest{TTL: "soon"}); err != nil || ttl != cluster.DefaultTokenTTL {
		t.Fatalf("unparsable TTL: %v %v", ttl, err)
	}
	if _, _, err = parseTokenRequest(tokenRequest{TTL: (cluster.MaxTokenTTL + time.Hour).String()}); err == nil || !strings.Contains(err.Error(), "maximum") {
		t.Fatalf("TTL over the maximum: %v", err)
	}
	for _, d := range []string{"0s", "-1h"} {
		if _, _, err = parseTokenRequest(tokenRequest{TTL: d}); err == nil || !strings.Contains(err.Error(), "positive") {
			t.Fatalf("TTL %s: %v", d, err)
		}
	}
}

// tokenLeader is a one-node cluster this process leads, which is all
// CreateToken needs. Init binds only the Raft transport, at grpc_port+1.
func tokenLeader(t *testing.T) *cluster.Manager {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	raftPort := l.Addr().(*net.TCPAddr).Port
	l.Close()
	dir := t.TempDir()
	mgr := cluster.NewManager(&config.ClusterConfig{
		DataDir: filepath.Join(dir, "raft"), CertDir: filepath.Join(dir, "certs"),
		AdvertiseAddress: "127.0.0.1", GRPCPort: raftPort - 1, APIPort: 1,
	})
	if err := mgr.Init("tokens"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(mgr.Shutdown)
	return mgr
}

// POST /cluster/token mints the kind its body asks for, and a manager token
// when the body names none — what the CLI and the token page send from before
// agents. A body it cannot read is refused rather than read as that default:
// it may have asked for an agent, and a manager token hands the server it
// joins the cluster's JWT key, admin account and CA key.
func TestCreateTokenMintsTheKindAsked(t *testing.T) {
	mgr := tokenLeader(t)
	h := &Handler{Manager: mgr}
	e := echo.New()
	post := func(body string) (int, string, string, string) {
		req := httptest.NewRequest(http.MethodPost, "/cluster/token", strings.NewReader(body))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		rec := httptest.NewRecorder()
		if err := h.CreateToken(e.NewContext(req, rec)); err != nil {
			t.Fatalf("%s: %v", body, err)
		}
		var out struct {
			Data struct {
				Token string `json:"token"`
				Kind  string `json:"kind"`
			} `json:"data"`
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("%s: %v (body %s)", body, err, rec.Body.String())
		}
		return rec.Code, out.Data.Token, out.Data.Kind, out.Error.Code
	}
	// The live tokens as GET /cluster/tokens lists them: ID → kind.
	listed := func() map[string]string {
		rec := httptest.NewRecorder()
		if err := h.ListTokens(e.NewContext(httptest.NewRequest(http.MethodGet, "/cluster/tokens", nil), rec)); err != nil {
			t.Fatal(err)
		}
		var out struct {
			Data struct {
				Tokens []struct {
					ID   string `json:"id"`
					Kind string `json:"kind"`
				} `json:"tokens"`
			} `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("%v (body %s)", err, rec.Body.String())
		}
		kinds := map[string]string{}
		for _, tok := range out.Data.Tokens {
			kinds[tok.ID] = tok.Kind
		}
		return kinds
	}
	listedKind := func(token string) string {
		for id, kind := range listed() {
			if strings.HasPrefix(token, id) {
				return kind
			}
		}
		t.Fatalf("token %q is not listed", token)
		return ""
	}

	for _, tc := range []struct {
		body string
		kind cluster.NodeKind
	}{
		{`{"ttl":""}`, cluster.KindManager},        // the token page before agents
		{`{"ttl":"24h0m0s"}`, cluster.KindManager}, // the CLI before agents
		{`{"ttl":"1h","kind":"agent"}`, cluster.KindAgent},
	} {
		status, token, kind, _ := post(tc.body)
		if status != http.StatusOK || kind != string(tc.kind) {
			t.Fatalf("%s: status %d kind %q, want 200 %q", tc.body, status, kind, tc.kind)
		}
		if got := listedKind(token); got != string(tc.kind) {
			t.Fatalf("%s: the response says %q, the leader lists a %q token", tc.body, kind, got)
		}
	}

	before := len(listed())
	for _, body := range []string{`{"kind":"observer"}`, `{"kind":"agent",}`} {
		if status, _, _, code := post(body); status != http.StatusBadRequest || code != response.ErrInvalidRequest {
			t.Fatalf("%s: status %d code %q, want 400 %s", body, status, code, response.ErrInvalidRequest)
		}
	}
	if after := len(listed()); after != before {
		t.Fatalf("a refused request minted a token: %d tokens, then %d", before, after)
	}
}

// The token's kind and the agent cap are the operator's to act on, so neither
// is the generic 500.
func TestClusterErrResponseNamesTheTokenKindAndTheAgentCap(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code string
	}{
		{cluster.ErrTokenKind, response.ErrClusterTokenKind},
		{fmt.Errorf("create token: %w", cluster.ErrMaxAgentsReached), response.ErrClusterMaxAgents},
	} {
		rec := httptest.NewRecorder()
		c := echo.New().NewContext(httptest.NewRequest(http.MethodPost, "/cluster/token", nil), rec)
		if err := clusterErrResponse(c, tc.err); err != nil {
			t.Fatal(err)
		}
		var out struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		if rec.Code != http.StatusBadRequest || out.Error.Code != tc.code || out.Error.Message != tc.err.Error() {
			t.Errorf("%v: status %d code %q message %q, want 400 %s with the error's text", tc.err, rec.Code, out.Error.Code, out.Error.Message, tc.code)
		}
	}
}

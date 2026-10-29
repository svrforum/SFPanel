package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/svrforum/SFPanel/internal/auth"
	"github.com/svrforum/SFPanel/internal/cluster"
	"github.com/svrforum/SFPanel/internal/config"
	"github.com/svrforum/SFPanel/internal/db"
	"gopkg.in/yaml.v3"
)

// defaultCfgPath is the fallback config path when --config is not provided.
const defaultCfgPath = "/etc/sfpanel/config.yaml"

// parseCfgFlag extracts "--config PATH" from args and returns (path, remainingArgs).
// Used by all cluster subcommands so operators can point at a non-default config.
func parseCfgFlag(args []string) (string, []string) {
	cfgPath := defaultCfgPath
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		if args[i] == "--config" && i+1 < len(args) {
			cfgPath = args[i+1]
			i++
			continue
		}
		out = append(out, args[i])
	}
	return cfgPath, out
}

// loadCfgForCLI reads a config file for read-only CLI commands. Unlike
// config.Load, it refuses to auto-generate a config when the file is missing —
// that auto-creation side effect would clobber unrelated state for users who
// mistyped --config.
func loadCfgForCLI(cfgPath string) *config.Config {
	if _, err := os.Stat(cfgPath); err != nil {
		if os.IsNotExist(err) {
			log.Fatalf("Config file not found: %s (use --config PATH)", cfgPath)
		}
		log.Fatalf("Failed to stat config: %v", err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}
	return cfg
}

// attachCSRFIfNeeded synthesizes a matching CSRF cookie+header pair on
// state-changing requests so CLI calls satisfy CSRFProtect middleware
// (internal/api/middleware/csrf.go). The middleware only checks that the
// two values are equal, so a same-value pair is sufficient — the CLI has
// no browser session to carry a real CSRF cookie. Safe methods (GET/HEAD/
// OPTIONS) bypass CSRF entirely and need no token.
func attachCSRFIfNeeded(req *http.Request) error {
	switch req.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return nil
	}
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Errorf("mint csrf token: %w", err)
	}
	token := hex.EncodeToString(buf)
	req.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: token})
	req.Header.Set(auth.CSRFHeaderName, token)
	return nil
}

// callLocalAPI issues an authenticated HTTP request to the local running
// sfpanel server, using a short-lived JWT minted from the config's jwt_secret.
// This is how CLI commands that need LIVE cluster state (token, remove, …)
// coordinate with the running process instead of spawning a conflicting one.
func callLocalAPI(cfg *config.Config, method, path string, body interface{}) ([]byte, error) {
	jwt, err := auth.GenerateToken("sfpanel-cli", cfg.Auth.JWTSecret, time.Minute)
	if err != nil {
		return nil, fmt.Errorf("mint admin token: %w", err)
	}

	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request body: %w", err)
		}
		reader = bytes.NewReader(raw)
	}

	self := selfPanel(cfg)
	url := self.URL(path)
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+jwt)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	if err := attachCSRFIfNeeded(req); err != nil {
		return nil, err
	}

	client, err := self.HTTPClient(15 * time.Second)
	if err != nil {
		return nil, fmt.Errorf("build local client: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("reach local sfpanel at %s: %w (is the server running?)", url, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	// Any success: a conversion answers 202, the node on its way out by then.
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("server returned %s: %s", resp.Status, string(raw))
	}
	return raw, nil
}

func clusterCommand(args []string) {
	if os.Getuid() != 0 {
		log.Fatal("SFPanel must be run as root. Use: sudo ./sfpanel cluster <command>")
	}

	if len(args) == 0 {
		printClusterHelp()
		return
	}
	if msg := clusterRefusal(args); msg != "" {
		log.Fatal(msg)
	}

	switch args[0] {
	case "help":
		printClusterHelp()
	case "init":
		clusterInit(args[1:])
	case "join":
		clusterJoin(args[1:])
	case "leave":
		clusterLeave(args[1:])
	case "status":
		clusterStatus(args[1:])
	case "token":
		clusterToken(args[1:])
	case "remove":
		clusterRemove(args[1:])
	case "convert":
		clusterConvert(args[1:])
	case "promote":
		clusterPromote(args[1:])
	case "rotate":
		clusterRotate(args[1:])
	case "reissue-cert":
		clusterReissueCert(args[1:])
	case "list":
		clusterList(args[1:])
	case "leader-transfer":
		clusterLeaderTransfer(args[1:])
	default:
		fmt.Printf("Unknown cluster command: %s\n", args[0])
		printClusterHelp()
	}
}

// clusterRefusal is what `sfpanel cluster args` answers on an agent, "" where
// it runs. Every cluster command but help works on a manager's Raft or its
// cluster API, with a JWT an agent's panel does not take, and on an agent
// would fail halfway or do harm: an offline leave or remove starts Raft. A
// config that cannot be read is left to the command, which says so.
func clusterRefusal(args []string) string {
	if len(args) == 0 || args[0] == "help" {
		return ""
	}
	cfgPath, _ := parseCfgFlag(args)
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		return ""
	}
	var cfg config.Config
	if yaml.Unmarshal(data, &cfg) != nil || !cfg.Cluster.IsAgent() {
		return ""
	}
	return "this server is an agent; use 'sfpanel agent status' or 'sfpanel agent reset'"
}

// clusterReissueCert re-issues this node's cluster mTLS certificate using
// the local CA. The running sfpanel process picks up the new cert on the
// next handshake (bounded by TLSManager.certReloadDebounce = 1 min) — no
// service restart is required. Only the node running this command is
// rotated; run it once per node that needs a fresh cert.
func clusterReissueCert(args []string) {
	cfgPath, _ := parseCfgFlag(args)
	cfg := loadCfgForCLI(cfgPath)

	if !cfg.Cluster.Enabled {
		log.Fatal("Cluster not initialized on this node. Run 'sfpanel cluster init' or 'join' first.")
	}
	if cfg.Cluster.NodeID == "" {
		log.Fatal("cluster.node_id missing from config; refusing to reissue.")
	}

	certDir := cfg.Cluster.CertDir
	tls := cluster.NewTLSManager(certDir)
	if !tls.HasCA() {
		log.Fatalf("CA cert not found under %s. This node is not the leader of a standalone-issued CA; only the leader can reissue with the cluster CA on disk. If you are a follower, ask the leader to reissue and redistribute.", certDir)
	}
	if !tls.HasCAKey() {
		log.Fatalf("CA private key (ca.key) not found under %s (ca.crt is present but ca.key is not). Reissuing signs against the CA key, which only a node that holds it has. If you are a follower, ask the leader — which holds the CA key — to reissue and redistribute.", certDir)
	}

	advertise := cfg.Cluster.AdvertiseAddress
	if advertise == "" {
		advertise = cluster.DetectFallbackIP()
		if advertise == "" {
			log.Fatal("advertise address not in config and no non-loopback IPv4 detected; pass via config manually first.")
		}
	}

	certPEM, keyPEM, err := tls.IssueNodeCert(cfg.Cluster.NodeID, []string{advertise}, cluster.CertRoleManager)
	if err != nil {
		log.Fatalf("issue node cert: %v", err)
	}
	if err := tls.SaveNodeCert(certPEM, keyPEM); err != nil {
		log.Fatalf("save node cert: %v", err)
	}

	fmt.Printf("Node cert reissued under %s (advertise=%s, node_id=%s).\n", certDir, advertise, cfg.Cluster.NodeID)
	fmt.Println("Running sfpanel will pick up the new cert on the next TLS handshake (≤ 1 minute). No restart required.")
}

func clusterInit(args []string) {
	cfgPath, rest := parseCfgFlag(args)
	clusterName := "sfpanel"
	var advertise string

	for i := 0; i < len(rest); i++ {
		if rest[i] == "--name" && i+1 < len(rest) {
			clusterName = rest[i+1]
			i++
		}
		if rest[i] == "--advertise" && i+1 < len(rest) {
			advertise = rest[i+1]
			i++
		}
	}

	// Try to delegate to running server
	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	if cfg.Cluster.Enabled {
		log.Fatal("Cluster already initialized. Use 'sfpanel cluster status' to check.")
	}

	if isServerRunning(cfg.Server.Port) {
		fmt.Println("Server is running — delegating init to live server...")
		body := map[string]string{
			"name":              clusterName,
			"advertise_address": advertise,
		}
		raw, err := callLocalAPI(cfg, "POST", "/api/v1/cluster/init", body)
		if err != nil {
			log.Fatalf("Init via server failed: %v", err)
		}
		fmt.Println(string(raw))
		return
	}

	// Server not running — init directly
	if advertise == "" {
		advertise = cfg.Cluster.AdvertiseAddress
	}
	if advertise == "" {
		advertise = cluster.DetectFallbackIP()
	}
	if advertise == "" {
		log.Fatal("Cannot detect advertise address. Use --advertise IP.")
	}

	cfg.Cluster.AdvertiseAddress = advertise
	cfg.Cluster.APIPort = cfg.Server.Port
	mgr := cluster.NewManager(&cfg.Cluster)
	if err := mgr.Init(clusterName); err != nil {
		log.Fatalf("Failed to initialize cluster: %v", err)
	}

	cfg.Cluster = *mgr.GetConfig()
	// A new cluster's key is generation 0; one kept from a cluster this node
	// left would stand above every rotation of this one.
	cfg.Auth.JWTGeneration = 0
	if err := saveConfig(cfgPath, cfg); err != nil {
		log.Printf("Warning: failed to save config: %v", err)
	}

	// Sync the admin account + JWT secret into the Raft FSM inline, before
	// shutting the manager down. Without this, the FSM only gets the state from
	// the boot-time goroutine after the next systemd restart — a race. The
	// server isn't running in this branch so the DB file is free to open;
	// db.Open runs idempotent migrations, so the admin table always exists
	// (a fresh node with no admin yet → SELECT returns ErrNoRows → skip). For a
	// fresh single-node bootstrap, leadership is acquired in <1s. Best-effort:
	// if db.Open fails, warn and proceed to Shutdown (the boot goroutine remains
	// a backstop) rather than aborting the init.
	if database, dbErr := db.Open(cfg.Database.Path); dbErr != nil {
		log.Printf("Warning: failed to open database for cluster sync: %v", dbErr)
	} else {
		// CLI is short-lived with no shutdown signal; the leaderWait deadline
		// still caps the wait, so a plain background context is correct here.
		syncBootstrapState(context.Background(), mgr, database, cfg.Auth.JWTSecret, 30*time.Second)
		database.Close()
	}

	mgr.Shutdown()

	fmt.Printf("Cluster '%s' initialized successfully.\n", clusterName)
	fmt.Printf("Node ID: %s\n", cfg.Cluster.NodeID)
	fmt.Println("\nNext steps:")
	fmt.Printf("  1. Restart sfpanel: %ssystemctl restart sfpanel\n", sudoPrefix())
	fmt.Printf("  2. Create a join token: %ssfpanel cluster token\n", sudoPrefix())
	fmt.Printf("  3. On other nodes: sfpanel cluster join <this-ip>:%d <token> --ca <fingerprint>\n", cfg.Cluster.GRPCPort)
	fmt.Println("     (step 2 prints this command, fingerprint included)")
}

func clusterJoin(args []string) {
	cfgPath, rest := parseCfgFlag(args)
	if len(rest) < 2 {
		fmt.Println("Usage: sfpanel cluster join <leader-address:port> <token> --ca <fingerprint> [--advertise IP] [--config PATH]")
		os.Exit(1)
	}

	leaderAddr := rest[0]
	token := rest[1]

	var advertise, caPin string
	for i := 2; i < len(rest); i++ {
		switch {
		case rest[i] == "--advertise" && i+1 < len(rest):
			advertise = rest[i+1]
			i++
		case rest[i] == "--ca" && i+1 < len(rest):
			caPin = rest[i+1]
			i++
		}
	}
	if caPin == "" {
		log.Fatal("Missing --ca: copy the join command from the leader's token page, or run 'sfpanel cluster token' on the leader.")
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	if cfg.Cluster.Enabled {
		log.Fatal("This node is already part of a cluster.")
	}

	// Try to delegate to running server
	if isServerRunning(cfg.Server.Port) {
		fmt.Println("Server is running — delegating join to live server...")
		body := map[string]string{
			"leader_address":    leaderAddr,
			"token":             token,
			"ca_pin":            caPin,
			"advertise_address": advertise,
		}
		raw, err := callLocalAPI(cfg, "POST", "/api/v1/cluster/join", body)
		if err != nil {
			log.Fatalf("Join via server failed: %v", err)
		}
		fmt.Println(string(raw))
		return
	}

	// Server not running — use JoinEngine directly (config-only mode)
	engine := &cluster.JoinEngine{
		ConfigPath: cfgPath,
		Config:     cfg,
	}
	// With the database the join makes the cluster admin this node's local
	// account, as a join through the running server does. Without it, an
	// offline join — a host with no systemd, or a stopped service — left a
	// differently named local admin working beside the cluster's.
	if database, dbErr := db.Open(cfg.Database.Path); dbErr != nil {
		log.Printf("Warning: failed to open database; the local admin account is left as is: %v", dbErr)
	} else {
		defer database.Close()
		engine.DB = database
	}

	fmt.Printf("Pre-flight check against %s...\n", leaderAddr)
	pf, err := engine.PreFlight(leaderAddr, token, caPin)
	if err != nil {
		log.Fatalf("Pre-flight failed: %v", err)
	}
	fmt.Printf("  Cluster: %s (%d/%d nodes)\n", pf.ClusterName, pf.NodeCount, pf.MaxNodes)

	if advertise == "" {
		if cfg.Cluster.AdvertiseAddress != "" {
			advertise = cfg.Cluster.AdvertiseAddress
		} else {
			advertise = pf.RecommendedIP
		}
	}
	fmt.Printf("  Advertise IP: %s (%s)\n", advertise, pf.IPReason)

	result, err := engine.Execute(leaderAddr, token, advertise, caPin)
	if err != nil {
		log.Fatalf("Join failed: %v", err)
	}

	fmt.Printf("\nSuccessfully joined cluster '%s'.\n", result.ClusterName)
	fmt.Printf("Node ID: %s\n", result.NodeID)
	fmt.Printf("\nRestart sfpanel to activate: %ssystemctl restart sfpanel\n", sudoPrefix())
}

// isServerRunning checks if the local sfpanel server is listening.
func isServerRunning(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

func clusterLeave(args []string) {
	cfgPath, _ := parseCfgFlag(args)
	cfg := loadCfgForCLI(cfgPath)

	if !cfg.Cluster.Enabled {
		log.Fatal("This node is not part of a cluster.")
	}

	// Delegate to running server to avoid Raft port conflict
	if isServerRunning(cfg.Server.Port) {
		fmt.Println("Server is running — delegating leave to live server...")
		raw, err := callLocalAPI(cfg, "POST", "/api/v1/cluster/leave", nil)
		if err != nil {
			log.Fatalf("Leave via server failed: %v", err)
		}
		fmt.Println(string(raw))
		return
	}

	// Server not running — handle locally
	if err := leaveOffline(cfg, cfgPath); err != nil {
		log.Fatalf("Leave failed: %v", err)
	}
}

// leaveOffline takes a stopped node out of its cluster. The cluster's files
// go before the config stops naming the cluster: saved as standalone first,
// a removal that then failed left ca.key and the cluster's JWT key beside a
// config that no longer said they were a cluster's, and 'agent join' took
// the server. A file that cannot be removed ends the leave there, config
// untouched, as the API's leave does.
func leaveOffline(cfg *config.Config, cfgPath string) error {
	fmt.Println("Leaving cluster...")
	mgr := cluster.NewManager(&cfg.Cluster)
	if startErr := mgr.Start(); startErr == nil {
		time.Sleep(3 * time.Second)
		switch err := mgr.Leave(); {
		case errors.Is(err, cluster.ErrLeaveUnacknowledged):
			fmt.Printf("Warning: %v\n", err)
		case err != nil:
			fmt.Printf("Warning: could not notify cluster of departure: %v\n", err)
		default:
			fmt.Println("Leader notified of departure.")
		}
		mgr.Shutdown()
	} else {
		// Without its Raft the node can ask no one to take it out; that
		// used to pass without a word.
		fmt.Printf("Warning: this node's Raft did not start (%v): %v\n", startErr, cluster.ErrLeaveUnacknowledged)
	}
	for _, d := range []struct{ label, path string }{{"cluster data", cfg.Cluster.DataDir}, {"cluster certs", cfg.Cluster.CertDir}} {
		if d.path == "" {
			continue
		}
		if err := os.RemoveAll(d.path); err != nil {
			return fmt.Errorf("remove the %s in %s: %w — the config still names the cluster; remove it by hand and run the leave again", d.label, d.path, err)
		}
		fmt.Printf("Removed %s: %s\n", d.label, d.path)
	}
	cfg.Cluster.Enabled = false
	if err := saveConfig(cfgPath, cfg); err != nil {
		return fmt.Errorf("save the config: %w", err)
	}
	fmt.Printf("Cluster left. Restart sfpanel to run in standalone mode: %ssystemctl restart sfpanel\n", sudoPrefix())
	return nil
}

// clusterOverviewEnvelope mirrors the /cluster/overview response. /cluster/status
// is leaner (no nodes[]) so we always call /overview for CLI presentation.
type clusterOverviewEnvelope struct {
	Success bool `json:"success"`
	Data    struct {
		Name      string `json:"name"`
		NodeCount int    `json:"node_count"`
		LeaderID  string `json:"leader_id"`
		Nodes     []struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			Status      string `json:"status"`
			APIAddress  string `json:"api_address"`
			GRPCAddress string `json:"grpc_address"`
			Role        string `json:"role"`
		} `json:"nodes"`
	} `json:"data"`
}

func clusterStatus(args []string) {
	cfgPath, _ := parseCfgFlag(args)
	cfg := loadCfgForCLI(cfgPath)

	if !cfg.Cluster.Enabled {
		fmt.Println("Cluster: not configured (standalone mode)")
		return
	}

	// If the server is running, query live raft role + leader + peer count
	// from /cluster/overview. The CLI cannot open Raft itself without
	// conflicting on the port, so a server outage just degrades to the
	// config-only output below with a one-line note. The name comes from the
	// same answer when there is one: a rename goes through Raft, and
	// config.yaml keeps the name this node joined with.
	running := isServerRunning(cfg.Server.Port)
	var env clusterOverviewEnvelope
	var fetchErr, parseErr error
	if running {
		var raw []byte
		if raw, fetchErr = callLocalAPI(cfg, http.MethodGet, "/api/v1/cluster/overview", nil); fetchErr == nil {
			parseErr = json.Unmarshal(raw, &env)
		}
	}
	name := cfg.Cluster.Name
	if running && fetchErr == nil && parseErr == nil && env.Data.Name != "" {
		name = env.Data.Name
	}

	fmt.Printf("Cluster: %s\n", name)
	fmt.Printf("Node ID: %s\n", cfg.Cluster.NodeID)
	fmt.Printf("Node Name: %s\n", cfg.Cluster.NodeName)
	fmt.Printf("gRPC Port: %d\n", cfg.Cluster.GRPCPort)
	fmt.Printf("Data Dir: %s\n", cfg.Cluster.DataDir)
	fmt.Printf("Advertise: %s\n", cfg.Cluster.AdvertiseAddress)

	if !running {
		fmt.Println("\n(Server not running — live role/leader/peer info unavailable.)")
		return
	}
	if fetchErr != nil {
		fmt.Printf("\n(Could not fetch live status: %v)\n", fetchErr)
		return
	}
	if parseErr != nil {
		fmt.Printf("\n(Could not parse live status: %v)\n", parseErr)
		return
	}

	var role string
	switch env.Data.LeaderID {
	case cfg.Cluster.NodeID:
		role = "Leader"
	case "":
		role = "Candidate / no leader"
	default:
		role = "Follower"
	}

	fmt.Println()
	fmt.Printf("Role: %s\n", role)
	if env.Data.LeaderID != "" {
		fmt.Printf("Current leader: %s\n", env.Data.LeaderID)
	} else {
		fmt.Println("Current leader: <none> — quorum lost or election in progress")
	}

	online, suspect, offline := 0, 0, 0
	for _, n := range env.Data.Nodes {
		switch n.Status {
		case "online":
			online++
		case "suspect":
			suspect++
		case "offline":
			offline++
		}
	}
	fmt.Printf("Peers: %d total (online=%d, suspect=%d, offline=%d)\n",
		env.Data.NodeCount, online, suspect, offline)
	fmt.Println("\n(Use 'sfpanel cluster list' for per-peer detail.)")
}

// clusterList prints a table of all cluster members with live role + health.
// Requires the local server to be running — the FSM state lives in the Raft
// log and cannot be safely read by a second process holding the same port.
func clusterList(args []string) {
	cfgPath, _ := parseCfgFlag(args)
	cfg := loadCfgForCLI(cfgPath)
	if !cfg.Cluster.Enabled {
		log.Fatal("Cluster not configured (standalone mode).")
	}
	if !isServerRunning(cfg.Server.Port) {
		log.Fatal("Local sfpanel server is not running — 'cluster list' needs the live FSM.")
	}

	raw, err := callLocalAPI(cfg, http.MethodGet, "/api/v1/cluster/overview", nil)
	if err != nil {
		log.Fatalf("Fetch cluster status: %v", err)
	}
	var env clusterOverviewEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		log.Fatalf("Parse cluster status: %v", err)
	}

	leaderID := env.Data.LeaderID
	fmt.Printf("Cluster: %s (leader=%s)\n\n", env.Data.Name, abbrevOrDash(leaderID))
	fmt.Printf("%-38s  %-22s  %-9s  %-8s  %-22s  %-22s\n",
		"NODE ID", "NAME", "ROLE", "STATUS", "API", "GRPC")
	fmt.Println(strings.Repeat("-", 130))
	for _, n := range env.Data.Nodes {
		role := "follower"
		if n.ID == leaderID {
			role = "LEADER"
		} else if n.Role == "nonvoter" {
			role = "nonvoter"
		}
		fmt.Printf("%-38s  %-22s  %-9s  %-8s  %-22s  %-22s\n",
			n.ID, truncate(n.Name, 22), role, n.Status,
			truncate(n.APIAddress, 22), truncate(n.GRPCAddress, 22))
	}
}

// clusterLeaderTransfer asks the current leader to hand off to the target.
// Useful for rolling-restart workflows: drain a node before restarting it
// instead of relying on raft election to figure it out (a noisy 15-20 s
// gap with no leader).
func clusterLeaderTransfer(args []string) {
	cfgPath, rest := parseCfgFlag(args)
	if len(rest) < 1 {
		fmt.Println("Usage: sfpanel cluster leader-transfer <target-node-id> [--config PATH]")
		os.Exit(1)
	}
	targetID := rest[0]

	cfg := loadCfgForCLI(cfgPath)
	if !cfg.Cluster.Enabled {
		log.Fatal("Cluster not configured (standalone mode).")
	}
	if !isServerRunning(cfg.Server.Port) {
		log.Fatal("Local sfpanel server is not running — leader-transfer needs the live raft loop.")
	}

	body := map[string]string{"target_node_id": targetID}
	raw, err := callLocalAPI(cfg, http.MethodPost, "/api/v1/cluster/leader-transfer", body)
	if err != nil {
		log.Fatalf("leader-transfer failed: %v", err)
	}
	fmt.Println(string(raw))
}

func abbrevOrDash(id string) string {
	if id == "" {
		return "<none>"
	}
	if len(id) > 8 {
		return id[:8] + "…"
	}
	return id
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func clusterToken(args []string) {
	cfgPath, rest := parseCfgFlag(args)
	ttl := 24 * time.Hour
	kind := cluster.KindManager
	for i := 0; i < len(rest); i++ {
		switch {
		case rest[i] == "--ttl" && i+1 < len(rest):
			d, err := time.ParseDuration(rest[i+1])
			if err != nil {
				log.Fatalf("Invalid TTL: %v", err)
			}
			ttl = d
			i++
		case rest[i] == "--agent":
			kind = cluster.KindAgent
		}
	}

	cfg := loadCfgForCLI(cfgPath)
	if !cfg.Cluster.Enabled {
		log.Fatal("Cluster not initialized. Run 'sfpanel cluster init' first.")
	}

	// Tokens live in-memory on the running server's TokenManager, so we must
	// delegate to it via the HTTP API. Spawning a separate Manager here would
	// (a) conflict on the Raft port and (b) produce tokens the real server
	// never sees.
	raw, err := callLocalAPI(cfg, http.MethodPost, "/api/v1/cluster/token", map[string]string{
		"ttl":  ttl.String(),
		"kind": string(kind),
	})
	if err != nil {
		log.Fatalf("Failed to create token: %v", err)
	}

	var envelope struct {
		Success bool `json:"success"`
		Data    struct {
			Token            string    `json:"token"`
			Kind             string    `json:"kind"`
			ExpiresAt        time.Time `json:"expires_at"`
			AdvertiseAddress string    `json:"advertise_address"`
			GRPCPort         int       `json:"grpc_port"`
			CAPin            string    `json:"ca_pin"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		log.Fatalf("Parse token response: %v\nbody: %s", err, string(raw))
	}
	minted, err := mintedTokenKind(kind, envelope.Data.Kind)
	if err != nil {
		log.Fatalf("Failed to create token: %v", err)
	}

	// A follower forwards the request to the leader, and a join has to be
	// aimed at the leader, so print the address the response carries — the
	// leader's. This node's own address was printed before, which on a
	// follower produced a command that failed with "not the cluster leader".
	addr := envelope.Data.AdvertiseAddress
	grpcPort := envelope.Data.GRPCPort
	if addr == "" {
		addr = cfg.Cluster.AdvertiseAddress
	}
	if addr == "" {
		addr = "YOUR_IP"
	}
	if grpcPort == 0 {
		grpcPort = cfg.Cluster.GRPCPort
	}

	label, instructions := "Join token", joinInstructions
	if minted == cluster.KindAgent {
		label, instructions = "Agent join token", agentJoinInstructions
	}
	fmt.Printf("%s (expires: %s):\n\n", label, envelope.Data.ExpiresAt.Format(time.RFC3339))
	fmt.Printf("  %s\n\n", envelope.Data.Token)
	fmt.Print(instructions(addr, grpcPort, envelope.Data.Token, envelope.Data.CAPin, sudoPrefix() != ""))
}

// mintedTokenKind is the kind of the token the leader made, which has to be
// the kind asked for. A leader from before agents ignores the kind it is
// asked for and names none in its answer: it made a manager token, as it
// always did, and a manager token makes the server that joins with it a full
// member holding the cluster's JWT key, admin account and CA key. Printing
// that for --agent would hand out the opposite of what was asked for.
func mintedTokenKind(asked cluster.NodeKind, reported string) (cluster.NodeKind, error) {
	minted := cluster.NodeKind(reported)
	if minted == "" {
		minted = cluster.KindManager
	}
	if minted == asked {
		return minted, nil
	}
	if reported == "" {
		return "", errors.New("the leader made a manager token, not an agent token: it runs a version from before agents, " +
			"and every manager must be upgraded before an agent can join; the token was not printed and expires unused")
	}
	return "", fmt.Errorf("the leader made a %s token, not the %s token asked for; it was not printed and expires unused", minted, asked)
}

// installScriptURL is the installer the README's one-liner fetches.
const installScriptURL = "https://raw.githubusercontent.com/svrforum/SFPanel/main/scripts/install.sh"

// joinInstructions is what `cluster token` prints for the server that is to
// join: the join command, and the installer chained to it for a server
// without SFPanel, each with sudo and as root. Proxmox VE has no sudo, root
// is its administrator, and neither has Debian installed with a root
// password: there "sudo" stops the && chain before anything is installed.
// The other server is likely the same kind as this one, so the form that
// works here comes first. --ca pins the cluster CA, so the joining server
// verifies the leader before it sends the token.
func joinInstructions(addr string, grpcPort int, token, caPin string, haveSudo bool) string {
	return joinForms(haveSudo, func(sudo string) (join, install string) {
		join = fmt.Sprintf("%ssfpanel cluster join %s:%d %s --ca %s", sudo, addr, grpcPort, token, caPin)
		return join, fmt.Sprintf("curl -fsSL %s | %sbash && %s", installScriptURL, sudo, join)
	})
}

// agentJoinInstructions is joinInstructions for an agent token: a server that
// runs SFPanel joins with `sfpanel agent join`, and a new one hands the same
// arguments to the installer (install.sh --agent), which installs and joins
// in one step.
func agentJoinInstructions(addr string, grpcPort int, token, caPin string, haveSudo bool) string {
	args := fmt.Sprintf("%s:%d %s --ca %s", addr, grpcPort, token, caPin)
	return joinForms(haveSudo, func(sudo string) (join, install string) {
		return fmt.Sprintf("%ssfpanel agent join %s", sudo, args),
			fmt.Sprintf("curl -fsSL %s | %sbash -s -- --agent %s", installScriptURL, sudo, args)
	})
}

// joinForms lays out the join and install commands forms makes, with sudo and
// as root, the form that works on this host first.
func joinForms(haveSudo bool, forms func(sudo string) (join, install string)) string {
	first, other := "sudo ", ""
	otherLabel := "As root on a server without sudo (Proxmox VE, Debian installed with a root password), leave sudo out:"
	if !haveSudo {
		first, other = "", "sudo "
		otherLabel = "On a server where you log in as a user with sudo:"
	}
	join, install := forms(first)
	otherJoin, otherInstall := forms(other)
	return fmt.Sprintf("On a server that already runs SFPanel:\n  %s\n\n"+
		"On a new server — install and join in one step:\n  %s\n\n"+
		"%s\n  %s\n  %s\n", join, install, otherLabel, otherJoin, otherInstall)
}

// lookPath is exec.LookPath, replaced in tests.
var lookPath = exec.LookPath

// sudoPrefix is what goes in front of a command printed for this host: "sudo "
// where there is sudo, nothing where there is none, as on Proxmox VE, whose
// operator is root already.
func sudoPrefix() string {
	if _, err := lookPath("sudo"); err != nil {
		return ""
	}
	return "sudo "
}

func clusterRemove(args []string) {
	cfgPath, rest := parseCfgFlag(args)
	if len(rest) < 1 {
		fmt.Println("Usage: sfpanel cluster remove <node-id> [--config PATH]")
		os.Exit(1)
	}
	nodeID := rest[0]

	cfg := loadCfgForCLI(cfgPath)
	if !cfg.Cluster.Enabled {
		log.Fatal("Cluster not initialized.")
	}

	// Delegate to running server to avoid Raft port conflict
	if isServerRunning(cfg.Server.Port) {
		fmt.Println("Server is running — delegating remove to live server...")
		raw, err := callLocalAPI(cfg, "DELETE", "/api/v1/cluster/nodes/"+nodeID, nil)
		if err != nil {
			log.Fatalf("Remove via server failed: %v", err)
		}
		fmt.Println(string(raw))
		return
	}

	// Server not running — handle locally
	mgr := cluster.NewManager(&cfg.Cluster)
	if err := mgr.Start(); err != nil {
		log.Fatalf("Failed to start cluster manager: %v", err)
	}
	time.Sleep(3 * time.Second)

	if err := mgr.RemoveNode(nodeID); err != nil {
		log.Fatalf("Failed to remove node: %v", err)
	}
	mgr.Shutdown()

	fmt.Printf("Node %s removed from cluster.\n", nodeID)
}

// clusterConvert turns a manager into an agent under its own ID, through the
// running panel: the leader checks every gate and the node goes on its own,
// so there is nothing to run offline.
func clusterConvert(args []string) {
	cfgPath, rest := parseCfgFlag(args)
	if len(rest) != 1 {
		fmt.Println("Usage: sfpanel cluster convert <node-id> [--config PATH]")
		os.Exit(1)
	}
	nodeID := rest[0]

	cfg := loadCfgForCLI(cfgPath)
	if !cfg.Cluster.Enabled {
		log.Fatal("Cluster not initialized.")
	}
	if !isServerRunning(cfg.Server.Port) {
		log.Fatal("Local sfpanel server is not running — a conversion runs on the live cluster.")
	}
	raw, err := callLocalAPI(cfg, http.MethodPost, "/api/v1/cluster/nodes/"+nodeID+"/convert", nil)
	if err != nil {
		log.Fatalf("Convert failed: %v", err)
	}
	fmt.Println(string(raw))
}

// clusterPromote turns an agent into a manager under its own ID, through the
// running panel, as clusterConvert does the other way: the leader checks the
// gates and the agent joins on its own.
func clusterPromote(args []string) {
	cfgPath, rest := parseCfgFlag(args)
	if len(rest) != 1 {
		fmt.Println("Usage: sfpanel cluster promote <node-id> [--config PATH]")
		os.Exit(1)
	}
	nodeID := rest[0]

	cfg := loadCfgForCLI(cfgPath)
	if !cfg.Cluster.Enabled {
		log.Fatal("Cluster not initialized.")
	}
	if !isServerRunning(cfg.Server.Port) {
		log.Fatal("Local sfpanel server is not running — a promotion runs on the live cluster.")
	}
	raw, err := callLocalAPI(cfg, http.MethodPost, "/api/v1/cluster/nodes/"+nodeID+"/promote", nil)
	if err != nil {
		log.Fatalf("Promote failed: %v", err)
	}
	fmt.Println(string(raw))
}

// rotateCall is the request a 'sfpanel cluster rotate' asks the running
// panel for.
type rotateCall struct {
	method, path string
}

// rotateRequest is the panel request 'sfpanel cluster rotate <what>' makes;
// anything else is answered with the usage.
func rotateRequest(args []string) (rotateCall, error) {
	if len(args) == 1 && args[0] == "jwt" {
		return rotateCall{method: http.MethodPost, path: "/api/v1/cluster/rotate/jwt"}, nil
	}
	return rotateCall{}, errors.New("usage: sfpanel cluster rotate jwt")
}

// clusterRotate replaces one of the cluster's secrets through the running
// panel, as clusterConvert does a conversion: the leader checks that every
// node can follow, and every node follows on its own.
func clusterRotate(args []string) {
	cfgPath, rest := parseCfgFlag(args)
	req, err := rotateRequest(rest)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	cfg := loadCfgForCLI(cfgPath)
	if !cfg.Cluster.Enabled {
		log.Fatal("Cluster not initialized.")
	}
	if !isServerRunning(cfg.Server.Port) {
		log.Fatal("Local sfpanel server is not running — a rotation runs on the live cluster.")
	}
	raw, err := callLocalAPI(cfg, req.method, req.path, nil)
	if err != nil {
		log.Fatalf("Rotate failed: %v", err)
	}
	fmt.Println(string(raw))
	if rest[0] == "jwt" {
		fmt.Println("Every session on every manager has ended: sign in again.")
	}
}

func saveConfig(path string, cfg *config.Config) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	return config.AtomicWriteFile(path, data, 0600)
}

func printClusterHelp() {
	fmt.Println("SFPanel Cluster Commands:")
	fmt.Println()
	fmt.Println("  sfpanel cluster init [--name NAME]        Initialize a new cluster")
	fmt.Println("  sfpanel cluster token [--ttl DURATION] [--agent]")
	fmt.Println("                                            Create a join token; --agent makes one for an agent")
	fmt.Println("  sfpanel cluster join ADDR:PORT TOKEN --ca FINGERPRINT")
	fmt.Println("                                            Join an existing cluster ('cluster token' prints the command)")
	fmt.Println("  sfpanel cluster status                    Show cluster status (live role + leader if server running)")
	fmt.Println("  sfpanel cluster list                      List all cluster members with role + health")
	fmt.Println("  sfpanel cluster leader-transfer NODE_ID   Hand off leadership to NODE_ID (for graceful drains)")
	fmt.Println("  sfpanel cluster remove NODE_ID            Remove a node")
	fmt.Println("  sfpanel cluster convert NODE_ID           Turn a manager into an agent, keeping its ID and labels")
	fmt.Println("  sfpanel cluster promote NODE_ID           Turn an agent into a manager, keeping its ID and labels")
	fmt.Println("  sfpanel cluster leave                     Leave the cluster")
	fmt.Println("  sfpanel cluster reissue-cert              Re-issue this node's cluster cert (hot reload)")
	fmt.Println("  sfpanel cluster rotate jwt                Replace the cluster's JWT key (everyone signs in again)")
	fmt.Println()
	fmt.Println("All subcommands accept --config PATH to select a config file")
	fmt.Println("(default: /etc/sfpanel/config.yaml).")
}

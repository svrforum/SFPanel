package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/svrforum/SFPanel/internal/auth"
	"github.com/svrforum/SFPanel/internal/cluster"
	"github.com/svrforum/SFPanel/internal/config"
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

	url := fmt.Sprintf("http://127.0.0.1:%d%s", cfg.Server.Port, path)
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+jwt)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("reach local sfpanel at %s: %w (is the server running?)", url, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
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

	switch args[0] {
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
	case "reissue-cert":
		clusterReissueCert(args[1:])
	default:
		fmt.Printf("Unknown cluster command: %s\n", args[0])
		printClusterHelp()
	}
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

	advertise := cfg.Cluster.AdvertiseAddress
	if advertise == "" {
		advertise = cluster.DetectFallbackIP()
		if advertise == "" {
			log.Fatal("advertise address not in config and no non-loopback IPv4 detected; pass via config manually first.")
		}
	}

	certPEM, keyPEM, err := tls.IssueNodeCert(cfg.Cluster.NodeID, []string{advertise})
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
	if err := saveConfig(cfgPath, cfg); err != nil {
		log.Printf("Warning: failed to save config: %v", err)
	}

	mgr.Shutdown()

	fmt.Printf("Cluster '%s' initialized successfully.\n", clusterName)
	fmt.Printf("Node ID: %s\n", cfg.Cluster.NodeID)
	fmt.Println("\nNext steps:")
	fmt.Println("  1. Restart sfpanel: sudo systemctl restart sfpanel")
	fmt.Println("  2. Create a join token: sfpanel cluster token")
	fmt.Println("  3. On other nodes: sfpanel cluster join <this-ip>:9444 <token>")
}

func clusterJoin(args []string) {
	cfgPath, rest := parseCfgFlag(args)
	if len(rest) < 2 {
		fmt.Println("Usage: sfpanel cluster join <leader-address:port> <token> [--advertise IP] [--config PATH]")
		os.Exit(1)
	}

	leaderAddr := rest[0]
	token := rest[1]

	var advertise string
	for i := 2; i < len(rest); i++ {
		if rest[i] == "--advertise" && i+1 < len(rest) {
			advertise = rest[i+1]
			i++
		}
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

	fmt.Printf("Pre-flight check against %s...\n", leaderAddr)
	pf, err := engine.PreFlight(leaderAddr, token)
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

	result, err := engine.Execute(leaderAddr, token, advertise)
	if err != nil {
		log.Fatalf("Join failed: %v", err)
	}

	fmt.Printf("\nSuccessfully joined cluster '%s'.\n", result.ClusterName)
	fmt.Printf("Node ID: %s\n", result.NodeID)
	fmt.Println("\nRestart sfpanel to activate: sudo systemctl restart sfpanel")
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
	fmt.Println("Leaving cluster...")

	mgr := cluster.NewManager(&cfg.Cluster)
	if startErr := mgr.Start(); startErr == nil {
		time.Sleep(3 * time.Second)
		if err := mgr.Leave(); err != nil {
			fmt.Printf("Warning: could not notify cluster of departure: %v\n", err)
		} else {
			fmt.Println("Leader notified of departure.")
		}
		mgr.Shutdown()
	}

	cfg.Cluster.Enabled = false
	if err := saveConfig(cfgPath, cfg); err != nil {
		log.Printf("Warning: failed to save config: %v", err)
	}

	if cfg.Cluster.DataDir != "" {
		if err := os.RemoveAll(cfg.Cluster.DataDir); err != nil {
			log.Printf("Warning: failed to remove cluster data: %v", err)
		} else {
			fmt.Printf("Removed cluster data: %s\n", cfg.Cluster.DataDir)
		}
	}
	if cfg.Cluster.CertDir != "" {
		if err := os.RemoveAll(cfg.Cluster.CertDir); err != nil {
			log.Printf("Warning: failed to remove cluster certs: %v", err)
		} else {
			fmt.Printf("Removed cluster certs: %s\n", cfg.Cluster.CertDir)
		}
	}

	fmt.Println("Cluster left. Restart sfpanel to run in standalone mode: sudo systemctl restart sfpanel")
}

func clusterStatus(args []string) {
	cfgPath, _ := parseCfgFlag(args)
	cfg := loadCfgForCLI(cfgPath)

	if !cfg.Cluster.Enabled {
		fmt.Println("Cluster: not configured (standalone mode)")
		return
	}

	fmt.Printf("Cluster: %s\n", cfg.Cluster.Name)
	fmt.Printf("Node ID: %s\n", cfg.Cluster.NodeID)
	fmt.Printf("Node Name: %s\n", cfg.Cluster.NodeName)
	fmt.Printf("gRPC Port: %d\n", cfg.Cluster.GRPCPort)
	fmt.Printf("Data Dir: %s\n", cfg.Cluster.DataDir)
	fmt.Printf("Advertise: %s\n", cfg.Cluster.AdvertiseAddress)
}

func clusterToken(args []string) {
	cfgPath, rest := parseCfgFlag(args)
	ttl := 24 * time.Hour
	for i := 0; i < len(rest); i++ {
		if rest[i] == "--ttl" && i+1 < len(rest) {
			d, err := time.ParseDuration(rest[i+1])
			if err != nil {
				log.Fatalf("Invalid TTL: %v", err)
			}
			ttl = d
			i++
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
		"ttl": ttl.String(),
	})
	if err != nil {
		log.Fatalf("Failed to create token: %v", err)
	}

	var envelope struct {
		Success bool `json:"success"`
		Data    struct {
			Token     string    `json:"token"`
			ExpiresAt time.Time `json:"expires_at"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		log.Fatalf("Parse token response: %v\nbody: %s", err, string(raw))
	}

	addr := cfg.Cluster.AdvertiseAddress
	if addr == "" {
		addr = "YOUR_IP"
	}
	grpcPort := cfg.Cluster.GRPCPort

	fmt.Printf("Join token (expires: %s):\n\n", envelope.Data.ExpiresAt.Format(time.RFC3339))
	fmt.Printf("  %s\n\n", envelope.Data.Token)
	fmt.Println("Join command:")
	fmt.Printf("  sfpanel cluster join %s:%d %s\n", addr, grpcPort, envelope.Data.Token)
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

func saveConfig(path string, cfg *config.Config) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	return config.AtomicWriteFile(path, data, 0644)
}

func printClusterHelp() {
	fmt.Println("SFPanel Cluster Commands:")
	fmt.Println()
	fmt.Println("  sfpanel cluster init [--name NAME]        Initialize a new cluster")
	fmt.Println("  sfpanel cluster token [--ttl DURATION]    Create a join token")
	fmt.Println("  sfpanel cluster join ADDR:PORT TOKEN      Join an existing cluster")
	fmt.Println("  sfpanel cluster status                    Show cluster status")
	fmt.Println("  sfpanel cluster remove NODE_ID            Remove a node")
	fmt.Println("  sfpanel cluster leave                     Leave the cluster")
	fmt.Println("  sfpanel cluster reissue-cert              Re-issue this node's cluster cert (hot reload)")
	fmt.Println()
	fmt.Println("All subcommands accept --config PATH to select a config file")
	fmt.Println("(default: /etc/sfpanel/config.yaml).")
}

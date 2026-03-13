package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/svrforum/SFPanel/internal/cluster"
	pb "github.com/svrforum/SFPanel/internal/cluster/proto"
	"github.com/svrforum/SFPanel/internal/config"
	"gopkg.in/yaml.v3"
)

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
		clusterLeave()
	case "status":
		clusterStatus()
	case "token":
		clusterToken(args[1:])
	case "remove":
		clusterRemove(args[1:])
	default:
		fmt.Printf("Unknown cluster command: %s\n", args[0])
		printClusterHelp()
	}
}

func clusterInit(args []string) {
	cfgPath := "/etc/sfpanel/config.yaml"
	clusterName := "sfpanel"

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--name":
			if i+1 < len(args) {
				clusterName = args[i+1]
				i++
			}
		case "--config":
			if i+1 < len(args) {
				cfgPath = args[i+1]
				i++
			}
		}
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	if cfg.Cluster.Enabled {
		log.Fatal("Cluster already initialized. Use 'sfpanel cluster status' to check.")
	}

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
	fmt.Println("  3. On other nodes: sfpanel cluster join <this-ip>:9443 <token>")
}

func clusterJoin(args []string) {
	if len(args) < 2 {
		fmt.Println("Usage: sfpanel cluster join <leader-address:port> <token>")
		os.Exit(1)
	}

	leaderAddr := args[0]
	token := args[1]

	cfgPath := "/etc/sfpanel/config.yaml"
	for i := 2; i < len(args); i++ {
		if args[i] == "--config" && i+1 < len(args) {
			cfgPath = args[i+1]
		}
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	if cfg.Cluster.Enabled {
		log.Fatal("This node is already part of a cluster.")
	}

	nodeID := uuid.New().String()
	hostname, _ := os.Hostname()

	advertise := cfg.Cluster.AdvertiseAddress
	if advertise == "" {
		// Auto-detect outbound IP instead of using 127.0.0.1
		conn, dialErr := net.Dial("udp", "8.8.8.8:80")
		if dialErr == nil {
			advertise = conn.LocalAddr().(*net.UDPAddr).IP.String()
			conn.Close()
		} else {
			advertise = "127.0.0.1"
		}
		log.Printf("No advertise_address configured, auto-detected: %s", advertise)
	}

	apiAddr := fmt.Sprintf("%s:%d", advertise, cfg.Server.Port)
	grpcAddr := fmt.Sprintf("%s:%d", advertise, cfg.Cluster.GRPCPort)

	fmt.Printf("Joining cluster at %s...\n", leaderAddr)

	client, err := cluster.DialNodeInsecure(leaderAddr)
	if err != nil {
		log.Fatalf("Failed to connect to leader: %v", err)
	}
	defer client.Close()

	resp, err := client.Join(context.Background(), &pb.JoinRequest{
		Token:       token,
		NodeId:      nodeID,
		NodeName:    hostname,
		ApiAddress:  apiAddr,
		GrpcAddress: grpcAddr,
	})
	if err != nil {
		log.Fatalf("Join failed: %v", err)
	}
	if !resp.Success {
		log.Fatalf("Join rejected: %s", resp.Error)
	}

	tlsMgr := cluster.NewTLSManager(cfg.Cluster.CertDir)
	if err := tlsMgr.SaveCACert(resp.CaCert); err != nil {
		log.Fatalf("Failed to save CA cert: %v", err)
	}
	if err := tlsMgr.SaveNodeCert(resp.NodeCert, resp.NodeKey); err != nil {
		log.Fatalf("Failed to save node cert: %v", err)
	}

	cfg.Cluster.Enabled = true
	cfg.Cluster.Name = resp.ClusterName
	cfg.Cluster.NodeID = nodeID
	cfg.Cluster.NodeName = hostname
	cfg.Cluster.AdvertiseAddress = advertise

	if err := saveConfig(cfgPath, cfg); err != nil {
		log.Printf("Warning: failed to save config: %v", err)
	}

	fmt.Printf("Successfully joined cluster '%s'.\n", resp.ClusterName)
	fmt.Printf("Node ID: %s\n", nodeID)
	fmt.Printf("Peers: %d nodes\n", len(resp.Peers))
	fmt.Println("\nRestart sfpanel to activate: sudo systemctl restart sfpanel")
}

func clusterLeave() {
	cfgPath := "/etc/sfpanel/config.yaml"
	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	if !cfg.Cluster.Enabled {
		log.Fatal("This node is not part of a cluster.")
	}

	fmt.Println("Leaving cluster...")

	cfg.Cluster.Enabled = false
	if err := saveConfig(cfgPath, cfg); err != nil {
		log.Printf("Warning: failed to save config: %v", err)
	}

	fmt.Println("Cluster left. Restart sfpanel to run in standalone mode: sudo systemctl restart sfpanel")
}

func clusterStatus() {
	cfgPath := "/etc/sfpanel/config.yaml"
	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

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
	ttl := 24 * time.Hour
	for i := 0; i < len(args); i++ {
		if args[i] == "--ttl" && i+1 < len(args) {
			d, err := time.ParseDuration(args[i+1])
			if err != nil {
				log.Fatalf("Invalid TTL: %v", err)
			}
			ttl = d
		}
	}

	cfgPath := "/etc/sfpanel/config.yaml"
	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	if !cfg.Cluster.Enabled {
		log.Fatal("Cluster not initialized. Run 'sfpanel cluster init' first.")
	}

	mgr := cluster.NewManager(&cfg.Cluster)
	if err := mgr.Start(); err != nil {
		log.Fatalf("Failed to start cluster manager: %v", err)
	}

	time.Sleep(3 * time.Second)

	token, err := mgr.CreateJoinToken(ttl)
	if err != nil {
		log.Fatalf("Failed to create token: %v", err)
	}

	mgr.Shutdown()

	addr := cfg.Cluster.AdvertiseAddress
	if addr == "" {
		addr = "YOUR_IP"
	}
	grpcPort := cfg.Cluster.GRPCPort

	fmt.Printf("Join token (expires: %s):\n\n", token.ExpiresAt.Format(time.RFC3339))
	fmt.Printf("  %s\n\n", token.Token)
	fmt.Println("Join command:")
	fmt.Printf("  sfpanel cluster join %s:%d %s\n", addr, grpcPort, token.Token)
}

func clusterRemove(args []string) {
	if len(args) < 1 {
		fmt.Println("Usage: sfpanel cluster remove <node-id>")
		os.Exit(1)
	}
	nodeID := args[0]

	cfgPath := "/etc/sfpanel/config.yaml"
	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	if !cfg.Cluster.Enabled {
		log.Fatal("Cluster not initialized.")
	}

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
	return os.WriteFile(path, data, 0644)
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
}

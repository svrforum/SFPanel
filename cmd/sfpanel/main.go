package main

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	sfpanel "github.com/svrforum/SFPanel"
	"github.com/svrforum/SFPanel/internal/api"
	"github.com/svrforum/SFPanel/internal/api/handlers"
	"github.com/svrforum/SFPanel/internal/api/middleware"
	"github.com/svrforum/SFPanel/internal/cluster"
	"github.com/svrforum/SFPanel/internal/config"
	"github.com/svrforum/SFPanel/internal/db"
	"github.com/svrforum/SFPanel/internal/docker"
	"github.com/svrforum/SFPanel/internal/monitor"
	"github.com/svrforum/SFPanel/internal/release"
)

var (
	version = "0.6.1"
	commit  = "none"
	date    = "unknown"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version":
			fmt.Printf("SFPanel %s (commit: %s, built: %s)\n", version, commit, date)
			os.Exit(0)
		case "reset":
			resetPanel()
			return
		case "update":
			updatePanel()
			return
		case "cluster":
			clusterCommand(os.Args[2:])
			return
		case "help":
			printHelp()
			return
		}
	}

	// SFPanel requires root privileges for system management operations
	// (apt, disk, swap, Docker socket, etc.)
	if os.Getuid() != 0 {
		log.Fatal("SFPanel must be run as root. Use: sudo ./sfpanel")
	}

	cfgPath := "config.yaml"
	if len(os.Args) > 1 {
		cfgPath = os.Args[1]
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Set SFPanel log source path from config
	if cfg.Log.File != "" {
		handlers.SetSFPanelLogPath(cfg.Log.File)
	}

	// Set up log file output if configured
	if cfg.Log.File != "" {
		if err := os.MkdirAll(filepath.Dir(cfg.Log.File), 0755); err != nil {
			log.Printf("Warning: failed to create log directory: %v", err)
		} else {
			logFile, err := os.OpenFile(cfg.Log.File, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
			if err != nil {
				log.Printf("Warning: failed to open log file %s: %v", cfg.Log.File, err)
			} else {
				multiWriter := io.MultiWriter(os.Stdout, logFile)
				log.SetOutput(multiWriter)
				log.Printf("Log file output enabled: %s", cfg.Log.File)
			}
		}
	}

	database, err := db.Open(cfg.Database.Path)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer func() {
		monitor.FlushPending()
		database.Close()
	}()
	log.Printf("Database ready at %s", cfg.Database.Path)

	// Start cluster manager if enabled
	var clusterMgr *cluster.Manager
	if cfg.Cluster.Enabled {
		cfg.Cluster.APIPort = cfg.Server.Port
		clusterMgr = cluster.NewManager(&cfg.Cluster)
		if err := clusterMgr.Start(); err != nil {
			log.Printf("Warning: cluster start failed: %v", err)
		} else {
			defer clusterMgr.Shutdown()
			log.Printf("Cluster mode active: %s (node: %s)", cfg.Cluster.Name, cfg.Cluster.NodeID)

			// Set version for cluster heartbeat reporting
			clusterMgr.SetVersion(version)

			// Start local metrics collection for cluster overview
			metricsDocker, _ := docker.NewClient(cfg.Docker.Socket)
			clusterMgr.StartLocalMetrics(func() (float64, float64, float64, int) {
				m, err := monitor.GetCoreMetrics()
				if err != nil {
					return 0, 0, 0, 0
				}
				diskPercent := 0.0
				if d, dErr := monitor.GetDiskPercent(); dErr == nil {
					diskPercent = d
				}
				containers := 0
				if metricsDocker != nil {
					ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
					if list, lErr := metricsDocker.ListContainers(ctx); lErr == nil {
						for _, c := range list {
							if c.State == "running" {
								containers++
							}
						}
					}
					cancel()
				}
				return m.CPU, m.MemPercent, diskPercent, containers
			})

			// Start gRPC server for cluster communication
			grpcServer, grpcErr := cluster.NewGRPCServer(clusterMgr, cfg.Server.Port)
			if grpcErr != nil {
				log.Fatalf("gRPC server setup failed (cluster requires gRPC): %v", grpcErr)
			}
			grpcAddr := fmt.Sprintf("0.0.0.0:%d", cfg.Cluster.GRPCPort)
			if startErr := grpcServer.Start(grpcAddr); startErr != nil {
				log.Fatalf("gRPC server start failed on %s (port may be in use): %v", grpcAddr, startErr)
			}
			defer grpcServer.Stop()
			middleware.SetClusterProxySecret(grpcServer.ProxySecret())
		}
	}
	// Start background metrics history collector (30s interval, 24h retention, persisted to SQLite)
	monitor.StartHistoryCollector(database)

	// Start terminal session cleanup (timeout from settings, 0 = never)
	handlers.CleanupTerminalSessions(database)

	// Restore DOCKER-USER firewall rules if previously saved
	handlers.RestoreDockerUserRules()

	// Start background update checker (polls GitHub every hour)
	monitor.StartUpdateChecker(version)

	e := api.NewRouter(database, cfg, sfpanel.WebDistFS, version, clusterMgr, cfgPath)
	e.Logger.SetOutput(log.Writer())

	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	log.Printf("SFPanel %s starting on %s", version, addr)

	// Start server in goroutine
	go func() {
		if err := e.Start(addr); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server failed: %v", err)
		}
	}()

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down server...")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := e.Shutdown(ctx); err != nil {
		log.Fatalf("Server forced shutdown: %v", err)
	}
	log.Println("Server stopped")
}

func resetPanel() {
	if os.Getuid() != 0 {
		log.Fatal("SFPanel must be run as root. Use: sudo ./sfpanel reset")
	}

	cfgPath := "config.yaml"
	if len(os.Args) > 2 {
		cfgPath = os.Args[2]
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	dbPath := cfg.Database.Path
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		fmt.Println("Nothing to reset: database not found at", dbPath)
		return
	}

	fmt.Printf("This will delete the database at %s and reset all settings.\n", dbPath)
	fmt.Print("Are you sure? (y/N): ")

	reader := bufio.NewReader(os.Stdin)
	answer, _ := reader.ReadString('\n')
	answer = strings.TrimSpace(strings.ToLower(answer))

	if answer != "y" && answer != "yes" {
		fmt.Println("Cancelled.")
		return
	}

	if err := os.Remove(dbPath); err != nil {
		log.Fatalf("Failed to delete database: %v", err)
	}
	fmt.Println("Database deleted. Run SFPanel again to start the setup wizard.")
}

func updatePanel() {
	if os.Getuid() != 0 {
		log.Fatal("SFPanel must be run as root. Use: sudo ./sfpanel update")
	}

	fmt.Println("Checking for latest version...")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get("https://api.github.com/repos/svrforum/SFPanel/releases/latest")
	if err != nil {
		log.Fatalf("Failed to check for updates: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		log.Fatalf("Failed to fetch release info (HTTP %d)", resp.StatusCode)
	}

	var ghRelease struct {
		TagName string          `json:"tag_name"`
		Assets  []release.Asset `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ghRelease); err != nil {
		log.Fatalf("Failed to parse release info: %v", err)
	}

	latest := strings.TrimPrefix(ghRelease.TagName, "v")
	if latest == version {
		fmt.Printf("Already up to date (v%s).\n", version)
		return
	}

	fmt.Printf("Current: v%s → Latest: v%s\n", version, latest)
	fmt.Print("Update now? (y/N): ")

	reader := bufio.NewReader(os.Stdin)
	answer, _ := reader.ReadString('\n')
	answer = strings.TrimSpace(strings.ToLower(answer))
	if answer != "y" && answer != "yes" {
		fmt.Println("Cancelled.")
		return
	}

	arch := runtime.GOARCH
	archiveName := fmt.Sprintf("sfpanel_%s_linux_%s.tar.gz", latest, arch)
	archiveURL := release.FindAssetURL(ghRelease.Assets, archiveName)
	checksumsURL := release.FindAssetURL(ghRelease.Assets, "checksums.txt")
	if archiveURL == "" || checksumsURL == "" {
		log.Fatal("Required release assets not found (archive or checksums.txt)")
	}
	fmt.Printf("Downloading SFPanel v%s (%s)...\n", latest, arch)

	dlClient := &http.Client{Timeout: 5 * time.Minute}
	dlResp, err := dlClient.Get(archiveURL)
	if err != nil {
		log.Fatalf("Download failed: %v", err)
	}
	defer dlResp.Body.Close()

	if dlResp.StatusCode != 200 {
		log.Fatalf("Download failed (HTTP %d)", dlResp.StatusCode)
	}

	checksumResp, err := dlClient.Get(checksumsURL)
	if err != nil {
		log.Fatalf("Checksum download failed: %v", err)
	}
	defer checksumResp.Body.Close()
	if checksumResp.StatusCode != 200 {
		log.Fatalf("Checksum download failed (HTTP %d)", checksumResp.StatusCode)
	}
	checksumBody, err := io.ReadAll(checksumResp.Body)
	if err != nil {
		log.Fatalf("Checksum read failed: %v", err)
	}
	expectedSHA256, err := release.ParseExpectedSHA256(checksumBody, archiveName)
	if err != nil {
		log.Fatalf("Checksum verification failed: %v", err)
	}

	archiveData, err := io.ReadAll(dlResp.Body)
	if err != nil {
		log.Fatalf("Failed to read archive: %v", err)
	}
	actualSHA256 := fmt.Sprintf("%x", sha256.Sum256(archiveData))
	if actualSHA256 != expectedSHA256 {
		log.Fatal("Checksum verification failed")
	}

	gzr, err := gzip.NewReader(bytes.NewReader(archiveData))
	if err != nil {
		log.Fatalf("Failed to decompress: %v", err)
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	var binaryData []byte
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Fatalf("Failed to read archive: %v", err)
		}
		if hdr.Name == "sfpanel" {
			binaryData, err = io.ReadAll(tr)
			if err != nil {
				log.Fatalf("Failed to read binary: %v", err)
			}
			break
		}
	}
	if binaryData == nil {
		log.Fatal("Binary not found in archive")
	}

	execPath, err := os.Executable()
	if err != nil {
		log.Fatalf("Failed to find current binary path: %v", err)
	}

	tmpPath := execPath + ".new"
	if err := os.WriteFile(tmpPath, binaryData, 0755); err != nil {
		log.Fatalf("Failed to write new binary: %v", err)
	}

	if err := os.Rename(tmpPath, execPath); err != nil {
		os.Remove(tmpPath)
		log.Fatalf("Failed to replace binary: %v", err)
	}

	fmt.Printf("Updated to v%s.\n", latest)

	// Restart systemd service if active
	if err := exec.Command("systemctl", "is-active", "--quiet", "sfpanel").Run(); err == nil {
		fmt.Println("Restarting sfpanel service...")
		if err := exec.Command("systemctl", "restart", "sfpanel").Run(); err != nil {
			log.Printf("Failed to restart service: %v (restart manually with: systemctl restart sfpanel)", err)
		} else {
			fmt.Println("Service restarted.")
		}
	}
}

func printHelp() {
	fmt.Printf("SFPanel %s - Server Management Panel\n\n", version)
	fmt.Println("Usage:")
	fmt.Println("  sfpanel [config.yaml]    Start the panel")
	fmt.Println("  sfpanel version          Show version info")
	fmt.Println("  sfpanel update           Download and install latest version")
	fmt.Println("  sfpanel reset            Delete database and reset to setup wizard")
	fmt.Println("  sfpanel cluster <cmd>    Cluster management (init/join/leave/status/token/remove)")
	fmt.Println("  sfpanel help             Show this help")
}

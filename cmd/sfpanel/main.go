package main

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"

	sfpanel "github.com/sfpanel/sfpanel"
	"github.com/sfpanel/sfpanel/internal/api"
	"github.com/sfpanel/sfpanel/internal/api/handlers"
	"github.com/sfpanel/sfpanel/internal/config"
	"github.com/sfpanel/sfpanel/internal/db"
	"github.com/sfpanel/sfpanel/internal/monitor"
)

var (
	version = "0.2.0"
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

	database, err := db.Open(cfg.Database.Path)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer database.Close()
	log.Printf("Database ready at %s", cfg.Database.Path)

	// Start background metrics history collector (30s interval, 24h retention)
	monitor.StartHistoryCollector()

	// Start terminal session cleanup (timeout from settings, 0 = never)
	handlers.CleanupTerminalSessions(database)

	e := api.NewRouter(database, cfg, sfpanel.WebDistFS)

	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	log.Printf("SFPanel %s starting on %s", version, addr)
	if err := e.Start(addr); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
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

	resp, err := http.Get("https://api.github.com/repos/sfpanel/sfpanel/releases/latest")
	if err != nil {
		log.Fatalf("Failed to check for updates: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		log.Fatalf("Failed to fetch release info (HTTP %d)", resp.StatusCode)
	}

	var release struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		log.Fatalf("Failed to parse release info: %v", err)
	}

	latest := strings.TrimPrefix(release.TagName, "v")
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
	url := fmt.Sprintf("https://github.com/sfpanel/sfpanel/releases/download/v%s/sfpanel_%s_linux_%s.tar.gz", latest, latest, arch)
	fmt.Printf("Downloading SFPanel v%s (%s)...\n", latest, arch)

	dlResp, err := http.Get(url)
	if err != nil {
		log.Fatalf("Download failed: %v", err)
	}
	defer dlResp.Body.Close()

	if dlResp.StatusCode != 200 {
		log.Fatalf("Download failed (HTTP %d)", dlResp.StatusCode)
	}

	gzr, err := gzip.NewReader(dlResp.Body)
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
	fmt.Println("  sfpanel help             Show this help")
}

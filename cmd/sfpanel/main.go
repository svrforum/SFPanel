package main

import (
	"fmt"
	"log"
	"os"

	sfpanel "github.com/sfpanel/sfpanel"
	"github.com/sfpanel/sfpanel/internal/api"
	"github.com/sfpanel/sfpanel/internal/api/handlers"
	"github.com/sfpanel/sfpanel/internal/config"
	"github.com/sfpanel/sfpanel/internal/db"
	"github.com/sfpanel/sfpanel/internal/monitor"
)

var (
	version = "0.1.0"
	commit  = "none"
	date    = "unknown"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Printf("SFPanel %s (commit: %s, built: %s)\n", version, commit, date)
		os.Exit(0)
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

package api

import (
	"context"
	"database/sql"
	"embed"
	"log/slog"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
	echoMw "github.com/labstack/echo/v4/middleware"
	mw "github.com/svrforum/SFPanel/internal/api/middleware"
	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/cluster"
	commonExec "github.com/svrforum/SFPanel/internal/common/exec"
	"github.com/svrforum/SFPanel/internal/config"
	sfdb "github.com/svrforum/SFPanel/internal/db"
	"github.com/svrforum/SFPanel/internal/docker"
	featureAudit "github.com/svrforum/SFPanel/internal/feature/audit"
	featureCron "github.com/svrforum/SFPanel/internal/feature/cron"
	featureDisk "github.com/svrforum/SFPanel/internal/feature/disk"
	featureAppstore "github.com/svrforum/SFPanel/internal/feature/appstore"
	featureAuth "github.com/svrforum/SFPanel/internal/feature/auth"
	featureCluster "github.com/svrforum/SFPanel/internal/feature/cluster"
	featureCompose "github.com/svrforum/SFPanel/internal/feature/compose"
	featureDocker "github.com/svrforum/SFPanel/internal/feature/docker"
	featureFiles "github.com/svrforum/SFPanel/internal/feature/files"
	featureFirewall "github.com/svrforum/SFPanel/internal/feature/firewall"
	featureLogs "github.com/svrforum/SFPanel/internal/feature/logs"
	featureMonitor "github.com/svrforum/SFPanel/internal/feature/monitor"
	featureNetwork "github.com/svrforum/SFPanel/internal/feature/network"
	featurePackages "github.com/svrforum/SFPanel/internal/feature/packages"
	"github.com/svrforum/SFPanel/internal/feature/portmap"
	featureProcess "github.com/svrforum/SFPanel/internal/feature/process"
	featureServices "github.com/svrforum/SFPanel/internal/feature/services"
	featureSettings "github.com/svrforum/SFPanel/internal/feature/settings"
	featureSystem "github.com/svrforum/SFPanel/internal/feature/system"
	featureAlert "github.com/svrforum/SFPanel/internal/feature/alert"
	featureTerminal "github.com/svrforum/SFPanel/internal/feature/terminal"
	featureWS "github.com/svrforum/SFPanel/internal/feature/websocket"
)

// healthHandler reports readiness (not just liveness): 200 when the SQLite
// store is reachable, 503 when a ping fails (locked / unwritable / corrupt DB),
// so a reverse proxy / load balancer / uptime monitor sees the panel as
// unhealthy even though the process is alive. Bounded so a stuck DB can't hang
// the probe. Stays public (no auth) — it's consumed by infra, not the SPA.
func healthHandler(db *sql.DB, version string) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx, cancel := context.WithTimeout(c.Request().Context(), 2*time.Second)
		defer cancel()
		if err := db.PingContext(ctx); err != nil {
			return Fail(c, http.StatusServiceUnavailable, response.ErrInternalError, "database unreachable")
		}
		return OK(c, map[string]string{"status": "ok", "version": version})
	}
}

// NewRouter wires the HTTP server and returns both the Echo instance and a
// cleanup function. The cleanup function is currently a no-op (see
// noopCleanup): all background workers are owned and torn down by
// cmd/sfpanel via the shared bgCtx, not by the router. It is still returned
// and called during graceful shutdown so the sequence stays stable if
// router-owned teardown is ever reintroduced.
//
// auditWriter is the shared *db.AsyncWriter used by the audit middleware and
// auth security-events to serialise INSERTs onto one background drain.
func NewRouter(database *sql.DB, auditWriter *sfdb.AsyncWriter, alertManager *featureAlert.Manager, cfg *config.Config, webFS embed.FS, version string, clusterMgr *cluster.Manager, cfgPath string, liveActivate cluster.LiveActivateFunc) (*echo.Echo, func()) {
	e := echo.New()
	e.HideBanner = true

	// Source-IP extraction: trust X-Forwarded-For / X-Real-IP only when the
	// request arrives from an allowlisted upstream. Without this, a client
	// can set X-Forwarded-For: <victim-IP> on /auth/login and bypass the
	// per-IP rate limiter. Defaults to localhost-only so the same-host
	// reverse-proxy case keeps working out of the box.
	trusted := cfg.Server.TrustedProxies
	if len(trusted) == 0 {
		trusted = []string{"127.0.0.0/8", "::1/128"}
	}
	trustOpts := []echo.TrustOption{}
	for _, cidr := range trusted {
		// echo.TrustIPRange accepts a *net.IPNet; parse defensively so a
		// malformed entry doesn't take the panel down.
		if _, ipnet, err := parseCIDROrIP(cidr); err == nil {
			trustOpts = append(trustOpts, echo.TrustIPRange(ipnet))
		} else {
			// A silently dropped entry shrinks the trust list: requests via
			// that proxy then rate-limit on the proxy's own IP instead of
			// the real client's. Make the misconfiguration diagnosable.
			slog.Warn("ignoring unparseable trusted_proxies entry", "entry", cidr, "error", err)
		}
	}
	e.IPExtractor = echo.ExtractIPFromXFFHeader(trustOpts...)

	e.Use(echoMw.Recover())
	e.Use(mw.SecurityHeaders())
	e.Use(echoMw.GzipWithConfig(echoMw.GzipConfig{
		Level:   5,
		MinLength: 1024,
	}))
	e.Use(mw.RequestLogger())
	corsOrigins := []string{
		"http://localhost:5173",
		"tauri://localhost",
		"http://tauri.localhost",
		"https://tauri.localhost",
	}
	corsOrigins = append(corsOrigins, cfg.Server.AllowedOrigins...)
	e.Use(echoMw.CORSWithConfig(echoMw.CORSConfig{
		AllowOrigins: corsOrigins,
		AllowMethods: []string{"GET", "POST", "PUT", "DELETE", "PATCH"},
		AllowHeaders: []string{"Authorization", "Content-Type", "X-CSRF-Token"},
	}))

	cmd := commonExec.NewCommander()

	authHandler := &featureAuth.Handler{DB: database, Config: cfg, ClusterMgr: clusterMgr, AuditWriter: auditWriter}
	dashboardHandler := &featureMonitor.Handler{Version: version}

	systemHandler := &featureSystem.Handler{
		Version:     version,
		DB:          database,
		DBPath:      cfg.Database.Path,
		ConfigPath:  cfgPath,
		ComposePath: cfg.Server.StacksPath,
		// Port lets the update flow point its rollback watchdog at the
		// local health endpoint after a binary swap.
		Port:       cfg.Server.Port,
		Cmd:        cmd,
		ClusterMgr: clusterMgr,
	}

	// Initialize Docker client
	dockerClient, err := docker.NewClient(cfg.Docker.Socket)
	if err != nil {
		slog.Warn("Docker not available", "error", err)
	}

	var dockerHandler *featureDocker.Handler
	if dockerClient != nil {
		dockerHandler = &featureDocker.Handler{Docker: dockerClient, DB: database}
	}

	// Unified port map handler — Cmd for ufw/ss, Docker (nil-safe) for DNAT.
	portmapHandler := &portmap.Handler{Cmd: cmd, Docker: dockerClient}

	// Initialize Compose manager — scans cfg.Server.StacksPath for compose projects
	composeManager := docker.NewComposeManager(cfg.Server.StacksPath, dockerClient)
	composeHandler := &featureCompose.Handler{Compose: composeManager, DB: database, ComposePath: cfg.Server.StacksPath}
	composeHandler.SetClusterMgr(clusterMgr) // boot-time manager (nil if cluster disabled); updated on runtime activation below

	v1 := e.Group("/api/v1")

	// Public routes
	v1.GET("/health", healthHandler(database, version))
	v1.POST("/auth/login", authHandler.Login)
	v1.GET("/auth/setup-status", authHandler.GetSetupStatus)
	v1.POST("/auth/setup", authHandler.SetupAdmin)
	// Refresh is public (consumed by clients holding a refresh token, not
	// an access JWT). The token itself is the credential; auth is the
	// presence of a matching DB row.
	v1.POST("/auth/refresh", authHandler.Refresh)

	// Cluster handler — created here so it can be referenced by the proxy
	// middleware's getter below. Route registration happens further down.
	clusterHandler := &featureCluster.Handler{
		Manager:      clusterMgr,
		Config:       cfg,
		ConfigPath:   cfgPath,
		DB:           database,
		LiveActivate: liveActivate,
		OnManagerActivated: func(m *cluster.Manager) {
			authHandler.SetClusterMgr(m)
			composeHandler.SetClusterMgr(m)
		},
	}
	// Boot path wires Manager via the struct literal above, bypassing
	// setManager — so register the manager-dependent callbacks (disband
	// self-clean) explicitly. Without this a node that booted into an existing
	// cluster never honors a replicated CmdDisband. The FSM's replay-index
	// guard makes this safe even though it runs after Raft replay has begun.
	clusterHandler.ActivateBootManager()

	// Protected routes
	authorized := v1.Group("")
	authorized.Use(mw.JWTMiddleware(cfg.Auth.JWTSecret))
	authorized.Use(mw.CSRFProtect())
	// Resolve the cluster manager dynamically so init-at-runtime becomes
	// effective without a service restart. The clusterHandler keeps the
	// authoritative live pointer; we route all middleware reads through it.
	authorized.Use(mw.ClusterProxyMiddleware(func() *cluster.Manager {
		return clusterHandler.GetManager()
	}))
	// Resolve the local node ID lazily so a node that joined a cluster mid-
	// process (after the middleware chain was built) starts stamping
	// audit rows correctly without a restart. Mirrors the same dynamic-
	// resolution pattern as ClusterProxyMiddleware above.
	localNodeIDFn := func() string {
		if m := clusterHandler.GetManager(); m != nil {
			return m.LocalNodeID()
		}
		return ""
	}
	authorized.Use(mw.AuditMiddleware(auditWriter, localNodeIDFn))
	// Settings
	settingsHandler := &featureSettings.Handler{DB: database}
	authorized.GET("/settings", settingsHandler.GetSettings)
	authorized.PUT("/settings", settingsHandler.UpdateSettings)

	authorized.GET("/auth/2fa/status", authHandler.Get2FAStatus)
	authorized.POST("/auth/2fa/setup", authHandler.Setup2FA)
	authorized.POST("/auth/2fa/verify", authHandler.Verify2FA)
	authorized.DELETE("/auth/2fa", authHandler.Disable2FA)
	authorized.POST("/auth/2fa/recovery", authHandler.RegenerateRecoveryCodes)
	authorized.GET("/auth/2fa/recovery/status", authHandler.GetRecoveryStatus)
	authorized.POST("/auth/change-password", authHandler.ChangePassword)
	authorized.POST("/auth/ws-ticket", authHandler.MintWSTicket)
	authorized.POST("/auth/logout", authHandler.Logout)
	authorized.GET("/system/info", dashboardHandler.GetSystemInfo)
	authorized.GET("/system/metrics-history", dashboardHandler.GetMetricsHistory)
	authorized.GET("/system/overview", dashboardHandler.GetOverview)

	// System management (update, backup, restore)
	// System tuning
	tuningHandler := &featureSystem.TuningHandler{Cmd: cmd}
	authorized.GET("/system/tuning", tuningHandler.GetTuningStatus)
	authorized.POST("/system/tuning/apply", tuningHandler.ApplyTuning)
	authorized.POST("/system/tuning/confirm", tuningHandler.ConfirmTuning)
	authorized.POST("/system/tuning/reset", tuningHandler.ResetTuning)

	// App Store
	appStoreHandler := &featureAppstore.Handler{
		DB:          database,
		ComposePath: cfg.Server.StacksPath,
		Cmd:         cmd,
	}
	appStore := authorized.Group("/appstore")
	appStore.GET("/categories", appStoreHandler.GetCategories)
	appStore.GET("/apps", appStoreHandler.ListApps)
	appStore.GET("/apps/:id", appStoreHandler.GetApp)
	appStore.POST("/apps/:id/install", appStoreHandler.InstallApp)
	appStore.DELETE("/apps/:id", appStoreHandler.UninstallApp)
	appStore.GET("/installed", appStoreHandler.GetInstalled)
	appStore.POST("/refresh", appStoreHandler.RefreshCache)
	appStore.GET("/status", appStoreHandler.GetStatus)

	authorized.GET("/system/update-check", systemHandler.CheckUpdate)
	authorized.POST("/system/update", systemHandler.RunUpdate)
	authorized.POST("/system/backup", systemHandler.CreateBackup)
	authorized.POST("/system/restore", systemHandler.RestoreBackup)
	authorized.GET("/system/backup/schedule", systemHandler.GetBackupSchedule)
	authorized.PUT("/system/backup/schedule", systemHandler.UpdateBackupSchedule)
	authorized.POST("/system/backup/schedule/run", systemHandler.RunBackupNow)
	authorized.GET("/system/backup/files/download", systemHandler.DownloadBackupFile)
	authorized.DELETE("/system/backup/files", systemHandler.DeleteBackupFile)
	authorized.GET("/system/portmap", portmapHandler.GetPortMap)

	// Cluster management (handler already created above so the proxy
	// middleware can reference it for dynamic manager resolution)
	clusterGroup := authorized.Group("/cluster")
	clusterGroup.GET("/status", clusterHandler.GetStatus)
	clusterGroup.GET("/overview", clusterHandler.GetOverview)
	clusterGroup.GET("/nodes", clusterHandler.GetNodes)
	clusterGroup.POST("/token", clusterHandler.CreateToken)
	clusterGroup.GET("/tokens", clusterHandler.ListTokens)
	clusterGroup.DELETE("/tokens/:id", clusterHandler.RevokeToken)
	clusterGroup.DELETE("/nodes/:id", clusterHandler.RemoveNode)
	clusterGroup.PATCH("/nodes/:id/labels", clusterHandler.UpdateNodeLabels)
	clusterGroup.PATCH("/nodes/:id/address", clusterHandler.UpdateNodeAddress)
	clusterGroup.GET("/events", clusterHandler.GetEvents)
	clusterGroup.POST("/leader-transfer", clusterHandler.TransferLeadership)
	clusterGroup.POST("/init", clusterHandler.InitCluster)
	clusterGroup.POST("/join", clusterHandler.JoinCluster)
	clusterGroup.POST("/leave", clusterHandler.LeaveCluster)
	clusterGroup.POST("/disband", clusterHandler.DisbandCluster)
	clusterGroup.GET("/interfaces", clusterHandler.GetNetworkInterfaces)
	clusterGroup.POST("/update", clusterHandler.ClusterUpdate)

	// Terminal: list the caller's live PTY sessions so the UI can reattach to a
	// preserved shell (scrollback included) instead of always opening a new one.
	authorized.GET("/terminal/sessions", featureTerminal.ListSessions)

	// Audit logs
	auditHandler := &featureAudit.Handler{DB: database, LocalNodeIDFn: localNodeIDFn}
	authorized.GET("/audit/logs", auditHandler.ListAuditLogs)
	authorized.DELETE("/audit/logs", auditHandler.ClearAuditLogs)

	// Alert system. The manager owns a background goroutine that periodically
	// evaluates rules; it's owned by main.go (started/stopped there) so the
	// docker observability dispatcher can share the same instance.
	alertHandler := &featureAlert.Handler{DB: database, Manager: alertManager}
	alerts := authorized.Group("/alerts")
	alerts.GET("/channels", alertHandler.ListChannels)
	alerts.POST("/channels", alertHandler.CreateChannel)
	alerts.PUT("/channels/:id", alertHandler.UpdateChannel)
	alerts.DELETE("/channels/:id", alertHandler.DeleteChannel)
	alerts.POST("/channels/:id/test", alertHandler.TestChannel)
	alerts.GET("/rules", alertHandler.ListRules)
	alerts.POST("/rules", alertHandler.CreateRule)
	alerts.PUT("/rules/:id", alertHandler.UpdateRule)
	alerts.DELETE("/rules/:id", alertHandler.DeleteRule)
	alerts.GET("/history", alertHandler.ListHistory)
	alerts.DELETE("/history", alertHandler.ClearHistory)

	// Processes
	processesHandler := &featureProcess.Handler{}
	authorized.GET("/system/processes", processesHandler.TopProcesses)
	authorized.GET("/system/processes/list", processesHandler.ListProcesses)
	authorized.POST("/system/processes/:pid/kill", processesHandler.KillProcess)
	authorized.POST("/system/processes/:pid/renice", processesHandler.ReniceProcess)

	// Systemd services
	servicesHandler := &featureServices.Handler{Cmd: cmd}
	authorized.GET("/system/services", servicesHandler.ListServices)
	authorized.POST("/system/services/:name/start", servicesHandler.StartService)
	authorized.POST("/system/services/:name/stop", servicesHandler.StopService)
	authorized.POST("/system/services/:name/restart", servicesHandler.RestartService)
	authorized.POST("/system/services/:name/enable", servicesHandler.EnableService)
	authorized.POST("/system/services/:name/disable", servicesHandler.DisableService)
	authorized.GET("/system/services/:name/logs", servicesHandler.ServiceLogs)
	authorized.GET("/system/services/:name/deps", servicesHandler.GetServiceDeps)
	authorized.GET("/system/services/:name/cat", servicesHandler.GetServiceUnit)

	// File manager routes
	filesHandler := &featureFiles.Handler{DB: database}
	files := authorized.Group("/files")
	files.GET("", filesHandler.ListDir)
	files.GET("/read", filesHandler.ReadFile)
	files.POST("/write", filesHandler.WriteFile)
	files.POST("/mkdir", filesHandler.MkDir)
	files.DELETE("", filesHandler.DeletePath)
	files.POST("/rename", filesHandler.RenamePath)
	files.POST("/copy", filesHandler.CopyPath)
	files.GET("/search", filesHandler.SearchFiles)
	files.GET("/download", filesHandler.DownloadFile)
	files.POST("/upload", filesHandler.UploadFile)

	// Cron job management routes
	cronHandler := &featureCron.Handler{Cmd: cmd}
	cron := authorized.Group("/cron")
	cron.GET("", cronHandler.ListJobs)
	cron.POST("", cronHandler.CreateJob)
	cron.PUT("/:id", cronHandler.UpdateJob)
	cron.DELETE("/:id", cronHandler.DeleteJob)
	cron.POST("/:id/run", cronHandler.RunJob)
	cron.GET("/logs", cronHandler.GetLogs)

	// Log viewer routes
	logsHandler := &featureLogs.Handler{DB: database}
	logsHandler.SetSFPanelLogPath(cfg.Log.File)
	logs := authorized.Group("/logs")
	logs.GET("/sources", logsHandler.ListSources)
	logs.GET("/read", logsHandler.ReadLog)
	logs.POST("/custom-sources", logsHandler.AddCustomSource)
	logs.DELETE("/custom-sources/:id", logsHandler.DeleteCustomSource)

	// Network
	networkHandler := &featureNetwork.Handler{Cmd: cmd}
	net := authorized.Group("/network")
	net.GET("/status", networkHandler.GetNetworkStatus)
	net.GET("/interfaces", networkHandler.ListInterfaces)
	net.GET("/interfaces/:name", networkHandler.GetInterface)
	net.PUT("/interfaces/:name", networkHandler.ConfigureInterface)
	net.POST("/apply", networkHandler.ApplyNetplan)
	net.GET("/dns", networkHandler.GetDNS)
	net.PUT("/dns", networkHandler.ConfigureDNS)
	net.GET("/routes", networkHandler.GetRoutes)
	net.GET("/bonds", networkHandler.ListBonds)
	net.POST("/bonds", networkHandler.CreateBond)
	net.DELETE("/bonds/:name", networkHandler.DeleteBond)

	// WireGuard VPN
	wireguardHandler := &featureNetwork.WireGuardHandler{Cmd: cmd}
	wg := authorized.Group("/network/wireguard")
	wg.GET("/status", wireguardHandler.GetStatus)
	wg.POST("/install", wireguardHandler.Install)
	wg.GET("/interfaces", wireguardHandler.ListInterfaces)
	wg.GET("/interfaces/:name", wireguardHandler.GetInterface)
	wg.POST("/interfaces/:name/up", wireguardHandler.InterfaceUp)
	wg.POST("/interfaces/:name/down", wireguardHandler.InterfaceDown)
	wg.POST("/configs", wireguardHandler.CreateConfig)
	wg.GET("/configs/:name", wireguardHandler.GetConfig)
	wg.PUT("/configs/:name", wireguardHandler.UpdateConfig)
	wg.DELETE("/configs/:name", wireguardHandler.DeleteConfig)
	wg.POST("/keypair", wireguardHandler.GenerateKeypair)
	wg.POST("/configs/:name/peers", wireguardHandler.AddPeer)
	wg.DELETE("/configs/:name/peers", wireguardHandler.RemovePeer)
	wg.POST("/configs/:name/autostart", wireguardHandler.SetAutostart)

	// Tailscale VPN
	tailscaleHandler := &featureNetwork.TailscaleHandler{Cmd: cmd}
	ts := authorized.Group("/network/tailscale")
	ts.GET("/status", tailscaleHandler.GetStatus)
	ts.POST("/install", tailscaleHandler.Install)
	ts.POST("/up", tailscaleHandler.Up)
	ts.POST("/down", tailscaleHandler.Down)
	ts.POST("/logout", tailscaleHandler.Logout)
	ts.GET("/peers", tailscaleHandler.ListPeers)
	ts.PUT("/preferences", tailscaleHandler.SetPreferences)
	ts.GET("/update-check", tailscaleHandler.CheckUpdate)

	// Disk management
	diskHandler := &featureDisk.Handler{Cmd: cmd}
	disks := authorized.Group("/disks")
	disks.GET("/overview", diskHandler.ListDisks)
	disks.GET("/iostat", diskHandler.GetIOStats)
	disks.POST("/usage", diskHandler.GetDiskUsage)
	disks.GET("/smartmontools-status", diskHandler.CheckSmartmontools)
	disks.POST("/install-smartmontools", diskHandler.InstallSmartmontools)
	disks.GET("/:device/smart", diskHandler.GetSmartInfo)
	disks.POST("/:device/smart/test", diskHandler.RunSmartTest)
	disks.GET("/:device/partitions", diskHandler.ListPartitions)
	disks.POST("/:device/partitions", diskHandler.CreatePartition)
	disks.DELETE("/:device/partitions/:number", diskHandler.DeletePartition)

	// Filesystems
	fsGroup := authorized.Group("/filesystems")
	fsGroup.GET("", diskHandler.ListFilesystems)
	fsGroup.POST("/format", diskHandler.FormatPartition)
	fsGroup.POST("/mount", diskHandler.MountFilesystem)
	fsGroup.POST("/unmount", diskHandler.UnmountFilesystem)
	fsGroup.POST("/resize", diskHandler.ResizeFilesystem)
	fsGroup.GET("/expand-check", diskHandler.CheckExpandable)
	fsGroup.POST("/expand", diskHandler.ExpandFilesystem)

	// LVM
	lvm := authorized.Group("/lvm")
	lvm.GET("/pvs", diskHandler.ListPVs)
	lvm.GET("/vgs", diskHandler.ListVGs)
	lvm.GET("/lvs", diskHandler.ListLVs)
	lvm.POST("/pvs", diskHandler.CreatePV)
	lvm.POST("/vgs", diskHandler.CreateVG)
	lvm.POST("/lvs", diskHandler.CreateLV)
	lvm.DELETE("/pvs/:name", diskHandler.RemovePV)
	lvm.DELETE("/vgs/:name", diskHandler.RemoveVG)
	lvm.DELETE("/lvs/:vg/:name", diskHandler.RemoveLV)
	lvm.POST("/lvs/resize", diskHandler.ResizeLV)

	// RAID
	raid := authorized.Group("/raid")
	raid.GET("", diskHandler.ListRAID)
	raid.GET("/:name", diskHandler.GetRAIDDetail)
	raid.POST("", diskHandler.CreateRAID)
	raid.DELETE("/:name", diskHandler.DeleteRAID)
	raid.POST("/:name/add", diskHandler.AddRAIDDisk)
	raid.POST("/:name/remove", diskHandler.RemoveRAIDDisk)

	// Swap
	swap := authorized.Group("/swap")
	swap.GET("", diskHandler.GetSwapInfo)
	swap.POST("", diskHandler.CreateSwap)
	swap.DELETE("", diskHandler.RemoveSwap)
	swap.PUT("/swappiness", diskHandler.SetSwappiness)
	swap.GET("/resize-check", diskHandler.CheckSwapResize)
	swap.PUT("/resize", diskHandler.ResizeSwap)

	// Firewall management (UFW)
	firewallHandler := &featureFirewall.Handler{Cmd: cmd, PanelPort: cfg.Server.Port}
	fw := authorized.Group("/firewall")
	fw.GET("/status", firewallHandler.GetUFWStatus)
	fw.POST("/enable", firewallHandler.EnableUFW)
	fw.POST("/disable", firewallHandler.DisableUFW)
	fw.GET("/rules", firewallHandler.ListRules)
	fw.POST("/rules", firewallHandler.AddRule)
	fw.DELETE("/rules/:number", firewallHandler.DeleteRule)
	fw.GET("/ports", firewallHandler.ListPorts)
	fw.GET("/docker", firewallHandler.GetDockerFirewall)
	fw.POST("/docker/rules", firewallHandler.AddDockerUserRule)
	fw.DELETE("/docker/rules/:number", firewallHandler.DeleteDockerUserRule)

	// Fail2ban
	f2b := authorized.Group("/fail2ban")
	f2b.GET("/status", firewallHandler.GetFail2banStatus)
	f2b.POST("/install", firewallHandler.InstallFail2ban)
	f2b.GET("/templates", firewallHandler.GetJailTemplates)
	f2b.GET("/jails", firewallHandler.ListJails)
	f2b.POST("/jails", firewallHandler.CreateJail)
	f2b.DELETE("/jails/:name", firewallHandler.DeleteJail)
	f2b.GET("/jails/:name", firewallHandler.GetJailDetail)
	f2b.POST("/jails/:name/enable", firewallHandler.EnableJail)
	f2b.POST("/jails/:name/disable", firewallHandler.DisableJail)
	f2b.PUT("/jails/:name/config", firewallHandler.UpdateJailConfig)
	f2b.POST("/jails/:name/unban", firewallHandler.UnbanIP)

	// Package management routes
	packagesHandler := &featurePackages.Handler{Cmd: cmd}
	packages := authorized.Group("/packages")
	packages.GET("/updates", packagesHandler.CheckUpdates)
	packages.POST("/upgrade", packagesHandler.UpgradePackages)
	packages.POST("/install", packagesHandler.InstallPackage)
	packages.POST("/remove", packagesHandler.RemovePackage)
	packages.GET("/search", packagesHandler.SearchPackages)
	packages.GET("/docker-status", packagesHandler.GetDockerStatus)
	packages.POST("/install-docker", packagesHandler.InstallDocker)
	packages.GET("/node-status", packagesHandler.GetNodeStatus)
	packages.POST("/install-node", packagesHandler.InstallNode)
	packages.GET("/node-versions", packagesHandler.GetNodeVersions)
	packages.POST("/node-switch", packagesHandler.SwitchNodeVersion)
	packages.POST("/node-install-version", packagesHandler.InstallNodeVersion)
	packages.POST("/node-uninstall-version", packagesHandler.UninstallNodeVersion)
	packages.GET("/claude-status", packagesHandler.GetClaudeStatus)
	packages.POST("/install-claude", packagesHandler.InstallClaude)
	packages.GET("/codex-status", packagesHandler.GetCodexStatus)
	packages.POST("/install-codex", packagesHandler.InstallCodex)
	packages.GET("/gemini-status", packagesHandler.GetGeminiStatus)
	packages.POST("/install-gemini", packagesHandler.InstallGemini)

	// Docker routes (only registered when Docker is available)
	if dockerHandler != nil {
		dk := authorized.Group("/docker")

		// Containers (static routes before :id to avoid shadowing)
		dk.GET("/containers", dockerHandler.ListContainers)
		dk.POST("/containers", dockerHandler.CreateContainer)
		dk.GET("/containers/stats/batch", dockerHandler.ContainerStatsBatch)
		dk.GET("/containers/:id/inspect", dockerHandler.InspectContainer)
		dk.GET("/containers/:id/stats", dockerHandler.ContainerStats)
		dk.POST("/containers/:id/start", dockerHandler.StartContainer)
		dk.POST("/containers/:id/stop", dockerHandler.StopContainer)
		dk.POST("/containers/:id/restart", dockerHandler.RestartContainer)
		dk.POST("/containers/:id/pause", dockerHandler.PauseContainer)
		dk.POST("/containers/:id/unpause", dockerHandler.UnpauseContainer)
		dk.DELETE("/containers/:id", dockerHandler.RemoveContainer)

		// Observability (theme F — metrics history + events timeline)
		obs := &featureDocker.ObservabilityHandler{
			DB:                   database,
			ObservabilityEnabled: cfg.Docker.Observability.IsEnabled(),
		}
		dk.GET("/containers/:id/metrics", obs.GetMetrics)
		dk.GET("/containers/:id/events", obs.GetEvents)
		dk.GET("/events/recent", obs.GetRecentEvents)

		// Images
		dk.GET("/images", dockerHandler.ListImages)
		dk.POST("/images/pull", dockerHandler.PullImage)
		dk.GET("/images/updates", dockerHandler.CheckImageUpdates)
		dk.DELETE("/images/:id", dockerHandler.RemoveImage)

		// Volumes
		dk.GET("/volumes", dockerHandler.ListVolumes)
		dk.POST("/volumes", dockerHandler.CreateVolume)
		dk.DELETE("/volumes/:name", dockerHandler.RemoveVolume)

		// Networks
		dk.GET("/networks", dockerHandler.ListNetworks)
		dk.POST("/networks", dockerHandler.CreateNetwork)
		dk.DELETE("/networks/:id", dockerHandler.RemoveNetwork)
		dk.GET("/networks/:id/inspect", dockerHandler.InspectNetwork)

		// Prune
		dk.POST("/prune/containers", dockerHandler.PruneContainers)
		dk.POST("/prune/images", dockerHandler.PruneImages)
		dk.POST("/prune/volumes", dockerHandler.PruneVolumes)
		dk.POST("/prune/networks", dockerHandler.PruneNetworks)
		dk.POST("/prune/all", dockerHandler.PruneAll)

		// Docker Hub search
		dk.GET("/images/search", dockerHandler.SearchImages)

		// Docker Compose
		compose := dk.Group("/compose")
		compose.GET("", composeHandler.ListProjectsWithStatus)
		compose.GET("/cluster-stacks", composeHandler.ClusterStacks) // cluster-wide aggregation (static before :project)
		compose.POST("", composeHandler.CreateProject)
		compose.GET("/:project", composeHandler.GetProject)
		compose.PUT("/:project", composeHandler.UpdateProject)
		compose.POST("/:project/diff", composeHandler.DiffStack)
		compose.PUT("/:project/healthcheck/:service", composeHandler.ApplyHealthcheck)
		compose.DELETE("/:project/healthcheck/:service", composeHandler.RemoveHealthcheck)
		compose.POST("/:project/healthcheck/:service/test", composeHandler.TestHealthcheck)
		compose.POST("/import", composeHandler.ImportFromGit)
		compose.POST("/migrate-import", composeHandler.MigrateImport)
		compose.POST("/:project/migrate/preflight", composeHandler.MigratePreflight)
		compose.POST("/:project/migrate", composeHandler.Migrate)
		compose.GET("/migrate/target-info", composeHandler.MigrateTargetInfo)
		compose.DELETE("/:project", composeHandler.DeleteProject)
		compose.POST("/:project/up", composeHandler.ProjectUp)
		compose.POST("/:project/up-stream", composeHandler.ProjectUpStream)
		compose.POST("/:project/down", composeHandler.ProjectDown)
		compose.GET("/:project/env", composeHandler.GetEnv)
		compose.PUT("/:project/env", composeHandler.UpdateEnv)
		compose.GET("/:project/services", composeHandler.GetProjectServices)
		compose.POST("/:project/services/:service/restart", composeHandler.RestartService)
		compose.POST("/:project/services/:service/stop", composeHandler.StopService)
		compose.POST("/:project/services/:service/start", composeHandler.StartService)
		compose.GET("/:project/services/:service/logs", composeHandler.ServiceLogs)
		compose.POST("/:project/validate", composeHandler.ValidateProject)
		compose.POST("/:project/check-updates", composeHandler.CheckStackUpdates)
		compose.POST("/:project/update", composeHandler.UpdateStack)
		compose.POST("/:project/update-stream", composeHandler.UpdateStackStream)
		compose.POST("/:project/rollback", composeHandler.RollbackStack)
		compose.GET("/:project/has-rollback", composeHandler.HasRollback)

		// Docker WebSocket routes (auth via query param token, cluster relay support).
		// Use a dynamic getter so runtime cluster init takes effect without a
		// process restart — see the same pattern on ClusterProxyMiddleware above.
		e.GET("/ws/docker/containers/:id/logs", cluster.WrapEchoWSHandler(clusterHandler.GetManager, featureWS.ContainerLogsWS(dockerClient, cfg.Auth.JWTSecret)))
		e.GET("/ws/docker/containers/:id/exec", cluster.WrapEchoWSHandler(clusterHandler.GetManager, featureWS.ContainerExecWS(dockerClient, cfg.Auth.JWTSecret)))
		e.GET("/ws/docker/compose/:project/logs", cluster.WrapEchoWSHandler(clusterHandler.GetManager, featureWS.ComposeLogsWS(composeManager, cfg.Auth.JWTSecret)))
	}

	// WebSocket routes (auth via query param token, cluster relay support)
	e.GET("/ws/metrics", cluster.WrapEchoWSHandler(clusterHandler.GetManager, featureWS.MetricsWS(cfg.Auth.JWTSecret)))
	// Cluster overview push: one shared sampler per node fans the combined
	// status+overview+events snapshot out to all dashboards, replacing the
	// per-tab 15s HTTP triple-poll. Served from the local FSM (no leader RPC).
	e.GET("/ws/cluster/overview", featureWS.ClusterOverviewWS(clusterHandler.GetManager, cfg.Auth.JWTSecret))
	e.GET("/ws/logs", cluster.WrapEchoWSHandler(clusterHandler.GetManager, featureLogs.LogStreamWS(cfg.Auth.JWTSecret, database)))
	e.GET("/ws/terminal", cluster.WrapEchoWSHandler(clusterHandler.GetManager, featureTerminal.TerminalWS(cfg.Auth.JWTSecret)))

	// SPA static file serving — catch-all AFTER all API and WS routes
	e.GET("/*", spaHandler(webFS))

	return e, noopCleanup
}

// noopCleanup is returned by NewRouter as its cleanup func. It currently does
// nothing: every background worker (alert manager, metrics/audit drains,
// retention pruners) is owned by cmd/sfpanel and stopped via the shared
// bgCtx cancel during shutdown, not here. It is kept so the call site's
// shutdown sequence stays stable if router-owned teardown is ever needed
// again — add it here rather than reintroducing a new return value.
func noopCleanup() {}

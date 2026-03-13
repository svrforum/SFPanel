package api

import (
	"database/sql"
	"embed"
	"io"
	"io/fs"
	"log"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
	echoMw "github.com/labstack/echo/v4/middleware"
	"github.com/svrforum/SFPanel/internal/api/handlers"
	mw "github.com/svrforum/SFPanel/internal/api/middleware"
	"github.com/svrforum/SFPanel/internal/cluster"
	"github.com/svrforum/SFPanel/internal/config"
	"github.com/svrforum/SFPanel/internal/docker"
)

func NewRouter(database *sql.DB, cfg *config.Config, webFS embed.FS, version string, clusterMgr *cluster.Manager, extra ...string) *echo.Echo {
	e := echo.New()
	e.HideBanner = true

	e.Use(echoMw.Logger())
	e.Use(echoMw.Recover())
	e.Use(echoMw.CORSWithConfig(echoMw.CORSConfig{
		AllowOrigins: []string{"http://localhost:5173"},
		AllowMethods: []string{"GET", "POST", "PUT", "DELETE"},
		AllowHeaders: []string{"Authorization", "Content-Type"},
	}))

	authHandler := &handlers.AuthHandler{DB: database, Config: cfg}
	dashboardHandler := &handlers.DashboardHandler{Version: version}

	cfgPath := ""
	if len(extra) > 0 {
		cfgPath = extra[0]
	}
	systemHandler := &handlers.SystemHandler{
		Version:     version,
		DBPath:      cfg.Database.Path,
		ConfigPath:  cfgPath,
		ComposePath: "/opt/stacks",
	}

	// Initialize Docker client
	dockerClient, err := docker.NewClient(cfg.Docker.Socket)
	if err != nil {
		log.Printf("Warning: Docker not available: %v", err)
	}

	var dockerHandler *handlers.DockerHandler
	if dockerClient != nil {
		dockerHandler = &handlers.DockerHandler{Docker: dockerClient}
	}

	// Initialize Compose manager — scans /opt/stacks for compose projects
	composeManager := docker.NewComposeManager("/opt/stacks", dockerClient)
	composeHandler := &handlers.ComposeHandler{Compose: composeManager, DB: database}

	v1 := e.Group("/api/v1")

	// Public routes
	v1.GET("/health", func(c echo.Context) error {
		return OK(c, map[string]string{"status": "ok"})
	})
	v1.POST("/auth/login", authHandler.Login)
	v1.GET("/auth/setup-status", authHandler.GetSetupStatus)
	v1.POST("/auth/setup", authHandler.SetupAdmin)

	// Protected routes
	authorized := v1.Group("")
	authorized.Use(mw.JWTMiddleware(cfg.Auth.JWTSecret))
	authorized.Use(mw.ClusterProxyMiddleware(clusterMgr))
	authorized.Use(mw.AuditMiddleware(database))
	// Settings
	settingsHandler := &handlers.SettingsHandler{DB: database}
	authorized.GET("/settings", settingsHandler.GetSettings)
	authorized.PUT("/settings", settingsHandler.UpdateSettings)

	authorized.GET("/auth/2fa/status", authHandler.Get2FAStatus)
	authorized.POST("/auth/2fa/setup", authHandler.Setup2FA)
	authorized.POST("/auth/2fa/verify", authHandler.Verify2FA)
	authorized.POST("/auth/change-password", authHandler.ChangePassword)
	authorized.GET("/system/info", dashboardHandler.GetSystemInfo)
	authorized.GET("/system/metrics-history", dashboardHandler.GetMetricsHistory)
	authorized.GET("/system/overview", dashboardHandler.GetOverview)

	// System management (update, backup, restore)
	// System tuning
	tuningHandler := &handlers.TuningHandler{}
	authorized.GET("/system/tuning", tuningHandler.GetTuningStatus)
	authorized.POST("/system/tuning/apply", tuningHandler.ApplyTuning)
	authorized.POST("/system/tuning/confirm", tuningHandler.ConfirmTuning)
	authorized.POST("/system/tuning/reset", tuningHandler.ResetTuning)

	// App Store
	appStoreHandler := &handlers.AppStoreHandler{DB: database, ComposePath: "/opt/stacks"}
	appStore := authorized.Group("/appstore")
	appStore.GET("/categories", appStoreHandler.GetCategories)
	appStore.GET("/apps", appStoreHandler.ListApps)
	appStore.GET("/apps/:id", appStoreHandler.GetApp)
	appStore.POST("/apps/:id/install", appStoreHandler.InstallApp)
	appStore.GET("/installed", appStoreHandler.GetInstalled)
	appStore.POST("/refresh", appStoreHandler.RefreshCache)

	authorized.GET("/system/update-check", systemHandler.CheckUpdate)
	authorized.POST("/system/update", systemHandler.RunUpdate)
	authorized.POST("/system/backup", systemHandler.CreateBackup)
	authorized.POST("/system/restore", systemHandler.RestoreBackup)

	// Cluster management
	clusterHandler := &handlers.ClusterHandler{Manager: clusterMgr}
	clusterGroup := authorized.Group("/cluster")
	clusterGroup.GET("/status", clusterHandler.GetStatus)
	clusterGroup.GET("/overview", clusterHandler.GetOverview)
	clusterGroup.GET("/nodes", clusterHandler.GetNodes)
	clusterGroup.POST("/token", clusterHandler.CreateToken)
	clusterGroup.DELETE("/nodes/:id", clusterHandler.RemoveNode)

	// Audit logs
	auditHandler := &handlers.AuditHandler{DB: database}
	authorized.GET("/audit/logs", auditHandler.ListAuditLogs)
	authorized.DELETE("/audit/logs", auditHandler.ClearAuditLogs)

	// Processes
	processesHandler := &handlers.ProcessesHandler{}
	authorized.GET("/system/processes", processesHandler.TopProcesses)
	authorized.GET("/system/processes/list", processesHandler.ListProcesses)
	authorized.POST("/system/processes/:pid/kill", processesHandler.KillProcess)

	// Systemd services
	servicesHandler := &handlers.ServicesHandler{}
	authorized.GET("/system/services", servicesHandler.ListServices)
	authorized.POST("/system/services/:name/start", servicesHandler.StartService)
	authorized.POST("/system/services/:name/stop", servicesHandler.StopService)
	authorized.POST("/system/services/:name/restart", servicesHandler.RestartService)
	authorized.POST("/system/services/:name/enable", servicesHandler.EnableService)
	authorized.POST("/system/services/:name/disable", servicesHandler.DisableService)
	authorized.GET("/system/services/:name/logs", servicesHandler.ServiceLogs)
	authorized.GET("/system/services/:name/deps", servicesHandler.GetServiceDeps)

	// File manager routes
	filesHandler := &handlers.FilesHandler{DB: database}
	files := authorized.Group("/files")
	files.GET("", filesHandler.ListDir)
	files.GET("/read", filesHandler.ReadFile)
	files.POST("/write", filesHandler.WriteFile)
	files.POST("/mkdir", filesHandler.MkDir)
	files.DELETE("", filesHandler.DeletePath)
	files.POST("/rename", filesHandler.RenamePath)
	files.GET("/download", filesHandler.DownloadFile)
	files.POST("/upload", filesHandler.UploadFile)

	// Cron job management routes
	cronHandler := &handlers.CronHandler{}
	cron := authorized.Group("/cron")
	cron.GET("", cronHandler.ListJobs)
	cron.POST("", cronHandler.CreateJob)
	cron.PUT("/:id", cronHandler.UpdateJob)
	cron.DELETE("/:id", cronHandler.DeleteJob)

	// Log viewer routes
	logsHandler := &handlers.LogsHandler{DB: database}
	logs := authorized.Group("/logs")
	logs.GET("/sources", logsHandler.ListSources)
	logs.GET("/read", logsHandler.ReadLog)
	logs.POST("/custom-sources", logsHandler.AddCustomSource)
	logs.DELETE("/custom-sources/:id", logsHandler.DeleteCustomSource)

	// Network
	networkHandler := &handlers.NetworkHandler{}
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
	wireguardHandler := &handlers.WireGuardHandler{}
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

	// Tailscale VPN
	tailscaleHandler := &handlers.TailscaleHandler{}
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
	diskHandler := &handlers.DiskHandler{}
	disks := authorized.Group("/disks")
	disks.GET("/overview", diskHandler.ListDisks)
	disks.GET("/iostat", diskHandler.GetIOStats)
	disks.POST("/usage", diskHandler.GetDiskUsage)
	disks.GET("/smartmontools-status", diskHandler.CheckSmartmontools)
	disks.POST("/install-smartmontools", diskHandler.InstallSmartmontools)
	disks.GET("/:device/smart", diskHandler.GetSmartInfo)
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
	firewallHandler := &handlers.FirewallHandler{}
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
	packagesHandler := &handlers.PackagesHandler{}
	packages := authorized.Group("/packages")
	packages.GET("/updates", packagesHandler.CheckUpdates)
	packages.POST("/upgrade", packagesHandler.UpgradePackages)
	packages.POST("/install", packagesHandler.InstallPackage)
	packages.POST("/remove", packagesHandler.RemovePackage)
	packages.GET("/search", packagesHandler.SearchPackages)
	packages.GET("/docker-status", packagesHandler.GetDockerStatus)
	packages.POST("/install-docker", packagesHandler.InstallDocker)

	// Docker routes (only registered when Docker is available)
	if dockerHandler != nil {
		dk := authorized.Group("/docker")

		// Containers (static routes before :id to avoid shadowing)
		dk.GET("/containers", dockerHandler.ListContainers)
		dk.GET("/containers/stats/batch", dockerHandler.ContainerStatsBatch)
		dk.GET("/containers/:id/inspect", dockerHandler.InspectContainer)
		dk.GET("/containers/:id/stats", dockerHandler.ContainerStats)
		dk.POST("/containers/:id/start", dockerHandler.StartContainer)
		dk.POST("/containers/:id/stop", dockerHandler.StopContainer)
		dk.POST("/containers/:id/restart", dockerHandler.RestartContainer)
		dk.POST("/containers/:id/pause", dockerHandler.PauseContainer)
		dk.POST("/containers/:id/unpause", dockerHandler.UnpauseContainer)
		dk.DELETE("/containers/:id", dockerHandler.RemoveContainer)

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
		compose.POST("", composeHandler.CreateProject)
		compose.GET("/:project", composeHandler.GetProject)
		compose.PUT("/:project", composeHandler.UpdateProject)
		compose.DELETE("/:project", composeHandler.DeleteProject)
		compose.POST("/:project/up", composeHandler.ProjectUp)
		compose.POST("/:project/down", composeHandler.ProjectDown)
		compose.GET("/:project/env", composeHandler.GetEnv)
		compose.PUT("/:project/env", composeHandler.UpdateEnv)
		compose.GET("/:project/services", composeHandler.GetProjectServices)
		compose.POST("/:project/services/:service/restart", composeHandler.RestartService)
		compose.POST("/:project/services/:service/stop", composeHandler.StopService)
		compose.POST("/:project/services/:service/start", composeHandler.StartService)
		compose.GET("/:project/services/:service/logs", composeHandler.ServiceLogs)
		compose.POST("/:project/validate", composeHandler.ValidateProject)

		// Docker WebSocket routes (auth via query param token)
		e.GET("/ws/docker/containers/:id/logs", handlers.ContainerLogsWS(dockerClient, cfg.Auth.JWTSecret))
		e.GET("/ws/docker/containers/:id/exec", handlers.ContainerExecWS(dockerClient, cfg.Auth.JWTSecret))
	}

	// WebSocket routes (auth via query param token)
	e.GET("/ws/metrics", handlers.MetricsWS(cfg.Auth.JWTSecret))
	e.GET("/ws/logs", handlers.LogStreamWS(cfg.Auth.JWTSecret, database))
	e.GET("/ws/terminal", handlers.TerminalWS(cfg.Auth.JWTSecret))

	// SPA static file serving — catch-all AFTER all API and WS routes
	e.GET("/*", spaHandler(webFS))

	return e
}

// spaHandler serves the embedded React SPA with fallback to index.html
// for client-side routing. API (/api/*) and WebSocket (/ws/*) routes are
// registered before this catch-all so they take precedence.
func spaHandler(fsys embed.FS) echo.HandlerFunc {
	subFS, err := fs.Sub(fsys, "web/dist")
	if err != nil {
		log.Fatalf("Failed to create sub-filesystem for embedded SPA: %v", err)
	}
	fileServer := http.FileServer(http.FS(subFS))

	return func(c echo.Context) error {
		path := c.Request().URL.Path

		// Strip leading slash and try to open the file
		cleanPath := strings.TrimPrefix(path, "/")
		if cleanPath == "" {
			cleanPath = "index.html"
		}

		f, err := subFS.Open(cleanPath)
		if err != nil {
			// File not found — serve index.html for SPA client-side routing
			index, indexErr := subFS.Open("index.html")
			if indexErr != nil {
				return c.String(http.StatusNotFound, "index.html not found")
			}
			defer index.Close()
			content, readErr := io.ReadAll(index)
			if readErr != nil {
				return c.String(http.StatusInternalServerError, "failed to read index.html")
			}
			return c.HTMLBlob(http.StatusOK, content)
		}
		f.Close()

		// Serve the actual file
		fileServer.ServeHTTP(c.Response(), c.Request())
		return nil
	}
}

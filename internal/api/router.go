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
	"github.com/sfpanel/sfpanel/internal/api/handlers"
	mw "github.com/sfpanel/sfpanel/internal/api/middleware"
	"github.com/sfpanel/sfpanel/internal/config"
	"github.com/sfpanel/sfpanel/internal/docker"
)

func NewRouter(database *sql.DB, cfg *config.Config, webFS embed.FS) *echo.Echo {
	e := echo.New()
	e.HideBanner = true

	e.Use(echoMw.Logger())
	e.Use(echoMw.Recover())
	e.Use(echoMw.CORSWithConfig(echoMw.CORSConfig{
		AllowOrigins: []string{"http://localhost:5173"},
		AllowMethods: []string{"GET", "POST", "PUT", "DELETE"},
	}))

	authHandler := &handlers.AuthHandler{DB: database, Config: cfg}
	dashboardHandler := &handlers.DashboardHandler{}

	// Initialize Docker client
	dockerClient, err := docker.NewClient(cfg.Docker.Socket)
	if err != nil {
		log.Printf("Warning: Docker not available: %v", err)
	}

	var dockerHandler *handlers.DockerHandler
	if dockerClient != nil {
		dockerHandler = &handlers.DockerHandler{Docker: dockerClient}
	}

	// Initialize Compose manager (always available, does not require Docker SDK client)
	composeManager := docker.NewComposeManager(database, "/var/lib/sfpanel/compose")
	composeHandler := &handlers.ComposeHandler{Compose: composeManager}

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
	// Settings
	settingsHandler := &handlers.SettingsHandler{DB: database}
	authorized.GET("/settings", settingsHandler.GetSettings)
	authorized.PUT("/settings", settingsHandler.UpdateSettings)

	authorized.POST("/auth/2fa/setup", authHandler.Setup2FA)
	authorized.POST("/auth/2fa/verify", authHandler.Verify2FA)
	authorized.POST("/auth/change-password", authHandler.ChangePassword)
	authorized.GET("/system/info", dashboardHandler.GetSystemInfo)
	authorized.GET("/system/metrics-history", dashboardHandler.GetMetricsHistory)

	// Processes
	processesHandler := &handlers.ProcessesHandler{}
	authorized.GET("/system/processes", processesHandler.TopProcesses)
	authorized.GET("/system/processes/list", processesHandler.ListProcesses)
	authorized.POST("/system/processes/:pid/kill", processesHandler.KillProcess)

	// File manager routes
	filesHandler := &handlers.FilesHandler{}
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
	logsHandler := &handlers.LogsHandler{}
	logs := authorized.Group("/logs")
	logs.GET("/sources", logsHandler.ListSources)
	logs.GET("/read", logsHandler.ReadLog)

	// Network
	networkHandler := &handlers.NetworkHandler{}
	net := authorized.Group("/network")
	net.GET("/interfaces", networkHandler.ListInterfaces)
	net.GET("/interfaces/:name", networkHandler.GetInterface)
	net.PUT("/interfaces/:name", networkHandler.ConfigureInterface)
	net.POST("/apply", networkHandler.ApplyNetplan)
	net.GET("/dns", networkHandler.GetDNS)
	net.GET("/routes", networkHandler.GetRoutes)
	net.GET("/bonds", networkHandler.ListBonds)
	net.POST("/bonds", networkHandler.CreateBond)
	net.DELETE("/bonds/:name", networkHandler.DeleteBond)

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

		// Containers
		dk.GET("/containers", dockerHandler.ListContainers)
		dk.GET("/containers/:id/inspect", dockerHandler.InspectContainer)
		dk.GET("/containers/:id/stats", dockerHandler.ContainerStats)
		dk.POST("/containers/:id/start", dockerHandler.StartContainer)
		dk.POST("/containers/:id/stop", dockerHandler.StopContainer)
		dk.POST("/containers/:id/restart", dockerHandler.RestartContainer)
		dk.DELETE("/containers/:id", dockerHandler.RemoveContainer)

		// Images
		dk.GET("/images", dockerHandler.ListImages)
		dk.POST("/images/pull", dockerHandler.PullImage)
		dk.DELETE("/images/:id", dockerHandler.RemoveImage)

		// Volumes
		dk.GET("/volumes", dockerHandler.ListVolumes)
		dk.POST("/volumes", dockerHandler.CreateVolume)
		dk.DELETE("/volumes/:name", dockerHandler.RemoveVolume)

		// Networks
		dk.GET("/networks", dockerHandler.ListNetworks)
		dk.POST("/networks", dockerHandler.CreateNetwork)
		dk.DELETE("/networks/:id", dockerHandler.RemoveNetwork)

		// Docker Compose
		compose := dk.Group("/compose")
		compose.GET("", composeHandler.ListProjects)
		compose.POST("", composeHandler.CreateProject)
		compose.GET("/:project", composeHandler.GetProject)
		compose.PUT("/:project", composeHandler.UpdateProject)
		compose.DELETE("/:project", composeHandler.DeleteProject)
		compose.POST("/:project/up", composeHandler.ProjectUp)
		compose.POST("/:project/down", composeHandler.ProjectDown)

		// Docker WebSocket routes (auth via query param token)
		e.GET("/ws/docker/containers/:id/logs", handlers.ContainerLogsWS(dockerClient, cfg.Auth.JWTSecret))
		e.GET("/ws/docker/containers/:id/exec", handlers.ContainerExecWS(dockerClient, cfg.Auth.JWTSecret))
	}

	// WebSocket routes (auth via query param token)
	e.GET("/ws/metrics", handlers.MetricsWS(cfg.Auth.JWTSecret))
	e.GET("/ws/logs", handlers.LogStreamWS(cfg.Auth.JWTSecret))
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

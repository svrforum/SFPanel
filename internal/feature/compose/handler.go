package compose

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/docker"
)

// Handler exposes REST handlers for Docker Compose project management.
type Handler struct {
	Compose *docker.ComposeManager
	DB      *sql.DB
}

// ListProjectsWithStatus returns all compose projects with real-time service status.
func (h *Handler) ListProjectsWithStatus(c echo.Context) error {
	ctx := c.Request().Context()
	projects, err := h.Compose.ListProjectsWithStatus(ctx)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrComposeError, response.SanitizeOutput(err.Error()))
	}
	if projects == nil {
		projects = []docker.ComposeProjectWithStatus{}
	}
	return response.OK(c, projects)
}

// CreateProject creates a new compose project.
// Accepts JSON body: {"name": "...", "yaml": "..."}.
func (h *Handler) CreateProject(c echo.Context) error {
	var req struct {
		Name string `json:"name"`
		YAML string `json:"yaml"`
	}
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}
	if req.Name == "" || req.YAML == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrMissingFields, "Name and yaml are required")
	}

	ctx := c.Request().Context()
	project, err := h.Compose.CreateProject(ctx, req.Name, req.YAML)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrComposeError, response.SanitizeOutput(err.Error()))
	}
	return response.OK(c, project)
}

// GetProject returns a single compose project by name, including the YAML content.
func (h *Handler) GetProject(c echo.Context) error {
	name := c.Param("project")
	ctx := c.Request().Context()

	project, err := h.Compose.GetProject(ctx, name)
	if err != nil {
		return response.Fail(c, http.StatusNotFound, response.ErrNotFound, response.SanitizeOutput(err.Error()))
	}

	yaml, _, err := h.Compose.GetProjectYAML(ctx, name)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrComposeError, response.SanitizeOutput(err.Error()))
	}

	return response.OK(c, map[string]interface{}{
		"project": project,
		"yaml":    yaml,
	})
}

// UpdateProject updates the YAML content of an existing compose project.
// Accepts JSON body: {"yaml": "..."}.
func (h *Handler) UpdateProject(c echo.Context) error {
	name := c.Param("project")
	var req struct {
		YAML string `json:"yaml"`
	}
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}
	if req.YAML == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrMissingFields, "YAML content is required")
	}

	ctx := c.Request().Context()
	if err := h.Compose.UpdateProject(ctx, name, req.YAML); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrComposeError, response.SanitizeOutput(err.Error()))
	}
	return response.OK(c, map[string]string{"message": "project updated"})
}

// DeleteProject deletes a compose project by name.
// Query params: removeImages=true, removeVolumes=true
func (h *Handler) DeleteProject(c echo.Context) error {
	name := c.Param("project")
	ctx := c.Request().Context()
	removeImages := c.QueryParam("removeImages") == "true"
	removeVolumes := c.QueryParam("removeVolumes") == "true"

	if err := h.Compose.DeleteProject(ctx, name, removeImages, removeVolumes); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrComposeError, response.SanitizeOutput(err.Error()))
	}

	// Clean up appstore install record if exists
	if h.DB != nil {
		_, _ = h.DB.Exec("DELETE FROM settings WHERE key = ?", "appstore_installed_"+name)
	}

	return response.OK(c, map[string]string{"message": "project deleted"})
}

// ProjectUp starts a compose project (docker compose up -d).
func (h *Handler) ProjectUp(c echo.Context) error {
	name := c.Param("project")
	ctx := c.Request().Context()

	output, err := h.Compose.Up(ctx, name)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrComposeError, response.SanitizeOutput(output))
	}
	return response.OK(c, map[string]string{"output": output})
}

// ProjectDown stops a compose project (docker compose down).
func (h *Handler) ProjectDown(c echo.Context) error {
	name := c.Param("project")
	ctx := c.Request().Context()

	output, err := h.Compose.Down(ctx, name)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrComposeError, response.SanitizeOutput(output))
	}
	return response.OK(c, map[string]string{"output": output})
}

// GetProjectServices returns the runtime state of each service in a compose project.
func (h *Handler) GetProjectServices(c echo.Context) error {
	name := c.Param("project")
	ctx := c.Request().Context()

	services, err := h.Compose.GetProjectServices(ctx, name)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrComposeError, response.SanitizeOutput(err.Error()))
	}
	if services == nil {
		services = []docker.ComposeService{}
	}
	return response.OK(c, services)
}

// GetEnv returns the .env file content for a compose project.
func (h *Handler) GetEnv(c echo.Context) error {
	name := c.Param("project")
	ctx := c.Request().Context()

	content, err := h.Compose.GetProjectEnv(ctx, name)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrComposeError, response.SanitizeOutput(err.Error()))
	}
	return response.OK(c, map[string]string{"content": content})
}

// UpdateEnv updates the .env file content for a compose project.
// Accepts JSON body: {"content": "..."}.
func (h *Handler) UpdateEnv(c echo.Context) error {
	name := c.Param("project")
	var req struct {
		Content string `json:"content"`
	}
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}

	ctx := c.Request().Context()
	if err := h.Compose.UpdateProjectEnv(ctx, name, req.Content); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrComposeError, response.SanitizeOutput(err.Error()))
	}
	return response.OK(c, map[string]string{"message": "env updated"})
}

// RestartService restarts a single service in a compose project.
func (h *Handler) RestartService(c echo.Context) error {
	project := c.Param("project")
	service := c.Param("service")
	ctx := c.Request().Context()

	output, err := h.Compose.RestartService(ctx, project, service)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrComposeError, response.SanitizeOutput(output))
	}
	return response.OK(c, map[string]string{"output": output})
}

// StopService stops a single service in a compose project.
func (h *Handler) StopService(c echo.Context) error {
	project := c.Param("project")
	service := c.Param("service")
	ctx := c.Request().Context()

	output, err := h.Compose.StopService(ctx, project, service)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrComposeError, response.SanitizeOutput(output))
	}
	return response.OK(c, map[string]string{"output": output})
}

// StartService starts a single service in a compose project.
func (h *Handler) StartService(c echo.Context) error {
	project := c.Param("project")
	service := c.Param("service")
	ctx := c.Request().Context()

	output, err := h.Compose.StartService(ctx, project, service)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrComposeError, response.SanitizeOutput(output))
	}
	return response.OK(c, map[string]string{"output": output})
}

// ValidateProject validates the docker-compose.yml of a project using `docker compose config`.
func (h *Handler) ValidateProject(c echo.Context) error {
	name := c.Param("project")
	ctx := c.Request().Context()

	output, err := h.Compose.ValidateConfig(ctx, name)
	if err != nil {
		return response.OK(c, map[string]interface{}{
			"valid":   false,
			"message": output,
		})
	}
	return response.OK(c, map[string]interface{}{
		"valid":   true,
		"message": "Configuration is valid",
	})
}

// CheckStackUpdates checks for image updates in a compose project.
func (h *Handler) CheckStackUpdates(c echo.Context) error {
	name := c.Param("project")
	ctx := c.Request().Context()

	result, err := h.Compose.CheckStackUpdates(ctx, name)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrComposeError, response.SanitizeOutput(err.Error()))
	}
	return response.OK(c, result)
}

// UpdateStack pulls latest images and recreates containers.
func (h *Handler) UpdateStack(c echo.Context) error {
	name := c.Param("project")
	ctx := c.Request().Context()

	output, err := h.Compose.UpdateStack(ctx, name)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrComposeError, response.SanitizeOutput(output))
	}
	return response.OK(c, map[string]string{"output": output})
}

// RollbackStack restores previous image versions.
func (h *Handler) RollbackStack(c echo.Context) error {
	name := c.Param("project")
	ctx := c.Request().Context()

	output, err := h.Compose.RollbackStack(ctx, name)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrComposeError, response.SanitizeOutput(output))
	}
	return response.OK(c, map[string]string{"output": output})
}

// HasRollback returns rollback availability and image details for a project.
func (h *Handler) HasRollback(c echo.Context) error {
	name := c.Param("project")
	ctx := c.Request().Context()
	return response.OK(c, h.Compose.GetRollbackInfo(ctx, name))
}

// ProjectUpStream starts a compose project with SSE streaming output.
func (h *Handler) ProjectUpStream(c echo.Context) error {
	name := c.Param("project")
	ctx := c.Request().Context()

	c.Response().Header().Set("Content-Type", "text/event-stream")
	c.Response().Header().Set("Cache-Control", "no-cache")
	c.Response().Header().Set("Connection", "keep-alive")
	c.Response().WriteHeader(http.StatusOK)

	flusher := c.Response()
	sendEvent := func(phase, line string) {
		data, _ := json.Marshal(map[string]string{"phase": phase, "line": line})
		fmt.Fprintf(flusher, "data: %s\n\n", data)
		flusher.Flush()
	}

	sendEvent("deploy", "Starting deployment...")

	err := h.Compose.UpStream(ctx, name, func(line string) {
		sendEvent("deploy", line)
	})

	if err != nil {
		sendEvent("error", err.Error())
	} else {
		sendEvent("complete", "Deployment completed successfully")
	}

	return nil
}

// UpdateStackStream pulls latest images and recreates containers with SSE streaming.
func (h *Handler) UpdateStackStream(c echo.Context) error {
	name := c.Param("project")
	ctx := c.Request().Context()

	c.Response().Header().Set("Content-Type", "text/event-stream")
	c.Response().Header().Set("Cache-Control", "no-cache")
	c.Response().Header().Set("Connection", "keep-alive")
	c.Response().WriteHeader(http.StatusOK)

	flusher := c.Response()
	sendEvent := func(phase, line string) {
		data, _ := json.Marshal(map[string]string{"phase": phase, "line": line})
		fmt.Fprintf(flusher, "data: %s\n\n", data)
		flusher.Flush()
	}

	sendEvent("pull", "Starting update...")

	err := h.Compose.UpdateStackStream(ctx, name, func(line string) {
		sendEvent("update", line)
	})

	if err != nil {
		sendEvent("error", err.Error())
	} else {
		sendEvent("complete", "Update completed successfully")
	}

	return nil
}

// ServiceLogs returns the last N lines of logs for a service.
func (h *Handler) ServiceLogs(c echo.Context) error {
	project := c.Param("project")
	service := c.Param("service")

	tail := 100
	if t := c.QueryParam("tail"); t != "" {
		if parsed, err := strconv.Atoi(t); err == nil && parsed > 0 {
			tail = parsed
		}
	}

	ctx := c.Request().Context()
	output, err := h.Compose.ServiceLogs(ctx, project, service, tail)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrComposeError, response.SanitizeOutput(output))
	}
	return response.OK(c, map[string]string{"logs": output})
}

package handlers

import (
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/docker"
)

// ComposeHandler exposes REST handlers for Docker Compose project management.
type ComposeHandler struct {
	Compose *docker.ComposeManager
}

// ListProjectsWithStatus returns all compose projects with real-time service status.
func (h *ComposeHandler) ListProjectsWithStatus(c echo.Context) error {
	ctx := c.Request().Context()
	projects, err := h.Compose.ListProjectsWithStatus(ctx)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrComposeError, err.Error())
	}
	if projects == nil {
		projects = []docker.ComposeProjectWithStatus{}
	}
	return response.OK(c, projects)
}

// CreateProject creates a new compose project.
// Accepts JSON body: {"name": "...", "yaml": "..."}.
func (h *ComposeHandler) CreateProject(c echo.Context) error {
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
		return response.Fail(c, http.StatusInternalServerError, response.ErrComposeError, err.Error())
	}
	return response.OK(c, project)
}

// GetProject returns a single compose project by name, including the YAML content.
func (h *ComposeHandler) GetProject(c echo.Context) error {
	name := c.Param("project")
	ctx := c.Request().Context()

	project, err := h.Compose.GetProject(ctx, name)
	if err != nil {
		return response.Fail(c, http.StatusNotFound, response.ErrNotFound, err.Error())
	}

	yaml, _, err := h.Compose.GetProjectYAML(ctx, name)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrComposeError, err.Error())
	}

	return response.OK(c, map[string]interface{}{
		"project": project,
		"yaml":    yaml,
	})
}

// UpdateProject updates the YAML content of an existing compose project.
// Accepts JSON body: {"yaml": "..."}.
func (h *ComposeHandler) UpdateProject(c echo.Context) error {
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
		return response.Fail(c, http.StatusInternalServerError, response.ErrComposeError, err.Error())
	}
	return response.OK(c, map[string]string{"message": "project updated"})
}

// DeleteProject deletes a compose project by name.
func (h *ComposeHandler) DeleteProject(c echo.Context) error {
	name := c.Param("project")
	ctx := c.Request().Context()

	if err := h.Compose.DeleteProject(ctx, name); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrComposeError, err.Error())
	}
	return response.OK(c, map[string]string{"message": "project deleted"})
}

// ProjectUp starts a compose project (docker compose up -d).
func (h *ComposeHandler) ProjectUp(c echo.Context) error {
	name := c.Param("project")
	ctx := c.Request().Context()

	output, err := h.Compose.Up(ctx, name)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrComposeError, output)
	}
	return response.OK(c, map[string]string{"output": output})
}

// ProjectDown stops a compose project (docker compose down).
func (h *ComposeHandler) ProjectDown(c echo.Context) error {
	name := c.Param("project")
	ctx := c.Request().Context()

	output, err := h.Compose.Down(ctx, name)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrComposeError, output)
	}
	return response.OK(c, map[string]string{"output": output})
}

// GetProjectServices returns the runtime state of each service in a compose project.
func (h *ComposeHandler) GetProjectServices(c echo.Context) error {
	name := c.Param("project")
	ctx := c.Request().Context()

	services, err := h.Compose.GetProjectServices(ctx, name)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrComposeError, err.Error())
	}
	if services == nil {
		services = []docker.ComposeService{}
	}
	return response.OK(c, services)
}

// GetEnv returns the .env file content for a compose project.
func (h *ComposeHandler) GetEnv(c echo.Context) error {
	name := c.Param("project")
	ctx := c.Request().Context()

	content, err := h.Compose.GetProjectEnv(ctx, name)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrComposeError, err.Error())
	}
	return response.OK(c, map[string]string{"content": content})
}

// UpdateEnv updates the .env file content for a compose project.
// Accepts JSON body: {"content": "..."}.
func (h *ComposeHandler) UpdateEnv(c echo.Context) error {
	name := c.Param("project")
	var req struct {
		Content string `json:"content"`
	}
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}

	ctx := c.Request().Context()
	if err := h.Compose.UpdateProjectEnv(ctx, name, req.Content); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrComposeError, err.Error())
	}
	return response.OK(c, map[string]string{"message": "env updated"})
}

// RestartService restarts a single service in a compose project.
func (h *ComposeHandler) RestartService(c echo.Context) error {
	project := c.Param("project")
	service := c.Param("service")
	ctx := c.Request().Context()

	output, err := h.Compose.RestartService(ctx, project, service)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrComposeError, output)
	}
	return response.OK(c, map[string]string{"output": output})
}

// StopService stops a single service in a compose project.
func (h *ComposeHandler) StopService(c echo.Context) error {
	project := c.Param("project")
	service := c.Param("service")
	ctx := c.Request().Context()

	output, err := h.Compose.StopService(ctx, project, service)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrComposeError, output)
	}
	return response.OK(c, map[string]string{"output": output})
}

// StartService starts a single service in a compose project.
func (h *ComposeHandler) StartService(c echo.Context) error {
	project := c.Param("project")
	service := c.Param("service")
	ctx := c.Request().Context()

	output, err := h.Compose.StartService(ctx, project, service)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrComposeError, output)
	}
	return response.OK(c, map[string]string{"output": output})
}

// ServiceLogs returns the last N lines of logs for a service.
func (h *ComposeHandler) ServiceLogs(c echo.Context) error {
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
		return response.Fail(c, http.StatusInternalServerError, response.ErrComposeError, output)
	}
	return response.OK(c, map[string]string{"logs": output})
}

package handlers

import (
	"net/http"
	"os"

	"github.com/labstack/echo/v4"
	"github.com/sfpanel/sfpanel/internal/api/response"
	"github.com/sfpanel/sfpanel/internal/docker"
)

// ComposeHandler exposes REST handlers for Docker Compose project management.
type ComposeHandler struct {
	Compose *docker.ComposeManager
}

// ListProjects returns all compose projects.
func (h *ComposeHandler) ListProjects(c echo.Context) error {
	ctx := c.Request().Context()
	projects, err := h.Compose.ListProjects(ctx)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "COMPOSE_ERROR", err.Error())
	}
	if projects == nil {
		projects = []docker.ComposeProject{}
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
		return response.Fail(c, http.StatusBadRequest, "INVALID_REQUEST", "Invalid request body")
	}
	if req.Name == "" || req.YAML == "" {
		return response.Fail(c, http.StatusBadRequest, "MISSING_FIELDS", "Name and yaml are required")
	}

	ctx := c.Request().Context()
	project, err := h.Compose.CreateProject(ctx, req.Name, req.YAML)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "COMPOSE_ERROR", err.Error())
	}
	return response.OK(c, project)
}

// GetProject returns a single compose project by name, including the YAML content read from disk.
func (h *ComposeHandler) GetProject(c echo.Context) error {
	name := c.Param("project")
	ctx := c.Request().Context()

	project, err := h.Compose.GetProject(ctx, name)
	if err != nil {
		return response.Fail(c, http.StatusNotFound, "NOT_FOUND", err.Error())
	}

	// Read YAML content from disk
	yamlContent, err := os.ReadFile(project.YAMLPath)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "COMPOSE_ERROR", "Failed to read YAML file: "+err.Error())
	}

	return response.OK(c, map[string]interface{}{
		"project": project,
		"yaml":    string(yamlContent),
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
		return response.Fail(c, http.StatusBadRequest, "INVALID_REQUEST", "Invalid request body")
	}
	if req.YAML == "" {
		return response.Fail(c, http.StatusBadRequest, "MISSING_FIELDS", "YAML content is required")
	}

	ctx := c.Request().Context()
	if err := h.Compose.UpdateProject(ctx, name, req.YAML); err != nil {
		return response.Fail(c, http.StatusInternalServerError, "COMPOSE_ERROR", err.Error())
	}
	return response.OK(c, map[string]string{"message": "project updated"})
}

// DeleteProject deletes a compose project by name.
func (h *ComposeHandler) DeleteProject(c echo.Context) error {
	name := c.Param("project")
	ctx := c.Request().Context()

	if err := h.Compose.DeleteProject(ctx, name); err != nil {
		return response.Fail(c, http.StatusInternalServerError, "COMPOSE_ERROR", err.Error())
	}
	return response.OK(c, map[string]string{"message": "project deleted"})
}

// ProjectUp starts a compose project (docker compose up -d).
func (h *ComposeHandler) ProjectUp(c echo.Context) error {
	name := c.Param("project")
	ctx := c.Request().Context()

	output, err := h.Compose.Up(ctx, name)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "COMPOSE_ERROR", output)
	}
	return response.OK(c, map[string]string{"output": output})
}

// ProjectDown stops a compose project (docker compose down).
func (h *ComposeHandler) ProjectDown(c echo.Context) error {
	name := c.Param("project")
	ctx := c.Request().Context()

	output, err := h.Compose.Down(ctx, name)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "COMPOSE_ERROR", output)
	}
	return response.OK(c, map[string]string{"output": output})
}

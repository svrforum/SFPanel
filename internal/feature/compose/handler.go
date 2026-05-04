package compose

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/composex"
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
	// Same compose-safety gate the App Store one-click installer uses —
	// blocks privileged, host-namespace, dangerous-cap, /-bind, docker.sock,
	// device-passthrough patterns. An operator who needs those for legit
	// reasons can still get them via shell access.
	if err := composex.ValidateAdvancedCompose(req.YAML); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, err.Error())
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
	if err := composex.ValidateAdvancedCompose(req.YAML); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, err.Error())
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

// DiffStack returns a categorized diff between the deployed compose YAML
// for :project and the YAML supplied in the request body.
// POST /api/v1/docker/compose/:project/diff   {"yaml": "..."}
func (h *Handler) DiffStack(c echo.Context) error {
	name := c.Param("project")
	if name == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "project name required")
	}
	var req struct {
		YAML string `json:"yaml"`
	}
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "invalid request body")
	}
	if req.YAML == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrMissingFields, "yaml required")
	}

	ctx := c.Request().Context()
	deployedYAML, _, err := h.Compose.GetProjectYAML(ctx, name)
	if err != nil {
		return response.Fail(c, http.StatusNotFound, response.ErrNotFound, response.SanitizeOutput(err.Error()))
	}

	res, err := ComputeDiff(deployedYAML, req.YAML)
	if err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidYAML, response.SanitizeOutput(err.Error()))
	}
	return response.OK(c, res)
}

// ImportFromGit clones a GitHub repo (one-shot, no persistent link),
// reads the compose YAML at the requested path, and creates a stack.
// POST /api/v1/docker/compose/import
func (h *Handler) ImportFromGit(c echo.Context) error {
	var req ImportRequest
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "invalid request body")
	}
	if req.Branch == "" {
		req.Branch = "main"
	}
	if req.Path == "" {
		req.Path = "docker-compose.yml"
	}
	if err := validateImportRequest(req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, err.Error())
	}

	ctx, cancel := context.WithTimeout(c.Request().Context(), importCloneTimeout)
	defer cancel()

	repo, err := cloneShallow(ctx, req.URL, req.Branch, req.Token)
	if err != nil {
		switch {
		case errors.Is(err, ErrAuthFailed):
			return response.Fail(c, http.StatusUnauthorized, response.ErrGitAuthFailed,
				"인증 실패. PAT가 필요한 private 저장소입니다.")
		case errors.Is(err, ErrRepoNotFound):
			return response.Fail(c, http.StatusNotFound, response.ErrGitRepoNotFound,
				"저장소를 찾을 수 없습니다.")
		default:
			return response.Fail(c, http.StatusInternalServerError, response.ErrGitCloneFailed,
				response.SanitizeOutput(err.Error()))
		}
	}

	yamlBody, err := readComposeFromRepo(ctx, repo, req.Branch, req.Path)
	if err != nil {
		if errors.Is(err, ErrPathNotFound) {
			return response.Fail(c, http.StatusNotFound, response.ErrGitPathNotFound,
				"해당 경로의 파일이 없습니다.")
		}
		return response.Fail(c, http.StatusInternalServerError, response.ErrGitCloneFailed,
			response.SanitizeOutput(err.Error()))
	}

	if err := composex.ValidateAdvancedCompose(yamlBody); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidYAML, err.Error())
	}

	project, err := h.Compose.CreateProject(ctx, req.Name, yamlBody)
	if err != nil {
		// CreateProject's error path includes "already exists" for collisions.
		if strings.Contains(strings.ToLower(err.Error()), "already exists") {
			return response.Fail(c, http.StatusConflict, response.ErrStackAlreadyExists,
				"이미 존재하는 스택 이름입니다.")
		}
		return response.Fail(c, http.StatusInternalServerError, response.ErrComposeError,
			response.SanitizeOutput(err.Error()))
	}

	return response.OK(c, map[string]string{"project_name": project.Name})
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

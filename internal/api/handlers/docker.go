package handlers

import (
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/sfpanel/sfpanel/internal/api/response"
	"github.com/sfpanel/sfpanel/internal/docker"
)

// DockerHandler holds a Docker client and exposes REST handlers for
// container, image, volume, and network management.
type DockerHandler struct {
	Docker *docker.Client
}

// ---------- Containers ----------

// ListContainers returns all containers (running and stopped).
func (h *DockerHandler) ListContainers(c echo.Context) error {
	ctx := c.Request().Context()
	containers, err := h.Docker.ListContainers(ctx)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "DOCKER_ERROR", err.Error())
	}
	return response.OK(c, containers)
}

// StartContainer starts a container by ID.
func (h *DockerHandler) StartContainer(c echo.Context) error {
	ctx := c.Request().Context()
	id := c.Param("id")
	if err := h.Docker.StartContainer(ctx, id); err != nil {
		return response.Fail(c, http.StatusInternalServerError, "DOCKER_ERROR", err.Error())
	}
	return response.OK(c, map[string]string{"message": "container started"})
}

// StopContainer stops a container by ID.
func (h *DockerHandler) StopContainer(c echo.Context) error {
	ctx := c.Request().Context()
	id := c.Param("id")
	if err := h.Docker.StopContainer(ctx, id); err != nil {
		return response.Fail(c, http.StatusInternalServerError, "DOCKER_ERROR", err.Error())
	}
	return response.OK(c, map[string]string{"message": "container stopped"})
}

// RestartContainer restarts a container by ID.
func (h *DockerHandler) RestartContainer(c echo.Context) error {
	ctx := c.Request().Context()
	id := c.Param("id")
	if err := h.Docker.RestartContainer(ctx, id); err != nil {
		return response.Fail(c, http.StatusInternalServerError, "DOCKER_ERROR", err.Error())
	}
	return response.OK(c, map[string]string{"message": "container restarted"})
}

// RemoveContainer removes a container by ID (force).
func (h *DockerHandler) RemoveContainer(c echo.Context) error {
	ctx := c.Request().Context()
	id := c.Param("id")
	if err := h.Docker.RemoveContainer(ctx, id); err != nil {
		return response.Fail(c, http.StatusInternalServerError, "DOCKER_ERROR", err.Error())
	}
	return response.OK(c, map[string]string{"message": "container removed"})
}

// InspectContainer returns detailed inspection data for a container.
func (h *DockerHandler) InspectContainer(c echo.Context) error {
	ctx := c.Request().Context()
	id := c.Param("id")
	data, err := h.Docker.GetContainer(ctx, id)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "DOCKER_ERROR", err.Error())
	}

	// Build a clean response with the most useful fields
	ports := []map[string]interface{}{}
	for containerPort, bindings := range data.NetworkSettings.Ports {
		for _, b := range bindings {
			ports = append(ports, map[string]interface{}{
				"container_port": containerPort.Port(),
				"protocol":       containerPort.Proto(),
				"host_ip":        b.HostIP,
				"host_port":      b.HostPort,
			})
		}
		if len(bindings) == 0 {
			ports = append(ports, map[string]interface{}{
				"container_port": containerPort.Port(),
				"protocol":       containerPort.Proto(),
				"host_ip":        "",
				"host_port":      "",
			})
		}
	}

	envVars := []string{}
	if data.Config != nil {
		envVars = data.Config.Env
	}

	mounts := []map[string]string{}
	for _, m := range data.Mounts {
		mounts = append(mounts, map[string]string{
			"type":        string(m.Type),
			"source":      m.Source,
			"destination": m.Destination,
			"mode":        m.Mode,
			"rw":          fmt.Sprintf("%v", m.RW),
		})
	}

	networks := []map[string]string{}
	if data.NetworkSettings != nil {
		for name, net := range data.NetworkSettings.Networks {
			networks = append(networks, map[string]string{
				"name":       name,
				"ip_address": net.IPAddress,
				"gateway":    net.Gateway,
				"mac_address": net.MacAddress,
			})
		}
	}

	cmd := ""
	entrypoint := ""
	if data.Config != nil {
		cmd = strings.Join(data.Config.Cmd, " ")
		entrypoint = strings.Join(data.Config.Entrypoint, " ")
	}

	result := map[string]interface{}{
		"id":            data.ID,
		"name":          strings.TrimPrefix(data.Name, "/"),
		"image":         data.Config.Image,
		"state":         data.State.Status,
		"started_at":    data.State.StartedAt,
		"finished_at":   data.State.FinishedAt,
		"restart_count": data.RestartCount,
		"platform":      data.Platform,
		"cmd":           cmd,
		"entrypoint":    entrypoint,
		"working_dir":   data.Config.WorkingDir,
		"hostname":      data.Config.Hostname,
		"ports":         ports,
		"env":           envVars,
		"mounts":        mounts,
		"networks":      networks,
	}

	return response.OK(c, result)
}

// ContainerStats returns CPU and memory usage stats for a container.
func (h *DockerHandler) ContainerStats(c echo.Context) error {
	ctx := c.Request().Context()
	id := c.Param("id")
	stats, err := h.Docker.ContainerStats(ctx, id)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "DOCKER_ERROR", err.Error())
	}

	// Calculate CPU percentage
	cpuDelta := float64(stats.CPUStats.CPUUsage.TotalUsage - stats.PreCPUStats.CPUUsage.TotalUsage)
	systemDelta := float64(stats.CPUStats.SystemUsage - stats.PreCPUStats.SystemUsage)
	cpuPercent := 0.0
	if systemDelta > 0 && cpuDelta > 0 {
		cpuPercent = (cpuDelta / systemDelta) * float64(stats.CPUStats.OnlineCPUs) * 100.0
	}

	memUsage := stats.MemoryStats.Usage
	memLimit := stats.MemoryStats.Limit
	memPercent := 0.0
	if memLimit > 0 {
		memPercent = float64(memUsage) / float64(memLimit) * 100.0
	}

	result := map[string]interface{}{
		"cpu_percent": cpuPercent,
		"mem_usage":   memUsage,
		"mem_limit":   memLimit,
		"mem_percent": memPercent,
	}

	return response.OK(c, result)
}

// ---------- Images ----------

// ListImages returns all local Docker images.
func (h *DockerHandler) ListImages(c echo.Context) error {
	ctx := c.Request().Context()
	images, err := h.Docker.ListImages(ctx)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "DOCKER_ERROR", err.Error())
	}
	return response.OK(c, images)
}

// PullImage pulls an image by reference. Accepts JSON body: {"image": "nginx:latest"}.
func (h *DockerHandler) PullImage(c echo.Context) error {
	var req struct {
		Image string `json:"image"`
	}
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, "INVALID_REQUEST", "Invalid request body")
	}
	if req.Image == "" {
		return response.Fail(c, http.StatusBadRequest, "MISSING_FIELDS", "Image reference is required")
	}

	ctx := c.Request().Context()
	reader, err := h.Docker.PullImage(ctx, req.Image)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "DOCKER_ERROR", err.Error())
	}
	defer reader.Close()

	// Consume the pull output so the operation completes
	if _, err := io.Copy(io.Discard, reader); err != nil {
		return response.Fail(c, http.StatusInternalServerError, "DOCKER_ERROR", err.Error())
	}

	return response.OK(c, map[string]string{"message": "image pulled", "image": req.Image})
}

// RemoveImage removes an image by ID.
func (h *DockerHandler) RemoveImage(c echo.Context) error {
	ctx := c.Request().Context()
	id := c.Param("id")
	if err := h.Docker.RemoveImage(ctx, id); err != nil {
		return response.Fail(c, http.StatusInternalServerError, "DOCKER_ERROR", err.Error())
	}
	return response.OK(c, map[string]string{"message": "image removed"})
}

// ---------- Volumes ----------

// ListVolumes returns all Docker volumes.
func (h *DockerHandler) ListVolumes(c echo.Context) error {
	ctx := c.Request().Context()
	volumes, err := h.Docker.ListVolumes(ctx)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "DOCKER_ERROR", err.Error())
	}
	return response.OK(c, volumes)
}

// CreateVolume creates a volume. Accepts JSON body: {"name": "myvolume"}.
func (h *DockerHandler) CreateVolume(c echo.Context) error {
	var req struct {
		Name string `json:"name"`
	}
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, "INVALID_REQUEST", "Invalid request body")
	}
	if req.Name == "" {
		return response.Fail(c, http.StatusBadRequest, "MISSING_FIELDS", "Volume name is required")
	}

	ctx := c.Request().Context()
	vol, err := h.Docker.CreateVolume(ctx, req.Name)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "DOCKER_ERROR", err.Error())
	}
	return response.OK(c, vol)
}

// RemoveVolume removes a volume by name.
func (h *DockerHandler) RemoveVolume(c echo.Context) error {
	ctx := c.Request().Context()
	name := c.Param("name")
	if err := h.Docker.RemoveVolume(ctx, name); err != nil {
		return response.Fail(c, http.StatusInternalServerError, "DOCKER_ERROR", err.Error())
	}
	return response.OK(c, map[string]string{"message": "volume removed"})
}

// ---------- Networks ----------

// ListNetworks returns all Docker networks.
func (h *DockerHandler) ListNetworks(c echo.Context) error {
	ctx := c.Request().Context()
	networks, err := h.Docker.ListNetworks(ctx)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "DOCKER_ERROR", err.Error())
	}
	return response.OK(c, networks)
}

// CreateNetwork creates a network. Accepts JSON body: {"name": "mynet", "driver": "bridge"}.
func (h *DockerHandler) CreateNetwork(c echo.Context) error {
	var req struct {
		Name   string `json:"name"`
		Driver string `json:"driver"`
	}
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, "INVALID_REQUEST", "Invalid request body")
	}
	if req.Name == "" {
		return response.Fail(c, http.StatusBadRequest, "MISSING_FIELDS", "Network name is required")
	}
	if req.Driver == "" {
		req.Driver = "bridge"
	}

	ctx := c.Request().Context()
	net, err := h.Docker.CreateNetwork(ctx, req.Name, req.Driver)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "DOCKER_ERROR", err.Error())
	}
	return response.OK(c, net)
}

// RemoveNetwork removes a network by ID.
func (h *DockerHandler) RemoveNetwork(c echo.Context) error {
	ctx := c.Request().Context()
	id := c.Param("id")
	if err := h.Docker.RemoveNetwork(ctx, id); err != nil {
		return response.Fail(c, http.StatusInternalServerError, "DOCKER_ERROR", err.Error())
	}
	return response.OK(c, map[string]string{"message": "network removed"})
}

package featuredocker

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/docker"
)

// Handler holds a Docker client and exposes REST handlers for
// container, image, volume, and network management.
type Handler struct {
	Docker *docker.Client
}

// safeLen returns the length of a string slice, safely handling nil.
func safeLen[T any](s []T) int {
	return len(s)
}

// ---------- Containers ----------

// ListContainers returns all containers (running and stopped).
func (h *Handler) ListContainers(c echo.Context) error {
	ctx := c.Request().Context()
	containers, err := h.Docker.ListContainers(ctx)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDockerError, response.SanitizeOutput(err.Error()))
	}
	return response.OK(c, containers)
}

// StartContainer starts a container by ID.
func (h *Handler) StartContainer(c echo.Context) error {
	ctx := c.Request().Context()
	id := c.Param("id")
	if err := h.Docker.StartContainer(ctx, id); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDockerError, response.SanitizeOutput(err.Error()))
	}
	return response.OK(c, map[string]string{"message": "container started"})
}

// StopContainer stops a container by ID.
func (h *Handler) StopContainer(c echo.Context) error {
	ctx := c.Request().Context()
	id := c.Param("id")
	if err := h.Docker.StopContainer(ctx, id); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDockerError, response.SanitizeOutput(err.Error()))
	}
	return response.OK(c, map[string]string{"message": "container stopped"})
}

// PauseContainer pauses a running container.
func (h *Handler) PauseContainer(c echo.Context) error {
	ctx := c.Request().Context()
	id := c.Param("id")
	if err := h.Docker.PauseContainer(ctx, id); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDockerError, response.SanitizeOutput(err.Error()))
	}
	return response.OK(c, map[string]string{"message": "container paused"})
}

// UnpauseContainer unpauses a paused container.
func (h *Handler) UnpauseContainer(c echo.Context) error {
	ctx := c.Request().Context()
	id := c.Param("id")
	if err := h.Docker.UnpauseContainer(ctx, id); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDockerError, response.SanitizeOutput(err.Error()))
	}
	return response.OK(c, map[string]string{"message": "container unpaused"})
}

// RestartContainer restarts a container by ID.
func (h *Handler) RestartContainer(c echo.Context) error {
	ctx := c.Request().Context()
	id := c.Param("id")
	if err := h.Docker.RestartContainer(ctx, id); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDockerError, response.SanitizeOutput(err.Error()))
	}
	return response.OK(c, map[string]string{"message": "container restarted"})
}

// RemoveContainer removes a container by ID (force).
func (h *Handler) RemoveContainer(c echo.Context) error {
	ctx := c.Request().Context()
	id := c.Param("id")
	if err := h.Docker.RemoveContainer(ctx, id); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDockerError, response.SanitizeOutput(err.Error()))
	}
	return response.OK(c, map[string]string{"message": "container removed"})
}

// InspectContainer returns detailed inspection data for a container.
func (h *Handler) InspectContainer(c echo.Context) error {
	ctx := c.Request().Context()
	id := c.Param("id")
	data, err := h.Docker.GetContainer(ctx, id)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDockerError, response.SanitizeOutput(err.Error()))
	}

	// Build a clean response with the most useful fields
	ports := []map[string]interface{}{}
	if data.NetworkSettings != nil {
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

	imageName, workingDir, hostname := "", "", ""
	if data.Config != nil {
		imageName = data.Config.Image
		workingDir = data.Config.WorkingDir
		hostname = data.Config.Hostname
	}
	state, startedAt, finishedAt := "", "", ""
	if data.State != nil {
		state = data.State.Status
		startedAt = data.State.StartedAt
		finishedAt = data.State.FinishedAt
	}

	result := map[string]interface{}{
		"id":            data.ID,
		"name":          strings.TrimPrefix(data.Name, "/"),
		"image":         imageName,
		"state":         state,
		"started_at":    startedAt,
		"finished_at":   finishedAt,
		"restart_count": data.RestartCount,
		"platform":      data.Platform,
		"cmd":           cmd,
		"entrypoint":    entrypoint,
		"working_dir":   workingDir,
		"hostname":      hostname,
		"ports":         ports,
		"env":           envVars,
		"mounts":        mounts,
		"networks":      networks,
	}

	return response.OK(c, result)
}

// ContainerStats returns CPU and memory usage stats for a container.
func (h *Handler) ContainerStats(c echo.Context) error {
	ctx := c.Request().Context()
	id := c.Param("id")
	stats, err := h.Docker.CalcContainerStats(ctx, id)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDockerError, response.SanitizeOutput(err.Error()))
	}
	return response.OK(c, stats)
}

// ContainerStatsBatch returns CPU and memory stats for all running containers.
func (h *Handler) ContainerStatsBatch(c echo.Context) error {
	ctx := c.Request().Context()
	stats, err := h.Docker.ContainerStatsBatch(ctx)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDockerError, response.SanitizeOutput(err.Error()))
	}
	return response.OK(c, stats)
}

// ---------- Images ----------

// ListImages returns all local Docker images with usage information.
func (h *Handler) ListImages(c echo.Context) error {
	ctx := c.Request().Context()
	images, err := h.Docker.ListImagesWithUsage(ctx)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDockerError, response.SanitizeOutput(err.Error()))
	}
	return response.OK(c, images)
}

// PullImage pulls an image by reference with SSE streaming progress.
// Accepts JSON body: {"image": "nginx:latest"}.
func (h *Handler) PullImage(c echo.Context) error {
	var req struct {
		Image string `json:"image"`
	}
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}
	if req.Image == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrMissingFields, "Image reference is required")
	}

	ctx := c.Request().Context()
	reader, err := h.Docker.PullImage(ctx, req.Image)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDockerError, response.SanitizeOutput(err.Error()))
	}
	defer reader.Close()

	// SSE streaming
	c.Response().Header().Set("Content-Type", "text/event-stream")
	c.Response().Header().Set("Cache-Control", "no-cache")
	c.Response().Header().Set("Connection", "keep-alive")
	c.Response().WriteHeader(http.StatusOK)

	decoder := json.NewDecoder(reader)
	flusher := c.Response()
	for {
		var event map[string]interface{}
		if err := decoder.Decode(&event); err != nil {
			break
		}
		data, _ := json.Marshal(event)
		fmt.Fprintf(flusher, "data: %s\n\n", data)
		flusher.Flush()
	}

	completeData, _ := json.Marshal(map[string]string{"status": "complete", "image": req.Image})
	fmt.Fprintf(flusher, "data: %s\n\n", completeData)
	flusher.Flush()
	return nil
}

// RemoveImage removes an image by ID.
func (h *Handler) RemoveImage(c echo.Context) error {
	ctx := c.Request().Context()
	id := c.Param("id")
	if err := h.Docker.RemoveImage(ctx, id); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDockerError, response.SanitizeOutput(err.Error()))
	}
	return response.OK(c, map[string]string{"message": "image removed"})
}

// CheckImageUpdates checks for updates for images used by running containers.
func (h *Handler) CheckImageUpdates(c echo.Context) error {
	ctx := c.Request().Context()
	containers, err := h.Docker.ListContainers(ctx)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDockerError, response.SanitizeOutput(err.Error()))
	}

	// Collect unique images from running containers
	imageSet := make(map[string]bool)
	for _, ct := range containers {
		if ct.State == "running" && ct.Image != "" {
			imageSet[ct.Image] = true
		}
	}

	var results []docker.ImageUpdateStatus
	for img := range imageSet {
		status, err := h.Docker.CheckImageUpdate(ctx, img)
		if err != nil {
			continue
		}
		results = append(results, *status)
	}
	if results == nil {
		results = []docker.ImageUpdateStatus{}
	}
	return response.OK(c, results)
}

// ---------- Volumes ----------

// ListVolumes returns all Docker volumes with usage information.
func (h *Handler) ListVolumes(c echo.Context) error {
	ctx := c.Request().Context()
	volumes, err := h.Docker.ListVolumesWithUsage(ctx)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDockerError, response.SanitizeOutput(err.Error()))
	}
	return response.OK(c, volumes)
}

// CreateVolume creates a volume. Accepts JSON body: {"name": "myvolume"}.
func (h *Handler) CreateVolume(c echo.Context) error {
	var req struct {
		Name string `json:"name"`
	}
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}
	if req.Name == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrMissingFields, "Volume name is required")
	}

	ctx := c.Request().Context()
	vol, err := h.Docker.CreateVolume(ctx, req.Name)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDockerError, response.SanitizeOutput(err.Error()))
	}
	return response.OK(c, vol)
}

// RemoveVolume removes a volume by name.
func (h *Handler) RemoveVolume(c echo.Context) error {
	ctx := c.Request().Context()
	name := c.Param("name")
	force := c.QueryParam("force") == "true"
	if err := h.Docker.RemoveVolume(ctx, name, force); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDockerError, response.SanitizeOutput(err.Error()))
	}
	return response.OK(c, map[string]string{"message": "volume removed"})
}

// ---------- Networks ----------

// ListNetworks returns all Docker networks with usage information.
func (h *Handler) ListNetworks(c echo.Context) error {
	ctx := c.Request().Context()
	networks, err := h.Docker.ListNetworksWithUsage(ctx)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDockerError, response.SanitizeOutput(err.Error()))
	}
	return response.OK(c, networks)
}

// CreateNetwork creates a network. Accepts JSON body: {"name": "mynet", "driver": "bridge"}.
func (h *Handler) CreateNetwork(c echo.Context) error {
	var req struct {
		Name   string `json:"name"`
		Driver string `json:"driver"`
	}
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}
	if req.Name == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrMissingFields, "Network name is required")
	}
	if req.Driver == "" {
		req.Driver = "bridge"
	}

	ctx := c.Request().Context()
	net, err := h.Docker.CreateNetwork(ctx, req.Name, req.Driver)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDockerError, response.SanitizeOutput(err.Error()))
	}
	return response.OK(c, net)
}

// RemoveNetwork removes a network by ID.
func (h *Handler) RemoveNetwork(c echo.Context) error {
	ctx := c.Request().Context()
	id := c.Param("id")
	if err := h.Docker.RemoveNetwork(ctx, id); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDockerError, response.SanitizeOutput(err.Error()))
	}
	return response.OK(c, map[string]string{"message": "network removed"})
}

// InspectNetwork returns detailed information about a network.
func (h *Handler) InspectNetwork(c echo.Context) error {
	ctx := c.Request().Context()
	id := c.Param("id")
	netInfo, err := h.Docker.InspectNetwork(ctx, id)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDockerError, response.SanitizeOutput(err.Error()))
	}

	// Build clean response
	containers := []map[string]string{}
	for cid, endpoint := range netInfo.Containers {
		shortID := cid
		if len(cid) > 12 {
			shortID = cid[:12]
		}
		containers = append(containers, map[string]string{
			"id":           shortID,
			"name":         endpoint.Name,
			"ipv4_address": endpoint.IPv4Address,
			"ipv6_address": endpoint.IPv6Address,
			"mac_address":  endpoint.MacAddress,
		})
	}

	subnet := ""
	gateway := ""
	if len(netInfo.IPAM.Config) > 0 {
		subnet = netInfo.IPAM.Config[0].Subnet
		gateway = netInfo.IPAM.Config[0].Gateway
	}

	result := map[string]interface{}{
		"id":         netInfo.ID,
		"name":       netInfo.Name,
		"driver":     netInfo.Driver,
		"scope":      netInfo.Scope,
		"internal":   netInfo.Internal,
		"subnet":     subnet,
		"gateway":    gateway,
		"containers": containers,
		"created":    netInfo.Created,
	}
	return response.OK(c, result)
}

// ---------- Prune ----------

// PruneContainers removes all stopped containers.
func (h *Handler) PruneContainers(c echo.Context) error {
	ctx := c.Request().Context()
	report, err := h.Docker.PruneContainers(ctx)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDockerError, response.SanitizeOutput(err.Error()))
	}
	return response.OK(c, map[string]interface{}{
		"deleted":         len(report.ContainersDeleted),
		"space_reclaimed": report.SpaceReclaimed,
	})
}

// PruneImages removes dangling images.
func (h *Handler) PruneImages(c echo.Context) error {
	ctx := c.Request().Context()
	report, err := h.Docker.PruneImages(ctx)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDockerError, response.SanitizeOutput(err.Error()))
	}
	return response.OK(c, map[string]interface{}{
		"deleted":         len(report.ImagesDeleted),
		"space_reclaimed": report.SpaceReclaimed,
	})
}

// PruneVolumes removes unused volumes.
func (h *Handler) PruneVolumes(c echo.Context) error {
	ctx := c.Request().Context()
	report, err := h.Docker.PruneVolumes(ctx)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDockerError, response.SanitizeOutput(err.Error()))
	}
	return response.OK(c, map[string]interface{}{
		"deleted":         len(report.VolumesDeleted),
		"space_reclaimed": report.SpaceReclaimed,
	})
}

// PruneNetworks removes unused networks.
func (h *Handler) PruneNetworks(c echo.Context) error {
	ctx := c.Request().Context()
	report, err := h.Docker.PruneNetworks(ctx)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDockerError, response.SanitizeOutput(err.Error()))
	}
	return response.OK(c, map[string]interface{}{
		"deleted": len(report.NetworksDeleted),
	})
}

// PruneAll removes all unused Docker resources.
func (h *Handler) PruneAll(c echo.Context) error {
	ctx := c.Request().Context()

	pruneErrors := make([]string, 0)

	containerReport, err := h.Docker.PruneContainers(ctx)
	if err != nil {
		pruneErrors = append(pruneErrors, "containers: "+err.Error())
	}
	imageReport, err := h.Docker.PruneImages(ctx)
	if err != nil {
		pruneErrors = append(pruneErrors, "images: "+err.Error())
	}
	volumeReport, err := h.Docker.PruneVolumes(ctx)
	if err != nil {
		pruneErrors = append(pruneErrors, "volumes: "+err.Error())
	}
	networkReport, err := h.Docker.PruneNetworks(ctx)
	if err != nil {
		pruneErrors = append(pruneErrors, "networks: "+err.Error())
	}

	result := map[string]interface{}{
		"containers": map[string]interface{}{
			"deleted":         safeLen(containerReport.ContainersDeleted),
			"space_reclaimed": containerReport.SpaceReclaimed,
		},
		"images": map[string]interface{}{
			"deleted":         safeLen(imageReport.ImagesDeleted),
			"space_reclaimed": imageReport.SpaceReclaimed,
		},
		"volumes": map[string]interface{}{
			"deleted":         safeLen(volumeReport.VolumesDeleted),
			"space_reclaimed": volumeReport.SpaceReclaimed,
		},
		"networks": map[string]interface{}{
			"deleted": safeLen(networkReport.NetworksDeleted),
		},
	}

	if len(pruneErrors) > 0 {
		result["partial_failure"] = true
		result["errors"] = pruneErrors
	}

	return response.OK(c, result)
}

// ---------- Docker Hub Search ----------

// SearchImages searches Docker Hub for images.
func (h *Handler) SearchImages(c echo.Context) error {
	q := c.QueryParam("q")
	if q == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrMissingFields, "Search query (q) is required")
	}

	limit := 25
	if l := c.QueryParam("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	ctx := c.Request().Context()
	results, err := h.Docker.SearchImages(ctx, q, limit)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDockerError, response.SanitizeOutput(err.Error()))
	}

	// Transform to cleaner response format
	items := make([]map[string]interface{}, 0, len(results))
	for _, r := range results {
		items = append(items, map[string]interface{}{
			"name":        r.Name,
			"description": r.Description,
			"star_count":  r.StarCount,
			"is_official": r.IsOfficial,
		})
	}

	return response.OK(c, items)
}

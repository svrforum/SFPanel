package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/registry"
	"github.com/docker/docker/api/types/volume"
	"github.com/docker/docker/client"
)

// cache holds a generic cached result with expiration.
type cache[T any] struct {
	mu      sync.Mutex
	data    T
	expires time.Time
}

// get returns cached data if still valid. ok is false on miss/expired.
func (c *cache[T]) get() (T, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Now().Before(c.expires) {
		return c.data, true
	}
	var zero T
	return zero, false
}

// set stores data with the given TTL.
func (c *cache[T]) set(data T, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data = data
	c.expires = time.Now().Add(ttl)
}

// Client wraps the Docker SDK client with convenience methods for
// container, image, volume, and network management.
type Client struct {
	cli *client.Client

	containersCache cache[[]types.Container]
	imagesCache     cache[[]ImageWithUsage]
}

// NewClient creates a new Docker client connected to the given host
// (e.g. "unix:///var/run/docker.sock").
func NewClient(host string) (*Client, error) {
	cli, err := client.NewClientWithOpts(
		client.WithHost(host),
		client.WithAPIVersionNegotiation(),
	)
	if err != nil {
		return nil, err
	}
	return &Client{cli: cli}, nil
}

// ---------- Containers ----------

// ListContainers returns all containers (running and stopped).
func (c *Client) ListContainers(ctx context.Context) ([]types.Container, error) {
	return c.cli.ContainerList(ctx, container.ListOptions{All: true})
}

const containersCacheTTL = 5 * time.Second

// listContainersCached returns containers from cache or fetches fresh data.
func (c *Client) listContainersCached(ctx context.Context) ([]types.Container, error) {
	if data, ok := c.containersCache.get(); ok {
		return data, nil
	}
	containers, err := c.ListContainers(ctx)
	if err != nil {
		return nil, err
	}
	c.containersCache.set(containers, containersCacheTTL)
	return containers, nil
}

// GetContainer inspects a single container by ID or name.
func (c *Client) GetContainer(ctx context.Context, id string) (types.ContainerJSON, error) {
	return c.cli.ContainerInspect(ctx, id)
}

// StartContainer starts a stopped container.
func (c *Client) StartContainer(ctx context.Context, id string) error {
	return c.cli.ContainerStart(ctx, id, container.StartOptions{})
}

// StopContainer gracefully stops a container with a 10-second timeout.
func (c *Client) StopContainer(ctx context.Context, id string) error {
	timeout := 10
	return c.cli.ContainerStop(ctx, id, container.StopOptions{Timeout: &timeout})
}

// RestartContainer restarts a container with a 10-second timeout.
func (c *Client) RestartContainer(ctx context.Context, id string) error {
	timeout := 10
	return c.cli.ContainerRestart(ctx, id, container.StopOptions{Timeout: &timeout})
}

// RemoveContainer forcefully removes a container.
func (c *Client) RemoveContainer(ctx context.Context, id string) error {
	return c.cli.ContainerRemove(ctx, id, container.RemoveOptions{Force: true})
}

// PauseContainer pauses a running container.
func (c *Client) PauseContainer(ctx context.Context, id string) error {
	return c.cli.ContainerPause(ctx, id)
}

// UnpauseContainer unpauses a paused container.
func (c *Client) UnpauseContainer(ctx context.Context, id string) error {
	return c.cli.ContainerUnpause(ctx, id)
}

// ContainerLogs returns a log stream for the given container. The stream
// follows new output and includes both stdout and stderr, tailing the
// last 100 lines.
func (c *Client) ContainerLogs(ctx context.Context, id string) (io.ReadCloser, error) {
	return c.cli.ContainerLogs(ctx, id, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     true,
		Tail:       "100",
	})
}

// ContainerExec creates and attaches to an exec instance in the given
// container. The default command is /bin/sh. It returns the hijacked
// connection for bidirectional I/O and the exec ID.
func (c *Client) ContainerExec(ctx context.Context, id string, cmd []string) (types.HijackedResponse, string, error) {
	if len(cmd) == 0 {
		cmd = []string{"/bin/sh"}
	}

	execConfig := container.ExecOptions{
		Cmd:          cmd,
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
		Tty:          true,
	}

	exec, err := c.cli.ContainerExecCreate(ctx, id, execConfig)
	if err != nil {
		return types.HijackedResponse{}, "", err
	}

	resp, err := c.cli.ContainerExecAttach(ctx, exec.ID, container.ExecAttachOptions{Tty: true})
	if err != nil {
		return types.HijackedResponse{}, "", err
	}

	return resp, exec.ID, nil
}

// ExecResize resizes the TTY of a running exec instance.
func (c *Client) ExecResize(ctx context.Context, execID string, cols, rows int) error {
	return c.cli.ContainerExecResize(ctx, execID, container.ResizeOptions{
		Width:  uint(cols),
		Height: uint(rows),
	})
}

// ContainerStats returns a one-shot stats snapshot for the given container.
func (c *Client) ContainerStats(ctx context.Context, id string) (*container.StatsResponse, error) {
	resp, err := c.cli.ContainerStatsOneShot(ctx, id)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var stats container.StatsResponse
	decoder := json.NewDecoder(resp.Body)
	if err := decoder.Decode(&stats); err != nil {
		return nil, err
	}
	return &stats, nil
}

// ContainerStatsResult holds calculated CPU/memory stats for a single container.
type ContainerStatsResult struct {
	ID         string  `json:"id"`
	CPUPercent float64 `json:"cpu_percent"`
	MemPercent float64 `json:"mem_percent"`
	MemUsage   uint64  `json:"mem_usage"`
	MemLimit   uint64  `json:"mem_limit"`
}

// calcMemUsage returns the actual memory usage by subtracting cache.
// cgroups v1 uses stats["cache"], cgroups v2 uses stats["inactive_file"].
func calcMemUsage(stats *container.StatsResponse) uint64 {
	usage := stats.MemoryStats.Usage
	if cache, ok := stats.MemoryStats.Stats["inactive_file"]; ok && cache > 0 {
		// cgroups v2
		if usage > cache {
			return usage - cache
		}
	} else if cache, ok := stats.MemoryStats.Stats["cache"]; ok && cache > 0 {
		// cgroups v1
		if usage > cache {
			return usage - cache
		}
	}
	return usage
}

// CalcContainerStats returns calculated CPU/memory stats for a single container.
func (c *Client) CalcContainerStats(ctx context.Context, id string) (*ContainerStatsResult, error) {
	stats, err := c.ContainerStats(ctx, id)
	if err != nil {
		return nil, err
	}

	cpuDelta := float64(stats.CPUStats.CPUUsage.TotalUsage - stats.PreCPUStats.CPUUsage.TotalUsage)
	systemDelta := float64(stats.CPUStats.SystemUsage - stats.PreCPUStats.SystemUsage)
	cpuPercent := 0.0
	if systemDelta > 0 && cpuDelta > 0 {
		cpuPercent = (cpuDelta / systemDelta) * float64(stats.CPUStats.OnlineCPUs) * 100.0
	}

	memUsage := calcMemUsage(stats)
	memLimit := stats.MemoryStats.Limit
	memPercent := 0.0
	if memLimit > 0 {
		memPercent = float64(memUsage) / float64(memLimit) * 100.0
	}

	return &ContainerStatsResult{
		ID:         id,
		CPUPercent: cpuPercent,
		MemPercent: memPercent,
		MemUsage:   memUsage,
		MemLimit:   memLimit,
	}, nil
}

// statsBatchConcurrency limits parallel Docker stats API calls.
const statsBatchConcurrency = 5

// ContainerStatsBatch returns CPU/memory stats for all running containers in parallel.
func (c *Client) ContainerStatsBatch(ctx context.Context) ([]ContainerStatsResult, error) {
	containers, err := c.listContainersCached(ctx)
	if err != nil {
		return nil, err
	}

	// Filter running containers.
	var running []types.Container
	for _, ct := range containers {
		if ct.State == "running" {
			running = append(running, ct)
		}
	}
	if len(running) == 0 {
		return []ContainerStatsResult{}, nil
	}

	type indexedResult struct {
		index  int
		result ContainerStatsResult
		ok     bool
	}

	resultsCh := make(chan indexedResult, len(running))
	sem := make(chan struct{}, statsBatchConcurrency)
	var wg sync.WaitGroup

	for i, ct := range running {
		wg.Add(1)
		go func(idx int, id string) {
			defer wg.Done()
			sem <- struct{}{}        // acquire
			defer func() { <-sem }() // release

			stats, err := c.ContainerStats(ctx, id)
			if err != nil {
				resultsCh <- indexedResult{index: idx, ok: false}
				return
			}

			cpuDelta := float64(stats.CPUStats.CPUUsage.TotalUsage - stats.PreCPUStats.CPUUsage.TotalUsage)
			systemDelta := float64(stats.CPUStats.SystemUsage - stats.PreCPUStats.SystemUsage)
			cpuPercent := 0.0
			if systemDelta > 0 && cpuDelta > 0 {
				cpuPercent = (cpuDelta / systemDelta) * float64(stats.CPUStats.OnlineCPUs) * 100.0
			}

			memUsage := calcMemUsage(stats)
			memLimit := stats.MemoryStats.Limit
			memPercent := 0.0
			if memLimit > 0 {
				memPercent = float64(memUsage) / float64(memLimit) * 100.0
			}

			resultsCh <- indexedResult{
				index: idx,
				ok:    true,
				result: ContainerStatsResult{
					ID:         id,
					CPUPercent: cpuPercent,
					MemPercent: memPercent,
					MemUsage:   memUsage,
					MemLimit:   memLimit,
				},
			}
		}(i, ct.ID)
	}

	wg.Wait()
	close(resultsCh)

	results := make([]ContainerStatsResult, 0, len(running))
	for ir := range resultsCh {
		if ir.ok {
			results = append(results, ir.result)
		}
	}
	return results, nil
}

// ---------- Images ----------

// ListImages returns all local images.
func (c *Client) ListImages(ctx context.Context) ([]image.Summary, error) {
	return c.cli.ImageList(ctx, image.ListOptions{})
}

// PullImage pulls an image by reference (e.g. "nginx:latest") and
// returns a progress reader.
func (c *Client) PullImage(ctx context.Context, ref string) (io.ReadCloser, error) {
	return c.cli.ImagePull(ctx, ref, image.PullOptions{})
}

// RemoveImage removes an image by ID or reference.
func (c *Client) RemoveImage(ctx context.Context, id string) error {
	_, err := c.cli.ImageRemove(ctx, id, image.RemoveOptions{Force: true, PruneChildren: true})
	return err
}

// InspectImage returns detailed information about an image.
func (c *Client) InspectImage(ctx context.Context, id string) (types.ImageInspect, error) {
	resp, _, err := c.cli.ImageInspectWithRaw(ctx, id)
	return resp, err
}

// ImageUpdateStatus holds update check result for a single image.
type ImageUpdateStatus struct {
	Image     string `json:"image"`
	CurrentID string `json:"current_id"`
	HasUpdate bool   `json:"has_update"`
	Error     string `json:"error,omitempty"`
}

// CheckImageUpdate checks if a newer version exists by comparing digests.
func (c *Client) CheckImageUpdate(ctx context.Context, imageRef string) (*ImageUpdateStatus, error) {
	status := &ImageUpdateStatus{Image: imageRef}

	localInspect, _, err := c.cli.ImageInspectWithRaw(ctx, imageRef)
	if err != nil {
		status.Error = fmt.Sprintf("inspect: %v", err)
		return status, nil
	}
	id := strings.TrimPrefix(localInspect.ID, "sha256:")
	if len(id) > 12 {
		id = id[:12]
	}
	status.CurrentID = id

	// Use DistributionInspect to get remote digest without pulling
	distInspect, err := c.cli.DistributionInspect(ctx, imageRef, "")
	if err != nil {
		// If distribution inspect fails (auth, private registry), skip
		status.Error = fmt.Sprintf("registry: %v", err)
		return status, nil
	}

	remoteDigest := string(distInspect.Descriptor.Digest)
	hasMatch := false
	for _, d := range localInspect.RepoDigests {
		if strings.Contains(d, remoteDigest) {
			hasMatch = true
			break
		}
	}
	status.HasUpdate = !hasMatch
	return status, nil
}

// ---------- Volumes ----------

// ListVolumes returns all Docker volumes.
func (c *Client) ListVolumes(ctx context.Context) ([]*volume.Volume, error) {
	resp, err := c.cli.VolumeList(ctx, volume.ListOptions{})
	if err != nil {
		return nil, err
	}
	return resp.Volumes, nil
}

// CreateVolume creates a volume with the given name.
func (c *Client) CreateVolume(ctx context.Context, name string) (*volume.Volume, error) {
	vol, err := c.cli.VolumeCreate(ctx, volume.CreateOptions{Name: name})
	if err != nil {
		return nil, err
	}
	return &vol, nil
}

// RemoveVolume removes a volume by name.
func (c *Client) RemoveVolume(ctx context.Context, name string, force bool) error {
	return c.cli.VolumeRemove(ctx, name, force)
}

// ---------- Networks ----------

// ListNetworks returns all Docker networks.
func (c *Client) ListNetworks(ctx context.Context) ([]network.Summary, error) {
	return c.cli.NetworkList(ctx, network.ListOptions{})
}

// CreateNetwork creates a network with the given name and driver.
func (c *Client) CreateNetwork(ctx context.Context, name, driver string) (*network.CreateResponse, error) {
	resp, err := c.cli.NetworkCreate(ctx, name, network.CreateOptions{Driver: driver})
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// RemoveNetwork removes a network by ID.
func (c *Client) RemoveNetwork(ctx context.Context, id string) error {
	return c.cli.NetworkRemove(ctx, id)
}

// InspectNetwork returns detailed information about a network.
func (c *Client) InspectNetwork(ctx context.Context, id string) (network.Inspect, error) {
	return c.cli.NetworkInspect(ctx, id, network.InspectOptions{})
}

// ---------- Prune ----------

// PruneContainers removes all stopped containers.
func (c *Client) PruneContainers(ctx context.Context) (container.PruneReport, error) {
	return c.cli.ContainersPrune(ctx, filters.Args{})
}

// PruneImages removes dangling images.
func (c *Client) PruneImages(ctx context.Context) (image.PruneReport, error) {
	return c.cli.ImagesPrune(ctx, filters.Args{})
}

// PruneVolumes removes unused volumes.
func (c *Client) PruneVolumes(ctx context.Context) (volume.PruneReport, error) {
	return c.cli.VolumesPrune(ctx, filters.Args{})
}

// PruneNetworks removes unused networks.
func (c *Client) PruneNetworks(ctx context.Context) (network.PruneReport, error) {
	return c.cli.NetworksPrune(ctx, filters.Args{})
}

// ---------- Docker Hub Search ----------

// SearchImages searches Docker Hub for images matching the given term.
func (c *Client) SearchImages(ctx context.Context, term string, limit int) ([]registry.SearchResult, error) {
	if limit <= 0 {
		limit = 25
	}
	results, err := c.cli.ImageSearch(ctx, term, registry.SearchOptions{Limit: limit})
	if err != nil {
		return nil, fmt.Errorf("search images: %w", err)
	}
	return results, nil
}

// ---------- Enriched List with Usage Info ----------

// ImageWithUsage wraps image data with container usage information.
type ImageWithUsage struct {
	image.Summary
	InUse  bool     `json:"in_use"`
	UsedBy []string `json:"used_by"`
}

const imagesCacheTTL = 10 * time.Second

// ListImagesWithUsage returns all images with container usage information.
// Results are cached for 10 seconds.
func (c *Client) ListImagesWithUsage(ctx context.Context) ([]ImageWithUsage, error) {
	if data, ok := c.imagesCache.get(); ok {
		return data, nil
	}
	containers, err := c.listContainersCached(ctx)
	if err != nil {
		return nil, err
	}
	result, err := c.listImagesWithContainers(ctx, containers)
	if err != nil {
		return nil, err
	}
	c.imagesCache.set(result, imagesCacheTTL)
	return result, nil
}

// listImagesWithContainers returns all images with usage info derived from the provided containers.
func (c *Client) listImagesWithContainers(ctx context.Context, containers []types.Container) ([]ImageWithUsage, error) {
	images, err := c.ListImages(ctx)
	if err != nil {
		return nil, err
	}

	usageMap := make(map[string][]string)
	for _, ctr := range containers {
		name := ""
		if len(ctr.Names) > 0 {
			name = strings.TrimPrefix(ctr.Names[0], "/")
		}
		usageMap[ctr.ImageID] = append(usageMap[ctr.ImageID], name)
	}

	result := make([]ImageWithUsage, len(images))
	for i, img := range images {
		usedBy := usageMap[img.ID]
		if usedBy == nil {
			usedBy = []string{}
		}
		result[i] = ImageWithUsage{
			Summary: img,
			InUse:   len(usedBy) > 0,
			UsedBy:  usedBy,
		}
	}
	return result, nil
}

// VolumeWithUsage wraps volume data with container usage information.
type VolumeWithUsage struct {
	Name       string   `json:"Name"`
	Driver     string   `json:"Driver"`
	Mountpoint string   `json:"Mountpoint"`
	CreatedAt  string   `json:"CreatedAt"`
	InUse      bool     `json:"in_use"`
	UsedBy     []string `json:"used_by"`
}

// ListVolumesWithUsage returns all volumes with container usage information.
func (c *Client) ListVolumesWithUsage(ctx context.Context) ([]VolumeWithUsage, error) {
	containers, err := c.listContainersCached(ctx)
	if err != nil {
		return nil, err
	}
	return c.listVolumesWithContainers(ctx, containers)
}

// listVolumesWithContainers returns all volumes with usage info derived from the provided containers.
func (c *Client) listVolumesWithContainers(ctx context.Context, containers []types.Container) ([]VolumeWithUsage, error) {
	volumes, err := c.ListVolumes(ctx)
	if err != nil {
		return nil, err
	}

	usageMap := make(map[string][]string)
	for _, ctr := range containers {
		name := ""
		if len(ctr.Names) > 0 {
			name = strings.TrimPrefix(ctr.Names[0], "/")
		}
		for _, m := range ctr.Mounts {
			if m.Type == "volume" {
				usageMap[m.Name] = append(usageMap[m.Name], name)
			}
		}
	}

	result := make([]VolumeWithUsage, len(volumes))
	for i, vol := range volumes {
		usedBy := usageMap[vol.Name]
		if usedBy == nil {
			usedBy = []string{}
		}
		result[i] = VolumeWithUsage{
			Name:       vol.Name,
			Driver:     vol.Driver,
			Mountpoint: vol.Mountpoint,
			CreatedAt:  vol.CreatedAt,
			InUse:      len(usedBy) > 0,
			UsedBy:     usedBy,
		}
	}
	return result, nil
}

// NetworkWithUsage wraps network data with container usage information.
type NetworkWithUsage struct {
	Id     string   `json:"Id"`
	Name   string   `json:"Name"`
	Driver string   `json:"Driver"`
	Scope  string   `json:"Scope"`
	InUse  bool     `json:"in_use"`
	UsedBy []string `json:"used_by"`
}

// ListNetworksWithUsage returns all networks with container usage information.
func (c *Client) ListNetworksWithUsage(ctx context.Context) ([]NetworkWithUsage, error) {
	containers, err := c.listContainersCached(ctx)
	if err != nil {
		return nil, err
	}
	return c.listNetworksWithContainers(ctx, containers)
}

// listNetworksWithContainers returns all networks with usage info derived from the provided containers.
func (c *Client) listNetworksWithContainers(ctx context.Context, containers []types.Container) ([]NetworkWithUsage, error) {
	networks, err := c.ListNetworks(ctx)
	if err != nil {
		return nil, err
	}

	usageMap := make(map[string][]string)
	for _, ctr := range containers {
		name := ""
		if len(ctr.Names) > 0 {
			name = strings.TrimPrefix(ctr.Names[0], "/")
		}
		if ctr.NetworkSettings != nil {
			for _, net := range ctr.NetworkSettings.Networks {
				if net != nil {
					usageMap[net.NetworkID] = append(usageMap[net.NetworkID], name)
				}
			}
		}
	}

	result := make([]NetworkWithUsage, len(networks))
	for i, net := range networks {
		usedBy := usageMap[net.ID]
		if usedBy == nil {
			usedBy = []string{}
		}
		result[i] = NetworkWithUsage{
			Id:     net.ID,
			Name:   net.Name,
			Driver: net.Driver,
			Scope:  net.Scope,
			InUse:  len(usedBy) > 0,
			UsedBy: usedBy,
		}
	}
	return result, nil
}

// ---------- Compose Project Containers ----------

// ListContainersByComposeProject returns containers belonging to a specific compose project.
func (c *Client) ListContainersByComposeProject(ctx context.Context, project string) ([]types.Container, error) {
	f := filters.NewArgs()
	f.Add("label", fmt.Sprintf("com.docker.compose.project=%s", project))
	return c.cli.ContainerList(ctx, container.ListOptions{All: true, Filters: f})
}

// ListContainersByComposeWorkingDir returns containers belonging to a compose project by working directory.
func (c *Client) ListContainersByComposeWorkingDir(ctx context.Context, workingDir string) ([]types.Container, error) {
	f := filters.NewArgs()
	f.Add("label", fmt.Sprintf("com.docker.compose.project.working_dir=%s", workingDir))
	return c.cli.ContainerList(ctx, container.ListOptions{All: true, Filters: f})
}

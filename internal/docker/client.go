package docker

import (
	"context"
	"encoding/json"
	"io"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/volume"
	"github.com/docker/docker/client"
)

// Client wraps the Docker SDK client with convenience methods for
// container, image, volume, and network management.
type Client struct {
	cli *client.Client
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
func (c *Client) RemoveVolume(ctx context.Context, name string) error {
	return c.cli.VolumeRemove(ctx, name, true)
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

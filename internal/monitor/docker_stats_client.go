package monitor

import (
	"context"

	"github.com/docker/docker/api/types/container"
)

// DockerStatsClient is the small subset of the Docker SDK that the metrics
// collector goroutine calls. Defined as a named interface here (instead of
// taking *docker.Client directly) so the collector can be tested with a
// fake — Docker SDK doesn't ship mocks and starting a real daemon for
// unit tests is overkill.
type DockerStatsClient interface {
	ListContainers(ctx context.Context) ([]container.Summary, error)
	ContainerStats(ctx context.Context, id string) (*container.StatsResponse, error)
}

package compose

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestComputeDiff_ImageChange(t *testing.T) {
	deployed := `services:
  web:
    image: nginx:1.24
    ports:
      - "8080:80"
`
	proposed := `services:
  web:
    image: nginx:1.25
    ports:
      - "8080:80"
`
	got, err := ComputeDiff(deployed, proposed)
	require.NoError(t, err)

	require.Equal(t, 0, got.Summary.Added)
	require.Equal(t, 1, got.Summary.Modified)
	require.Equal(t, 0, got.Summary.Removed)

	images, ok := got.ByCategory["image"].([]ImageChange)
	require.True(t, ok, "ByCategory[image] should be []ImageChange")
	require.Len(t, images, 1)
	require.Equal(t, "web", images[0].Service)
	require.Equal(t, "nginx:1.24", images[0].From)
	require.Equal(t, "nginx:1.25", images[0].To)
}

func TestComputeDiff_PortsAddedAndRemoved(t *testing.T) {
	deployed := `services:
  api:
    image: api:1
    ports:
      - "8080:80"
      - "8443:443"
`
	proposed := `services:
  api:
    image: api:1
    ports:
      - "8081:80"
      - "8443:443"
`
	got, err := ComputeDiff(deployed, proposed)
	require.NoError(t, err)
	ports, ok := got.ByCategory["ports"].([]SetChange)
	require.True(t, ok)
	require.Len(t, ports, 1)
	require.Equal(t, "api", ports[0].Service)
	require.Equal(t, []string{"8081:80"}, ports[0].Added)
	require.Equal(t, []string{"8080:80"}, ports[0].Removed)
}

func TestComputeDiff_VolumesUnchanged_NotInOutput(t *testing.T) {
	deployed := `services:
  db:
    image: pg:16
    volumes:
      - db-data:/var/lib/postgresql/data
`
	proposed := `services:
  db:
    image: pg:16
    volumes:
      - db-data:/var/lib/postgresql/data
`
	got, err := ComputeDiff(deployed, proposed)
	require.NoError(t, err)
	require.Empty(t, got.ByCategory["volumes"])
}

func TestComputeDiff_EnvMapAndListForms(t *testing.T) {
	// docker-compose accepts both list and map env. Treat both as map.
	deployed := `services:
  app:
    image: app:1
    environment:
      LOG_LEVEL: info
      DEBUG: "false"
`
	proposed := `services:
  app:
    image: app:1
    environment:
      - LOG_LEVEL=debug
      - DEBUG=false
`
	got, err := ComputeDiff(deployed, proposed)
	require.NoError(t, err)
	env, ok := got.ByCategory["env"].([]SetChange)
	require.True(t, ok)
	require.Len(t, env, 1)
	require.Equal(t, "app", env[0].Service)
	require.Equal(t, []string{"LOG_LEVEL=debug"}, env[0].Added)
	require.Equal(t, []string{"LOG_LEVEL=info"}, env[0].Removed)
}

func TestComputeDiff_RestartPolicy(t *testing.T) {
	deployed := `services:
  web:
    image: nginx:1
`
	proposed := `services:
  web:
    image: nginx:1
    restart: unless-stopped
`
	got, err := ComputeDiff(deployed, proposed)
	require.NoError(t, err)
	rs, ok := got.ByCategory["restart"].([]ScalarChange)
	require.True(t, ok)
	require.Len(t, rs, 1)
	require.Equal(t, "web", rs[0].Service)
	require.Equal(t, "", rs[0].From)
	require.Equal(t, "unless-stopped", rs[0].To)
}

func TestComputeDiff_HealthcheckAdded(t *testing.T) {
	deployed := `services:
  web:
    image: nginx:1
`
	proposed := `services:
  web:
    image: nginx:1
    healthcheck:
      test: ["CMD", "curl", "-f", "http://localhost"]
      interval: 30s
      retries: 3
`
	got, err := ComputeDiff(deployed, proposed)
	require.NoError(t, err)
	hcs, ok := got.ByCategory["healthcheck"].([]HealthcheckChange)
	require.True(t, ok)
	require.Len(t, hcs, 1)
	require.Equal(t, "web", hcs[0].Service)
	require.Equal(t, "", hcs[0].From)
	require.NotEmpty(t, hcs[0].To)
}

func TestComputeDiff_ServiceAddedAndRemoved(t *testing.T) {
	deployed := `services:
  web:
    image: nginx:1
  legacy:
    image: legacy:1
`
	proposed := `services:
  web:
    image: nginx:1
  cache:
    image: redis:7
`
	got, err := ComputeDiff(deployed, proposed)
	require.NoError(t, err)
	require.Equal(t, 1, got.Summary.Added)
	require.Equal(t, 1, got.Summary.Removed)
	require.Equal(t, 0, got.Summary.Modified)
}

func TestComputeDiff_RawDiffPopulated(t *testing.T) {
	deployed := "services:\n  web:\n    image: nginx:1.24\n"
	proposed := "services:\n  web:\n    image: nginx:1.25\n"
	got, err := ComputeDiff(deployed, proposed)
	require.NoError(t, err)
	require.Contains(t, got.RawDiff, "nginx:1.24")
	require.Contains(t, got.RawDiff, "nginx:1.25")
	require.True(t, strings.HasPrefix(got.RawDiff, "--- deployed") || strings.Contains(got.RawDiff, "@@"))
}

func TestComputeDiff_InvalidProposedYAML(t *testing.T) {
	_, err := ComputeDiff("services: {}", "this is not yaml: [unclosed")
	require.Error(t, err)
}

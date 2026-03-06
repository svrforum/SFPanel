package docker

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Supported compose file names in priority order.
var composeFileNames = []string{
	"docker-compose.yml",
	"docker-compose.yaml",
	"compose.yml",
	"compose.yaml",
}

// ComposeProject represents a Docker Compose project discovered from the filesystem.
type ComposeProject struct {
	Name        string `json:"name"`
	ComposeFile string `json:"compose_file"` // filename found (e.g., "docker-compose.yml")
	HasEnv      bool   `json:"has_env"`
	Path        string `json:"path"` // full directory path
}

// ComposeService represents a single service within a compose project with its runtime state.
type ComposeService struct {
	Name        string `json:"name"`
	ContainerID string `json:"container_id"`
	Image       string `json:"image"`
	State       string `json:"state"`
	Status      string `json:"status"`
	Ports       string `json:"ports"`
}

// ComposeProjectWithStatus extends ComposeProject with runtime service counts.
type ComposeProjectWithStatus struct {
	ComposeProject
	ServiceCount int    `json:"service_count"`
	RunningCount int    `json:"running_count"`
	RealStatus   string `json:"real_status"` // running, partial, stopped
}

// ComposeManager manages Docker Compose projects by scanning a base directory
// and executing docker compose commands via os/exec.
type ComposeManager struct {
	baseDir      string // e.g., /opt/stacks
	dockerClient *Client
}

// NewComposeManager creates a new ComposeManager, ensuring the base directory exists.
func NewComposeManager(baseDir string, dockerClient *Client) *ComposeManager {
	os.MkdirAll(baseDir, 0755)
	return &ComposeManager{baseDir: baseDir, dockerClient: dockerClient}
}

// findComposeFile returns the compose filename found in the given directory, or empty string if none.
func findComposeFile(dir string) string {
	for _, name := range composeFileNames {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return name
		}
	}
	return ""
}

// ListProjects scans the base directory for subdirectories containing a compose file.
func (m *ComposeManager) ListProjects(_ context.Context) ([]ComposeProject, error) {
	entries, err := os.ReadDir(m.baseDir)
	if err != nil {
		return nil, fmt.Errorf("read stacks directory: %w", err)
	}

	var projects []ComposeProject
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(m.baseDir, entry.Name())
		composeFile := findComposeFile(dir)
		if composeFile == "" {
			continue
		}

		_, envErr := os.Stat(filepath.Join(dir, ".env"))
		projects = append(projects, ComposeProject{
			Name:        entry.Name(),
			ComposeFile: composeFile,
			HasEnv:      envErr == nil,
			Path:        dir,
		})
	}

	sort.Slice(projects, func(i, j int) bool {
		return projects[i].Name < projects[j].Name
	})
	return projects, nil
}

// GetProject returns a single compose project by name.
func (m *ComposeManager) GetProject(_ context.Context, name string) (*ComposeProject, error) {
	dir := filepath.Join(m.baseDir, name)
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("project %q not found", name)
	}

	composeFile := findComposeFile(dir)
	if composeFile == "" {
		return nil, fmt.Errorf("no compose file found in %q", name)
	}

	_, envErr := os.Stat(filepath.Join(dir, ".env"))
	return &ComposeProject{
		Name:        name,
		ComposeFile: composeFile,
		HasEnv:      envErr == nil,
		Path:        dir,
	}, nil
}

// GetProjectYAML reads the compose file content for a project.
func (m *ComposeManager) GetProjectYAML(_ context.Context, name string) (string, string, error) {
	dir := filepath.Join(m.baseDir, name)
	composeFile := findComposeFile(dir)
	if composeFile == "" {
		return "", "", fmt.Errorf("no compose file found in %q", name)
	}

	content, err := os.ReadFile(filepath.Join(dir, composeFile))
	if err != nil {
		return "", "", fmt.Errorf("read compose file: %w", err)
	}
	return string(content), composeFile, nil
}

// GetProjectEnv reads the .env file content for a project. Returns empty string if no .env exists.
func (m *ComposeManager) GetProjectEnv(_ context.Context, name string) (string, error) {
	envPath := filepath.Join(m.baseDir, name, ".env")
	content, err := os.ReadFile(envPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("read .env: %w", err)
	}
	return string(content), nil
}

// UpdateProjectEnv writes the .env file for a project.
func (m *ComposeManager) UpdateProjectEnv(_ context.Context, name, content string) error {
	dir := filepath.Join(m.baseDir, name)
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("project %q not found", name)
	}
	return os.WriteFile(filepath.Join(dir, ".env"), []byte(content), 0644)
}

// CreateProject creates a new compose project directory with a docker-compose.yml.
func (m *ComposeManager) CreateProject(_ context.Context, name, yamlContent string) (*ComposeProject, error) {
	projectDir := filepath.Join(m.baseDir, name)

	// Check if directory already exists with a compose file
	if composeFile := findComposeFile(projectDir); composeFile != "" {
		return nil, fmt.Errorf("project %q already exists", name)
	}

	if err := os.MkdirAll(projectDir, 0755); err != nil {
		return nil, fmt.Errorf("create project directory: %w", err)
	}

	yamlPath := filepath.Join(projectDir, "docker-compose.yml")
	if err := os.WriteFile(yamlPath, []byte(yamlContent), 0644); err != nil {
		return nil, fmt.Errorf("write docker-compose.yml: %w", err)
	}

	return &ComposeProject{
		Name:        name,
		ComposeFile: "docker-compose.yml",
		HasEnv:      false,
		Path:        projectDir,
	}, nil
}

// UpdateProject updates the compose file content of an existing project.
func (m *ComposeManager) UpdateProject(_ context.Context, name, yamlContent string) error {
	dir := filepath.Join(m.baseDir, name)
	composeFile := findComposeFile(dir)
	if composeFile == "" {
		return fmt.Errorf("no compose file found in %q", name)
	}
	return os.WriteFile(filepath.Join(dir, composeFile), []byte(yamlContent), 0644)
}

// DeleteProject tears down a compose project and removes its directory.
func (m *ComposeManager) DeleteProject(ctx context.Context, name string) error {
	// Attempt docker compose down; ignore errors
	_, _ = m.runCompose(ctx, name, "down")

	projectDir := filepath.Join(m.baseDir, name)
	return os.RemoveAll(projectDir)
}

// Up starts a compose project in detached mode.
func (m *ComposeManager) Up(ctx context.Context, name string) (string, error) {
	return m.runCompose(ctx, name, "up", "-d")
}

// Down stops a compose project.
func (m *ComposeManager) Down(ctx context.Context, name string) (string, error) {
	return m.runCompose(ctx, name, "down")
}

// runCompose executes a docker compose command for the given project.
func (m *ComposeManager) runCompose(ctx context.Context, name string, args ...string) (string, error) {
	dir := filepath.Join(m.baseDir, name)
	composeFile := findComposeFile(dir)
	if composeFile == "" {
		return "", fmt.Errorf("no compose file found in %q", name)
	}

	yamlPath := filepath.Join(dir, composeFile)
	cmdArgs := append([]string{"compose", "-f", yamlPath}, args...)
	cmd := exec.CommandContext(ctx, "docker", cmdArgs...)
	cmd.Dir = dir // Set working directory so .env is picked up
	output, err := cmd.CombinedOutput()
	return string(output), err
}

// GetProjectServices returns the runtime state of each service in a compose project.
func (m *ComposeManager) GetProjectServices(ctx context.Context, name string) ([]ComposeService, error) {
	if m.dockerClient == nil {
		return nil, fmt.Errorf("docker client not available")
	}

	// Docker compose normalizes project names to lowercase
	containers, err := m.dockerClient.ListContainersByComposeProject(ctx, strings.ToLower(name))
	if err != nil {
		return nil, fmt.Errorf("list containers for project %q: %w", name, err)
	}

	var services []ComposeService
	for _, c := range containers {
		svcName := c.Labels["com.docker.compose.service"]
		if svcName == "" {
			continue
		}

		ports := ""
		for _, p := range c.Ports {
			if p.PublicPort > 0 {
				if ports != "" {
					ports += ", "
				}
				ports += fmt.Sprintf("%s:%d->%d/%s", p.IP, p.PublicPort, p.PrivatePort, p.Type)
			}
		}

		services = append(services, ComposeService{
			Name:        svcName,
			ContainerID: c.ID,
			Image:       c.Image,
			State:       c.State,
			Status:      c.Status,
			Ports:       ports,
		})
	}
	return services, nil
}

// ListProjectsWithStatus returns all compose projects with real-time service status.
// Optimized: fetches all containers in a single Docker API call instead of per-project.
func (m *ComposeManager) ListProjectsWithStatus(ctx context.Context) ([]ComposeProjectWithStatus, error) {
	projects, err := m.ListProjects(ctx)
	if err != nil {
		return nil, err
	}

	// Build per-project container stats from a single API call
	type projectStats struct {
		serviceCount  int
		runningCount  int
	}
	statsMap := make(map[string]*projectStats)

	if m.dockerClient != nil {
		containers, cErr := m.dockerClient.ListContainers(ctx)
		if cErr == nil {
			for _, c := range containers {
				proj := c.Labels["com.docker.compose.project"]
				if proj == "" {
					continue
				}
				ps, ok := statsMap[proj]
				if !ok {
					ps = &projectStats{}
					statsMap[proj] = ps
				}
				ps.serviceCount++
				if c.State == "running" {
					ps.runningCount++
				}
			}
		}
	}

	result := make([]ComposeProjectWithStatus, len(projects))
	for i, p := range projects {
		pwStatus := ComposeProjectWithStatus{
			ComposeProject: p,
		}
		if ps, ok := statsMap[strings.ToLower(p.Name)]; ok {
			pwStatus.ServiceCount = ps.serviceCount
			pwStatus.RunningCount = ps.runningCount
		}

		if pwStatus.ServiceCount == 0 {
			pwStatus.RealStatus = "stopped"
		} else if pwStatus.RunningCount == pwStatus.ServiceCount {
			pwStatus.RealStatus = "running"
		} else if pwStatus.RunningCount > 0 {
			pwStatus.RealStatus = "partial"
		} else {
			pwStatus.RealStatus = "stopped"
		}

		result[i] = pwStatus
	}
	return result, nil
}

// RestartService restarts a single service within a compose project.
func (m *ComposeManager) RestartService(ctx context.Context, project, service string) (string, error) {
	return m.runCompose(ctx, project, "restart", service)
}

// StopService stops a single service within a compose project.
func (m *ComposeManager) StopService(ctx context.Context, project, service string) (string, error) {
	return m.runCompose(ctx, project, "stop", service)
}

// StartService starts a single service within a compose project.
func (m *ComposeManager) StartService(ctx context.Context, project, service string) (string, error) {
	return m.runCompose(ctx, project, "start", service)
}

// ServiceLogs returns the last N lines of logs for a service.
func (m *ComposeManager) ServiceLogs(ctx context.Context, project, service string, tail int) (string, error) {
	if tail <= 0 {
		tail = 100
	}
	return m.runCompose(ctx, project, "logs", "--tail", fmt.Sprintf("%d", tail), "--no-color", service)
}

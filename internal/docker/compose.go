package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var validProjectName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)

// validServiceName matches valid Docker Compose service names.
var validServiceName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)

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

func (m *ComposeManager) validateProjectName(name string) error {
	if name == "" || !validProjectName.MatchString(name) {
		return fmt.Errorf("invalid project name %q", name)
	}
	resolved := filepath.Clean(filepath.Join(m.baseDir, name))
	if !strings.HasPrefix(resolved, filepath.Clean(m.baseDir)+string(filepath.Separator)) {
		return fmt.Errorf("invalid project name %q: path traversal", name)
	}
	return nil
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
	if err := m.validateProjectName(name); err != nil {
		return nil, err
	}
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
	if err := m.validateProjectName(name); err != nil {
		return "", "", err
	}
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
	if err := m.validateProjectName(name); err != nil {
		return "", err
	}
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
	if err := m.validateProjectName(name); err != nil {
		return err
	}
	dir := filepath.Join(m.baseDir, name)
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("project %q not found", name)
	}
	return os.WriteFile(filepath.Join(dir, ".env"), []byte(content), 0644)
}

// CreateProject creates a new compose project directory with a docker-compose.yml.
func (m *ComposeManager) CreateProject(_ context.Context, name, yamlContent string) (*ComposeProject, error) {
	if err := m.validateProjectName(name); err != nil {
		return nil, err
	}
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
	if err := m.validateProjectName(name); err != nil {
		return err
	}
	dir := filepath.Join(m.baseDir, name)
	composeFile := findComposeFile(dir)
	if composeFile == "" {
		return fmt.Errorf("no compose file found in %q", name)
	}
	return os.WriteFile(filepath.Join(dir, composeFile), []byte(yamlContent), 0644)
}

// DeleteProject tears down a compose project and removes its directory.
// If removeImages is true, also removes the images used by the project.
// If removeVolumes is true, also removes named volumes.
func (m *ComposeManager) DeleteProject(ctx context.Context, name string, removeImages, removeVolumes bool) error {
	if err := m.validateProjectName(name); err != nil {
		return err
	}

	args := []string{"down"}
	if removeImages {
		args = append(args, "--rmi", "all")
	}
	if removeVolumes {
		args = append(args, "-v")
	}
	// Attempt docker compose down; ignore errors
	_, _ = m.runCompose(ctx, name, args...)

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

// ValidateConfig validates the docker-compose.yml of a project.
func (m *ComposeManager) ValidateConfig(ctx context.Context, name string) (string, error) {
	return m.runCompose(ctx, name, "config", "--quiet")
}

// runCompose executes a docker compose command for the given project.
func (m *ComposeManager) runCompose(ctx context.Context, name string, args ...string) (string, error) {
	if err := m.validateProjectName(name); err != nil {
		return "", err
	}
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
	if err := m.validateProjectName(name); err != nil {
		return nil, err
	}
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

// validateServiceName checks that a compose service name is safe.
func validateServiceName(service string) error {
	if service == "" {
		return fmt.Errorf("service name is required")
	}
	if !validServiceName.MatchString(service) {
		return fmt.Errorf("invalid service name %q (allowed: alphanumeric, underscore, dot, hyphen)", service)
	}
	return nil
}

// RestartService restarts a single service within a compose project.
func (m *ComposeManager) RestartService(ctx context.Context, project, service string) (string, error) {
	if err := validateServiceName(service); err != nil {
		return "", err
	}
	return m.runCompose(ctx, project, "restart", service)
}

// StopService stops a single service within a compose project.
func (m *ComposeManager) StopService(ctx context.Context, project, service string) (string, error) {
	if err := validateServiceName(service); err != nil {
		return "", err
	}
	return m.runCompose(ctx, project, "stop", service)
}

// StartService starts a single service within a compose project.
func (m *ComposeManager) StartService(ctx context.Context, project, service string) (string, error) {
	if err := validateServiceName(service); err != nil {
		return "", err
	}
	return m.runCompose(ctx, project, "start", service)
}

// ServiceLogs returns the last N lines of logs for a service.
func (m *ComposeManager) ServiceLogs(ctx context.Context, project, service string, tail int) (string, error) {
	if err := validateServiceName(service); err != nil {
		return "", err
	}
	if tail <= 0 {
		tail = 100
	}
	return m.runCompose(ctx, project, "logs", "--tail", fmt.Sprintf("%d", tail), "--no-color", service)
}

// StackUpdateCheck holds the update status for an entire stack.
type StackUpdateCheck struct {
	Project    string              `json:"project"`
	Images     []ImageUpdateStatus `json:"images"`
	HasUpdates bool                `json:"has_updates"`
}

// rollbackEntry stores image info for rollback purposes.
type rollbackEntry struct {
	Service string `json:"service"`
	Image   string `json:"image"`
	ImageID string `json:"image_id"`
}

// CheckStackUpdates checks each unique image in a compose project for available updates.
func (m *ComposeManager) CheckStackUpdates(ctx context.Context, name string) (*StackUpdateCheck, error) {
	if m.dockerClient == nil {
		return nil, fmt.Errorf("docker client not available")
	}

	services, err := m.GetProjectServices(ctx, name)
	if err != nil {
		return nil, err
	}

	result := &StackUpdateCheck{Project: name}
	checked := make(map[string]bool)

	for _, svc := range services {
		img := svc.Image
		if img == "" || checked[img] {
			continue
		}
		checked[img] = true

		status, err := m.dockerClient.CheckImageUpdate(ctx, img)
		if err != nil {
			result.Images = append(result.Images, ImageUpdateStatus{
				Image: img,
				Error: err.Error(),
			})
			continue
		}
		result.Images = append(result.Images, *status)
		if status.HasUpdate {
			result.HasUpdates = true
		}
	}

	return result, nil
}

// UpdateStack pulls latest images and recreates containers.
// Saves current image IDs for rollback before pulling.
func (m *ComposeManager) UpdateStack(ctx context.Context, name string) (string, error) {
	if m.dockerClient == nil {
		return "", fmt.Errorf("docker client not available")
	}

	// Save current image IDs for rollback
	services, err := m.GetProjectServices(ctx, name)
	if err != nil {
		return "", fmt.Errorf("get services: %w", err)
	}

	var rollback []rollbackEntry
	for _, svc := range services {
		if svc.Image == "" {
			continue
		}
		inspect, inspErr := m.dockerClient.InspectImage(ctx, svc.Image)
		if inspErr == nil {
			rollback = append(rollback, rollbackEntry{
				Service: svc.Name,
				Image:   svc.Image,
				ImageID: inspect.ID,
			})
		}
	}

	// Write rollback file
	if len(rollback) > 0 {
		rbData, _ := json.Marshal(rollback)
		rbPath := filepath.Join(m.baseDir, name, ".sfpanel-rollback.json")
		os.WriteFile(rbPath, rbData, 0644)
	}

	// Pull latest images
	pullOutput, pullErr := m.runCompose(ctx, name, "pull")
	if pullErr != nil {
		return pullOutput, fmt.Errorf("pull failed: %w", pullErr)
	}

	// Recreate containers with new images
	upOutput, upErr := m.runCompose(ctx, name, "up", "-d", "--force-recreate")
	output := pullOutput + "\n" + upOutput
	if upErr != nil {
		return output, fmt.Errorf("recreate failed: %w", upErr)
	}

	return output, nil
}

// RollbackStack restores previous image versions and recreates containers.
func (m *ComposeManager) RollbackStack(ctx context.Context, name string) (string, error) {
	if err := m.validateProjectName(name); err != nil {
		return "", err
	}

	rbPath := filepath.Join(m.baseDir, name, ".sfpanel-rollback.json")
	rbData, err := os.ReadFile(rbPath)
	if err != nil {
		return "", fmt.Errorf("no rollback data available (update first)")
	}

	var entries []rollbackEntry
	if err := json.Unmarshal(rbData, &entries); err != nil {
		return "", fmt.Errorf("invalid rollback data: %w", err)
	}

	// Re-tag old images
	for _, e := range entries {
		cmd := exec.CommandContext(ctx, "docker", "tag", e.ImageID, e.Image)
		if out, tagErr := cmd.CombinedOutput(); tagErr != nil {
			return string(out), fmt.Errorf("tag %s → %s failed: %w", e.ImageID, e.Image, tagErr)
		}
	}

	// Recreate containers with restored images
	output, upErr := m.runCompose(ctx, name, "up", "-d", "--force-recreate")
	if upErr != nil {
		return output, fmt.Errorf("recreate failed: %w", upErr)
	}

	// Remove rollback file after successful rollback
	os.Remove(rbPath)

	return output, nil
}

// HasRollback checks if rollback data exists for a project.
func (m *ComposeManager) HasRollback(name string) bool {
	rbPath := filepath.Join(m.baseDir, name, ".sfpanel-rollback.json")
	_, err := os.Stat(rbPath)
	return err == nil
}

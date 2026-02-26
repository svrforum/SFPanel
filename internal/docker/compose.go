package docker

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// ComposeProject represents a Docker Compose project stored on disk and tracked in the database.
type ComposeProject struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	YAMLPath  string `json:"yaml_path"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
}

// ComposeManager manages Docker Compose projects by storing YAML files on disk
// and executing docker compose commands via os/exec.
type ComposeManager struct {
	db       *sql.DB
	storeDir string // e.g., /var/lib/sfpanel/compose
}

// NewComposeManager creates a new ComposeManager, ensuring the storage directory exists.
func NewComposeManager(db *sql.DB, storeDir string) *ComposeManager {
	os.MkdirAll(storeDir, 0755)
	return &ComposeManager{db: db, storeDir: storeDir}
}

// ListProjects returns all compose projects from the database.
func (m *ComposeManager) ListProjects(ctx context.Context) ([]ComposeProject, error) {
	rows, err := m.db.QueryContext(ctx, "SELECT id, name, yaml_path, status, created_at FROM compose_projects ORDER BY id")
	if err != nil {
		return nil, fmt.Errorf("query compose projects: %w", err)
	}
	defer rows.Close()

	var projects []ComposeProject
	for rows.Next() {
		var p ComposeProject
		if err := rows.Scan(&p.ID, &p.Name, &p.YAMLPath, &p.Status, &p.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan compose project: %w", err)
		}
		projects = append(projects, p)
	}
	return projects, rows.Err()
}

// GetProject returns a single compose project by name.
func (m *ComposeManager) GetProject(ctx context.Context, name string) (*ComposeProject, error) {
	var p ComposeProject
	err := m.db.QueryRowContext(ctx,
		"SELECT id, name, yaml_path, status, created_at FROM compose_projects WHERE name = ?", name,
	).Scan(&p.ID, &p.Name, &p.YAMLPath, &p.Status, &p.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("get compose project %q: %w", name, err)
	}
	return &p, nil
}

// CreateProject creates a new compose project: writes the YAML file to disk and inserts a record into the database.
func (m *ComposeManager) CreateProject(ctx context.Context, name, yamlContent string) (*ComposeProject, error) {
	projectDir := filepath.Join(m.storeDir, name)
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		return nil, fmt.Errorf("create project directory: %w", err)
	}

	yamlPath := filepath.Join(projectDir, "docker-compose.yml")
	if err := os.WriteFile(yamlPath, []byte(yamlContent), 0644); err != nil {
		return nil, fmt.Errorf("write docker-compose.yml: %w", err)
	}

	result, err := m.db.ExecContext(ctx,
		"INSERT INTO compose_projects (name, yaml_path, status) VALUES (?, ?, 'stopped')",
		name, yamlPath,
	)
	if err != nil {
		// Clean up the directory on DB error
		os.RemoveAll(projectDir)
		return nil, fmt.Errorf("insert compose project: %w", err)
	}

	id, _ := result.LastInsertId()
	return m.getProjectByID(ctx, int(id))
}

// UpdateProject updates the YAML content of an existing project on disk.
func (m *ComposeManager) UpdateProject(ctx context.Context, name, yamlContent string) error {
	yamlPath := filepath.Join(m.storeDir, name, "docker-compose.yml")
	if err := os.WriteFile(yamlPath, []byte(yamlContent), 0644); err != nil {
		return fmt.Errorf("update docker-compose.yml: %w", err)
	}
	return nil
}

// DeleteProject tears down a compose project (ignoring errors), removes its directory, and deletes the DB record.
func (m *ComposeManager) DeleteProject(ctx context.Context, name string) error {
	// Attempt docker compose down; ignore errors (project may not be running)
	_, _ = m.runCompose(ctx, name, "down")

	projectDir := filepath.Join(m.storeDir, name)
	if err := os.RemoveAll(projectDir); err != nil {
		return fmt.Errorf("remove project directory: %w", err)
	}

	if _, err := m.db.ExecContext(ctx, "DELETE FROM compose_projects WHERE name = ?", name); err != nil {
		return fmt.Errorf("delete compose project from db: %w", err)
	}

	return nil
}

// Up starts a compose project in detached mode and updates its status to "running".
func (m *ComposeManager) Up(ctx context.Context, name string) (string, error) {
	output, err := m.runCompose(ctx, name, "up", "-d")
	if err != nil {
		return output, err
	}

	_, dbErr := m.db.ExecContext(ctx, "UPDATE compose_projects SET status = 'running' WHERE name = ?", name)
	if dbErr != nil {
		return output, fmt.Errorf("update status: %w", dbErr)
	}

	return output, nil
}

// Down stops a compose project and updates its status to "stopped".
func (m *ComposeManager) Down(ctx context.Context, name string) (string, error) {
	output, err := m.runCompose(ctx, name, "down")
	if err != nil {
		return output, err
	}

	_, dbErr := m.db.ExecContext(ctx, "UPDATE compose_projects SET status = 'stopped' WHERE name = ?", name)
	if dbErr != nil {
		return output, fmt.Errorf("update status: %w", dbErr)
	}

	return output, nil
}

// runCompose executes a docker compose command for the given project.
func (m *ComposeManager) runCompose(ctx context.Context, name string, args ...string) (string, error) {
	yamlPath := filepath.Join(m.storeDir, name, "docker-compose.yml")
	cmdArgs := append([]string{"compose", "-f", yamlPath}, args...)
	cmd := exec.CommandContext(ctx, "docker", cmdArgs...)
	output, err := cmd.CombinedOutput()
	return string(output), err
}

// getProjectByID is an internal helper to fetch a project by its database ID.
func (m *ComposeManager) getProjectByID(ctx context.Context, id int) (*ComposeProject, error) {
	var p ComposeProject
	err := m.db.QueryRowContext(ctx,
		"SELECT id, name, yaml_path, status, created_at FROM compose_projects WHERE id = ?", id,
	).Scan(&p.ID, &p.Name, &p.YAMLPath, &p.Status, &p.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("get compose project by id %d: %w", id, err)
	}
	return &p, nil
}

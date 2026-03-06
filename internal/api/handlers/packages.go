package handlers

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
)

// PackageInfo represents a system package with version and architecture details.
type PackageInfo struct {
	Name           string `json:"name"`
	CurrentVersion string `json:"current_version,omitempty"`
	NewVersion     string `json:"new_version,omitempty"`
	Architecture   string `json:"arch,omitempty"`
	Description    string `json:"description,omitempty"`
}

// PackagesHandler exposes REST handlers for system package management via apt.
type PackagesHandler struct{}

// validPackageName matches only safe package name characters.
var validPackageName = regexp.MustCompile(`^[a-zA-Z0-9._+\-]+$`)

// ---------- Helpers ----------

// validatePackageName checks that a package name contains only allowed characters.
func validatePackageName(name string) bool {
	return validPackageName.MatchString(name)
}

// ---------- CheckUpdates ----------

// CheckUpdates runs `apt list --upgradable` and returns a structured list of
// packages that have updates available.
// GET /packages/updates
func (h *PackagesHandler) CheckUpdates(c echo.Context) error {
	output, err := runCommandEnv(aptEnv(), "apt", "list", "--upgradable")
	if err != nil {
		// apt list --upgradable may return exit code 0 even with warnings;
		// only fail if output is completely empty.
		if strings.TrimSpace(output) == "" {
			return response.Fail(c, http.StatusInternalServerError, response.ErrAPTError, "Failed to check updates: "+err.Error())
		}
	}

	packages := parseUpgradablePackages(output)

	return response.OK(c, map[string]interface{}{
		"updates":      packages,
		"total":        len(packages),
		"last_checked": time.Now().UTC().Format(time.RFC3339),
	})
}

// parseUpgradablePackages parses the output of `apt list --upgradable`.
// Each upgradable line looks like:
//
//	package-name/suite current_ver [upgradable from: old_ver]
//
// or more precisely:
//
//	package/focal-updates 1.2.3-4ubuntu1 amd64 [upgradable from: 1.2.3-3ubuntu1]
func parseUpgradablePackages(output string) []PackageInfo {
	var packages []PackageInfo
	lines := strings.Split(output, "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		// Skip the header line "Listing..." and empty lines
		if line == "" || strings.HasPrefix(line, "Listing") {
			continue
		}

		pkg := parseUpgradableLine(line)
		if pkg != nil {
			packages = append(packages, *pkg)
		}
	}

	return packages
}

// parseUpgradableLine parses a single line from `apt list --upgradable` output.
// Format: "name/suite version arch [upgradable from: old_version]"
func parseUpgradableLine(line string) *PackageInfo {
	// Split on the first slash to get the package name
	slashIdx := strings.Index(line, "/")
	if slashIdx < 0 {
		return nil
	}
	name := line[:slashIdx]

	// Everything after the slash
	rest := line[slashIdx+1:]

	// Split the remainder by spaces
	fields := strings.Fields(rest)
	if len(fields) < 3 {
		return nil
	}

	// fields[0] = suite (e.g., "focal-updates")
	// fields[1] = new version
	// fields[2] = architecture
	newVersion := fields[1]
	arch := fields[2]

	// Extract old version from "[upgradable from: X.Y.Z]"
	var currentVersion string
	fromIdx := strings.Index(rest, "upgradable from: ")
	if fromIdx >= 0 {
		after := rest[fromIdx+len("upgradable from: "):]
		closeBracket := strings.Index(after, "]")
		if closeBracket >= 0 {
			currentVersion = after[:closeBracket]
		}
	}

	return &PackageInfo{
		Name:           name,
		CurrentVersion: currentVersion,
		NewVersion:     newVersion,
		Architecture:   arch,
	}
}

// ---------- UpgradePackages ----------

// UpgradePackages runs apt-get update followed by apt-get upgrade for all or
// specific packages. This is a potentially long-running operation.
// POST /packages/upgrade
// JSON body: { "packages": ["pkg1", "pkg2"] } (optional; empty upgrades all)
func (h *PackagesHandler) UpgradePackages(c echo.Context) error {
	var req struct {
		Packages []string `json:"packages"`
	}
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}

	// Validate all package names if specific packages were requested.
	for _, pkg := range req.Packages {
		if !validatePackageName(pkg) {
			return response.Fail(c, http.StatusBadRequest, response.ErrInvalidPackageName,
				fmt.Sprintf("Invalid package name: %s", pkg))
		}
	}

	env := aptEnv()

	// Step 1: apt-get update
	updateOutput, err := runCommandEnv(env, "apt-get", "update")
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrAPTUpdateError,
			"Failed to update package lists: "+err.Error())
	}

	// Step 2: apt-get upgrade
	var upgradeArgs []string
	if len(req.Packages) > 0 {
		// Upgrade specific packages via install (upgrade only works on all)
		upgradeArgs = append([]string{"install", "--only-upgrade", "-y"}, req.Packages...)
	} else {
		upgradeArgs = []string{"upgrade", "-y"}
	}

	upgradeOutput, err := runCommandEnv(env, "apt-get", upgradeArgs...)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrAPTUpgradeError,
			"Failed to upgrade packages: "+err.Error())
	}

	return response.OK(c, map[string]interface{}{
		"message":        "Packages upgraded successfully",
		"update_output":  updateOutput,
		"upgrade_output": upgradeOutput,
	})
}

// ---------- InstallPackage ----------

// InstallPackage installs a single package via apt-get install.
// POST /packages/install
// JSON body: { "name": "nginx" }
func (h *PackagesHandler) InstallPackage(c echo.Context) error {
	var req struct {
		Name string `json:"name"`
	}
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}
	if req.Name == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrMissingFields, "Package name is required")
	}
	if !validatePackageName(req.Name) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidPackageName,
			"Package name contains invalid characters (allowed: a-zA-Z0-9._+-)")
	}

	output, err := runCommandEnv(aptEnv(), "apt-get", "install", "-y", req.Name)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrAPTInstallError,
			"Failed to install package: "+err.Error())
	}

	return response.OK(c, map[string]interface{}{
		"message": fmt.Sprintf("Package %s installed successfully", req.Name),
		"output":  output,
	})
}

// ---------- RemovePackage ----------

// RemovePackage removes a single package via apt-get remove.
// POST /packages/remove
// JSON body: { "name": "nginx" }
func (h *PackagesHandler) RemovePackage(c echo.Context) error {
	var req struct {
		Name string `json:"name"`
	}
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}
	if req.Name == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrMissingFields, "Package name is required")
	}
	if !validatePackageName(req.Name) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidPackageName,
			"Package name contains invalid characters (allowed: a-zA-Z0-9._+-)")
	}

	output, err := runCommandEnv(aptEnv(), "apt-get", "remove", "-y", req.Name)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrAPTRemoveError,
			"Failed to remove package: "+err.Error())
	}

	return response.OK(c, map[string]interface{}{
		"message": fmt.Sprintf("Package %s removed successfully", req.Name),
		"output":  output,
	})
}

// ---------- SearchPackages ----------

// SearchPackages searches the apt cache for packages matching a query string.
// GET /packages/search?q=nginx
func (h *PackagesHandler) SearchPackages(c echo.Context) error {
	query := strings.TrimSpace(c.QueryParam("q"))
	if query == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrMissingQuery, "Query parameter 'q' is required")
	}
	if !validatePackageName(query) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidQuery,
			"Search query contains invalid characters (allowed: a-zA-Z0-9._+-)")
	}

	output, err := runCommand("apt-cache", "search", query)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrAPTSearchError,
			"Failed to search packages: "+err.Error())
	}

	packages := parseSearchResults(output, 50)

	return response.OK(c, map[string]interface{}{
		"packages": packages,
		"total":    len(packages),
		"query":    query,
	})
}

// parseSearchResults parses the output of `apt-cache search` and limits results.
// Each line has the format: "package-name - Short description"
func parseSearchResults(output string, limit int) []PackageInfo {
	var packages []PackageInfo
	lines := strings.Split(output, "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Split on " - " to separate name from description
		parts := strings.SplitN(line, " - ", 2)
		if len(parts) < 2 {
			continue
		}

		packages = append(packages, PackageInfo{
			Name:        strings.TrimSpace(parts[0]),
			Description: strings.TrimSpace(parts[1]),
		})

		if len(packages) >= limit {
			break
		}
	}

	return packages
}

// ---------- GetDockerStatus ----------

// GetDockerStatus checks whether Docker and Docker Compose are installed and running.
// GET /packages/docker-status
func (h *PackagesHandler) GetDockerStatus(c echo.Context) error {
	status := map[string]interface{}{
		"installed":         false,
		"version":           "",
		"running":           false,
		"compose_available": false,
	}

	// Check if docker is installed
	dockerPath, err := exec.LookPath("docker")
	if err != nil || dockerPath == "" {
		return response.OK(c, status)
	}
	status["installed"] = true

	// Get docker version
	versionOutput, err := runCommand("docker", "--version")
	if err == nil {
		status["version"] = strings.TrimSpace(versionOutput)
	}

	// Check if docker service is running
	activeOutput, err := runCommand("systemctl", "is-active", "docker")
	if err == nil && strings.TrimSpace(activeOutput) == "active" {
		status["running"] = true
	}

	// Check if docker compose is available (plugin form first, then standalone)
	_, err = runCommand("docker", "compose", "version")
	if err == nil {
		status["compose_available"] = true
	} else {
		// Fallback: check for standalone docker-compose binary
		_, lookErr := exec.LookPath("docker-compose")
		if lookErr == nil {
			status["compose_available"] = true
		}
	}

	return response.OK(c, status)
}

// ---------- InstallDocker ----------

// InstallDocker installs Docker Engine using the official get.docker.com script.
// Uses Server-Sent Events (SSE) to stream installation output in real-time.
// POST /packages/install-docker
func (h *PackagesHandler) InstallDocker(c echo.Context) error {
	// Set SSE headers for streaming
	c.Response().Header().Set("Content-Type", "text/event-stream")
	c.Response().Header().Set("Cache-Control", "no-cache")
	c.Response().Header().Set("Connection", "keep-alive")
	c.Response().WriteHeader(http.StatusOK)

	flusher, ok := c.Response().Writer.(http.Flusher)
	if !ok {
		return response.Fail(c, http.StatusInternalServerError, response.ErrSSEError, "Streaming not supported")
	}

	sendLine := func(line string) {
		fmt.Fprintf(c.Response(), "data: %s\n\n", line)
		flusher.Flush()
	}

	sendLine(">>> Downloading Docker install script from https://get.docker.com ...")

	// Step 1: Download get-docker.sh
	dlCmd := exec.CommandContext(context.Background(), "curl", "-fsSL", "https://get.docker.com", "-o", "/tmp/get-docker.sh")
	dlOut, err := dlCmd.CombinedOutput()
	if len(dlOut) > 0 {
		for _, line := range strings.Split(string(dlOut), "\n") {
			if line != "" {
				sendLine(line)
			}
		}
	}
	if err != nil {
		sendLine("ERROR: Failed to download Docker install script: " + err.Error())
		sendLine("[DONE]")
		return nil
	}

	sendLine(">>> Running install script (this may take a few minutes) ...")

	// Step 2: Run the install script with real-time output streaming
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "/tmp/get-docker.sh")
	cmd.Env = append(cmd.Environ(), "DEBIAN_FRONTEND=noninteractive")

	// Create a pipe for real-time output
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		sendLine("ERROR: " + err.Error())
		sendLine("[DONE]")
		return nil
	}
	cmd.Stderr = cmd.Stdout // Merge stderr into stdout

	if err := cmd.Start(); err != nil {
		sendLine("ERROR: Failed to start install script: " + err.Error())
		sendLine("[DONE]")
		return nil
	}

	// Stream output line by line
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		sendLine(scanner.Text())
	}

	if err := cmd.Wait(); err != nil {
		sendLine("ERROR: Install script failed: " + err.Error())
		sendLine("[DONE]")
		return nil
	}

	sendLine(">>> Docker installation completed successfully!")
	sendLine("[DONE]")
	return nil
}

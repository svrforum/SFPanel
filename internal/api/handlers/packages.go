package handlers

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"os"
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
	Installed      bool   `json:"installed"`
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
	// Refresh package lists first so results are up-to-date
	env := aptEnv()
	if _, err := runCommandEnv(env, "apt-get", "update"); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrAPTUpdateError,
			"Failed to update package lists: "+err.Error())
	}

	output, err := runCommandEnv(env, "apt", "list", "--upgradable")
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

	env := append(aptEnv(), "LANG=C")

	output, err := runCommandEnv(env, "apt-cache", "search", query)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrAPTSearchError,
			"Failed to search packages: "+err.Error())
	}

	packages := parseSearchResults(output, 50)

	// Enrich with installed status via dpkg-query
	if len(packages) > 0 {
		installed := getInstalledPackages(packages)
		for i := range packages {
			packages[i].Installed = installed[packages[i].Name]
		}
	}

	return response.OK(c, map[string]interface{}{
		"packages": packages,
		"total":    len(packages),
		"query":    query,
	})
}

// getInstalledPackages checks which packages from the list are currently installed.
func getInstalledPackages(packages []PackageInfo) map[string]bool {
	installed := make(map[string]bool, len(packages))

	names := make([]string, len(packages))
	for i, pkg := range packages {
		names[i] = pkg.Name
	}

	// dpkg-query -W -f='${Package}\t${db:Status-Abbrev}\n' pkg1 pkg2 ...
	args := append([]string{"-W", "-f=${Package}\t${db:Status-Abbrev}\n"}, names...)
	output, _ := runCommandWithTimeout(10*time.Second, "dpkg-query", args...)

	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) == 2 && strings.HasPrefix(parts[1], "ii") {
			installed[parts[0]] = true
		}
	}

	return installed
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

	// Step 1: Download get-docker.sh (30s timeout)
	dlCtx, dlCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer dlCancel()
	dlCmd := exec.CommandContext(dlCtx, "curl", "-fsSL", "https://get.docker.com", "-o", "/tmp/get-docker.sh")
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

	// Clean up temp file
	os.Remove("/tmp/get-docker.sh")

	sendLine(">>> Docker installation completed successfully!")
	sendLine("[DONE]")
	return nil
}

// ---------- GetNodeStatus ----------

// GetNodeStatus checks whether Node.js and NVM are installed.
// GET /packages/node-status
func (h *PackagesHandler) GetNodeStatus(c echo.Context) error {
	status := map[string]interface{}{
		"installed":     false,
		"version":       "",
		"nvm_installed": false,
		"npm_version":   "",
	}

	// Check NVM (search root and user home directories)
	nvmDir := findNVMDir()
	if nvmDir != "" {
		status["nvm_installed"] = true
	}

	// Check if node is in PATH
	nodePath, err := exec.LookPath("node")
	if err != nil || nodePath == "" {
		// NVM-installed node might not be in PATH, check common location
		nvmNode := nvmDir + "/versions/node"
		if entries, dirErr := os.ReadDir(nvmNode); dirErr == nil && len(entries) > 0 {
			// Find latest version
			latest := entries[len(entries)-1].Name()
			testPath := nvmNode + "/" + latest + "/bin/node"
			if _, statErr := os.Stat(testPath); statErr == nil {
				out, runErr := exec.Command(testPath, "--version").Output()
				if runErr == nil {
					status["installed"] = true
					status["version"] = strings.TrimSpace(string(out))
				}
				npmPath := nvmNode + "/" + latest + "/bin/npm"
				npmOut, npmErr := exec.Command(npmPath, "--version").Output()
				if npmErr == nil {
					status["npm_version"] = strings.TrimSpace(string(npmOut))
				}
			}
		}
		return response.OK(c, status)
	}

	status["installed"] = true
	versionOutput, err := exec.Command("node", "--version").Output()
	if err == nil {
		status["version"] = strings.TrimSpace(string(versionOutput))
	}
	npmOut, err := exec.Command("npm", "--version").Output()
	if err == nil {
		status["npm_version"] = strings.TrimSpace(string(npmOut))
	}

	return response.OK(c, status)
}

// ---------- InstallNode ----------

// InstallNode installs Node.js via NVM (Node Version Manager).
// Uses Server-Sent Events (SSE) to stream installation output in real-time.
// POST /packages/install-node
func (h *PackagesHandler) InstallNode(c echo.Context) error {
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

	homeDir := "/root"
	nvmDir := homeDir + "/.nvm"

	// Step 1: Install NVM if not present
	if _, err := os.Stat(nvmDir + "/nvm.sh"); os.IsNotExist(err) {
		sendLine(">>> Installing NVM (Node Version Manager) ...")

		dlCtx, dlCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer dlCancel()
		dlCmd := exec.CommandContext(dlCtx, "curl", "-fsSL", "https://raw.githubusercontent.com/nvm-sh/nvm/v0.40.3/install.sh", "-o", "/tmp/install-nvm.sh")
		dlOut, err := dlCmd.CombinedOutput()
		if len(dlOut) > 0 {
			for _, line := range strings.Split(string(dlOut), "\n") {
				if line != "" {
					sendLine(line)
				}
			}
		}
		if err != nil {
			sendLine("ERROR: Failed to download NVM install script: " + err.Error())
			sendLine("[DONE]")
			return nil
		}

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()

		cmd := exec.CommandContext(ctx, "bash", "/tmp/install-nvm.sh")
		cmd.Env = append(os.Environ(), "HOME="+homeDir, "NVM_DIR="+nvmDir)
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			sendLine("ERROR: " + err.Error())
			sendLine("[DONE]")
			return nil
		}
		cmd.Stderr = cmd.Stdout

		if err := cmd.Start(); err != nil {
			sendLine("ERROR: Failed to run NVM install: " + err.Error())
			sendLine("[DONE]")
			return nil
		}

		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			sendLine(scanner.Text())
		}

		if err := cmd.Wait(); err != nil {
			sendLine("ERROR: NVM install failed: " + err.Error())
			sendLine("[DONE]")
			return nil
		}
		os.Remove("/tmp/install-nvm.sh")
		sendLine(">>> NVM installed successfully!")
	} else {
		sendLine(">>> NVM already installed, skipping...")
	}

	// Step 2: Install Node.js LTS via NVM
	sendLine(">>> Installing Node.js LTS via NVM ...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Source nvm and install LTS in a single bash invocation
	script := fmt.Sprintf(`export NVM_DIR="%s" && . "$NVM_DIR/nvm.sh" && nvm install --lts && nvm alias default lts/* && node --version && npm --version`, nvmDir)
	cmd := exec.CommandContext(ctx, "bash", "-c", script)
	cmd.Env = append(os.Environ(), "HOME="+homeDir, "NVM_DIR="+nvmDir)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		sendLine("ERROR: " + err.Error())
		sendLine("[DONE]")
		return nil
	}
	cmd.Stderr = cmd.Stdout

	if err := cmd.Start(); err != nil {
		sendLine("ERROR: Failed to start Node.js install: " + err.Error())
		sendLine("[DONE]")
		return nil
	}

	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		sendLine(scanner.Text())
	}

	if err := cmd.Wait(); err != nil {
		sendLine("ERROR: Node.js install failed: " + err.Error())
		sendLine("[DONE]")
		return nil
	}

	// Step 3: Symlink node/npm/npx to /usr/local/bin so they're in global PATH
	sendLine(">>> Creating symlinks in /usr/local/bin ...")
	linkScript := fmt.Sprintf(`export NVM_DIR="%s" && . "$NVM_DIR/nvm.sh" && NODE_PATH=$(which node) && NODE_DIR=$(dirname "$NODE_PATH") && ln -sf "$NODE_DIR/node" /usr/local/bin/node && ln -sf "$NODE_DIR/npm" /usr/local/bin/npm && ln -sf "$NODE_DIR/npx" /usr/local/bin/npx && echo "Linked: $(node --version), npm $(npm --version)"`, nvmDir)
	linkCtx, linkCancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer linkCancel()
	linkCmd := exec.CommandContext(linkCtx, "bash", "-c", linkScript)
	linkCmd.Env = append(os.Environ(), "HOME="+homeDir, "NVM_DIR="+nvmDir)
	linkOut, err := linkCmd.CombinedOutput()
	if err != nil {
		sendLine("WARNING: Symlink creation failed (Node.js is still available via NVM): " + err.Error())
	} else {
		for _, line := range strings.Split(strings.TrimSpace(string(linkOut)), "\n") {
			if line != "" {
				sendLine(line)
			}
		}
	}

	sendLine(">>> Node.js installation completed successfully!")
	sendLine("[DONE]")
	return nil
}

// ---------- GetNodeVersions ----------

// GetNodeVersions lists installed Node.js versions via NVM and identifies the active one.
// GET /packages/node-versions
func (h *PackagesHandler) GetNodeVersions(c echo.Context) error {
	nvmDir := findNVMDir()

	type NodeVersion struct {
		Version string `json:"version"`
		Active  bool   `json:"active"`
		LTS     bool   `json:"lts"`
	}

	result := map[string]interface{}{
		"versions":       []NodeVersion{},
		"current":        "",
		"remote_lts":     []string{},
	}

	if nvmDir == "" {
		return response.OK(c, result)
	}

	// Get current active version
	currentScript := fmt.Sprintf(`export NVM_DIR="%s" && . "$NVM_DIR/nvm.sh" && nvm current 2>/dev/null`, nvmDir)
	currentOut, _ := exec.Command("bash", "-c", currentScript).Output()
	current := strings.TrimSpace(string(currentOut))
	result["current"] = current

	// List installed versions by scanning the NVM versions directory directly
	// This is more reliable than parsing `nvm ls` output which has ANSI codes and varying formats
	var versions []NodeVersion
	versionsDir := nvmDir + "/versions/node"
	if entries, err := os.ReadDir(versionsDir); err == nil {
		for _, e := range entries {
			if e.IsDir() && strings.HasPrefix(e.Name(), "v") {
				v := e.Name()
				versions = append(versions, NodeVersion{
					Version: v,
					Active:  v == current,
					LTS:     false, // will be determined below
				})
			}
		}
	}

	// Check which versions are LTS using nvm ls (best effort)
	script := fmt.Sprintf(`export NVM_DIR="%s" && . "$NVM_DIR/nvm.sh" && nvm ls --no-colors 2>/dev/null`, nvmDir)
	if out, err := exec.Command("bash", "-c", script).Output(); err == nil {
		ltsVersions := make(map[string]bool)
		versionRe := regexp.MustCompile(`(v\d+\.\d+\.\d+)`)
		for _, line := range strings.Split(string(out), "\n") {
			if strings.Contains(line, "lts/") {
				if matches := versionRe.FindStringSubmatch(line); len(matches) > 1 {
					ltsVersions[matches[1]] = true
				}
			}
		}
		for i, v := range versions {
			if ltsVersions[v.Version] {
				versions[i].LTS = true
			}
		}
	}

	result["versions"] = versions

	// List available remote LTS versions (cached, quick)
	ltsScript := fmt.Sprintf(`export NVM_DIR="%s" && . "$NVM_DIR/nvm.sh" && nvm ls-remote --lts --no-colors 2>/dev/null | grep "Latest" | tail -5`, nvmDir)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	ltsOut, err := exec.CommandContext(ctx, "bash", "-c", ltsScript).Output()
	if err == nil {
		var remoteLTS []string
		for _, line := range strings.Split(string(ltsOut), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			parts := strings.Fields(line)
			if len(parts) > 0 {
				v := parts[0]
				if strings.HasPrefix(v, "v") {
					remoteLTS = append(remoteLTS, v)
				}
			}
		}
		result["remote_lts"] = remoteLTS
	}

	return response.OK(c, result)
}

// ---------- SwitchNodeVersion ----------

// SwitchNodeVersion switches the active Node.js version via NVM.
// POST /packages/node-switch
func (h *PackagesHandler) SwitchNodeVersion(c echo.Context) error {
	var body struct {
		Version string `json:"version"`
	}
	if err := c.Bind(&body); err != nil || body.Version == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidBody, "version is required")
	}

	if !regexp.MustCompile(`^v?\d+(\.\d+)*$`).MatchString(body.Version) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidBody, "invalid version format")
	}

	nvmDir := findNVMDir()
	if nvmDir == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrCommandFailed, "NVM is not installed")
	}

	script := fmt.Sprintf(`export NVM_DIR="%s" && . "$NVM_DIR/nvm.sh" && nvm alias default %s && nvm use %s 2>&1`, nvmDir, body.Version, body.Version)
	out, err := exec.Command("bash", "-c", script).CombinedOutput()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrCommandFailed, string(out))
	}

	// Update symlinks in /usr/local/bin
	linkScript := fmt.Sprintf(`export NVM_DIR="%s" && . "$NVM_DIR/nvm.sh" && nvm use %s && NODE_PATH=$(which node) && NODE_DIR=$(dirname "$NODE_PATH") && ln -sf "$NODE_DIR/node" /usr/local/bin/node && ln -sf "$NODE_DIR/npm" /usr/local/bin/npm && ln -sf "$NODE_DIR/npx" /usr/local/bin/npx`, nvmDir, body.Version)
	exec.Command("bash", "-c", linkScript).Run()

	return response.OK(c, map[string]string{"switched": body.Version, "output": strings.TrimSpace(string(out))})
}

// ---------- InstallNodeVersion ----------

// InstallNodeVersion installs a specific Node.js version via NVM.
// POST /packages/node-install-version
func (h *PackagesHandler) InstallNodeVersion(c echo.Context) error {
	var body struct {
		Version string `json:"version"`
	}
	if err := c.Bind(&body); err != nil || body.Version == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidBody, "version is required")
	}

	if !regexp.MustCompile(`^v?\d+(\.\d+)*$`).MatchString(body.Version) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidBody, "invalid version format")
	}

	nvmDir := findNVMDir()
	if nvmDir == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrCommandFailed, "NVM is not installed")
	}

	// SSE streaming
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

	sendLine(fmt.Sprintf(">>> Installing Node.js %s ...", body.Version))

	script := fmt.Sprintf(`export NVM_DIR="%s" && . "$NVM_DIR/nvm.sh" && nvm install %s`, nvmDir, body.Version)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", "-c", script)
	cmd.Env = append(os.Environ(), "NVM_DIR="+nvmDir)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		sendLine("ERROR: " + err.Error())
		sendLine("[DONE]")
		return nil
	}
	cmd.Stderr = cmd.Stdout

	if err := cmd.Start(); err != nil {
		sendLine("ERROR: " + err.Error())
		sendLine("[DONE]")
		return nil
	}

	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		sendLine(scanner.Text())
	}

	if err := cmd.Wait(); err != nil {
		sendLine("ERROR: " + err.Error())
		sendLine("[DONE]")
		return nil
	}

	sendLine(fmt.Sprintf(">>> Node.js %s installed successfully!", body.Version))
	sendLine("[DONE]")
	return nil
}

// ---------- UninstallNodeVersion ----------

// UninstallNodeVersion removes a specific Node.js version via NVM.
// POST /packages/node-uninstall-version
func (h *PackagesHandler) UninstallNodeVersion(c echo.Context) error {
	var body struct {
		Version string `json:"version"`
	}
	if err := c.Bind(&body); err != nil || body.Version == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidBody, "version is required")
	}

	if !regexp.MustCompile(`^v?\d+(\.\d+)*$`).MatchString(body.Version) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidBody, "invalid version format")
	}

	nvmDir := findNVMDir()
	if nvmDir == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrCommandFailed, "NVM is not installed")
	}

	script := fmt.Sprintf(`export NVM_DIR="%s" && . "$NVM_DIR/nvm.sh" && nvm uninstall %s 2>&1`, nvmDir, body.Version)
	out, err := exec.Command("bash", "-c", script).CombinedOutput()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrCommandFailed, string(out))
	}

	return response.OK(c, map[string]string{"removed": body.Version, "output": strings.TrimSpace(string(out))})
}

// ---------- GetClaudeStatus ----------

// findNVMDir searches for NVM installation across root and user home directories.
var safePathRe = regexp.MustCompile(`^[a-zA-Z0-9/_.-]+$`)

func findNVMDir() string {
	candidates := []string{"/root/.nvm"}
	if entries, err := os.ReadDir("/home"); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				p := "/home/" + e.Name() + "/.nvm"
				if safePathRe.MatchString(p) {
					candidates = append(candidates, p)
				}
			}
		}
	}
	for _, d := range candidates {
		if _, err := os.Stat(d + "/nvm.sh"); err == nil {
			return d
		}
	}
	return ""
}

// findBinaryPath searches for a binary in PATH and common user-local directories.
func findBinaryPath(name string) string {
	if p, err := exec.LookPath(name); err == nil && p != "" {
		return p
	}
	// Check common locations: /root/.local/bin, /home/*/.local/bin, /usr/local/bin
	candidates := []string{
		"/root/.local/bin/" + name,
		"/usr/local/bin/" + name,
	}
	// Also check all home directories
	if entries, err := os.ReadDir("/home"); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				candidates = append(candidates, "/home/"+e.Name()+"/.local/bin/"+name)
			}
		}
	}
	for _, p := range candidates {
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p
		}
	}
	return ""
}

// GetClaudeStatus checks whether Claude Code CLI is installed.
// GET /packages/claude-status
func (h *PackagesHandler) GetClaudeStatus(c echo.Context) error {
	status := map[string]interface{}{
		"installed": false,
		"version":   "",
	}

	claudePath := findBinaryPath("claude")
	if claudePath == "" {
		return response.OK(c, status)
	}
	status["installed"] = true

	versionOutput, err := exec.Command(claudePath, "--version").Output()
	if err == nil {
		status["version"] = strings.TrimSpace(string(versionOutput))
	}

	return response.OK(c, status)
}

// ---------- InstallClaude ----------

// InstallClaude installs Claude Code CLI using the official install script.
// Uses Server-Sent Events (SSE) to stream installation output in real-time.
// POST /packages/install-claude
func (h *PackagesHandler) InstallClaude(c echo.Context) error {
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

	sendLine(">>> Installing Claude Code CLI ...")

	dlCtx, dlCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer dlCancel()
	dlCmd := exec.CommandContext(dlCtx, "curl", "-fsSL", "https://claude.ai/install.sh", "-o", "/tmp/install-claude.sh")
	dlOut, err := dlCmd.CombinedOutput()
	if len(dlOut) > 0 {
		for _, line := range strings.Split(string(dlOut), "\n") {
			if line != "" {
				sendLine(line)
			}
		}
	}
	if err != nil {
		sendLine("ERROR: Failed to download Claude install script: " + err.Error())
		sendLine("[DONE]")
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", "/tmp/install-claude.sh")
	cmd.Env = append(os.Environ(), "DEBIAN_FRONTEND=noninteractive")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		sendLine("ERROR: " + err.Error())
		sendLine("[DONE]")
		return nil
	}
	cmd.Stderr = cmd.Stdout

	if err := cmd.Start(); err != nil {
		sendLine("ERROR: Failed to start Claude install: " + err.Error())
		sendLine("[DONE]")
		return nil
	}

	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		sendLine(scanner.Text())
	}

	if err := cmd.Wait(); err != nil {
		sendLine("ERROR: Claude install failed: " + err.Error())
		sendLine("[DONE]")
		return nil
	}

	os.Remove("/tmp/install-claude.sh")

	sendLine(">>> Claude Code CLI installed successfully!")
	sendLine("[DONE]")
	return nil
}

// ---------- GetCodexStatus ----------

// GetCodexStatus checks whether OpenAI Codex CLI is installed.
// GET /packages/codex-status
func (h *PackagesHandler) GetCodexStatus(c echo.Context) error {
	status := map[string]interface{}{
		"installed": false,
		"version":   "",
	}

	codexPath := findBinaryPath("codex")
	if codexPath == "" {
		return response.OK(c, status)
	}
	status["installed"] = true

	versionOutput, err := exec.Command(codexPath, "--version").Output()
	if err == nil {
		status["version"] = strings.TrimSpace(string(versionOutput))
	}

	return response.OK(c, status)
}

// ---------- InstallCodex ----------

// InstallCodex installs OpenAI Codex CLI via npm.
// Uses Server-Sent Events (SSE) to stream installation output in real-time.
// POST /packages/install-codex
func (h *PackagesHandler) InstallCodex(c echo.Context) error {
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

	// Check npm is available
	npmPath, err := exec.LookPath("npm")
	if err != nil || npmPath == "" {
		sendLine("ERROR: npm is not installed. Please install Node.js first.")
		sendLine("[DONE]")
		return nil
	}

	sendLine(">>> Installing OpenAI Codex CLI via npm ...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "npm", "install", "-g", "@openai/codex")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		sendLine("ERROR: " + err.Error())
		sendLine("[DONE]")
		return nil
	}
	cmd.Stderr = cmd.Stdout

	if err := cmd.Start(); err != nil {
		sendLine("ERROR: Failed to start Codex install: " + err.Error())
		sendLine("[DONE]")
		return nil
	}

	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		sendLine(scanner.Text())
	}

	if err := cmd.Wait(); err != nil {
		sendLine("ERROR: Codex install failed: " + err.Error())
		sendLine("[DONE]")
		return nil
	}

	sendLine(">>> OpenAI Codex CLI installed successfully!")
	sendLine("[DONE]")
	return nil
}

// ---------- GetGeminiStatus ----------

// GetGeminiStatus checks whether Google Gemini CLI is installed.
// GET /packages/gemini-status
func (h *PackagesHandler) GetGeminiStatus(c echo.Context) error {
	status := map[string]interface{}{
		"installed": false,
		"version":   "",
	}

	geminiPath := findBinaryPath("gemini")
	if geminiPath == "" {
		return response.OK(c, status)
	}
	status["installed"] = true

	versionOutput, err := exec.Command(geminiPath, "--version").Output()
	if err == nil {
		status["version"] = strings.TrimSpace(string(versionOutput))
	}

	return response.OK(c, status)
}

// ---------- InstallGemini ----------

// InstallGemini installs Google Gemini CLI via npm.
// Uses Server-Sent Events (SSE) to stream installation output in real-time.
// POST /packages/install-gemini
func (h *PackagesHandler) InstallGemini(c echo.Context) error {
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

	// Check npm is available
	npmPath, err := exec.LookPath("npm")
	if err != nil || npmPath == "" {
		sendLine("ERROR: npm is not installed. Please install Node.js first.")
		sendLine("[DONE]")
		return nil
	}

	sendLine(">>> Installing Google Gemini CLI via npm ...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "npm", "install", "-g", "@google/gemini-cli")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		sendLine("ERROR: " + err.Error())
		sendLine("[DONE]")
		return nil
	}
	cmd.Stderr = cmd.Stdout

	if err := cmd.Start(); err != nil {
		sendLine("ERROR: Failed to start Gemini install: " + err.Error())
		sendLine("[DONE]")
		return nil
	}

	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		sendLine(scanner.Text())
	}

	if err := cmd.Wait(); err != nil {
		sendLine("ERROR: Gemini install failed: " + err.Error())
		sendLine("[DONE]")
		return nil
	}

	sendLine(">>> Google Gemini CLI installed successfully!")
	sendLine("[DONE]")
	return nil
}

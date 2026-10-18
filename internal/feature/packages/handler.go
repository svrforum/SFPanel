package packages

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	osExec "os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/common/exec"
	"github.com/svrforum/SFPanel/internal/common/platform"
	"github.com/svrforum/SFPanel/internal/release"
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

// Handler exposes REST handlers for system package management via apt.
type Handler struct {
	Cmd exec.Commander

	// status memoises the Get*Status answers. Opening the packages page runs
	// node, npm and docker to ask their versions — each a couple of hundred
	// milliseconds of process start-up, measured — for answers that change
	// only when something is installed or switched, and this handler is what
	// does the installing. Kept for statusTTL and dropped by every mutation
	// here; an install done from a shell shows up within the TTL. Docker's
	// running state is not memoised: it is one cheap systemctl query and it
	// can change under the panel at any moment.
	statusMu sync.Mutex
	status   map[string]statusEntry

	// tx is the package transaction running, or the last one this process
	// ran (transaction.go).
	txMu sync.Mutex
	tx   *aptTx
}

type statusEntry struct {
	value map[string]interface{}
	at    time.Time
}

const statusTTL = 10 * time.Minute

// cachedStatus returns a copy of the memoised answer for key, if fresh.
func (h *Handler) cachedStatus(key string) (map[string]interface{}, bool) {
	h.statusMu.Lock()
	defer h.statusMu.Unlock()
	e, ok := h.status[key]
	if !ok || time.Since(e.at) > statusTTL {
		return nil, false
	}
	out := make(map[string]interface{}, len(e.value))
	for k, v := range e.value {
		out[k] = v
	}
	return out, true
}

func (h *Handler) storeStatus(key string, value map[string]interface{}) {
	h.statusMu.Lock()
	defer h.statusMu.Unlock()
	if h.status == nil {
		h.status = map[string]statusEntry{}
	}
	cp := make(map[string]interface{}, len(value))
	for k, v := range value {
		cp[k] = v
	}
	h.status[key] = statusEntry{value: cp, at: time.Now()}
}

// invalidateStatus forgets every memoised answer. Deferred at the top of
// each mutation so it runs after the install stream has finished, whatever
// the outcome — a failed install may still have changed what is on disk.
func (h *Handler) invalidateStatus() {
	h.statusMu.Lock()
	h.status = nil
	h.statusMu.Unlock()
}

// validPackageName matches only safe package name characters.
//
// Anchored to require an alphanumeric first character: a leading `-` would
// be parsed by apt-get as a flag (e.g. `--reinstall`, `-y`, `--force-yes`),
// turning a "install pkg" call into "install --reinstall" — even though
// every install/remove/upgrade path now also passes `--` before the package
// name, the validator is the first line of defense and must not accept the
// shape on its own.
var validPackageName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._+\-]*$`)

// validNodeVersion accepts MAJOR, MAJOR.MINOR, or MAJOR.MINOR.PATCH with an
// optional leading 'v'. Previously the regex was ^v?\d+(\.\d+)*$ which
// accepted 'v1' (too coarse to act on) and 'v1.2.3.4.5' (not a real
// Node version). Tightening to at most three segments matches every Node
// release nvm understands.
var validNodeVersion = regexp.MustCompile(`^v?\d+(\.\d+){0,2}$`)

// validSearchQuery is a deliberately wider character set than package names —
// users routinely search with multi-word phrases ("redis server", "python
// async") that the package-name validator would reject. apt-cache search
// takes its query via argv (not a shell), so command injection is structurally
// absent; the validator only needs to keep out characters that confuse
// apt-cache itself (tabs, newlines, glob wildcards) and the obvious
// shell-ish forms that would clearly be a typo or paste accident.
var validSearchQuery = regexp.MustCompile(`^[a-zA-Z0-9._+\- ]+$`)

// ---------- Helpers ----------

// validatePackageName checks that a package name contains only allowed characters.
func validatePackageName(name string) bool {
	return validPackageName.MatchString(name)
}

// validateSearchQuery accepts a wider set than validatePackageName: spaces
// are permitted (multi-word queries) but no shell metacharacters or
// whitespace other than space. apt-cache search treats the query as a regex
// over package descriptions; we don't try to validate it as a regex (let
// apt-cache surface its own error) — only block what looks like an injection
// attempt or a paste accident.
func validateSearchQuery(q string) bool {
	return validSearchQuery.MatchString(q)
}

// ---------- CheckUpdates ----------

// CheckUpdates runs `apt list --upgradable` and returns a structured list of
// packages that have updates available.
//
// A repository apt-get update could not fetch does not stop the check: the
// other repositories were refreshed, and it comes back as a warning naming
// the repository. On a Proxmox VE host without a subscription the enterprise
// repository answers 401 to every update, and the check used to fail there
// every time with "exit status 100".
// GET /packages/updates
func (h *Handler) CheckUpdates(c echo.Context) error {
	if tx := h.runningTx(); tx != nil {
		return txRunningFail(c, tx.view())
	}

	// Refresh package lists first so results are up-to-date
	var warnings []response.Warning
	_, err := exec.AptUpdate(c.Request().Context(), h.Cmd)
	var ue *exec.AptUpdateError
	switch {
	case err == nil:
	case errors.Is(err, exec.ErrDpkgLocked):
		return response.Fail(c, http.StatusConflict, response.ErrAPTUpdateError,
			"Another package manager operation is in progress; try again shortly")
	case errors.As(err, &ue) && ue.Partial:
		warnings = response.RepoWarnings(ue.Unauthorized, ue.Failed)
	default:
		return response.Fail(c, http.StatusInternalServerError, response.ErrAPTUpdateError,
			"Failed to update package lists: "+response.SanitizeOutput(err.Error()))
	}

	// Parsed below, so in C: "upgradable from:" is translated on a host
	// whose language is not English, and the current version came back empty.
	output, err := h.Cmd.RunWithEnv(exec.AptParseEnv(), "apt", "list", "--upgradable")
	if err != nil {
		// apt list --upgradable may return exit code 0 even with warnings;
		// only fail if output is completely empty.
		if strings.TrimSpace(output) == "" {
			return response.Fail(c, http.StatusInternalServerError, response.ErrAPTError, "Failed to check updates: "+response.SanitizeOutput(err.Error()))
		}
	}

	packages := parseUpgradablePackages(output)

	data := map[string]interface{}{
		"updates":      packages,
		"total":        len(packages),
		"last_checked": time.Now().UTC().Format(time.RFC3339),
	}
	if len(warnings) > 0 {
		data["warnings"] = warnings
	}
	return response.OK(c, data)
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
	packages := make([]PackageInfo, 0)
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

// UpgradePackages refreshes the package lists and upgrades all packages, or
// the ones named, as a package transaction (transaction.go): apt runs in a
// transient unit of its own and this request streams its log. A client that
// goes away stops the streaming and not apt, and GET /packages/transaction/stream
// picks the stream up again.
// POST /packages/upgrade
// JSON body: { "packages": ["pkg1", "pkg2"] } (optional; empty upgrades all)
func (h *Handler) UpgradePackages(c echo.Context) error {
	// Phase 1: validate body BEFORE switching to SSE so we can return a
	// structured 400. After SSE headers are set the response is committed
	// to text/event-stream.
	var req struct {
		Packages []string `json:"packages"`
	}
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}
	for _, pkg := range req.Packages {
		if !validatePackageName(pkg) {
			return response.Fail(c, http.StatusBadRequest, response.ErrInvalidPackageName,
				fmt.Sprintf("Invalid package name: %s", pkg))
		}
	}
	if err := sseReady(c); err != nil {
		return err
	}

	// Refused while another transaction runs, or while something outside
	// the panel holds the dpkg lock — apt-get would otherwise fail on it at
	// once, after the lists were refreshed for nothing.
	tx, err := h.beginTx(txUpgrade, req.Packages, upgradeArgv(req.Packages))
	if err != nil {
		return failTx(c, err, response.ErrAPTUpgradeError)
	}

	// Phase 2: stream the transaction's log via SSE.
	go h.runTx(tx, true)
	return h.followTx(c, tx, 0)
}

// upgradeArgv is the apt-get command line of an upgrade. With packages named,
// only those are upgraded. Without, everything is, and the verb depends on
// the system:
//
//   - Proxmox VE: dist-upgrade, which Proxmox requires. Every kernel update
//     is a new package and its releases add and replace packages; "upgrade"
//     holds exactly those back while it upgrades the rest of the stack, the
//     partial state Proxmox warns about.
//   - elsewhere: upgrade --with-new-pkgs, which also installs the new packages
//     an update needs (a kernel with a new ABI on Debian and Ubuntu) and never
//     removes one. Plain "upgrade" kept those back and reported success.
//
// AptDpkgOptions answer dpkg's question about a configuration file the
// operator edited, which DEBIAN_FRONTEND does not reach.
func upgradeArgv(packages []string) []string {
	argv := append([]string{"apt-get"}, exec.AptDpkgOptions...)
	if len(packages) > 0 {
		// `--` separates apt-get's own flags from the positional package list,
		// so a name that somehow slipped past validatePackageName still cannot
		// be reinterpreted as a flag.
		argv = append(argv, "install", "--only-upgrade", "-y", "--")
		return append(argv, packages...)
	}
	if platform.Get().PVE {
		return append(argv, "-y", "dist-upgrade")
	}
	return append(argv, "-y", "--with-new-pkgs", "upgrade")
}

// installArgv is the apt-get command line of an install, the one
// exec.AptInstall runs.
func installArgv(name string) []string {
	argv := append([]string{"apt-get"}, exec.AptDpkgOptions...)
	return append(argv, "install", "-y", "--", name)
}

// ---------- InstallPackage ----------

// InstallPackage installs a single package via apt-get install, in the
// request: the terminal's tmux prompt uses it for one small package. The
// Packages page installs through InstallPackageStream instead.
// POST /packages/install
// JSON body: { "name": "nginx" }
func (h *Handler) InstallPackage(c echo.Context) error {
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
	if engine := h.dockerEngineOnPVE(req.Name); engine != "" {
		return failDockerOnPVE(c, engine)
	}
	if tx := h.runningTx(); tx != nil {
		return txRunningFail(c, tx.view())
	}

	// What is installed is what the Docker and Node.js cards report.
	defer h.invalidateStatus()

	// The lock pre-check, the non-interactive environment, the request
	// context and the "--" all live in exec.AptInstall now — they used to be
	// six different combinations across six handlers.
	output, err := exec.AptInstall(c.Request().Context(), h.Cmd, req.Name)
	if errors.Is(err, exec.ErrDpkgLocked) {
		return response.Fail(c, http.StatusConflict, response.ErrAPTInstallError,
			"Another package manager operation is in progress; try again shortly")
	}
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrAPTInstallError,
			"Failed to install package: "+response.SanitizeOutput(err.Error()))
	}

	return response.OK(c, map[string]interface{}{
		"message": fmt.Sprintf("Package %s installed successfully", req.Name),
		"output":  response.SanitizeOutput(output),
	})
}

// InstallPackageStream installs a package from the Packages page as a
// package transaction (transaction.go), streaming apt's output as it goes.
// The unary InstallPackage ran apt as the request's child: a closed tab or a
// proxy timeout killed it while dpkg was unpacking.
// POST /packages/install-stream
// JSON body: { "name": "nginx" }
func (h *Handler) InstallPackageStream(c echo.Context) error {
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
	if engine := h.dockerEngineOnPVE(req.Name); engine != "" {
		return failDockerOnPVE(c, engine)
	}
	if err := sseReady(c); err != nil {
		return err
	}

	tx, err := h.beginTx(txInstall, []string{req.Name}, installArgv(req.Name))
	if err != nil {
		return failTx(c, err, response.ErrAPTInstallError)
	}
	go h.runTx(tx, false)
	return h.followTx(c, tx, 0)
}

// dockerEngines are the packages that put a Docker daemon on the host.
var dockerEngines = map[string]bool{"docker.io": true, "docker-ce": true, "moby-engine": true}

// aptSimInstall matches a package apt's simulation would install new; an
// upgrade of one already there names its old version in brackets first.
var aptSimInstall = regexp.MustCompile(`(?m)^Inst (\S+) \(`)

// dockerEngineOnPVE names the Docker engine that installing name would put on
// a Proxmox VE host, or is "". dockerd turns forwarding on and sets the
// FORWARD policy to DROP, which the guests' routed traffic passes through, and
// their bridged traffic too wherever bridge filtering is on. An engine already
// installed is managed as usual.
//
// Asked of apt itself, in a simulation that changes nothing: another package
// can bring the engine as a dependency or a recommendation (on Ubuntu,
// docker-buildx depends on docker.io and docker-compose-v2 recommends it),
// which a list of names would let through. A simulation that fails (no such
// package) leaves the name itself to go by.
func (h *Handler) dockerEngineOnPVE(name string) string {
	if !platform.Get().PVE {
		return ""
	}
	out, err := h.Cmd.RunWithEnv(exec.AptParseEnv(), "apt-get", "-s", "install", "--", name)
	if err != nil && dockerEngines[name] {
		return name
	}
	for _, m := range aptSimInstall.FindAllStringSubmatch(out, -1) {
		if dockerEngines[m[1]] {
			return m[1]
		}
	}
	return ""
}

// failDockerOnPVE refuses to put a Docker engine on a Proxmox VE host.
func failDockerOnPVE(c echo.Context, engine string) error {
	return response.FailWith(c, http.StatusConflict, response.ErrDockerOnPVE,
		"Docker is not installed on a Proxmox VE host ("+engine+"): its FORWARD DROP policy can cut the guests off the network. Install it in a VM or LXC container.",
		map[string]string{"package": engine})
}

// ---------- RemovePackage ----------

// RemovePackage removes a single package via apt-get remove.
// POST /packages/remove
// JSON body: { "name": "nginx" }
func (h *Handler) RemovePackage(c echo.Context) error {
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
	if tx := h.runningTx(); tx != nil {
		return txRunningFail(c, tx.view())
	}

	defer h.invalidateStatus()

	output, err := exec.AptRemove(c.Request().Context(), h.Cmd, req.Name)
	if errors.Is(err, exec.ErrDpkgLocked) {
		return response.Fail(c, http.StatusConflict, response.ErrAPTRemoveError,
			"Another package manager operation is in progress; try again shortly")
	}
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrAPTRemoveError,
			"Failed to remove package: "+response.SanitizeOutput(err.Error()))
	}

	return response.OK(c, map[string]interface{}{
		"message": fmt.Sprintf("Package %s removed successfully", req.Name),
		"output":  response.SanitizeOutput(output),
	})
}

// ---------- SearchPackages ----------

// SearchPackages searches the apt cache for packages matching a query string.
// GET /packages/search?q=nginx
func (h *Handler) SearchPackages(c echo.Context) error {
	query := strings.TrimSpace(c.QueryParam("q"))
	if query == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrMissingQuery, "Query parameter 'q' is required")
	}
	if !validateSearchQuery(query) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidQuery,
			"Search query contains invalid characters (allowed: a-zA-Z0-9._+- and spaces)")
	}

	// In C, like every apt call whose output is parsed: this used LANG=C,
	// which an LC_ALL or LC_MESSAGES in the environment outranks.
	output, err := h.Cmd.RunWithEnv(exec.AptParseEnv(), "apt-cache", "search", query)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrAPTSearchError,
			"Failed to search packages: "+response.SanitizeOutput(err.Error()))
	}

	packages := parseSearchResults(output, 50)

	// Enrich with installed status via dpkg-query
	if len(packages) > 0 {
		installed := h.getInstalledPackages(packages)
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
func (h *Handler) getInstalledPackages(packages []PackageInfo) map[string]bool {
	installed := make(map[string]bool, len(packages))

	names := make([]string, len(packages))
	for i, pkg := range packages {
		names[i] = pkg.Name
	}

	// dpkg-query -W -f='${Package}\t${db:Status-Abbrev}\n' pkg1 pkg2 ...
	args := append([]string{"-W", "-f=${Package}\t${db:Status-Abbrev}\n"}, names...)
	output, _ := h.Cmd.RunWithTimeout(10*time.Second, "dpkg-query", args...)

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
	packages := make([]PackageInfo, 0)
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
//
// proxmox_host tells the page to explain why it offers no install there.
// Where Docker is installed, forward_policy and bridge_nf_call_iptables say
// what it did to the host's forwarding: dockerd sets the FORWARD policy to
// DROP when it has to turn forwarding on itself, and with bridge filtering on
// that policy reaches every frame crossing a Linux bridge — on a Proxmox VE
// host, the guests' traffic. bridge_nf_call_iptables is null while
// br_netfilter is not loaded, which means off.
//
// compose_available means `docker compose`, the v2 plugin the panel's stacks
// run on; a host with only the old docker-compose binary was reported as
// having Compose and then failed every stack. compose_v1_only says so instead.
// GET /packages/docker-status
func (h *Handler) GetDockerStatus(c echo.Context) error {
	pve := platform.Get().PVE
	status := map[string]interface{}{
		"installed":         false,
		"version":           "",
		"running":           false,
		"compose_available": false,
		"compose_v1_only":   false,
		"proxmox_host":      pve,
	}

	if !h.Cmd.Exists("docker") {
		return response.OK(c, status)
	}
	status["installed"] = true

	if cached, ok := h.cachedStatus("docker"); ok {
		status = cached
		status["proxmox_host"] = pve
	} else {
		versionOutput, err := h.Cmd.Run("docker", "--version")
		if err == nil {
			status["version"] = strings.TrimSpace(versionOutput)
		}
		if _, err := h.Cmd.Run("docker", "compose", "version"); err == nil {
			status["compose_available"] = true
		} else if h.Cmd.Exists("docker-compose") {
			status["compose_v1_only"] = true
		}
		h.storeStatus("docker", status)
	}

	// Live, never memoised.
	status["running"] = false
	activeOutput, err := h.Cmd.Run("systemctl", "is-active", "docker")
	if err == nil && strings.TrimSpace(activeOutput) == "active" {
		status["running"] = true
	}
	status["forward_policy"] = ""
	if out, err := h.Cmd.Run("iptables", "-S", "FORWARD"); err == nil {
		status["forward_policy"] = forwardPolicy(out)
	}
	status["bridge_nf_call_iptables"] = bridgeNFCallIptables()

	return response.OK(c, status)
}

// forwardPolicy is the filter table's FORWARD policy in `iptables -S
// FORWARD` ("-P FORWARD DROP"), or "".
func forwardPolicy(out string) string {
	for _, line := range strings.Split(out, "\n") {
		if f := strings.Fields(line); len(f) == 3 && f[0] == "-P" && f[1] == "FORWARD" {
			return f[2]
		}
	}
	return ""
}

// bridgeNFPath says whether frames crossing a Linux bridge pass through
// iptables. It exists only while br_netfilter is loaded. A var for tests.
var bridgeNFPath = "/proc/sys/net/bridge/bridge-nf-call-iptables"

// bridgeNFCallIptables is bridgeNFPath's value, or nil without br_netfilter.
func bridgeNFCallIptables() *bool {
	data, err := os.ReadFile(bridgeNFPath)
	if err != nil {
		return nil
	}
	on := strings.TrimSpace(string(data)) == "1"
	return &on
}

// ---------- InstallDocker ----------

// downloadInstaller is release.DownloadInstaller; tests replace it so that
// nothing is fetched.
var downloadInstaller = release.DownloadInstaller

// InstallDocker installs Docker Engine using the official get.docker.com script.
// Uses Server-Sent Events (SSE) to stream installation output in real-time.
//
// Not on a Proxmox VE host: get.docker.com takes it for the Debian it is
// built on, and the daemon it installs turns forwarding on and sets the
// FORWARD policy to DROP, under the guests' routed traffic and, wherever
// bridge filtering is on, their bridged traffic. Proxmox recommends Docker in
// a VM or an LXC container, and the page says so instead of offering this.
// POST /packages/install-docker
func (h *Handler) InstallDocker(c echo.Context) error {
	if platform.Get().PVE {
		return response.Fail(c, http.StatusConflict, response.ErrDockerOnPVE,
			"Docker is not installed on a Proxmox VE host: its FORWARD DROP policy can cut the guests off the network. Install it in a VM or LXC container.")
	}
	// The script runs apt, which would meet the transaction's lock.
	if tx := h.runningTx(); tx != nil {
		return txRunningFail(c, tx.view())
	}

	defer h.invalidateStatus()
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
		// Sanitize centrally so every SSE line — status, error, and streamed
		// command output — is stripped of ANSI and secret patterns uniformly.
		fmt.Fprintf(c.Response(), "data: %s\n\n", response.SanitizeOutput(line))
		flusher.Flush()
	}

	sendLine(">>> Downloading Docker install script from https://get.docker.com ...")

	// Step 1: Download get-docker.sh (30s timeout) into a private temp file
	dlCtx, dlCancel := context.WithTimeout(c.Request().Context(), 30*time.Second)
	defer dlCancel()
	scriptPath, dlOut, err := downloadInstaller(dlCtx, "https://get.docker.com")
	for _, line := range strings.Split(dlOut, "\n") {
		if line != "" {
			sendLine(line)
		}
	}
	if err != nil {
		sendLine("ERROR: Failed to download Docker install script: " + err.Error())
		sendLine("[DONE]")
		return nil
	}
	defer os.Remove(scriptPath)

	// Step 1.5: Verify SHA-256 if operator pinned one. Soft-pass when env
	// var unset (matches CLAUDE.md "track-latest by default" stance).
	if err := release.VerifyInstaller(scriptPath, "SFPANEL_DOCKER_INSTALLER_SHA256", "docker"); err != nil {
		sendLine("ERROR: " + response.SanitizeOutput(err.Error()))
		sendLine("[DONE]")
		return nil
	}

	sendLine(">>> Running install script (this may take a few minutes) ...")

	// Step 2: Run the install script with real-time output streaming
	ctx, cancel := context.WithTimeout(c.Request().Context(), 10*time.Minute)
	defer cancel()

	cmd := osExec.CommandContext(ctx, "sh", scriptPath)
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
	exec.PrepareScanner(scanner)
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

// ---------- GetNodeStatus ----------

// GetNodeStatus checks whether Node.js and NVM are installed.
// GET /packages/node-status
func (h *Handler) GetNodeStatus(c echo.Context) error {
	if cached, ok := h.cachedStatus("node"); ok {
		return response.OK(c, cached)
	}
	status := map[string]interface{}{
		"installed":     false,
		"version":       "",
		"nvm_installed": false,
		"npm_version":   "",
	}
	defer h.storeStatus("node", status)

	// Check NVM (search root and user home directories)
	nvmDir := findNVMDir()
	if nvmDir != "" {
		status["nvm_installed"] = true
	}

	// Check if node is in PATH
	if !h.Cmd.Exists("node") {
		// NVM-installed node might not be in PATH, check common location
		nvmNode := nvmDir + "/versions/node"
		if entries, dirErr := os.ReadDir(nvmNode); dirErr == nil && len(entries) > 0 {
			// Find latest version
			latest := entries[len(entries)-1].Name()
			testPath := nvmNode + "/" + latest + "/bin/node"
			if _, statErr := os.Stat(testPath); statErr == nil {
				out, runErr := h.Cmd.Run(testPath, "--version")
				if runErr == nil {
					status["installed"] = true
					status["version"] = strings.TrimSpace(out)
				}
				npmPath := nvmNode + "/" + latest + "/bin/npm"
				npmOut, npmErr := h.Cmd.Run(npmPath, "--version")
				if npmErr == nil {
					status["npm_version"] = strings.TrimSpace(npmOut)
				}
			}
		}
		return response.OK(c, status)
	}

	status["installed"] = true
	versionOutput, err := h.Cmd.Run("node", "--version")
	if err == nil {
		status["version"] = strings.TrimSpace(versionOutput)
	}
	npmOut, err := h.Cmd.Run("npm", "--version")
	if err == nil {
		status["npm_version"] = strings.TrimSpace(npmOut)
	}

	return response.OK(c, status)
}

// keepLTSDefault pins a default that resolves through an lts/ alias to the
// version it resolves to now. nvm rewrites every lts/ alias from the release
// index each time it reads one — `nvm install` does, and listing the remote
// versions did — so a default of lts/* turns N/A as soon as a newer LTS
// ships, and node, npm and every tool installed with `npm install -g` drop
// off PATH in each new shell. nvm sets that default itself on a first
// `nvm install --lts`, and this page used to set it. Pinned, it stays what it
// is.
const keepLTSDefault = `case "$(cat "$NVM_DIR/alias/default" 2>/dev/null)" in lts/*) v=$(nvm version default); [ "$v" = N/A ] || nvm alias default "$v" >/dev/null;; esac`

// ---------- InstallNode ----------

// InstallNode installs Node.js via NVM (Node Version Manager).
// Uses Server-Sent Events (SSE) to stream installation output in real-time.
// POST /packages/install-node
func (h *Handler) InstallNode(c echo.Context) error {
	defer h.invalidateStatus()
	c.Response().Header().Set("Content-Type", "text/event-stream")
	c.Response().Header().Set("Cache-Control", "no-cache")
	c.Response().Header().Set("Connection", "keep-alive")
	c.Response().WriteHeader(http.StatusOK)

	flusher, ok := c.Response().Writer.(http.Flusher)
	if !ok {
		return response.Fail(c, http.StatusInternalServerError, response.ErrSSEError, "Streaming not supported")
	}

	sendLine := func(line string) {
		// Sanitize centrally so every SSE line — status, error, and streamed
		// command output — is stripped of ANSI and secret patterns uniformly.
		fmt.Fprintf(c.Response(), "data: %s\n\n", response.SanitizeOutput(line))
		flusher.Flush()
	}

	homeDir := "/root"
	nvmDir := homeDir + "/.nvm"

	// Step 1: Install NVM if not present
	if _, err := os.Stat(nvmDir + "/nvm.sh"); os.IsNotExist(err) {
		sendLine(">>> Installing NVM (Node Version Manager) ...")

		dlCtx, dlCancel := context.WithTimeout(c.Request().Context(), 30*time.Second)
		defer dlCancel()
		scriptPath, dlOut, err := release.DownloadInstaller(dlCtx, release.NVMInstallerURL)
		for _, line := range strings.Split(dlOut, "\n") {
			if line != "" {
				sendLine(line)
			}
		}
		if err != nil {
			sendLine("ERROR: Failed to download NVM install script: " + err.Error())
			sendLine("[DONE]")
			return nil
		}
		defer os.Remove(scriptPath)

		// Verify SHA-256 if operator pinned one. Soft-pass when env unset.
		if err := release.VerifyInstaller(scriptPath, "SFPANEL_NVM_INSTALLER_SHA256", "nvm"); err != nil {
			sendLine("ERROR: " + response.SanitizeOutput(err.Error()))
			sendLine("[DONE]")
			return nil
		}

		ctx, cancel := context.WithTimeout(c.Request().Context(), 2*time.Minute)
		defer cancel()

		cmd := osExec.CommandContext(ctx, "bash", scriptPath)
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
		exec.PrepareScanner(scanner)
		for scanner.Scan() {
			sendLine(scanner.Text())
		}

		if err := cmd.Wait(); err != nil {
			sendLine("ERROR: NVM install failed: " + err.Error())
			sendLine("[DONE]")
			return nil
		}
		sendLine(">>> NVM installed successfully!")
	} else {
		sendLine(">>> NVM already installed, skipping...")
	}

	// Step 2: Install Node.js LTS via NVM
	sendLine(">>> Installing Node.js LTS via NVM ...")

	ctx, cancel := context.WithTimeout(c.Request().Context(), 5*time.Minute)
	defer cancel()

	// Source nvm and install LTS in a single bash invocation. The default is
	// the version just installed, not lts/*: see keepLTSDefault.
	script := fmt.Sprintf(`export NVM_DIR="%s" && . "$NVM_DIR/nvm.sh" && nvm install --lts && nvm alias default "$(nvm current)" && node --version && npm --version`, nvmDir)
	cmd := osExec.CommandContext(ctx, "bash", "-c", script)
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
	exec.PrepareScanner(scanner)
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
	linkCtx, linkCancel := context.WithTimeout(c.Request().Context(), 5*time.Minute)
	defer linkCancel()
	linkCmd := osExec.CommandContext(linkCtx, "bash", "-c", linkScript)
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
func (h *Handler) GetNodeVersions(c echo.Context) error {
	nvmDir := findNVMDir()

	type NodeVersion struct {
		Version string `json:"version"`
		Active  bool   `json:"active"`
		LTS     bool   `json:"lts"`
	}

	result := map[string]interface{}{
		"versions":   []NodeVersion{},
		"current":    "",
		"remote_lts": []string{},
	}

	if nvmDir == "" {
		return response.OK(c, result)
	}

	// Get current active version
	currentScript := fmt.Sprintf(`export NVM_DIR="%s" && . "$NVM_DIR/nvm.sh" && nvm current 2>/dev/null`, nvmDir)
	currentOut, _ := h.Cmd.Run("bash", "-c", currentScript)
	current := strings.TrimSpace(currentOut)
	result["current"] = current

	// List installed versions by scanning the NVM versions directory directly
	// This is more reliable than parsing `nvm ls` output which has ANSI codes and varying formats
	versions := make([]NodeVersion, 0)
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
	if out, err := h.Cmd.Run("bash", "-c", script); err == nil {
		ltsVersions := make(map[string]bool)
		versionRe := regexp.MustCompile(`(v\d+\.\d+\.\d+)`)
		for _, line := range strings.Split(out, "\n") {
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

	// List available remote LTS versions (cached, quick), against a scratch
	// NVM_DIR: nvm writes the lts/ aliases from every index it reads, and a
	// listing must not move what a default of lts/* resolves to
	// (keepLTSDefault).
	ltsScript := fmt.Sprintf(`export NVM_DIR="%s" && . "$NVM_DIR/nvm.sh" && scratch=$(mktemp -d) && (NVM_DIR="$scratch" nvm ls-remote --lts --no-colors 2>/dev/null) | grep "Latest" | tail -5; rm -rf "$scratch"`, nvmDir)
	ltsOut, err := h.Cmd.RunWithTimeout(15*time.Second, "bash", "-c", ltsScript)
	if err == nil {
		remoteLTS := make([]string, 0)
		for _, line := range strings.Split(ltsOut, "\n") {
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
func (h *Handler) SwitchNodeVersion(c echo.Context) error {
	defer h.invalidateStatus()
	var body struct {
		Version string `json:"version"`
	}
	if err := c.Bind(&body); err != nil || body.Version == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidBody, "version is required")
	}

	if !validNodeVersion.MatchString(body.Version) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidBody, "invalid version format")
	}

	nvmDir := findNVMDir()
	if nvmDir == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrCommandFailed, "NVM is not installed")
	}

	// Pass NVM_DIR via environment so the shell-injectable path component
	// (findNVMDir result) never lives inside the `bash -c` script string.
	// body.Version is already validated above against versionRe.
	env := append(os.Environ(), "NVM_DIR="+nvmDir)
	script := fmt.Sprintf(`. "$NVM_DIR/nvm.sh" && nvm alias default %s && nvm use %s 2>&1`, body.Version, body.Version)
	out, err := h.Cmd.RunWithEnv(env, "bash", "-c", script)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrCommandFailed, out)
	}

	// Update symlinks in /usr/local/bin
	linkScript := fmt.Sprintf(`. "$NVM_DIR/nvm.sh" && nvm use %s && NODE_PATH=$(which node) && NODE_DIR=$(dirname "$NODE_PATH") && ln -sf "$NODE_DIR/node" /usr/local/bin/node && ln -sf "$NODE_DIR/npm" /usr/local/bin/npm && ln -sf "$NODE_DIR/npx" /usr/local/bin/npx`, body.Version)
	h.Cmd.RunWithEnv(env, "bash", "-c", linkScript)

	return response.OK(c, map[string]string{"switched": body.Version, "output": strings.TrimSpace(out)})
}

// ---------- InstallNodeVersion ----------

// InstallNodeVersion installs a specific Node.js version via NVM.
// POST /packages/node-install-version
func (h *Handler) InstallNodeVersion(c echo.Context) error {
	defer h.invalidateStatus()
	var body struct {
		Version string `json:"version"`
	}
	if err := c.Bind(&body); err != nil || body.Version == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidBody, "version is required")
	}

	if !validNodeVersion.MatchString(body.Version) {
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
		// Sanitize centrally so every SSE line — status, error, and streamed
		// command output — is stripped of ANSI and secret patterns uniformly.
		fmt.Fprintf(c.Response(), "data: %s\n\n", response.SanitizeOutput(line))
		flusher.Flush()
	}

	sendLine(fmt.Sprintf(">>> Installing Node.js %s ...", body.Version))

	script := fmt.Sprintf(`export NVM_DIR="%s" && . "$NVM_DIR/nvm.sh" && { `+keepLTSDefault+`; nvm install %s; }`, nvmDir, body.Version)
	ctx, cancel := context.WithTimeout(c.Request().Context(), 5*time.Minute)
	defer cancel()

	cmd := osExec.CommandContext(ctx, "bash", "-c", script)
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
	exec.PrepareScanner(scanner)
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
func (h *Handler) UninstallNodeVersion(c echo.Context) error {
	defer h.invalidateStatus()
	var body struct {
		Version string `json:"version"`
	}
	if err := c.Bind(&body); err != nil || body.Version == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidBody, "version is required")
	}

	if !validNodeVersion.MatchString(body.Version) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidBody, "invalid version format")
	}

	nvmDir := findNVMDir()
	if nvmDir == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrCommandFailed, "NVM is not installed")
	}

	script := fmt.Sprintf(`export NVM_DIR="%s" && . "$NVM_DIR/nvm.sh" && nvm uninstall %s 2>&1`, nvmDir, body.Version)
	out, err := h.Cmd.Run("bash", "-c", script)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrCommandFailed, out)
	}

	return response.OK(c, map[string]string{"removed": body.Version, "output": strings.TrimSpace(out)})
}

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

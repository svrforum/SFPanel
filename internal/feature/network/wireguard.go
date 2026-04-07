package network

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/common/exec"
)

// WireGuardHandler exposes REST handlers for WireGuard VPN management.
type WireGuardHandler struct {
	Cmd exec.Commander
}

// ---------- Types ----------

// WireGuardStatus represents the installation status of WireGuard tools.
type WireGuardStatus struct {
	Installed bool   `json:"installed"`
	Version   string `json:"version"`
}

// WireGuardInterface represents a WireGuard interface with its configuration and peers.
type WireGuardInterface struct {
	Name       string          `json:"name"`
	Active     bool            `json:"active"`
	PublicKey  string          `json:"public_key"`
	ListenPort int             `json:"listen_port"`
	Address    string          `json:"address,omitempty"`
	DNS        string          `json:"dns,omitempty"`
	Peers      []WireGuardPeer `json:"peers"`
}

// WireGuardPeer represents a peer in a WireGuard interface.
type WireGuardPeer struct {
	PublicKey       string   `json:"public_key"`
	Endpoint        string   `json:"endpoint"`
	AllowedIPs      []string `json:"allowed_ips"`
	LatestHandshake int64    `json:"latest_handshake"`
	TransferRx      uint64   `json:"transfer_rx"`
	TransferTx      uint64   `json:"transfer_tx"`
}

// CreateWGConfigRequest is the request body for creating a new WireGuard config.
type CreateWGConfigRequest struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

// ---------- Validation ----------

// validWGName matches safe WireGuard interface names (path traversal prevention).
var validWGName = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_-]{0,14}$`)

// ---------- Helpers ----------

// wgConfigDir is the directory where WireGuard config files are stored.
const wgConfigDir = "/etc/wireguard"

// parseWGDump parses the output of `wg show <name> dump` (machine-readable, tab-separated).
// First line: interface (private_key, public_key, listen_port, fwmark)
// Subsequent lines: peers (public_key, preshared_key, endpoint, allowed_ips, latest_handshake, transfer_rx, transfer_tx, persistent_keepalive)
func parseWGDump(dump string) (publicKey string, listenPort int, peers []WireGuardPeer) {
	lines := strings.Split(strings.TrimSpace(dump), "\n")
	if len(lines) == 0 {
		return
	}

	// Parse interface line
	ifaceFields := strings.Split(lines[0], "\t")
	if len(ifaceFields) >= 3 {
		publicKey = ifaceFields[1]
		listenPort, _ = strconv.Atoi(ifaceFields[2])
	}

	// Parse peer lines
	for _, line := range lines[1:] {
		fields := strings.Split(line, "\t")
		if len(fields) < 8 {
			continue
		}

		var allowedIPs []string
		if fields[3] != "(none)" && fields[3] != "" {
			allowedIPs = strings.Split(fields[3], ",")
		}

		handshake, _ := strconv.ParseInt(fields[4], 10, 64)
		rx, _ := strconv.ParseUint(fields[5], 10, 64)
		tx, _ := strconv.ParseUint(fields[6], 10, 64)

		endpoint := fields[2]
		if endpoint == "(none)" {
			endpoint = ""
		}

		peers = append(peers, WireGuardPeer{
			PublicKey:       fields[0],
			Endpoint:        endpoint,
			AllowedIPs:      allowedIPs,
			LatestHandshake: handshake,
			TransferRx:      rx,
			TransferTx:      tx,
		})
	}

	return
}

// parseWGConfField extracts a field value from a WireGuard config file content.
func parseWGConfField(content, field string) string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(strings.ToLower(line), strings.ToLower(field)) {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				return strings.TrimSpace(parts[1])
			}
		}
	}
	return ""
}

// isWGInterfaceActive checks if a WireGuard interface is currently active.
func (h *WireGuardHandler) isWGInterfaceActive(name string) bool {
	output, err := h.Cmd.Run("ip", "link", "show", "type", "wireguard")
	if err != nil {
		return false
	}
	return strings.Contains(output, name+":")
}

// listWGConfigNames returns the names of all .conf files in /etc/wireguard/.
func listWGConfigNames() ([]string, error) {
	entries, err := os.ReadDir(wgConfigDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".conf") {
			names = append(names, strings.TrimSuffix(e.Name(), ".conf"))
		}
	}
	return names, nil
}

// maskPrivateKey replaces PrivateKey value in a WireGuard config with asterisks.
func maskPrivateKey(content string) string {
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)
		if strings.HasPrefix(lower, "privatekey") {
			parts := strings.SplitN(trimmed, "=", 2)
			if len(parts) == 2 {
				lines[i] = parts[0] + "= ********"
			}
		}
	}
	return strings.Join(lines, "\n")
}

// ---------- Handlers ----------

// GetStatus returns WireGuard installation status.
// GET /network/wireguard/status
func (h *WireGuardHandler) GetStatus(c echo.Context) error {
	status := WireGuardStatus{}

	if !h.Cmd.Exists("wg") {
		return response.OK(c, status)
	}

	status.Installed = true
	versionOutput, err := h.Cmd.Run("wg", "--version")
	if err == nil {
		status.Version = strings.TrimSpace(versionOutput)
	}

	return response.OK(c, status)
}

// Install installs wireguard-tools via apt.
// POST /network/wireguard/install
func (h *WireGuardHandler) Install(c echo.Context) error {
	_, err := h.Cmd.RunWithEnv(exec.AptEnv(), "apt-get", "update")
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrAPTUpdateError,
			"Failed to update package lists: "+err.Error())
	}

	output, err := h.Cmd.RunWithEnv(exec.AptEnv(), "apt-get", "install", "-y", "wireguard-tools")
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrWGInstallError,
			"Failed to install wireguard-tools: "+err.Error())
	}

	return response.OK(c, map[string]interface{}{
		"message":        "WireGuard tools installed successfully",
		"install_output": strings.TrimSpace(output),
	})
}

// ListInterfaces returns all WireGuard interfaces with their status.
// GET /network/wireguard/interfaces
func (h *WireGuardHandler) ListInterfaces(c echo.Context) error {
	names, err := listWGConfigNames()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrWGListError,
			"Failed to list WireGuard configs: "+err.Error())
	}

	interfaces := make([]WireGuardInterface, 0, len(names))
	for _, name := range names {
		iface := WireGuardInterface{
			Name:   name,
			Active: h.isWGInterfaceActive(name),
			Peers:  []WireGuardPeer{},
		}

		// Read Address/DNS from config
		confPath := filepath.Join(wgConfigDir, name+".conf")
		confData, err := os.ReadFile(confPath)
		if err == nil {
			iface.Address = parseWGConfField(string(confData), "Address")
			iface.DNS = parseWGConfField(string(confData), "DNS")
		}

		// Get runtime info if active
		if iface.Active {
			dump, err := h.Cmd.Run("wg", "show", name, "dump")
			if err == nil {
				iface.PublicKey, iface.ListenPort, iface.Peers = parseWGDump(dump)
			}
		}

		interfaces = append(interfaces, iface)
	}

	return response.OK(c, interfaces)
}

// GetInterface returns detailed info for a single WireGuard interface.
// GET /network/wireguard/interfaces/:name
func (h *WireGuardHandler) GetInterface(c echo.Context) error {
	name := c.Param("name")
	if !validWGName.MatchString(name) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidName, "Invalid interface name")
	}

	confPath := filepath.Join(wgConfigDir, name+".conf")
	if _, err := os.Stat(confPath); os.IsNotExist(err) {
		return response.Fail(c, http.StatusNotFound, response.ErrNotFound, "Interface config not found")
	}

	iface := WireGuardInterface{
		Name:   name,
		Active: h.isWGInterfaceActive(name),
		Peers:  []WireGuardPeer{},
	}

	confData, err := os.ReadFile(confPath)
	if err == nil {
		iface.Address = parseWGConfField(string(confData), "Address")
		iface.DNS = parseWGConfField(string(confData), "DNS")
	}

	if iface.Active {
		dump, err := h.Cmd.Run("wg", "show", name, "dump")
		if err == nil {
			iface.PublicKey, iface.ListenPort, iface.Peers = parseWGDump(dump)
		}
	}

	return response.OK(c, iface)
}

// InterfaceUp brings up a WireGuard interface.
// POST /network/wireguard/interfaces/:name/up
func (h *WireGuardHandler) InterfaceUp(c echo.Context) error {
	name := c.Param("name")
	if !validWGName.MatchString(name) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidName, "Invalid interface name")
	}

	confPath := filepath.Join(wgConfigDir, name+".conf")
	if _, err := os.Stat(confPath); os.IsNotExist(err) {
		return response.Fail(c, http.StatusNotFound, response.ErrNotFound, "Interface config not found")
	}

	output, err := h.Cmd.Run("wg-quick", "up", name)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrWGUpError,
			"Failed to bring up interface: "+err.Error()+"\n"+output)
	}

	return response.OK(c, map[string]string{"message": fmt.Sprintf("Interface %s is now up", name)})
}

// InterfaceDown brings down a WireGuard interface.
// POST /network/wireguard/interfaces/:name/down
func (h *WireGuardHandler) InterfaceDown(c echo.Context) error {
	name := c.Param("name")
	if !validWGName.MatchString(name) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidName, "Invalid interface name")
	}

	output, err := h.Cmd.Run("wg-quick", "down", name)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrWGDownError,
			"Failed to bring down interface: "+err.Error()+"\n"+output)
	}

	return response.OK(c, map[string]string{"message": fmt.Sprintf("Interface %s is now down", name)})
}

// CreateConfig creates a new WireGuard config file.
// POST /network/wireguard/configs
func (h *WireGuardHandler) CreateConfig(c echo.Context) error {
	var req CreateWGConfigRequest
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}

	if !validWGName.MatchString(req.Name) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidName,
			"Name must start with a letter, contain only alphanumeric, hyphens, underscores, max 15 chars")
	}

	if strings.TrimSpace(req.Content) == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrEmptyContent, "Config content cannot be empty")
	}

	confPath := filepath.Join(wgConfigDir, req.Name+".conf")
	if _, err := os.Stat(confPath); err == nil {
		return response.Fail(c, http.StatusConflict, response.ErrAlreadyExists,
			fmt.Sprintf("Config %s already exists", req.Name))
	}

	// Ensure directory exists
	if err := os.MkdirAll(wgConfigDir, 0700); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDirError,
			"Failed to create config directory: "+err.Error())
	}

	if err := os.WriteFile(confPath, []byte(req.Content), 0600); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrWriteError,
			"Failed to write config file: "+err.Error())
	}

	return response.OK(c, map[string]string{
		"message": fmt.Sprintf("Config %s created", req.Name),
		"path":    confPath,
	})
}

// GetConfig reads a WireGuard config file with PrivateKey masked.
// GET /network/wireguard/configs/:name
func (h *WireGuardHandler) GetConfig(c echo.Context) error {
	name := c.Param("name")
	if !validWGName.MatchString(name) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidName, "Invalid config name")
	}

	confPath := filepath.Join(wgConfigDir, name+".conf")
	data, err := os.ReadFile(confPath)
	if err != nil {
		if os.IsNotExist(err) {
			return response.Fail(c, http.StatusNotFound, response.ErrNotFound, "Config not found")
		}
		return response.Fail(c, http.StatusInternalServerError, response.ErrReadError,
			"Failed to read config: "+err.Error())
	}

	return response.OK(c, map[string]string{
		"name":    name,
		"content": maskPrivateKey(string(data)),
	})
}

// UpdateConfig updates a WireGuard config file.
// PUT /network/wireguard/configs/:name
func (h *WireGuardHandler) UpdateConfig(c echo.Context) error {
	name := c.Param("name")
	if !validWGName.MatchString(name) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidName, "Invalid config name")
	}

	var req struct {
		Content string `json:"content"`
	}
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}

	if strings.TrimSpace(req.Content) == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrEmptyContent, "Config content cannot be empty")
	}

	confPath := filepath.Join(wgConfigDir, name+".conf")
	if _, err := os.Stat(confPath); os.IsNotExist(err) {
		return response.Fail(c, http.StatusNotFound, response.ErrNotFound, "Config not found")
	}

	if err := os.WriteFile(confPath, []byte(req.Content), 0600); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrWriteError,
			"Failed to update config: "+err.Error())
	}

	return response.OK(c, map[string]string{"message": fmt.Sprintf("Config %s updated", name)})
}

// DeleteConfig deletes a WireGuard config file (brings interface down first if active).
// DELETE /network/wireguard/configs/:name
func (h *WireGuardHandler) DeleteConfig(c echo.Context) error {
	name := c.Param("name")
	if !validWGName.MatchString(name) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidName, "Invalid config name")
	}

	confPath := filepath.Join(wgConfigDir, name+".conf")
	if _, err := os.Stat(confPath); os.IsNotExist(err) {
		return response.Fail(c, http.StatusNotFound, response.ErrNotFound, "Config not found")
	}

	// Bring down interface if active
	if h.isWGInterfaceActive(name) {
		h.Cmd.Run("wg-quick", "down", name)
	}

	if err := os.Remove(confPath); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDeleteError,
			"Failed to delete config: "+err.Error())
	}

	return response.OK(c, map[string]string{"message": fmt.Sprintf("Config %s deleted", name)})
}

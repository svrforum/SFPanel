package network

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	osExec "os/exec"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/common/exec"
)

// TailscaleHandler exposes REST handlers for Tailscale VPN management.
type TailscaleHandler struct {
	Cmd exec.Commander
}

// ---------- Types ----------

// TailscaleStatus represents the overall Tailscale status.
type TailscaleStatus struct {
	Installed         bool           `json:"installed"`
	DaemonRunning     bool           `json:"daemon_running"`
	Version           string         `json:"version"`
	BackendState      string         `json:"backend_state"` // Running, NeedsLogin, Stopped, NoState
	Self              *TailscaleSelf `json:"self"`
	TailnetName       string         `json:"tailnet_name"`
	MagicDNSSuffix    string         `json:"magic_dns_suffix"`
	AuthURL           string         `json:"auth_url"`
	AcceptRoutes      bool           `json:"accept_routes"`
	AdvertiseExitNode bool           `json:"advertise_exit_node"`
	CurrentExitNode   string         `json:"current_exit_node"` // IP of active exit node peer
}

// TailscaleSelf represents the local node info.
type TailscaleSelf struct {
	Hostname       string `json:"hostname"`
	TailscaleIP    string `json:"tailscale_ip"`
	TailscaleIPv6  string `json:"tailscale_ipv6"`
	Online         bool   `json:"online"`
	OS             string `json:"os"`
	ExitNodeOption bool   `json:"exit_node_option"` // true if advertising as exit node
}

// TailscalePeer represents a peer node in the tailnet.
type TailscalePeer struct {
	Hostname       string `json:"hostname"`
	DNSName        string `json:"dns_name"`
	TailscaleIP    string `json:"tailscale_ip"`
	OS             string `json:"os"`
	Online         bool   `json:"online"`
	LastSeen       string `json:"last_seen"`
	ExitNode       bool   `json:"exit_node"`
	ExitNodeOption bool   `json:"exit_node_option"`
	RxBytes        uint64 `json:"rx_bytes"`
	TxBytes        uint64 `json:"tx_bytes"`
}

// ---------- Internal JSON structures for `tailscale status --json` ----------

type tsStatusJSON struct {
	BackendState   string                 `json:"BackendState"`
	AuthURL        string                 `json:"AuthURL"`
	Self           *tsNodeJSON            `json:"Self"`
	Peer           map[string]*tsNodeJSON `json:"Peer"`
	CurrentTailnet *tsCurrentTailnetJSON  `json:"CurrentTailnet"`
	MagicDNSSuffix string                 `json:"MagicDNSSuffix"`
}

type tsNodeJSON struct {
	HostName       string   `json:"HostName"`
	DNSName        string   `json:"DNSName"`
	TailscaleIPs   []string `json:"TailscaleIPs"`
	OS             string   `json:"OS"`
	Online         bool     `json:"Online"`
	LastSeen       string   `json:"LastSeen"`
	ExitNode       bool     `json:"ExitNode"`
	ExitNodeOption bool     `json:"ExitNodeOption"`
	RxBytes        uint64   `json:"RxBytes"`
	TxBytes        uint64   `json:"TxBytes"`
}

type tsCurrentTailnetJSON struct {
	Name            string `json:"Name"`
	MagicDNSSuffix  string `json:"MagicDNSSuffix"`
	MagicDNSEnabled bool   `json:"MagicDNSEnabled"`
}

// ---------- Helpers ----------

// getTailscaleStatusJSON runs `tailscale status --json` and parses the output.
func (h *TailscaleHandler) getTailscaleStatusJSON() (*tsStatusJSON, error) {
	output, err := h.Cmd.Run("tailscale", "status", "--json")
	if err != nil {
		return nil, fmt.Errorf("tailscale status failed: %w", err)
	}

	var status tsStatusJSON
	if err := json.Unmarshal([]byte(output), &status); err != nil {
		return nil, fmt.Errorf("failed to parse tailscale status: %w", err)
	}

	return &status, nil
}

// isTailscaleDaemonRunning checks if the tailscaled service is active.
func (h *TailscaleHandler) isTailscaleDaemonRunning() bool {
	output, err := h.Cmd.Run("systemctl", "is-active", "tailscaled")
	if err != nil {
		return false
	}
	return strings.TrimSpace(output) == "active"
}

// tsPrefsJSON represents the relevant fields from `tailscale debug prefs`.
type tsPrefsJSON struct {
	RouteAll        bool     `json:"RouteAll"`        // accept-routes
	AdvertiseRoutes []string `json:"AdvertiseRoutes"` // contains "0.0.0.0/0" if advertising as exit node
}

// getTailscalePrefs parses `tailscale debug prefs` for current preference state.
func (h *TailscaleHandler) getTailscalePrefs() (acceptRoutes bool, advertiseExitNode bool) {
	output, err := h.Cmd.Run("tailscale", "debug", "prefs")
	if err != nil {
		return false, false
	}

	var prefs tsPrefsJSON
	if err := json.Unmarshal([]byte(output), &prefs); err != nil {
		return false, false
	}

	acceptRoutes = prefs.RouteAll
	for _, route := range prefs.AdvertiseRoutes {
		if route == "0.0.0.0/0" || route == "::/0" {
			advertiseExitNode = true
			break
		}
	}
	return
}

// ---------- Handlers ----------

// GetStatus returns Tailscale installation and connection status.
// GET /network/tailscale/status
func (h *TailscaleHandler) GetStatus(c echo.Context) error {
	status := TailscaleStatus{}

	if !h.Cmd.Exists("tailscale") {
		return response.OK(c, status)
	}

	status.Installed = true

	// Get version
	versionOutput, err := h.Cmd.Run("tailscale", "version")
	if err == nil {
		lines := strings.Split(strings.TrimSpace(versionOutput), "\n")
		if len(lines) > 0 {
			status.Version = strings.TrimSpace(lines[0])
		}
	}

	// Check daemon
	status.DaemonRunning = h.isTailscaleDaemonRunning()
	if !status.DaemonRunning {
		status.BackendState = "Stopped"
		return response.OK(c, status)
	}

	// Get detailed status from JSON
	tsStatus, err := h.getTailscaleStatusJSON()
	if err != nil {
		status.BackendState = "NoState"
		return response.OK(c, status)
	}

	status.BackendState = tsStatus.BackendState
	status.AuthURL = tsStatus.AuthURL
	status.MagicDNSSuffix = tsStatus.MagicDNSSuffix

	if tsStatus.CurrentTailnet != nil {
		status.TailnetName = tsStatus.CurrentTailnet.Name
		if status.MagicDNSSuffix == "" {
			status.MagicDNSSuffix = tsStatus.CurrentTailnet.MagicDNSSuffix
		}
	}

	// Parse self node
	if tsStatus.Self != nil {
		self := &TailscaleSelf{
			Hostname:       tsStatus.Self.HostName,
			Online:         tsStatus.Self.Online,
			OS:             tsStatus.Self.OS,
			ExitNodeOption: tsStatus.Self.ExitNodeOption,
		}
		if len(tsStatus.Self.TailscaleIPs) > 0 {
			self.TailscaleIP = tsStatus.Self.TailscaleIPs[0]
		}
		if len(tsStatus.Self.TailscaleIPs) > 1 {
			self.TailscaleIPv6 = tsStatus.Self.TailscaleIPs[1]
		}
		status.Self = self
	}

	// Get current preferences (accept-routes, advertise-exit-node) from debug prefs
	acceptRoutes, advertiseExitNode := h.getTailscalePrefs()
	status.AcceptRoutes = acceptRoutes
	status.AdvertiseExitNode = advertiseExitNode

	// Find current exit node from peers
	for _, node := range tsStatus.Peer {
		if node.ExitNode {
			if len(node.TailscaleIPs) > 0 {
				status.CurrentExitNode = node.TailscaleIPs[0]
			}
			break
		}
	}

	return response.OK(c, status)
}

// Install installs Tailscale using the official install script.
// Uses SSE to stream installation output in real-time.
// POST /network/tailscale/install
func (h *TailscaleHandler) Install(c echo.Context) error {
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

	sendLine(">>> Downloading Tailscale install script...")

	// Step 1: Download install script
	// Streaming command — cannot use Commander (needs live stdout pipe)
	dlCmd := osExec.CommandContext(context.Background(), "curl", "-fsSL", "https://tailscale.com/install.sh", "-o", "/tmp/tailscale-install.sh")
	dlOut, err := dlCmd.CombinedOutput()
	if len(dlOut) > 0 {
		for _, line := range strings.Split(string(dlOut), "\n") {
			if line != "" {
				sendLine(line)
			}
		}
	}
	if err != nil {
		sendLine("ERROR: Failed to download install script: " + err.Error())
		sendLine("[DONE]")
		return nil
	}

	sendLine(">>> Running install script (this may take a few minutes)...")

	// Step 2: Run install script with real-time output
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	// Streaming command — cannot use Commander (needs live stdout pipe)
	cmd := osExec.CommandContext(ctx, "sh", "/tmp/tailscale-install.sh")
	cmd.Env = append(cmd.Environ(), "DEBIAN_FRONTEND=noninteractive")

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		sendLine("ERROR: " + err.Error())
		sendLine("[DONE]")
		return nil
	}
	cmd.Stderr = cmd.Stdout

	if err := cmd.Start(); err != nil {
		sendLine("ERROR: Failed to start install script: " + err.Error())
		sendLine("[DONE]")
		return nil
	}

	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		sendLine(scanner.Text())
	}

	if err := cmd.Wait(); err != nil {
		sendLine("ERROR: Install script failed: " + err.Error())
		sendLine("[DONE]")
		return nil
	}

	// Step 3: Enable and start tailscaled
	sendLine(">>> Enabling tailscaled service...")
	enableOut, err := h.Cmd.Run("systemctl", "enable", "--now", "tailscaled")
	if err != nil {
		sendLine("WARNING: Failed to enable tailscaled: " + err.Error())
	} else if enableOut != "" {
		sendLine(strings.TrimSpace(enableOut))
	}

	sendLine(">>> Tailscale installation completed successfully!")
	sendLine("[DONE]")
	return nil
}

// Up connects to Tailscale (optionally with auth key and exit node).
// Uses a short timeout and reads stderr to capture the auth URL.
// POST /network/tailscale/up
func (h *TailscaleHandler) Up(c echo.Context) error {
	var req struct {
		AuthKey  string `json:"auth_key"`
		ExitNode string `json:"exit_node"`
	}
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}

	// If auth key is provided, use it directly (blocking is fine, it will complete quickly)
	if req.AuthKey != "" {
		args := []string{"up", "--authkey=" + req.AuthKey}
		if req.ExitNode != "" {
			args = append(args, "--exit-node="+req.ExitNode)
		}
		output, err := h.Cmd.Run("tailscale", args...)
		if err != nil {
			return response.Fail(c, http.StatusInternalServerError, response.ErrTSUpError,
				"Failed to connect: "+err.Error()+"\n"+output)
		}
		return response.OK(c, map[string]string{"message": "Tailscale connected"})
	}

	// Without auth key: `tailscale up` blocks waiting for browser auth.
	// Run it in background with a short timeout to capture the auth URL from stderr output.
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	args := []string{"up"}
	if req.ExitNode != "" {
		args = append(args, "--exit-node="+req.ExitNode)
	}

	// Streaming command — cannot use Commander (needs custom context timeout + auth URL extraction)
	cmd := osExec.CommandContext(ctx, "tailscale", args...)
	outputBytes, err := cmd.CombinedOutput()
	output := string(outputBytes)

	// Check if already connected (command succeeded)
	if err == nil {
		return response.OK(c, map[string]string{"message": "Tailscale connected"})
	}

	// Extract auth URL from output (tailscale up prints it to stderr)
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.Contains(line, "https://login.tailscale.com/") || strings.Contains(line, "https://") {
			// Extract just the URL
			parts := strings.Fields(line)
			for _, part := range parts {
				if strings.HasPrefix(part, "https://") {
					return response.OK(c, map[string]interface{}{
						"needs_auth": true,
						"auth_url":   part,
					})
				}
			}
		}
	}

	// Also check tailscale status for the auth URL
	tsStatus, statusErr := h.getTailscaleStatusJSON()
	if statusErr == nil && tsStatus.AuthURL != "" {
		return response.OK(c, map[string]interface{}{
			"needs_auth": true,
			"auth_url":   tsStatus.AuthURL,
		})
	}

	// If context was canceled (timeout) check status for auth URL
	if ctx.Err() == context.DeadlineExceeded {
		tsStatus, statusErr := h.getTailscaleStatusJSON()
		if statusErr == nil && tsStatus.AuthURL != "" {
			return response.OK(c, map[string]interface{}{
				"needs_auth": true,
				"auth_url":   tsStatus.AuthURL,
			})
		}
		// Timeout but no auth URL found — the command is still running in background
		return response.OK(c, map[string]interface{}{
			"needs_auth": true,
			"auth_url":   "",
			"message":    "Authentication required. Check Tailscale status for the auth URL.",
		})
	}

	return response.Fail(c, http.StatusInternalServerError, response.ErrTSUpError,
		"Failed to connect: "+err.Error()+"\n"+output)
}

// Down disconnects from Tailscale.
// POST /network/tailscale/down
func (h *TailscaleHandler) Down(c echo.Context) error {
	output, err := h.Cmd.Run("tailscale", "down")
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrTSDownError,
			"Failed to disconnect: "+err.Error()+"\n"+output)
	}

	return response.OK(c, map[string]string{"message": "Tailscale disconnected"})
}

// Logout logs out from Tailscale and deauthorizes the device.
// POST /network/tailscale/logout
func (h *TailscaleHandler) Logout(c echo.Context) error {
	output, err := h.Cmd.Run("tailscale", "logout")
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrTSLogoutError,
			"Failed to logout: "+err.Error()+"\n"+output)
	}

	return response.OK(c, map[string]string{"message": "Tailscale logged out"})
}

// ListPeers returns the list of peers in the tailnet.
// GET /network/tailscale/peers
func (h *TailscaleHandler) ListPeers(c echo.Context) error {
	tsStatus, err := h.getTailscaleStatusJSON()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrTSPeersError,
			"Failed to get peers: "+err.Error())
	}

	peers := make([]TailscalePeer, 0, len(tsStatus.Peer))
	for _, node := range tsStatus.Peer {
		peer := TailscalePeer{
			Hostname:       node.HostName,
			DNSName:        node.DNSName,
			OS:             node.OS,
			Online:         node.Online,
			LastSeen:       node.LastSeen,
			ExitNode:       node.ExitNode,
			ExitNodeOption: node.ExitNodeOption,
			RxBytes:        node.RxBytes,
			TxBytes:        node.TxBytes,
		}
		if len(node.TailscaleIPs) > 0 {
			peer.TailscaleIP = node.TailscaleIPs[0]
		}
		peers = append(peers, peer)
	}

	return response.OK(c, peers)
}

// SetPreferences updates Tailscale preferences (exit node, accept routes, advertise exit node).
// PUT /network/tailscale/preferences
func (h *TailscaleHandler) SetPreferences(c echo.Context) error {
	var req struct {
		ExitNode          *string `json:"exit_node"`           // nil=don't change, ""=clear, "ip"=set
		AcceptRoutes      *bool   `json:"accept_routes"`
		AdvertiseExitNode *bool   `json:"advertise_exit_node"`
	}
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}

	args := []string{"set"}

	if req.ExitNode != nil {
		args = append(args, "--exit-node="+*req.ExitNode)
	}

	if req.AcceptRoutes != nil {
		if *req.AcceptRoutes {
			args = append(args, "--accept-routes")
		} else {
			args = append(args, "--accept-routes=false")
		}
	}

	if req.AdvertiseExitNode != nil {
		if *req.AdvertiseExitNode {
			args = append(args, "--advertise-exit-node")
		} else {
			args = append(args, "--advertise-exit-node=false")
		}
	}

	if len(args) == 1 {
		return response.OK(c, map[string]string{"message": "No preferences to update"})
	}

	output, err := h.Cmd.Run("tailscale", args...)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrTSSetError,
			"Failed to update preferences: "+err.Error()+"\n"+output)
	}

	return response.OK(c, map[string]string{"message": "Preferences updated"})
}

// CheckUpdate checks if a newer version of Tailscale is available.
// GET /network/tailscale/update-check
func (h *TailscaleHandler) CheckUpdate(c echo.Context) error {
	// Get current version
	currentVersion := ""
	versionOutput, err := h.Cmd.Run("tailscale", "version")
	if err == nil {
		lines := strings.Split(strings.TrimSpace(versionOutput), "\n")
		if len(lines) > 0 {
			currentVersion = strings.TrimSpace(lines[0])
		}
	}

	// Check for update using `apt list --upgradable` and filter in Go
	aptOutput, _ := h.Cmd.Run("apt", "list", "--upgradable")
	output := ""
	for _, line := range strings.Split(aptOutput, "\n") {
		if strings.Contains(line, "tailscale") {
			output = strings.TrimSpace(line)
			break
		}
	}

	updateAvailable := output != ""

	// Parse the new version from apt output (format: tailscale/unknown 1.xx.x amd64 [upgradable from: 1.xx.x])
	newVersion := ""
	if updateAvailable {
		parts := strings.Fields(output)
		if len(parts) >= 2 {
			// parts[1] is the version
			newVersion = parts[1]
		}
	}

	return response.OK(c, map[string]interface{}{
		"current_version":  currentVersion,
		"update_available": updateAvailable,
		"new_version":      newVersion,
		"apt_output":       output,
	})
}

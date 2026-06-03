package network

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
)

// wgKeyRe matches a base64-encoded 32-byte WireGuard key (43 chars + '=').
var wgKeyRe = regexp.MustCompile(`^[A-Za-z0-9+/]{43}=$`)

// GenerateKeypair returns a fresh WireGuard private/public key pair for a new
// peer. The private key is for the CLIENT device and is never stored
// server-side — it's handed to the operator once to drop into the client
// config (standard WireGuard onboarding). Admin-only over the authenticated
// session, same trust level as every other root operation here.
// POST /network/wireguard/keypair
func (h *WireGuardHandler) GenerateKeypair(c echo.Context) error {
	priv, err := h.Cmd.Run("wg", "genkey")
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError,
			"wg genkey failed: "+response.SanitizeOutput(err.Error()))
	}
	priv = strings.TrimSpace(priv)

	pub, err := h.Cmd.RunWithInput(priv, "wg", "pubkey")
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError,
			"wg pubkey failed: "+response.SanitizeOutput(err.Error()))
	}
	return response.OK(c, map[string]string{
		"private_key": priv,
		"public_key":  strings.TrimSpace(pub),
	})
}

// AddPeerRequest is the body for AddPeer.
type AddPeerRequest struct {
	PublicKey           string   `json:"public_key"`
	PresharedKey        string   `json:"preshared_key"`
	AllowedIPs          []string `json:"allowed_ips"`
	Endpoint            string   `json:"endpoint"`
	PersistentKeepalive int      `json:"persistent_keepalive"`
}

// AddPeer appends a [Peer] block to an interface config and, if the interface
// is up, applies it live with `wg set` so the operator doesn't have to bounce
// the tunnel. The config file is the source of truth (survives reboot); the
// live apply is best-effort.
// POST /network/wireguard/configs/:name/peers
func (h *WireGuardHandler) AddPeer(c echo.Context) error {
	name := c.Param("name")
	if !validWGName.MatchString(name) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidName, "Invalid config name")
	}

	var req AddPeerRequest
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}
	req.PublicKey = strings.TrimSpace(req.PublicKey)
	req.PresharedKey = strings.TrimSpace(req.PresharedKey)
	req.Endpoint = strings.TrimSpace(req.Endpoint)

	if !wgKeyRe.MatchString(req.PublicKey) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidValue, "invalid public_key")
	}
	if req.PresharedKey != "" && !wgKeyRe.MatchString(req.PresharedKey) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidValue, "invalid preshared_key")
	}
	if len(req.AllowedIPs) == 0 {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidValue, "at least one allowed_ip (CIDR) is required")
	}
	for _, cidr := range req.AllowedIPs {
		if _, _, err := net.ParseCIDR(strings.TrimSpace(cidr)); err != nil {
			return response.Fail(c, http.StatusBadRequest, response.ErrInvalidValue, fmt.Sprintf("invalid allowed_ip %q (want CIDR like 10.0.0.2/32)", cidr))
		}
	}
	if req.Endpoint != "" && !validWGEndpoint(req.Endpoint) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidValue, "invalid endpoint (want host:port)")
	}
	if req.PersistentKeepalive < 0 || req.PersistentKeepalive > 65535 {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidValue, "invalid persistent_keepalive")
	}

	confPath := filepath.Join(wgConfigDir, name+".conf")
	data, err := os.ReadFile(confPath)
	if err != nil {
		if os.IsNotExist(err) {
			return response.Fail(c, http.StatusNotFound, response.ErrNotFound, "config not found")
		}
		return response.Fail(c, http.StatusInternalServerError, response.ErrReadError, "failed to read config")
	}

	// Reject a duplicate peer rather than silently creating a second block with
	// the same key (wg-quick up would then error on the dup).
	if _, exists := removePeerFromConfig(string(data), req.PublicKey); exists {
		return response.Fail(c, http.StatusConflict, response.ErrAlreadyExists, "a peer with this public_key already exists")
	}

	updated := appendPeerBlock(string(data), req)
	if err := atomicWriteFile(confPath, []byte(updated), 0600); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrWriteError, "failed to write config")
	}

	// Best-effort live apply (only meaningful when the interface is up).
	h.applyPeerLive(name, req)

	return response.OK(c, map[string]string{"message": "peer added", "public_key": req.PublicKey})
}

// RemovePeer deletes the [Peer] block matching the public key and, if the
// interface is up, removes it live. The key comes via ?public_key= rather than
// a path segment because WireGuard keys are standard base64 (they contain '/'
// and '+'), which would break path routing.
// DELETE /network/wireguard/configs/:name/peers?public_key=<key>
func (h *WireGuardHandler) RemovePeer(c echo.Context) error {
	name := c.Param("name")
	if !validWGName.MatchString(name) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidName, "Invalid config name")
	}
	pubKey := strings.TrimSpace(c.QueryParam("public_key"))
	if !wgKeyRe.MatchString(pubKey) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidValue, "invalid peer public key")
	}

	confPath := filepath.Join(wgConfigDir, name+".conf")
	data, err := os.ReadFile(confPath)
	if err != nil {
		if os.IsNotExist(err) {
			return response.Fail(c, http.StatusNotFound, response.ErrNotFound, "config not found")
		}
		return response.Fail(c, http.StatusInternalServerError, response.ErrReadError, "failed to read config")
	}

	updated, removed := removePeerFromConfig(string(data), pubKey)
	if !removed {
		return response.Fail(c, http.StatusNotFound, response.ErrNotFound, "peer not found in config")
	}
	if err := atomicWriteFile(confPath, []byte(updated), 0600); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrWriteError, "failed to write config")
	}

	// Best-effort live removal.
	_, _ = h.Cmd.Run("wg", "set", name, "peer", pubKey, "remove")

	return response.OK(c, map[string]string{"message": "peer removed", "public_key": pubKey})
}

// applyPeerLive pushes a newly-added peer into the running interface. Failures
// (interface down, wg missing) are non-fatal — the config file already holds
// the change for the next `wg-quick up`.
func (h *WireGuardHandler) applyPeerLive(name string, req AddPeerRequest) {
	args := []string{"set", name, "peer", req.PublicKey, "allowed-ips", strings.Join(req.AllowedIPs, ",")}
	if req.Endpoint != "" {
		args = append(args, "endpoint", req.Endpoint)
	}
	if req.PersistentKeepalive > 0 {
		args = append(args, "persistent-keepalive", strconv.Itoa(req.PersistentKeepalive))
	}
	// Note: preshared-key needs a file/fd for `wg set`; we skip live PSK apply
	// and rely on the config file (a tunnel bounce activates it). The peer is
	// still reachable immediately for non-PSK setups.
	if _, err := h.Cmd.Run("wg", args...); err != nil {
		// Down interface is the common, expected case — debug, not warn.
		return
	}
}

// validWGEndpoint accepts host:port where host is an IP or hostname and port is 1-65535.
func validWGEndpoint(ep string) bool {
	host, portStr, err := net.SplitHostPort(ep)
	if err != nil || host == "" {
		return false
	}
	p, err := strconv.Atoi(portStr)
	return err == nil && p >= 1 && p <= 65535
}

// appendPeerBlock returns content with a new [Peer] block appended.
func appendPeerBlock(content string, req AddPeerRequest) string {
	var b strings.Builder
	b.WriteString(strings.TrimRight(content, "\n"))
	b.WriteString("\n\n[Peer]\n")
	b.WriteString("PublicKey = " + req.PublicKey + "\n")
	if req.PresharedKey != "" {
		b.WriteString("PresharedKey = " + req.PresharedKey + "\n")
	}
	b.WriteString("AllowedIPs = " + strings.Join(req.AllowedIPs, ", ") + "\n")
	if req.Endpoint != "" {
		b.WriteString("Endpoint = " + req.Endpoint + "\n")
	}
	if req.PersistentKeepalive > 0 {
		b.WriteString("PersistentKeepalive = " + strconv.Itoa(req.PersistentKeepalive) + "\n")
	}
	return b.String()
}

// removePeerFromConfig returns content with the [Peer] block whose PublicKey
// matches pubKey removed, and whether a block was removed. Section boundaries
// are any `[...]` header line; the matched block (and a trailing blank line) is
// dropped while [Interface] and other peers are preserved verbatim.
func removePeerFromConfig(content, pubKey string) (string, bool) {
	lines := strings.Split(content, "\n")
	out := make([]string, 0, len(lines))
	removed := false

	i := 0
	for i < len(lines) {
		if strings.EqualFold(strings.TrimSpace(lines[i]), "[Peer]") {
			block := []string{lines[i]}
			j := i + 1
			for j < len(lines) {
				t := strings.TrimSpace(lines[j])
				if strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]") {
					break
				}
				block = append(block, lines[j])
				j++
			}

			match := false
			for _, bl := range block {
				bt := strings.TrimSpace(bl)
				if len(bt) > 9 && strings.EqualFold(bt[:9], "publickey") {
					if eq := strings.IndexByte(bt, '='); eq >= 0 {
						if strings.TrimSpace(bt[eq+1:]) == pubKey {
							match = true
							break
						}
					}
				}
			}

			if match {
				removed = true
				// Drop a blank separator line we'd otherwise leave dangling.
				for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
					out = out[:len(out)-1]
				}
				i = j
				continue
			}
			out = append(out, block...)
			i = j
			continue
		}
		out = append(out, lines[i])
		i++
	}

	result := strings.Join(out, "\n")
	if !strings.HasSuffix(result, "\n") {
		result += "\n"
	}
	return result, removed
}

// SetAutostart enables or disables wg-quick@<name> at boot.
// POST /network/wireguard/configs/:name/autostart  body: { enabled: bool }
func (h *WireGuardHandler) SetAutostart(c echo.Context) error {
	name := c.Param("name")
	if !validWGName.MatchString(name) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidName, "Invalid config name")
	}
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}
	verb := "disable"
	if req.Enabled {
		verb = "enable"
	}
	unit := "wg-quick@" + name
	if out, err := h.Cmd.Run("systemctl", verb, unit); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError,
			fmt.Sprintf("systemctl %s %s failed: %s", verb, unit, response.SanitizeOutput(out)))
	}
	return response.OK(c, map[string]interface{}{"message": "autostart updated", "enabled": req.Enabled})
}

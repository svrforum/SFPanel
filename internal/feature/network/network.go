package network

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/common/exec"
	"gopkg.in/yaml.v3"
)

// validInterfaceName matches safe Linux network interface names.
var validInterfaceName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,14}$`)

// Handler exposes REST handlers for host network management
// (interfaces, DNS, routes, bonding, netplan configuration).
type Handler struct {
	Cmd exec.Commander
}

// ---------- Types ----------

// NetworkInterface represents a host network interface with its state and statistics.
type NetworkInterface struct {
	Name       string    `json:"name"`
	Type       string    `json:"type"`        // ethernet, loopback, bond, bridge, vlan, virtual
	State      string    `json:"state"`       // up, down
	MacAddress string    `json:"mac_address"`
	MTU        int       `json:"mtu"`
	Speed      int       `json:"speed"`       // Mbps (-1 if unavailable)
	Addresses  []IPAddr  `json:"addresses"`
	IsDefault  bool      `json:"is_default"`
	Driver     string    `json:"driver"`
	TxBytes    uint64    `json:"tx_bytes"`
	RxBytes    uint64    `json:"rx_bytes"`
	TxPackets  uint64    `json:"tx_packets"`
	RxPackets  uint64    `json:"rx_packets"`
	TxErrors   uint64    `json:"tx_errors"`
	RxErrors   uint64    `json:"rx_errors"`
	BondInfo   *BondInfo `json:"bond_info,omitempty"`
}

// IPAddr represents a single IP address assigned to an interface.
type IPAddr struct {
	Address string `json:"address"`
	Prefix  int    `json:"prefix"`
	Family  string `json:"family"` // ipv4, ipv6
}

// BondInfo holds bonding-specific information for a bond interface.
type BondInfo struct {
	Mode    string   `json:"mode"`
	Slaves  []string `json:"slaves"`
	Primary string   `json:"primary"`
}

// InterfaceDetail extends NetworkInterface with the current netplan configuration.
type InterfaceDetail struct {
	NetworkInterface
	Config *InterfaceConfig `json:"config"`
}

// InterfaceConfig describes the netplan-level configuration for an interface.
type InterfaceConfig struct {
	DHCP4     bool     `json:"dhcp4"`
	DHCP6     bool     `json:"dhcp6"`
	Addresses []string `json:"addresses"`
	Gateway4  string   `json:"gateway4"`
	Gateway6  string   `json:"gateway6"`
	DNS       []string `json:"dns"`
}

// ConfigureInterfaceRequest is the payload for PUT /network/interfaces/:name.
type ConfigureInterfaceRequest struct {
	DHCP4     *bool    `json:"dhcp4"`
	DHCP6     *bool    `json:"dhcp6"`
	Addresses []string `json:"addresses"`
	Gateway4  string   `json:"gateway4"`
	Gateway6  string   `json:"gateway6"`
	DNS       []string `json:"dns"`
	MTU       *int     `json:"mtu"`
}

// DNSConfig holds the system-wide DNS configuration.
type DNSConfig struct {
	Servers []string `json:"servers"`
	Search  []string `json:"search"`
}

// Route represents a single entry from the kernel routing table.
type Route struct {
	Destination string `json:"destination"`
	Gateway     string `json:"gateway"`
	Interface   string `json:"interface"`
	Metric      int    `json:"metric"`
	Protocol    string `json:"protocol"`
	Scope       string `json:"scope"`
}

// CreateBondRequest is the payload for POST /network/bonds.
type CreateBondRequest struct {
	Name    string   `json:"name"`
	Mode    string   `json:"mode"`
	Slaves  []string `json:"slaves"`
	Primary string   `json:"primary"`
}

// ---------- Cache ----------

// networkStatusCache caches the combined network status to avoid spawning
// multiple subprocesses (ip route, resolvectl, ethtool) on every request.
var networkStatusCache struct {
	sync.RWMutex
	interfaces []NetworkInterface
	routes     []Route
	dns        DNSConfig
	bonds      []NetworkInterface
	updatedAt  time.Time
}

const networkCacheTTL = 3 * time.Second

// cachedNetworkStatus returns cached network data, refreshing when stale.
func (h *Handler) cachedNetworkStatus() ([]NetworkInterface, []Route, DNSConfig, []NetworkInterface, error) {
	networkStatusCache.RLock()
	if time.Since(networkStatusCache.updatedAt) < networkCacheTTL && networkStatusCache.interfaces != nil {
		ifaces := make([]NetworkInterface, len(networkStatusCache.interfaces))
		copy(ifaces, networkStatusCache.interfaces)
		routes := make([]Route, len(networkStatusCache.routes))
		copy(routes, networkStatusCache.routes)
		dns := networkStatusCache.dns
		bonds := make([]NetworkInterface, len(networkStatusCache.bonds))
		copy(bonds, networkStatusCache.bonds)
		networkStatusCache.RUnlock()
		return ifaces, routes, dns, bonds, nil
	}
	networkStatusCache.RUnlock()

	networkStatusCache.Lock()
	defer networkStatusCache.Unlock()

	// Double-check after acquiring write lock
	if time.Since(networkStatusCache.updatedAt) < networkCacheTTL && networkStatusCache.interfaces != nil {
		ifaces := make([]NetworkInterface, len(networkStatusCache.interfaces))
		copy(ifaces, networkStatusCache.interfaces)
		routes := make([]Route, len(networkStatusCache.routes))
		copy(routes, networkStatusCache.routes)
		dns := networkStatusCache.dns
		bonds := make([]NetworkInterface, len(networkStatusCache.bonds))
		copy(bonds, networkStatusCache.bonds)
		return ifaces, routes, dns, bonds, nil
	}

	// Gather fresh data concurrently
	var ifaces []NetworkInterface
	var routeList []Route
	var ifaceErr, routeErr error
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		ifaces, ifaceErr = h.gatherInterfaces()
	}()
	go func() {
		defer wg.Done()
		routeList, routeErr = h.parseRoutes()
	}()

	dns := h.readDNSConfig()
	wg.Wait()

	if ifaceErr != nil {
		return nil, nil, DNSConfig{}, nil, ifaceErr
	}
	if routeErr != nil {
		return nil, nil, DNSConfig{}, nil, routeErr
	}

	// Sort interfaces: loopback last, then alphabetical
	sort.Slice(ifaces, func(i, j int) bool {
		if ifaces[i].Type == "loopback" && ifaces[j].Type != "loopback" {
			return false
		}
		if ifaces[i].Type != "loopback" && ifaces[j].Type == "loopback" {
			return true
		}
		return ifaces[i].Name < ifaces[j].Name
	})

	// Extract bonds
	bonds := make([]NetworkInterface, 0)
	for _, iface := range ifaces {
		if iface.Type == "bond" {
			bonds = append(bonds, iface)
		}
	}

	networkStatusCache.interfaces = ifaces
	networkStatusCache.routes = routeList
	networkStatusCache.dns = dns
	networkStatusCache.bonds = bonds
	networkStatusCache.updatedAt = time.Now()

	// Return copies
	ifacesCopy := make([]NetworkInterface, len(ifaces))
	copy(ifacesCopy, ifaces)
	routesCopy := make([]Route, len(routeList))
	copy(routesCopy, routeList)
	bondsCopy := make([]NetworkInterface, len(bonds))
	copy(bondsCopy, bonds)
	return ifacesCopy, routesCopy, dns, bondsCopy, nil
}

// invalidateNetworkCache forces the next request to re-fetch.
func invalidateNetworkCache() {
	networkStatusCache.Lock()
	networkStatusCache.updatedAt = time.Time{}
	networkStatusCache.Unlock()
}

// ---------- Handlers ----------

// ListInterfaces returns all host network interfaces with statistics.
func (h *Handler) ListInterfaces(c echo.Context) error {
	ifaces, _, _, _, err := h.cachedNetworkStatus()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrNetworkError, fmt.Sprintf("failed to list interfaces: %v", err))
	}
	return response.OK(c, ifaces)
}

// GetNetworkStatus returns a combined snapshot of interfaces, routes, DNS and bonds
// in a single API call to reduce frontend round-trips.
func (h *Handler) GetNetworkStatus(c echo.Context) error {
	ifaces, routes, dns, bonds, err := h.cachedNetworkStatus()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrNetworkError, fmt.Sprintf("failed to gather network status: %v", err))
	}

	return response.OK(c, map[string]interface{}{
		"interfaces": ifaces,
		"routes":     routes,
		"dns":        dns,
		"bonds":      bonds,
	})
}

// GetInterface returns detailed information for a single interface including netplan config.
func (h *Handler) GetInterface(c echo.Context) error {
	name := c.Param("name")
	if !validInterfaceName.MatchString(name) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidName, "Invalid interface name")
	}

	ifaces, _, _, _, err := h.cachedNetworkStatus()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrNetworkError, fmt.Sprintf("failed to get interfaces: %v", err))
	}

	var found *NetworkInterface
	for i := range ifaces {
		if ifaces[i].Name == name {
			found = &ifaces[i]
			break
		}
	}
	if found == nil {
		return response.Fail(c, http.StatusNotFound, response.ErrNotFound, fmt.Sprintf("interface %s not found", name))
	}

	cfg := readNetplanConfigForInterface(name)

	detail := InterfaceDetail{
		NetworkInterface: *found,
		Config:           cfg,
	}
	return response.OK(c, detail)
}

// ConfigureInterface modifies the netplan configuration for a given interface.
func (h *Handler) ConfigureInterface(c echo.Context) error {
	name := c.Param("name")
	if !validInterfaceName.MatchString(name) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidName, "Invalid interface name")
	}

	var req ConfigureInterfaceRequest
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}

	// Validate addresses (must be valid CIDR)
	for _, addr := range req.Addresses {
		if _, _, err := net.ParseCIDR(addr); err != nil {
			return response.Fail(c, http.StatusBadRequest, response.ErrInvalidValue,
				fmt.Sprintf("Invalid address %q: must be valid CIDR notation", addr))
		}
	}

	// Validate gateway4 (must be valid IPv4)
	if req.Gateway4 != "" {
		ip := net.ParseIP(req.Gateway4)
		if ip == nil || ip.To4() == nil {
			return response.Fail(c, http.StatusBadRequest, response.ErrInvalidValue,
				fmt.Sprintf("Invalid gateway4 %q: must be a valid IPv4 address", req.Gateway4))
		}
	}

	// Validate gateway6 (must be valid IPv6, including IPv4-mapped like ::ffff:x.x.x.x)
	if req.Gateway6 != "" {
		if net.ParseIP(req.Gateway6) == nil || !strings.Contains(req.Gateway6, ":") {
			return response.Fail(c, http.StatusBadRequest, response.ErrInvalidValue,
				fmt.Sprintf("Invalid gateway6 %q: must be a valid IPv6 address", req.Gateway6))
		}
	}

	// Validate DNS entries
	for _, dns := range req.DNS {
		if net.ParseIP(dns) == nil {
			return response.Fail(c, http.StatusBadRequest, response.ErrInvalidValue,
				fmt.Sprintf("Invalid DNS server %q: must be a valid IP address", dns))
		}
	}

	// Validate MTU range (576 minimum, 9216 max for jumbo frames)
	if req.MTU != nil {
		if *req.MTU < 576 || *req.MTU > 9216 {
			return response.Fail(c, http.StatusBadRequest, response.ErrInvalidValue,
				fmt.Sprintf("MTU %d is out of valid range [576-9216]", *req.MTU))
		}
	}

	if err := updateNetplanInterface(name, &req); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrNetplanError, fmt.Sprintf("failed to update netplan config: %v", err))
	}

	invalidateNetworkCache()
	return response.OK(c, map[string]string{"message": fmt.Sprintf("interface %s configuration updated", name)})
}

// ApplyNetplan runs `netplan apply` to activate the current configuration.
func (h *Handler) ApplyNetplan(c echo.Context) error {
	out, err := h.Cmd.Run("netplan", "apply")
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrNetplanError, fmt.Sprintf("netplan apply failed: %s", strings.TrimSpace(out)))
	}
	invalidateNetworkCache()
	return response.OK(c, map[string]string{"message": "netplan applied successfully"})
}

// GetDNS returns the current system DNS configuration.
func (h *Handler) GetDNS(c echo.Context) error {
	_, _, dns, _, err := h.cachedNetworkStatus()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrNetworkError, fmt.Sprintf("failed to read DNS: %v", err))
	}
	return response.OK(c, dns)
}

// ConfigureDNS updates DNS servers via netplan configuration.
// PUT /network/dns  body: { servers: ["8.8.8.8", "1.1.1.1"] }
func (h *Handler) ConfigureDNS(c echo.Context) error {
	var req struct {
		Servers []string `json:"servers"`
	}
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}

	// Validate each server is a valid IP
	for _, s := range req.Servers {
		if net.ParseIP(s) == nil {
			return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, fmt.Sprintf("Invalid DNS server: %s", s))
		}
	}

	if err := updateNetplanDNS(req.Servers); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrNetplanError, fmt.Sprintf("failed to update DNS: %v", err))
	}

	invalidateNetworkCache()
	return response.OK(c, map[string]string{"message": "DNS configuration updated"})
}

// GetRoutes returns the kernel routing table.
func (h *Handler) GetRoutes(c echo.Context) error {
	_, routes, _, _, err := h.cachedNetworkStatus()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrNetworkError, fmt.Sprintf("failed to read routes: %v", err))
	}
	return response.OK(c, routes)
}

// ListBonds returns only bond interfaces.
func (h *Handler) ListBonds(c echo.Context) error {
	_, _, _, bonds, err := h.cachedNetworkStatus()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrNetworkError, fmt.Sprintf("failed to list bonds: %v", err))
	}
	return response.OK(c, bonds)
}

// CreateBond adds a bond configuration to netplan.
func (h *Handler) CreateBond(c echo.Context) error {
	var req CreateBondRequest
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}
	if req.Name == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrMissingFields, "Bond name is required")
	}
	if len(req.Slaves) == 0 {
		return response.Fail(c, http.StatusBadRequest, response.ErrMissingFields, "At least one slave interface is required")
	}

	// Validate slave interface names
	for _, slave := range req.Slaves {
		if !isValidInterfaceName(slave) {
			return response.Fail(c, http.StatusBadRequest, response.ErrInvalidName,
				fmt.Sprintf("Invalid slave interface name: %s", slave))
		}
	}

	if req.Mode == "" {
		req.Mode = "active-backup"
	}

	// Validate bond mode against allowlist
	validBondModes := map[string]bool{
		"balance-rr":    true,
		"active-backup": true,
		"balance-xor":   true,
		"broadcast":     true,
		"802.3ad":       true,
		"balance-tlb":   true,
		"balance-alb":   true,
	}
	if !validBondModes[req.Mode] {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidValue,
			fmt.Sprintf("Invalid bond mode %q: must be one of balance-rr, active-backup, balance-xor, broadcast, 802.3ad, balance-tlb, balance-alb", req.Mode))
	}

	// Validate primary interface name if set
	if req.Primary != "" && !isValidInterfaceName(req.Primary) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidName,
			fmt.Sprintf("Invalid primary interface name: %s", req.Primary))
	}

	if err := createNetplanBond(&req); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrNetplanError, fmt.Sprintf("failed to create bond: %v", err))
	}
	invalidateNetworkCache()
	return response.OK(c, map[string]string{"message": fmt.Sprintf("bond %s created in netplan config", req.Name)})
}

// DeleteBond removes a bond from the netplan configuration.
func (h *Handler) DeleteBond(c echo.Context) error {
	name := c.Param("name")
	if !validInterfaceName.MatchString(name) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidName, "Invalid bond name")
	}

	if err := deleteNetplanBond(name); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrNetplanError, fmt.Sprintf("failed to delete bond: %v", err))
	}
	invalidateNetworkCache()
	return response.OK(c, map[string]string{"message": fmt.Sprintf("bond %s removed from netplan config", name)})
}

// ---------- Internal helpers ----------

// gatherInterfaces collects information about every network interface on the host.
func (h *Handler) gatherInterfaces() ([]NetworkInterface, error) {
	goIfaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("net.Interfaces: %w", err)
	}

	defaultIface := h.detectDefaultInterface()

	result := make([]NetworkInterface, 0, len(goIfaces))
	for _, iface := range goIfaces {
		ni := NetworkInterface{
			Name:       iface.Name,
			MacAddress: iface.HardwareAddr.String(),
			MTU:        iface.MTU,
			Speed:      -1,
			Addresses:  []IPAddr{},
		}

		// State
		if iface.Flags&net.FlagUp != 0 {
			ni.State = "up"
		} else {
			ni.State = "down"
		}

		// Default gateway interface
		ni.IsDefault = (iface.Name == defaultIface)

		// Type detection
		ni.Type = detectInterfaceType(iface.Name)

		// IP addresses
		addrs, err := iface.Addrs()
		if err == nil {
			for _, addr := range addrs {
				ipNet, ok := addr.(*net.IPNet)
				if !ok {
					// Try parsing as *net.IPAddr
					raw := addr.String()
					ip, ipn, parseErr := net.ParseCIDR(raw)
					if parseErr != nil {
						continue
					}
					prefix, _ := ipn.Mask.Size()
					family := "ipv4"
					if ip.To4() == nil {
						family = "ipv6"
					}
					ni.Addresses = append(ni.Addresses, IPAddr{
						Address: ip.String(),
						Prefix:  prefix,
						Family:  family,
					})
					continue
				}
				prefix, _ := ipNet.Mask.Size()
				family := "ipv4"
				if ipNet.IP.To4() == nil {
					family = "ipv6"
				}
				ni.Addresses = append(ni.Addresses, IPAddr{
					Address: ipNet.IP.String(),
					Prefix:  prefix,
					Family:  family,
				})
			}
		}

		// Speed (only meaningful for physical interfaces)
		ni.Speed = readInterfaceSpeed(iface.Name)

		// Driver
		ni.Driver = h.readInterfaceDriver(iface.Name)

		// Statistics
		ni.TxBytes = readSysStat(iface.Name, "tx_bytes")
		ni.RxBytes = readSysStat(iface.Name, "rx_bytes")
		ni.TxPackets = readSysStat(iface.Name, "tx_packets")
		ni.RxPackets = readSysStat(iface.Name, "rx_packets")
		ni.TxErrors = readSysStat(iface.Name, "tx_errors")
		ni.RxErrors = readSysStat(iface.Name, "rx_errors")

		// Bond info
		if ni.Type == "bond" {
			ni.BondInfo = readBondInfo(iface.Name)
		}

		result = append(result, ni)
	}

	return result, nil
}

// detectInterfaceType determines the type of a network interface by inspecting sysfs.
func detectInterfaceType(name string) string {
	if name == "lo" {
		return "loopback"
	}

	base := filepath.Join("/sys/class/net", name)

	// Bond: /sys/class/net/{name}/bonding exists
	if dirExists(filepath.Join(base, "bonding")) {
		return "bond"
	}

	// Bridge: /sys/class/net/{name}/bridge exists
	if dirExists(filepath.Join(base, "bridge")) {
		return "bridge"
	}

	// VLAN: check for /proc/net/vlan/{name} or name contains '.'
	if fileExists(filepath.Join("/proc/net/vlan", name)) || strings.Contains(name, ".") {
		return "vlan"
	}

	// Check /sys/class/net/{name}/type — 1 means ethernet (ARPHRD_ETHER)
	typeVal := readSysFile(filepath.Join(base, "type"))
	if typeVal == "1" {
		// Physical ethernet has a device symlink
		if _, err := os.Stat(filepath.Join(base, "device")); err == nil {
			return "ethernet"
		}
		// ARPHRD_ETHER but no device → virtual (veth, tap, etc.)
		return "virtual"
	}

	return "virtual"
}

// detectDefaultInterface returns the name of the interface used for the default route.
func (h *Handler) detectDefaultInterface() string {
	out, err := h.Cmd.Run("ip", "route", "show", "default")
	if err != nil {
		return ""
	}
	// Expected: "default via 192.168.1.1 dev eth0 ..."
	fields := strings.Fields(out)
	for i, f := range fields {
		if f == "dev" && i+1 < len(fields) {
			return fields[i+1]
		}
	}
	return ""
}

// readInterfaceSpeed reads the link speed in Mbps from sysfs. Returns -1 on failure.
func readInterfaceSpeed(name string) int {
	path := filepath.Join("/sys/class/net", name, "speed")
	data, err := os.ReadFile(path)
	if err != nil {
		return -1
	}
	val, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || val < 0 {
		return -1
	}
	return val
}

// readInterfaceDriver tries to determine the driver for an interface.
func (h *Handler) readInterfaceDriver(name string) string {
	// Try the sysfs device/driver symlink first
	link, err := os.Readlink(filepath.Join("/sys/class/net", name, "device", "driver"))
	if err == nil {
		return filepath.Base(link)
	}

	// Fallback: ethtool -i
	out, err := h.Cmd.Run("ethtool", "-i", name)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "driver:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "driver:"))
		}
	}
	return ""
}

// readSysStat reads a numeric statistic from /sys/class/net/{name}/statistics/{stat}.
func readSysStat(name, stat string) uint64 {
	path := filepath.Join("/sys/class/net", name, "statistics", stat)
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	val, err := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return 0
	}
	return val
}

// readBondInfo reads bonding details from sysfs.
func readBondInfo(name string) *BondInfo {
	base := filepath.Join("/sys/class/net", name, "bonding")
	info := &BondInfo{}

	// Mode: "balance-rr 0" → take first word
	modeRaw := readSysFile(filepath.Join(base, "mode"))
	parts := strings.Fields(modeRaw)
	if len(parts) > 0 {
		info.Mode = parts[0]
	}

	// Slaves
	slavesRaw := readSysFile(filepath.Join(base, "slaves"))
	if slavesRaw != "" {
		info.Slaves = strings.Fields(slavesRaw)
	} else {
		info.Slaves = []string{}
	}

	// Primary
	info.Primary = readSysFile(filepath.Join(base, "primary"))

	return info
}

// ---------- DNS ----------

// readDNSConfig reads DNS configuration from resolvectl or /etc/resolv.conf.
func (h *Handler) readDNSConfig() DNSConfig {
	cfg := DNSConfig{
		Servers: []string{},
		Search:  []string{},
	}

	// Try resolvectl first (systemd-resolved)
	out, err := h.Cmd.Run("resolvectl", "status")
	if err == nil {
		cfg = parseResolvectlOutput(out)
		if len(cfg.Servers) > 0 {
			return cfg
		}
	}

	// Fallback to /etc/resolv.conf
	data, err := os.ReadFile("/etc/resolv.conf")
	if err != nil {
		return cfg
	}

	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "#") || line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		switch fields[0] {
		case "nameserver":
			cfg.Servers = append(cfg.Servers, fields[1])
		case "search", "domain":
			cfg.Search = append(cfg.Search, fields[1:]...)
		}
	}
	return cfg
}

// parseResolvectlOutput extracts DNS servers and search domains from resolvectl status output.
func parseResolvectlOutput(output string) DNSConfig {
	cfg := DNSConfig{
		Servers: []string{},
		Search:  []string{},
	}

	lines := strings.Split(output, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// "DNS Servers: 8.8.8.8" or "DNS Servers: 8.8.8.8 1.1.1.1"
		if strings.HasPrefix(trimmed, "DNS Servers:") {
			rest := strings.TrimPrefix(trimmed, "DNS Servers:")
			for _, s := range strings.Fields(rest) {
				if net.ParseIP(s) != nil {
					cfg.Servers = append(cfg.Servers, s)
				}
			}
		}
		// "DNS Domain: example.com"
		if strings.HasPrefix(trimmed, "DNS Domain:") {
			rest := strings.TrimPrefix(trimmed, "DNS Domain:")
			cfg.Search = append(cfg.Search, strings.Fields(rest)...)
		}
	}
	return cfg
}

// ---------- Routes ----------

// parseRoutes parses the output of `ip route show`.
func (h *Handler) parseRoutes() ([]Route, error) {
	out, err := h.Cmd.Run("ip", "route", "show")
	if err != nil {
		return nil, fmt.Errorf("ip route show: %w", err)
	}

	routes := []Route{}
	scanner := bufio.NewScanner(strings.NewReader(out))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		r := parseRouteLine(line)
		routes = append(routes, r)
	}
	return routes, nil
}

// parseRouteLine parses a single line from `ip route show`.
// Example lines:
//
//	default via 192.168.1.1 dev eth0 proto dhcp metric 100
//	192.168.1.0/24 dev eth0 proto kernel scope link src 192.168.1.100 metric 100
func parseRouteLine(line string) Route {
	fields := strings.Fields(line)
	r := Route{}
	if len(fields) == 0 {
		return r
	}

	r.Destination = fields[0]

	for i := 1; i < len(fields); i++ {
		switch fields[i] {
		case "via":
			if i+1 < len(fields) {
				r.Gateway = fields[i+1]
				i++
			}
		case "dev":
			if i+1 < len(fields) {
				r.Interface = fields[i+1]
				i++
			}
		case "proto":
			if i+1 < len(fields) {
				r.Protocol = fields[i+1]
				i++
			}
		case "scope":
			if i+1 < len(fields) {
				r.Scope = fields[i+1]
				i++
			}
		case "metric":
			if i+1 < len(fields) {
				val, err := strconv.Atoi(fields[i+1])
				if err == nil {
					r.Metric = val
				}
				i++
			}
		}
	}
	return r
}

// ---------- Netplan ----------

// netplanData represents the top-level netplan YAML structure.
type netplanData struct {
	Network *netplanNetwork `yaml:"network"`
}

type netplanNetwork struct {
	Version   int                           `yaml:"version,omitempty"`
	Renderer  string                        `yaml:"renderer,omitempty"`
	Ethernets map[string]*netplanEthernet   `yaml:"ethernets,omitempty"`
	Bonds     map[string]*netplanBond       `yaml:"bonds,omitempty"`
	Bridges   map[string]*netplanBridge     `yaml:"bridges,omitempty"`
	Vlans     map[string]*netplanVlan       `yaml:"vlans,omitempty"`
}

type netplanEthernet struct {
	DHCP4       *bool                  `yaml:"dhcp4,omitempty"`
	DHCP6       *bool                  `yaml:"dhcp6,omitempty"`
	Addresses   []string               `yaml:"addresses,omitempty"`
	Gateway4    string                 `yaml:"gateway4,omitempty"`
	Gateway6    string                 `yaml:"gateway6,omitempty"`
	Nameservers *netplanNameservers    `yaml:"nameservers,omitempty"`
	MTU         *int                   `yaml:"mtu,omitempty"`
	Routes      []map[string]string    `yaml:"routes,omitempty"`
	Extra       map[string]interface{} `yaml:",inline"`
}

type netplanBond struct {
	DHCP4       *bool                  `yaml:"dhcp4,omitempty"`
	DHCP6       *bool                  `yaml:"dhcp6,omitempty"`
	Addresses   []string               `yaml:"addresses,omitempty"`
	Gateway4    string                 `yaml:"gateway4,omitempty"`
	Gateway6    string                 `yaml:"gateway6,omitempty"`
	Nameservers *netplanNameservers    `yaml:"nameservers,omitempty"`
	MTU         *int                   `yaml:"mtu,omitempty"`
	Interfaces  []string               `yaml:"interfaces,omitempty"`
	Parameters  *netplanBondParameters `yaml:"parameters,omitempty"`
	Extra       map[string]interface{} `yaml:",inline"`
}

type netplanBondParameters struct {
	Mode    string `yaml:"mode,omitempty"`
	Primary string `yaml:"primary,omitempty"`
	Extra   map[string]interface{} `yaml:",inline"`
}

type netplanBridge struct {
	Extra map[string]interface{} `yaml:",inline"`
}

type netplanVlan struct {
	Extra map[string]interface{} `yaml:",inline"`
}

type netplanNameservers struct {
	Addresses []string `yaml:"addresses,omitempty"`
	Search    []string `yaml:"search,omitempty"`
}

// findNetplanFiles returns all YAML files in /etc/netplan/ sorted alphabetically.
func findNetplanFiles() ([]string, error) {
	matches, err := filepath.Glob("/etc/netplan/*.yaml")
	if err != nil {
		return nil, fmt.Errorf("glob netplan yaml: %w", err)
	}
	yml, err := filepath.Glob("/etc/netplan/*.yml")
	if err == nil {
		matches = append(matches, yml...)
	}
	sort.Strings(matches)
	return matches, nil
}

// loadNetplanFile reads and parses a single netplan YAML file.
func loadNetplanFile(path string) (*netplanData, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var np netplanData
	if err := yaml.Unmarshal(data, &np); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &np, nil
}

// saveNetplanFile writes a netplanData struct back to the file.
func saveNetplanFile(path string, np *netplanData) error {
	data, err := yaml.Marshal(np)
	if err != nil {
		return fmt.Errorf("marshal netplan: %w", err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// readNetplanConfigForInterface searches all netplan files for the configuration
// of the given interface name and returns it.
func readNetplanConfigForInterface(name string) *InterfaceConfig {
	files, err := findNetplanFiles()
	if err != nil || len(files) == 0 {
		return nil
	}

	for _, f := range files {
		np, err := loadNetplanFile(f)
		if err != nil || np.Network == nil {
			continue
		}

		// Check ethernets
		if np.Network.Ethernets != nil {
			if eth, ok := np.Network.Ethernets[name]; ok {
				return ethernetToConfig(eth)
			}
		}
		// Check bonds
		if np.Network.Bonds != nil {
			if bond, ok := np.Network.Bonds[name]; ok {
				return bondToConfig(bond)
			}
		}
	}
	return nil
}

// ethernetToConfig converts a netplan ethernet block to InterfaceConfig.
func ethernetToConfig(eth *netplanEthernet) *InterfaceConfig {
	cfg := &InterfaceConfig{
		Addresses: eth.Addresses,
		Gateway4:  eth.Gateway4,
		Gateway6:  eth.Gateway6,
		DNS:       []string{},
	}
	if cfg.Addresses == nil {
		cfg.Addresses = []string{}
	}
	if eth.DHCP4 != nil {
		cfg.DHCP4 = *eth.DHCP4
	}
	if eth.DHCP6 != nil {
		cfg.DHCP6 = *eth.DHCP6
	}
	if eth.Nameservers != nil {
		cfg.DNS = eth.Nameservers.Addresses
		if cfg.DNS == nil {
			cfg.DNS = []string{}
		}
	}
	return cfg
}

// bondToConfig converts a netplan bond block to InterfaceConfig.
func bondToConfig(bond *netplanBond) *InterfaceConfig {
	cfg := &InterfaceConfig{
		Addresses: bond.Addresses,
		Gateway4:  bond.Gateway4,
		Gateway6:  bond.Gateway6,
		DNS:       []string{},
	}
	if cfg.Addresses == nil {
		cfg.Addresses = []string{}
	}
	if bond.DHCP4 != nil {
		cfg.DHCP4 = *bond.DHCP4
	}
	if bond.DHCP6 != nil {
		cfg.DHCP6 = *bond.DHCP6
	}
	if bond.Nameservers != nil {
		cfg.DNS = bond.Nameservers.Addresses
		if cfg.DNS == nil {
			cfg.DNS = []string{}
		}
	}
	return cfg
}

// updateNetplanInterface modifies the netplan configuration for an interface.
// It finds the netplan file containing the interface or adds it to the first file.
func updateNetplanInterface(name string, req *ConfigureInterfaceRequest) error {
	files, err := findNetplanFiles()
	if err != nil {
		return fmt.Errorf("find netplan files: %w", err)
	}
	if len(files) == 0 {
		return fmt.Errorf("no netplan configuration files found in /etc/netplan/")
	}

	// Find which file contains this interface
	targetFile := ""
	var targetNP *netplanData

	for _, f := range files {
		np, loadErr := loadNetplanFile(f)
		if loadErr != nil || np.Network == nil {
			continue
		}
		if np.Network.Ethernets != nil {
			if _, ok := np.Network.Ethernets[name]; ok {
				targetFile = f
				targetNP = np
				break
			}
		}
		if np.Network.Bonds != nil {
			if _, ok := np.Network.Bonds[name]; ok {
				targetFile = f
				targetNP = np
				break
			}
		}
	}

	// If not found in any file, add to the first file under ethernets
	if targetFile == "" {
		targetFile = files[0]
		np, loadErr := loadNetplanFile(targetFile)
		if loadErr != nil {
			return fmt.Errorf("load %s: %w", targetFile, loadErr)
		}
		if np.Network == nil {
			np.Network = &netplanNetwork{Version: 2}
		}
		if np.Network.Ethernets == nil {
			np.Network.Ethernets = make(map[string]*netplanEthernet)
		}
		np.Network.Ethernets[name] = &netplanEthernet{}
		targetNP = np
	}

	// Update the interface configuration
	if targetNP.Network.Ethernets != nil {
		if eth, ok := targetNP.Network.Ethernets[name]; ok {
			applyConfigToEthernet(eth, req)
			return saveNetplanFile(targetFile, targetNP)
		}
	}
	if targetNP.Network.Bonds != nil {
		if bond, ok := targetNP.Network.Bonds[name]; ok {
			applyConfigToBond(bond, req)
			return saveNetplanFile(targetFile, targetNP)
		}
	}

	return fmt.Errorf("interface %s not found in netplan config after load", name)
}

// applyConfigToEthernet applies the request fields to a netplan ethernet block.
// When DHCP is enabled, static fields (addresses, gateways, DNS) are cleared.
func applyConfigToEthernet(eth *netplanEthernet, req *ConfigureInterfaceRequest) {
	if req.DHCP4 != nil {
		eth.DHCP4 = req.DHCP4
	}
	if req.DHCP6 != nil {
		eth.DHCP6 = req.DHCP6
	}

	// When switching to DHCP, clear static config to prevent conflicts
	if req.DHCP4 != nil && *req.DHCP4 {
		eth.Addresses = nil
		eth.Gateway4 = ""
		eth.Gateway6 = ""
		eth.Nameservers = nil
	} else {
		if req.Addresses != nil {
			eth.Addresses = req.Addresses
		}
		eth.Gateway4 = req.Gateway4
		eth.Gateway6 = req.Gateway6
		if req.DNS != nil {
			if eth.Nameservers == nil {
				eth.Nameservers = &netplanNameservers{}
			}
			eth.Nameservers.Addresses = req.DNS
		}
	}

	if req.MTU != nil {
		eth.MTU = req.MTU
	}
}

// applyConfigToBond applies the request fields to a netplan bond block.
// When DHCP is enabled, static fields are cleared.
func applyConfigToBond(bond *netplanBond, req *ConfigureInterfaceRequest) {
	if req.DHCP4 != nil {
		bond.DHCP4 = req.DHCP4
	}
	if req.DHCP6 != nil {
		bond.DHCP6 = req.DHCP6
	}

	if req.DHCP4 != nil && *req.DHCP4 {
		bond.Addresses = nil
		bond.Gateway4 = ""
		bond.Gateway6 = ""
		bond.Nameservers = nil
	} else {
		if req.Addresses != nil {
			bond.Addresses = req.Addresses
		}
		bond.Gateway4 = req.Gateway4
		bond.Gateway6 = req.Gateway6
		if req.DNS != nil {
			if bond.Nameservers == nil {
				bond.Nameservers = &netplanNameservers{}
			}
			bond.Nameservers.Addresses = req.DNS
		}
	}

	if req.MTU != nil {
		bond.MTU = req.MTU
	}
}

// createNetplanBond adds a new bond to the netplan configuration.
func createNetplanBond(req *CreateBondRequest) error {
	files, err := findNetplanFiles()
	if err != nil {
		return fmt.Errorf("find netplan files: %w", err)
	}
	if len(files) == 0 {
		return fmt.Errorf("no netplan configuration files found in /etc/netplan/")
	}

	// Use the first netplan file
	targetFile := files[0]
	np, err := loadNetplanFile(targetFile)
	if err != nil {
		return fmt.Errorf("load %s: %w", targetFile, err)
	}
	if np.Network == nil {
		np.Network = &netplanNetwork{Version: 2}
	}

	// Check for duplicate
	if np.Network.Bonds != nil {
		if _, exists := np.Network.Bonds[req.Name]; exists {
			return fmt.Errorf("bond %s already exists in netplan config", req.Name)
		}
	} else {
		np.Network.Bonds = make(map[string]*netplanBond)
	}

	// Validate bond name format
	if !isValidInterfaceName(req.Name) {
		return fmt.Errorf("invalid bond name: %s", req.Name)
	}

	bond := &netplanBond{
		Interfaces: req.Slaves,
		Parameters: &netplanBondParameters{
			Mode: req.Mode,
		},
	}
	if req.Primary != "" {
		bond.Parameters.Primary = req.Primary
	}

	// Set DHCP4 true by default for new bonds
	dhcp4 := true
	bond.DHCP4 = &dhcp4

	np.Network.Bonds[req.Name] = bond

	return saveNetplanFile(targetFile, np)
}

// deleteNetplanBond removes a bond from netplan configuration.
func deleteNetplanBond(name string) error {
	files, err := findNetplanFiles()
	if err != nil {
		return fmt.Errorf("find netplan files: %w", err)
	}

	for _, f := range files {
		np, loadErr := loadNetplanFile(f)
		if loadErr != nil || np.Network == nil || np.Network.Bonds == nil {
			continue
		}
		if _, ok := np.Network.Bonds[name]; ok {
			delete(np.Network.Bonds, name)
			// Clean up empty maps
			if len(np.Network.Bonds) == 0 {
				np.Network.Bonds = nil
			}
			return saveNetplanFile(f, np)
		}
	}

	return fmt.Errorf("bond %s not found in any netplan configuration file", name)
}

// updateNetplanDNS updates the DNS nameservers in the primary netplan file.
// It sets nameservers on the default/first ethernet interface found.
func updateNetplanDNS(servers []string) error {
	files, err := findNetplanFiles()
	if err != nil {
		return fmt.Errorf("find netplan files: %w", err)
	}
	if len(files) == 0 {
		return fmt.Errorf("no netplan configuration files found in /etc/netplan/")
	}

	// Find the file with the default/primary interface to set DNS on
	for _, f := range files {
		np, loadErr := loadNetplanFile(f)
		if loadErr != nil || np.Network == nil {
			continue
		}
		if len(np.Network.Ethernets) == 0 {
			continue
		}

		// Set DNS on the first ethernet interface found
		for _, eth := range np.Network.Ethernets {
			if eth.Nameservers == nil {
				eth.Nameservers = &netplanNameservers{}
			}
			eth.Nameservers.Addresses = servers
			return saveNetplanFile(f, np)
		}
	}

	return fmt.Errorf("no ethernet interface found in netplan configuration to set DNS")
}

// ---------- Utility functions ----------

// readSysFile reads a sysfs file and returns its trimmed content.
func readSysFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// dirExists checks if a directory exists.
func dirExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}

// fileExists checks if a file exists.
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// isValidInterfaceName checks that a network interface name is safe.
func isValidInterfaceName(name string) bool {
	return validInterfaceName.MatchString(name)
}

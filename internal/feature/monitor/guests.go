package featuremonitor

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
)

// defaultPVEDir is where pmxcfs mounts the Proxmox cluster file system.
const defaultPVEDir = "/etc/pve"

// guestsTimeout bounds pvesh: on a node whose cluster file system has lost
// quorum it can hang, and the running board asks every node in turn.
const guestsTimeout = 10 * time.Second

// Guest is one virtual machine or LXC container of this Proxmox VE node.
type Guest struct {
	VMID int    `json:"vmid"`
	Name string `json:"name"`
	// Kind is qemu (a virtual machine) or lxc (a container).
	Kind string `json:"kind"`
	// Status is Proxmox's word for it: running, stopped, paused, …
	Status string `json:"status"`
	// CPUs is Proxmox's maxcpu. It is a number, not a count: a container's
	// cpulimit may be a fraction of a CPU.
	CPUs   float64  `json:"cpus"`
	Mem    int64    `json:"mem"`
	MaxMem int64    `json:"maxmem"`
	Uptime int64    `json:"uptime"`
	Tags   []string `json:"tags"`
	// MACs are the guest's NIC addresses, upper-case, in NIC order. The
	// running board tells which guest is which of the cluster's own servers
	// by them.
	MACs []string `json:"macs"`
}

// ListGuests returns the virtual machines and containers of this node.
// GET /system/guests
//
// Per node: ?node= reaches another node's list through the cluster proxy.
// A host that is not Proxmox VE answers an empty list without running
// anything, which is what most nodes answer. On a Proxmox VE host a list
// pvesh could not give is an error, not an empty list, so the page can tell
// "no guests" from "the guests could not be read". Read-only.
func (h *Handler) ListGuests(c echo.Context) error {
	if !h.Platform().PVE {
		return response.OK(c, []Guest{})
	}

	ctx, cancel := context.WithTimeout(c.Request().Context(), guestsTimeout)
	defer cancel()
	out, err := h.Cmd.RunCtx(ctx, "pvesh", "get", "/cluster/resources", "--type", "vm", "--output-format", "json")
	if err != nil {
		detail := out
		if strings.TrimSpace(detail) == "" {
			detail = err.Error()
		}
		slog.Warn("guest list: pvesh failed", "component", "monitor", "error", err, "output", response.SanitizeOutput(out))
		return response.Fail(c, http.StatusInternalServerError, response.ErrPVEGuestsError,
			"pvesh failed: "+response.SanitizeOutput(detail))
	}

	pveDir := h.PVEDir
	if pveDir == "" {
		pveDir = defaultPVEDir
	}
	node := guestNodeName(pveDir)
	guests, err := parseGuestResources([]byte(out), node)
	if err != nil {
		slog.Warn("guest list: pvesh output unreadable", "component", "monitor", "error", err, "output", response.SanitizeOutput(out))
		return response.Fail(c, http.StatusInternalServerError, response.ErrPVEGuestsError,
			"cannot read pvesh output: "+response.SanitizeOutput(err.Error()+": "+out))
	}

	for i := range guests {
		dir := "qemu-server"
		if guests[i].Kind == "lxc" {
			dir = "lxc"
		}
		conf, err := os.ReadFile(filepath.Join(pveDir, "nodes", node, dir, strconv.Itoa(guests[i].VMID)+".conf"))
		if err != nil {
			// The guest is still listed: its NICs only name which server
			// it is, and a guest without them is still running here.
			slog.Debug("guest list: config unreadable", "component", "monitor", "vmid", guests[i].VMID, "error", err)
			continue
		}
		guests[i].MACs = guestMACs(conf)
	}
	return response.OK(c, guests)
}

// guestNodeName is this host's Proxmox node name: /etc/pve/local points at
// nodes/<name>. Where the link cannot be read, the short host name, which is
// what Proxmox names a node after.
func guestNodeName(pveDir string) string {
	if target, err := os.Readlink(filepath.Join(pveDir, "local")); err == nil {
		return filepath.Base(target)
	}
	host, _ := os.Hostname()
	short, _, _ := strings.Cut(host, ".")
	return short
}

// pveResource is the part of an entry of /cluster/resources a Guest is made
// from. The figures are read as float64: a fraction where an integer field
// was expected would refuse the whole list, not the one guest.
type pveResource struct {
	VMID     int     `json:"vmid"`
	Name     string  `json:"name"`
	Node     string  `json:"node"`
	Type     string  `json:"type"`
	Status   string  `json:"status"`
	Template int     `json:"template"`
	MaxCPU   float64 `json:"maxcpu"`
	Mem      float64 `json:"mem"`
	MaxMem   float64 `json:"maxmem"`
	Uptime   float64 `json:"uptime"`
	Tags     string  `json:"tags"`
}

// parseGuestResources reads what `pvesh get /cluster/resources --type vm`
// answers, which is every guest of the Proxmox cluster, and keeps those of
// node that are not templates, by vmid.
func parseGuestResources(data []byte, node string) ([]Guest, error) {
	var resources []pveResource
	if err := json.Unmarshal(data, &resources); err != nil {
		return nil, err
	}
	guests := []Guest{}
	for _, r := range resources {
		if r.Node != node || r.Template == 1 || (r.Type != "qemu" && r.Type != "lxc") {
			continue
		}
		guests = append(guests, Guest{
			VMID:   r.VMID,
			Name:   r.Name,
			Kind:   r.Type,
			Status: r.Status,
			CPUs:   r.MaxCPU,
			Mem:    int64(r.Mem),
			MaxMem: int64(r.MaxMem),
			Uptime: int64(r.Uptime),
			// Proxmox separates tags with ';'; older versions also wrote
			// ',' or spaces.
			Tags: append([]string{}, strings.FieldsFunc(r.Tags, func(c rune) bool {
				return c == ';' || c == ',' || unicode.IsSpace(c)
			})...),
			MACs: []string{},
		})
	}
	slices.SortFunc(guests, func(a, b Guest) int { return a.VMID - b.VMID })
	return guests, nil
}

// guestMACs reads the NIC addresses from a guest's config. Only the lines
// before the first [section] count: the sections are snapshots and pending
// changes, NICs the guest does not have now. A NIC is a netN line, and its
// address is the value on it that is a MAC address: the model key's in a VM
// (virtio=…, e1000=…), macaddr= in the older VM form, hwaddr= in a container.
// They come in NIC order, which is not the file's: Proxmox sorts the keys as
// strings, so net10 is written before net2.
func guestMACs(conf []byte) []string {
	type nic struct {
		index int
		mac   string
	}
	var nics []nic
	for line := range strings.Lines(string(conf)) {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			break
		}
		key, value, _ := strings.Cut(line, ":")
		digits, isNet := strings.CutPrefix(key, "net")
		index, err := strconv.Atoi(digits)
		if !isNet || err != nil {
			continue
		}
		for _, opt := range strings.Split(value, ",") {
			_, v, ok := strings.Cut(opt, "=")
			if !ok {
				continue
			}
			if hw, err := net.ParseMAC(strings.TrimSpace(v)); err == nil {
				nics = append(nics, nic{index, strings.ToUpper(hw.String())})
			}
		}
	}
	slices.SortStableFunc(nics, func(a, b nic) int { return a.index - b.index })

	macs := []string{}
	for _, n := range nics {
		if !slices.Contains(macs, n.mac) {
			macs = append(macs, n.mac)
		}
	}
	return macs
}

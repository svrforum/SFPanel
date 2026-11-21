// Package lanscan finds the devices on the LANs a node is attached to, with
// nmap, when the operator asks.
//
// Per node: the networks are this node's own, the scan runs here, and its
// result is stored in this node's database (lan_scans). ?node= reaches
// another node's through the cluster proxy; all three routes answer at once,
// the scan itself running on after POST returns, so the proxy's 30-second
// unary limit is never near. Nothing is replicated.
package lanscan

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/common/exec"
)

// How a scan ended, in Last.State.
const (
	stateDone      = "done"
	stateFailed    = "failed"
	stateTimeout   = "timeout"
	stateCancelled = "cancelled"
)

const (
	// routeTimeout bounds `ip -4 route show default`.
	routeTimeout = 5 * time.Second
	// cancelWait is how long DELETE waits for a cancelled scan to end, so
	// the GET that follows already reads it as cancelled. nmap is killed,
	// which takes moments; past this DELETE answers anyway.
	cancelWait = 10 * time.Second
)

// Handler serves /network/scan. One per node: it is what keeps a node to one
// scan at a time.
type Handler struct {
	DB  *sql.DB
	Cmd exec.Commander
	// Interfaces lists the host's network interfaces; nil reads the
	// kernel's (net.Interfaces).
	Interfaces func() ([]Interface, error)

	// scan runs nmap over cidr and returns its XML output; nil is runNmap.
	// Tests set it to hand in XML without running anything.
	scan func(ctx context.Context, cidr string, ports bool) ([]byte, error)

	mu      sync.Mutex
	running *Running
	cancel  context.CancelFunc
	done    chan struct{}
	last    *Last
}

// Running is the scan under way.
type Running struct {
	Network   string `json:"network"`
	Ports     bool   `json:"ports"`
	StartedAt string `json:"started_at"`
}

// Last is how the last scan since the panel started ended. It is kept in
// memory only: the stored result is the last successful one, and this says
// whether a later one failed.
type Last struct {
	Network string `json:"network"`
	Ports   bool   `json:"ports"`
	// State is done, failed, timeout or cancelled.
	State string `json:"state"`
	// At is when it ended, RFC 3339 UTC.
	At string `json:"at"`
}

// Status is GET /network/scan.
type Status struct {
	// Installed says whether nmap is there to scan with.
	Installed bool      `json:"installed"`
	Networks  []Network `json:"networks"`
	Running   *Running  `json:"running"`
	Last      *Last     `json:"last"`
	// Results are the stored results of the networks listed now, in their
	// order; a network this node is no longer on is not answered.
	Results []Result `json:"results"`
}

type startRequest struct {
	Network string `json:"network"`
	Ports   bool   `json:"ports"`
}

// GetScan answers which networks this node can scan, the scan under way,
// how the last one ended, and the stored results.
//
//	GET /api/v1/network/scan
func (h *Handler) GetScan(c echo.Context) error {
	nets, err := h.networks()
	if err != nil {
		slog.Warn("network interfaces unreadable", "component", "lanscan", "error", err)
		return response.Fail(c, http.StatusInternalServerError, response.ErrNetworkError, "Cannot list this node's network interfaces")
	}
	stored, err := loadResults(h.DB)
	if err != nil {
		slog.Warn("stored LAN scans unreadable", "component", "lanscan", "error", err)
		return response.Fail(c, http.StatusInternalServerError, response.ErrDBError, "Cannot read the stored scans")
	}
	results := []Result{}
	for _, n := range nets {
		if r, ok := stored[n.CIDR]; ok {
			results = append(results, r)
		}
	}

	h.mu.Lock()
	var running *Running
	if h.running != nil {
		r := *h.running
		running = &r
	}
	var last *Last
	if h.last != nil {
		l := *h.last
		last = &l
	}
	h.mu.Unlock()

	return response.OK(c, Status{
		Installed: h.Cmd.Exists("nmap"),
		Networks:  nets,
		Running:   running,
		Last:      last,
		Results:   results,
	})
}

// StartScan starts a scan of one of this node's scannable networks and
// answers without waiting for it.
//
//	POST /api/v1/network/scan {"network":"10.20.30.0/24","ports":true}
//
// The network must be, character for character, the cidr of a network
// GET lists as scannable. nmap is given that list's string, never the
// request's: nothing a request says reaches nmap's command line.
func (h *Handler) StartScan(c echo.Context) error {
	var req startRequest
	if err := c.Bind(&req); err != nil || req.Network == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Body must be {\"network\": \"<cidr>\", \"ports\": true|false}")
	}
	nets, err := h.networks()
	if err != nil {
		slog.Warn("network interfaces unreadable", "component", "lanscan", "error", err)
		return response.Fail(c, http.StatusInternalServerError, response.ErrNetworkError, "Cannot list this node's network interfaces")
	}
	var target *Network
	for i := range nets {
		if nets[i].Scannable && nets[i].CIDR == req.Network {
			target = &nets[i]
			break
		}
	}
	if target == nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrLanScanNetworkNotAllowed,
			"Only a network this node lists as scannable can be scanned")
	}
	if !h.Cmd.Exists("nmap") {
		return response.Fail(c, http.StatusServiceUnavailable, response.ErrToolNotInstalled,
			"nmap is not installed. Install it from Packages.")
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if h.running != nil {
		return response.Fail(c, http.StatusConflict, response.ErrLanScanInProgress, "A scan is already running on this node")
	}
	timeout := hostsTimeout
	if req.Ports {
		timeout = portsTimeout
	}
	// Its own context, not the request's: the scan outlives the request.
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	started := time.Now().UTC()
	done := make(chan struct{})
	h.running = &Running{Network: target.CIDR, Ports: req.Ports, StartedAt: started.Format(time.RFC3339)}
	h.cancel = cancel
	h.done = done
	go h.run(ctx, cancel, done, *target, req.Ports, started)
	return response.OK(c, map[string]bool{"started": true})
}

// CancelScan stops the scan under way. cancelled is false when there was
// none. A scan that ends of itself in the same moment may still be stored
// as done; GET's last says which.
//
//	DELETE /api/v1/network/scan
func (h *Handler) CancelScan(c echo.Context) error {
	h.mu.Lock()
	cancel, done := h.cancel, h.done
	h.mu.Unlock()
	if cancel == nil {
		return response.OK(c, map[string]bool{"cancelled": false})
	}
	cancel()
	select {
	case <-done:
	case <-time.After(cancelWait):
		slog.Warn("cancelled scan has not ended yet", "component", "lanscan", "waited", cancelWait.String())
	}
	return response.OK(c, map[string]bool{"cancelled": true})
}

// run is one scan, from start to its Last.
func (h *Handler) run(ctx context.Context, cancel context.CancelFunc, done chan struct{}, n Network, ports bool, started time.Time) {
	defer close(done)
	defer cancel()
	state := h.scanNetwork(ctx, n, ports, started)

	h.mu.Lock()
	h.running = nil
	h.cancel = nil
	h.done = nil
	h.last = &Last{Network: n.CIDR, Ports: ports, State: state, At: time.Now().UTC().Format(time.RFC3339)}
	h.mu.Unlock()
}

// scanNetwork scans n and stores the result when nmap finished. Anything
// else leaves the stored result as it was and only says how it ended.
func (h *Handler) scanNetwork(ctx context.Context, n Network, ports bool, started time.Time) string {
	log := slog.With("component", "lanscan", "network", n.CIDR, "ports", ports)
	scan := h.scan
	if scan == nil {
		scan = h.runNmap
	}
	data, err := scan(ctx, n.CIDR, ports)
	if err != nil {
		switch ctx.Err() {
		case context.Canceled:
			log.Info("scan cancelled")
			return stateCancelled
		case context.DeadlineExceeded:
			log.Warn("scan timed out", "after", time.Since(started).Round(time.Second).String())
			return stateTimeout
		}
		log.Warn("scan failed", "error", err)
		return stateFailed
	}
	hosts, err := parseScan(data)
	if err != nil {
		log.Warn("scan output refused", "error", err)
		return stateFailed
	}
	// nmap does not learn its own host's MAC; the scanned interface's is it.
	for i := range hosts {
		if hosts[i].Self {
			hosts[i].MAC = n.MAC
		}
	}
	finished := time.Now().UTC()
	r := Result{
		Network:    n.CIDR,
		Interface:  n.Interface,
		Address:    n.Address,
		Gateway:    n.Gateway,
		Ports:      ports,
		StartedAt:  started.Format(time.RFC3339),
		FinishedAt: finished.Format(time.RFC3339),
		Hosts:      hosts,
	}
	if err := saveResult(h.DB, r); err != nil {
		log.Error("scan result not stored", "error", err)
		return stateFailed
	}
	log.Info("scan done", "hosts", len(hosts), "duration_ms", finished.Sub(started).Milliseconds())
	return stateDone
}

// runNmap runs nmap with its XML written to a file of its own and returns
// the file. The Commander hands back stdout and stderr in one, and nmap
// writes warnings to stderr in the middle of whatever it prints, so its
// output is not where the XML is read from. The file is in a directory only
// this process can open (os.MkdirTemp makes it 0700), removed afterwards.
func (h *Handler) runNmap(ctx context.Context, cidr string, ports bool) ([]byte, error) {
	dir, err := os.MkdirTemp("", "sfpanel-lanscan-")
	if err != nil {
		return nil, fmt.Errorf("make a directory for the output: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(dir); err != nil {
			slog.Warn("scan output not removed", "component", "lanscan", "dir", dir, "error", err)
		}
	}()
	file := filepath.Join(dir, "scan.xml")
	out, err := h.Cmd.RunCtx(ctx, "nmap", nmapArgs(cidr, ports, file)...)
	if err != nil {
		return nil, fmt.Errorf("nmap: %w: %s", err, tail(response.SanitizeOutput(out), 2000))
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("read nmap's output: %w", err)
	}
	return data, nil
}

// networks is this node's network list, read now.
func (h *Handler) networks() ([]Network, error) {
	list := h.Interfaces
	if list == nil {
		list = systemInterfaces
	}
	ifaces, err := list()
	if err != nil {
		return nil, err
	}
	routes, err := h.Cmd.RunWithTimeout(routeTimeout, "ip", "-4", "route", "show", "default")
	if err != nil {
		// Without the routes the networks are still listed, without a
		// gateway.
		slog.Warn("default routes unreadable", "component", "lanscan", "error", err)
		routes = ""
	}
	return networks(ifaces, routes), nil
}

// tail is the last n bytes of s, for a log line.
func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + strings.ToValidUTF8(s[len(s)-n:], "")
}

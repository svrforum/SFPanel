package lanscan

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/netip"
	"sort"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/common/safe"
)

const (
	// scheduleTick is how often the scheduler wakes.
	scheduleTick = time.Minute
	// scheduleHoldoff is how long after the scheduler starts it does
	// nothing, so that a panel just started (or restarted by an update)
	// is not also scanning.
	scheduleHoldoff = 2 * time.Minute
	// portsEvery is how often a schedule with ports_daily scans ports.
	portsEvery = 24 * time.Hour

	// maxKnown and maxKnownMACs bound a schedule's known list.
	maxKnown     = 64
	maxKnownMACs = 16
)

// intervals are the schedule intervals offered, in minutes; 0 is off.
var intervals = map[int]bool{0: true, 15: true, 60: true, 360: true, 1440: true}

// Why a schedule that is on does not run, in Schedule.PausedReason.
const (
	// pausedNoTool: nmap is not installed.
	pausedNoTool = "no_tool"
	// pausedGone: this node is no longer on the network, or it is no
	// longer scannable.
	pausedGone = "gone"
	// pausedConflict: in the network's stored result another device
	// answers at one of the cluster's own nodes' addresses (inConflict).
	pausedConflict = "conflict"
)

// KnownAddress is one of the cluster's own nodes on a network, as the page
// knows it: its address there and its interfaces' MACs.
type KnownAddress struct {
	Address string   `json:"address"`
	MACs    []string `json:"macs"`
}

// Schedule is a network's scheduled scan, as GET /network/scan's schedules
// and PUT /network/scan/schedule answer it.
type Schedule struct {
	Network         string `json:"network"`
	IntervalMinutes int    `json:"interval_minutes"`
	PortsDaily      bool   `json:"ports_daily"`
	// LastRun is when a scheduled scan last started, RFC 3339 UTC; "" for
	// never.
	LastRun string `json:"last_run"`
	// NextRun is when the next one is due, RFC 3339 UTC; "" when the
	// schedule is off or paused.
	NextRun string `json:"next_run"`
	// PausedReason is "", or why a schedule that is on does not run:
	// no_tool, gone or conflict.
	PausedReason string `json:"paused_reason"`
}

// scheduleRow is a row of lan_schedules.
type scheduleRow struct {
	Network         string
	IntervalMinutes int
	PortsDaily      bool
	Known           []KnownAddress
	LastRun         string
	LastPortsRun    string
}

type scheduleRequest struct {
	Network         string         `json:"network"`
	IntervalMinutes int            `json:"interval_minutes"`
	PortsDaily      bool           `json:"ports_daily"`
	Known           []KnownAddress `json:"known"`
}

// PutSchedule sets a network's scheduled scan. The network is checked as
// POST /network/scan checks it; known is the cluster's own nodes on it,
// stored as given (MACs lower case) for the conflict rule.
//
//	PUT /api/v1/network/scan/schedule
//	{"network":"10.20.30.0/24","interval_minutes":60,"ports_daily":false,
//	 "known":[{"address":"10.20.30.12","macs":["02:42:ac:14:1e:0c"]}]}
func (h *Handler) PutSchedule(c echo.Context) error {
	var req scheduleRequest
	if err := c.Bind(&req); err != nil || req.Network == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest,
			"Body must be {\"network\": \"<cidr>\", \"interval_minutes\": n, \"ports_daily\": true|false, \"known\": […]}")
	}
	nets, err := h.networks()
	if err != nil {
		slog.Warn("network interfaces unreadable", "component", "lanscan", "error", err)
		return response.Fail(c, http.StatusInternalServerError, response.ErrNetworkError, "Cannot list this node's network interfaces")
	}
	target := scannableNetwork(nets, req.Network)
	if target == nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrLanScanNetworkNotAllowed,
			"Only a network this node lists as scannable can be scheduled")
	}
	if !intervals[req.IntervalMinutes] {
		return invalidSchedule(c, "interval_minutes", "The interval is 0 (off), 15, 60, 360 or 1440 minutes")
	}
	known, ok := readKnown(req.Known)
	if !ok {
		return invalidSchedule(c, "known", "known is at most 64 IPv4 addresses, each with 1 to 16 MAC addresses")
	}

	row := scheduleRow{Network: target.CIDR, IntervalMinutes: req.IntervalMinutes, PortsDaily: req.PortsDaily, Known: known}
	if err := saveSchedule(h.DB, row); err != nil {
		slog.Warn("scan schedule not stored", "component", "lanscan", "network", row.Network, "error", err)
		return response.Fail(c, http.StatusInternalServerError, response.ErrDBError, "Cannot store the schedule")
	}
	stored, ok, err := loadSchedule(h.DB, row.Network)
	if err == nil && !ok {
		err = errors.New("the row just written is not there")
	}
	if err != nil {
		slog.Warn("scan schedule unreadable", "component", "lanscan", "network", row.Network, "error", err)
		return response.Fail(c, http.StatusInternalServerError, response.ErrDBError, "Cannot read the schedule")
	}
	results, err := loadResults(h.DB)
	if err != nil {
		slog.Warn("stored LAN scans unreadable", "component", "lanscan", "error", err)
		return response.Fail(c, http.StatusInternalServerError, response.ErrDBError, "Cannot read the stored scans")
	}
	return response.OK(c, h.scheduleEntry(stored, h.scheduleEnv(nets, h.Cmd.Exists("nmap"), results)))
}

// StartScheduler runs the scheduled scans until ctx ends: a pass every
// minute, none in the first two minutes.
func (h *Handler) StartScheduler(ctx context.Context) {
	h.mu.Lock()
	h.scheduleFrom = h.clock().Add(scheduleHoldoff)
	h.mu.Unlock()
	safe.Go("lanscan-scheduler", func() {
		ticker := time.NewTicker(scheduleTick)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				h.schedulePass()
			}
		}
	})
}

// schedulePass is one wake of the scheduler. Nothing happens before the
// scheduler has started and its first two minutes are over, or while any
// scan runs. Of the schedules that are on, in network order, one that is
// paused is skipped (its reason logged when it changes), and the first that
// is due is started — a port scan when ports_daily and the last was 24
// hours ago or never, else a host scan when the interval has passed since
// the last run or there was none. At most one scan starts.
func (h *Handler) schedulePass() {
	log := slog.With("component", "lanscan")
	now := h.clock()
	h.mu.Lock()
	from := h.scheduleFrom
	h.mu.Unlock()
	if from.IsZero() || now.Before(from) {
		return
	}
	rows, err := loadSchedules(h.DB)
	if err != nil {
		log.Warn("scan schedules unreadable", "error", err)
		return
	}
	active := rows[:0]
	for _, row := range rows {
		if row.IntervalMinutes > 0 {
			active = append(active, row)
		}
	}
	if len(active) == 0 {
		h.notePaused(map[string]string{})
		return
	}
	nets, err := h.networks()
	if err != nil {
		log.Warn("network interfaces unreadable; no scheduled scan", "error", err)
		return
	}
	results, err := loadResults(h.DB)
	if err != nil {
		log.Warn("stored LAN scans unreadable; no scheduled scan", "error", err)
		return
	}
	env := h.scheduleEnv(nets, h.Cmd.Exists("nmap"), results)
	env.now = now

	paused := map[string]string{}
	var pick *scheduleRow
	pickPorts := false
	for i := range active {
		row := &active[i]
		if reason := pausedReason(*row, env); reason != "" {
			paused[row.Network] = reason
			continue
		}
		if pick != nil {
			continue
		}
		if ports, ok := due(*row, now); ok {
			pick, pickPorts = row, ports
		}
	}
	h.notePaused(paused)
	if pick == nil {
		return
	}
	n := scannableNetwork(nets, pick.Network)
	if n == nil {
		// pausedReason already says gone for this; kept so a change there
		// cannot become a nil dereference here.
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if h.running != nil {
		// Any scan, manual or scheduled: this one waits for a pass when
		// none runs.
		return
	}
	// Written before the scan starts, so a scan that fails is not tried
	// again every minute.
	if err := markStarted(h.DB, pick.Network, now, pickPorts); err != nil {
		log.Warn("scheduled scan not started: its start cannot be recorded", "network", pick.Network, "error", err)
		return
	}
	h.startLocked(*n, pickPorts, true)
	log.Info("scheduled scan started", "network", pick.Network, "ports", pickPorts)
}

// notePaused logs each schedule that has become paused, changed its reason
// or is no longer paused (it can run, or was turned off) since the last
// pass, rather than once a minute while it stays so.
func (h *Handler) notePaused(now map[string]string) {
	h.mu.Lock()
	before := h.paused
	h.paused = now
	h.mu.Unlock()
	for network, reason := range now {
		if before[network] != reason {
			slog.Info("scheduled scan paused", "component", "lanscan", "network", network, "reason", reason)
		}
	}
	for network := range before {
		if _, still := now[network]; !still {
			slog.Info("scheduled scan no longer paused", "component", "lanscan", "network", network)
		}
	}
}

// scheduleEnv is what a schedule's state is judged against.
type scheduleEnv struct {
	now       time.Time
	from      time.Time
	nets      []Network
	installed bool
	results   map[string]Result
}

func (h *Handler) scheduleEnv(nets []Network, installed bool, results map[string]Result) scheduleEnv {
	h.mu.Lock()
	from := h.scheduleFrom
	h.mu.Unlock()
	return scheduleEnv{now: h.clock(), from: from, nets: nets, installed: installed, results: results}
}

// scheduleEntry is a row as the page reads it.
func (h *Handler) scheduleEntry(row scheduleRow, env scheduleEnv) Schedule {
	s := Schedule{Network: row.Network, IntervalMinutes: row.IntervalMinutes, PortsDaily: row.PortsDaily, LastRun: row.LastRun}
	if row.IntervalMinutes == 0 {
		return s
	}
	if s.PausedReason = pausedReason(row, env); s.PausedReason != "" {
		return s
	}
	s.NextRun = stamp(nextRun(row, env))
	return s
}

// pausedReason is why a schedule that is on would not run now, or "".
func pausedReason(row scheduleRow, env scheduleEnv) string {
	switch {
	case !env.installed:
		return pausedNoTool
	case scannableNetwork(env.nets, row.Network) == nil:
		return pausedGone
	case inConflict(row, env.results[row.Network]):
		return pausedConflict
	}
	return ""
}

// inConflict says whether, in a network's stored result, a host answers at
// an address the schedule's known list gives one of the cluster's own nodes,
// with a MAC that is none of that node's. An ARP scan of a LAN with a
// duplicated address can turn the scanning host's ARP entry for it to the
// other device and cut the host off from the real owner, so a schedule does
// not keep giving it the chance. The scanning node's own answer, and a host
// with no MAC, prove nothing either way.
func inConflict(row scheduleRow, r Result) bool {
	return len(knownConflicts(row, r.Hosts)) > 0
}

// knownConflicts are the hosts that put a schedule in conflict (inConflict):
// each answers at an address the known list gives one of the cluster's own
// nodes, with a MAC that is none of that node's, and is neither the
// scanning node itself nor a host without a MAC. In the hosts' order.
func knownConflicts(row scheduleRow, hosts []Host) []Host {
	known := map[string]map[string]bool{}
	for _, k := range row.Known {
		if known[k.Address] == nil {
			known[k.Address] = map[string]bool{}
		}
		for _, mac := range k.MACs {
			known[k.Address][mac] = true
		}
	}
	var out []Host
	for _, host := range hosts {
		macs, listed := known[host.IP]
		if !listed || host.Self || host.MAC == "" {
			continue
		}
		if !macs[host.MAC] {
			out = append(out, host)
		}
	}
	return out
}

// due says whether a schedule that is on is due at now, and whether for a
// port scan.
func due(row scheduleRow, now time.Time) (ports, ok bool) {
	if row.PortsDaily && elapsed(row.LastPortsRun, now, portsEvery) {
		return true, true
	}
	if elapsed(row.LastRun, now, time.Duration(row.IntervalMinutes)*time.Minute) {
		return false, true
	}
	return false, false
}

// elapsed says whether d has passed since last, or there was no last.
func elapsed(last string, now time.Time, d time.Duration) bool {
	t, err := time.Parse(time.RFC3339, last)
	if err != nil {
		return true
	}
	return now.Sub(t) >= d
}

// nextRun is when a schedule that is on and not paused will next run: when
// it falls due, but not before the scheduler's first two minutes are over,
// and not before now.
func nextRun(row scheduleRow, env scheduleEnv) time.Time {
	next := after(row.LastRun, time.Duration(row.IntervalMinutes)*time.Minute)
	if row.PortsDaily {
		if p := after(row.LastPortsRun, portsEvery); p.Before(next) {
			next = p
		}
	}
	if next.Before(env.from) {
		next = env.from
	}
	if next.Before(env.now) {
		next = env.now
	}
	return next
}

// after is last plus d; the zero time when there was no last.
func after(last string, d time.Duration) time.Time {
	t, err := time.Parse(time.RFC3339, last)
	if err != nil {
		return time.Time{}
	}
	return t.Add(d)
}

// scannableNetwork is the network of nets that is scannable and whose cidr
// is, character for character, cidr; nil when there is none.
func scannableNetwork(nets []Network, cidr string) *Network {
	for i := range nets {
		if nets[i].Scannable && nets[i].CIDR == cidr {
			return &nets[i]
		}
	}
	return nil
}

// readKnown checks a known list and gives it as stored: [] for none, each
// address an IPv4 one, each MAC six bytes in lower case.
func readKnown(list []KnownAddress) ([]KnownAddress, bool) {
	if len(list) > maxKnown {
		return nil, false
	}
	out := make([]KnownAddress, 0, len(list))
	for _, k := range list {
		addr, err := netip.ParseAddr(k.Address)
		if err != nil || !addr.Is4() {
			return nil, false
		}
		if len(k.MACs) == 0 || len(k.MACs) > maxKnownMACs {
			return nil, false
		}
		entry := KnownAddress{Address: addr.String(), MACs: make([]string, 0, len(k.MACs))}
		for _, m := range k.MACs {
			mac, ok := parseDeviceMAC(m)
			if !ok {
				return nil, false
			}
			entry.MACs = append(entry.MACs, mac)
		}
		out = append(out, entry)
	}
	return out, true
}

func invalidSchedule(c echo.Context, field, message string) error {
	return response.FailWith(c, http.StatusBadRequest, response.ErrLanScheduleInvalid, message,
		map[string]string{"field": field})
}

// saveSchedule writes a network's schedule, keeping when it last ran.
func saveSchedule(db *sql.DB, row scheduleRow) error {
	known, err := json.Marshal(row.Known)
	if err != nil {
		return err
	}
	_, err = db.Exec(`INSERT INTO lan_schedules (network, interval_minutes, ports_daily, known) VALUES (?, ?, ?, ?)
		ON CONFLICT(network) DO UPDATE SET interval_minutes = excluded.interval_minutes,
			ports_daily = excluded.ports_daily, known = excluded.known`,
		row.Network, row.IntervalMinutes, row.PortsDaily, string(known))
	return err
}

// markStarted records that a scheduled scan of network started at at.
func markStarted(db *sql.DB, network string, at time.Time, ports bool) error {
	_, err := db.Exec(`UPDATE lan_schedules SET last_run = ?,
			last_ports_run = CASE WHEN ? THEN ? ELSE last_ports_run END
		WHERE network = ?`, stamp(at), ports, stamp(at), network)
	return err
}

const scheduleColumns = `network, interval_minutes, ports_daily, known, last_run, last_ports_run`

func scanSchedule(row rowScanner) (scheduleRow, error) {
	var s scheduleRow
	var known string
	if err := row.Scan(&s.Network, &s.IntervalMinutes, &s.PortsDaily, &known, &s.LastRun, &s.LastPortsRun); err != nil {
		return scheduleRow{}, err
	}
	if err := json.Unmarshal([]byte(known), &s.Known); err != nil {
		// Without its known list a schedule cannot see a conflict; it runs
		// as one that was given none.
		slog.Warn("scan schedule's known list unreadable", "component", "lanscan", "network", s.Network, "error", err)
		s.Known = nil
	}
	return s, nil
}

// loadSchedules is every schedule, in network order.
func loadSchedules(db *sql.DB) ([]scheduleRow, error) {
	rows, err := db.Query(`SELECT ` + scheduleColumns + ` FROM lan_schedules`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []scheduleRow{}
	for rows.Next() {
		s, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return networkLess(out[i].Network, out[j].Network) })
	return out, nil
}

// loadSchedule is one network's schedule; ok is false when it has none.
func loadSchedule(db *sql.DB, network string) (scheduleRow, bool, error) {
	s, err := scanSchedule(db.QueryRow(`SELECT `+scheduleColumns+` FROM lan_schedules WHERE network = ?`, network))
	if errors.Is(err, sql.ErrNoRows) {
		return scheduleRow{}, false, nil
	}
	if err != nil {
		return scheduleRow{}, false, err
	}
	return s, true, nil
}

package alert

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"time"
)

// AlertLanEvent is a stored LAN scan, as main.go hands over lanscan's
// ScanEvent without coupling alert/ to lanscan/. See lanscan.ScanEvent for
// what each list holds.
type AlertLanEvent struct {
	Network    string
	FinishedAt time.Time
	Scheduled  bool
	New        []LanSeenDevice
	Down       []LanKnownDevice
	Back       []LanKnownDevice
	Conflicts  []LanAddressConflict
}

// LanSeenDevice is a device as the scan found it.
type LanSeenDevice struct{ IP, MAC, Vendor, Hostname string }

// LanKnownDevice is a registry device; Name is its label's.
type LanKnownDevice struct{ MAC, Name, IP, Vendor, Hostname, LastSeen string }

// LanAddressConflict is an address two devices claim. MACs is the one that
// answered for a certain conflict, the ones that overlapped for a suspected
// one; Vendor is the answering host's, for a certain conflict only.
type LanAddressConflict struct {
	IP      string
	MACs    []string
	Vendor  string
	Certain bool
}

// lanFire is one alert a LAN rule sends: what it is about, which its
// cooldown runs per, and the message.
type lanFire struct{ subject, message string }

// lanRules are the rule types LanDispatcher evaluates, each with the alerts
// one stored scan makes for it. Their condition is not read. Together with
// hostMetricRules and containerRules this is everything that is evaluated,
// and the handlers accept no other type (ruleTypeEvaluated).
var lanRules = map[string]func(ev *AlertLanEvent) []lanFire{
	// One per device the scan put in the registry for the first time; the
	// subject is its MAC, so a device is told of once per cooldown.
	"lan_new_device": newDeviceFires,
	// One per address in conflict the scan tells of, which is only a
	// conflict seen for the first time or after a day without it (lanscan
	// keeps that); the subject is the network and the address.
	"lan_address_conflict": addressConflictFires,
	// One per watched device the second scan in a row did not find (the
	// subject its MAC), and one per such device found again (its MAC and
	// "/back", so the cooldown of the first does not hold back the second).
	"lan_device_down": deviceDownFires,
}

func newDeviceFires(ev *AlertLanEvent) []lanFire {
	out := make([]lanFire, 0, len(ev.New))
	for _, d := range ev.New {
		about := []string{d.MAC}
		for _, s := range []string{d.Vendor, d.Hostname} {
			if s != "" {
				about = append(about, s)
			}
		}
		out = append(out, lanFire{
			subject: d.MAC,
			message: fmt.Sprintf("New device on %s: %s (%s)", ev.Network, d.IP, strings.Join(about, ", ")),
		})
	}
	return out
}

func addressConflictFires(ev *AlertLanEvent) []lanFire {
	out := make([]lanFire, 0, len(ev.Conflicts))
	for _, c := range ev.Conflicts {
		var message string
		if c.Certain {
			who := strings.Join(c.MACs, ", ")
			if c.Vendor != "" {
				who += " (" + c.Vendor + ")"
			}
			message = fmt.Sprintf("Address conflict on %s: %s is a server's address, and %s answered on it.", ev.Network, c.IP, who)
		} else {
			message = fmt.Sprintf("Suspected address conflict on %s: %s was held by %s over the same period.", ev.Network, c.IP, andList(c.MACs))
		}
		out = append(out, lanFire{subject: ev.Network + "/" + c.IP, message: message})
	}
	return out
}

func deviceDownFires(ev *AlertLanEvent) []lanFire {
	out := make([]lanFire, 0, len(ev.Down)+len(ev.Back))
	for _, d := range ev.Down {
		name, named := deviceName(d)
		var where []string
		if d.IP != "" {
			where = append(where, d.IP)
		}
		if named {
			where = append(where, d.MAC)
		}
		if len(where) > 0 {
			name += " (" + strings.Join(where, ", ") + ")"
		}
		message := fmt.Sprintf("%s was not found by the last 2 scans of %s.", name, ev.Network)
		if seen, err := time.Parse(time.RFC3339, d.LastSeen); err == nil {
			message += " Last seen " + seen.UTC().Format("2006-01-02 15:04") + " UTC."
		}
		out = append(out, lanFire{subject: d.MAC, message: message})
	}
	for _, d := range ev.Back {
		name, _ := deviceName(d)
		if d.IP != "" {
			name += " (" + d.IP + ")"
		}
		out = append(out, lanFire{subject: d.MAC + "/back", message: fmt.Sprintf("%s is back on %s.", name, ev.Network)})
	}
	return out
}

// deviceName is what a message calls a device: its label's name, else its
// hostname, else its MAC; named is false for the MAC.
func deviceName(d LanKnownDevice) (name string, named bool) {
	switch {
	case d.Name != "":
		return d.Name, true
	case d.Hostname != "":
		return d.Hostname, true
	}
	return d.MAC, false
}

// andList is "a", "a and b", "a, b and c".
func andList(items []string) string {
	if len(items) < 2 {
		return strings.Join(items, "")
	}
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}

// LanDispatcher evaluates the LAN rules each time this node has stored a
// scan: lanscan's Handler.Notify, adapted in main.go.
type LanDispatcher struct {
	db       *sql.DB
	chDsp    ChannelDispatcher
	mu       sync.RWMutex
	identity NodeIdentity // nil in single-node mode; guarded by mu
}

func NewLanDispatcher(db *sql.DB, ch ChannelDispatcher) *LanDispatcher {
	return &LanDispatcher{db: db, chDsp: ch}
}

// NewLanDispatcherWithIdentity wires the cluster node identity so LAN rules
// respect the per-rule node_scope/node_ids as every other rule does.
func NewLanDispatcherWithIdentity(db *sql.DB, ch ChannelDispatcher, identity NodeIdentity) *LanDispatcher {
	return &LanDispatcher{db: db, chDsp: ch, identity: identity}
}

// SetIdentity replaces the node identity rules are scoped by: a live init or
// join makes a standalone node (nil identity: every rule applies) a cluster
// member. A typed nil must not be passed: it is not a nil interface.
func (d *LanDispatcher) SetIdentity(id NodeIdentity) {
	d.mu.Lock()
	d.identity = id
	d.mu.Unlock()
}

// currentIdentity is the identity rules are scoped by at this moment.
func (d *LanDispatcher) currentIdentity() NodeIdentity {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.identity
}

// lanRule is an enabled LAN rule that applies on this node.
type lanRule struct {
	id                       int
	name, typ, channels, sev string
	cooldown                 int
}

// Dispatch fires, for each enabled LAN rule that applies on this node, the
// alerts the scan makes for its type.
func (d *LanDispatcher) Dispatch(ctx context.Context, ev *AlertLanEvent) {
	rules, err := d.rules()
	if err != nil {
		logger().Warn("LAN alert rules query failed", "error", err)
		return
	}
	if d.chDsp == nil {
		return
	}
	for _, r := range rules {
		for _, f := range lanRules[r.typ](ev) {
			d.chDsp.Fire(ctx, AlertFire{
				RuleID:     r.id,
				RuleName:   r.name,
				Type:       r.typ,
				Severity:   ruleSeverity(r.typ, r.sev),
				Message:    f.message,
				ChannelIDs: r.channels,
				Cooldown:   r.cooldown,
				Subject:    f.subject,
			})
		}
	}
}

// rules are the enabled LAN rules that apply on this node, read before any
// fires: nothing waits on Fire with the rows open.
func (d *LanDispatcher) rules() ([]lanRule, error) {
	rows, err := d.db.Query(`SELECT id, name, type, channel_ids, severity, cooldown, node_scope, node_ids FROM alert_rules WHERE enabled=1 ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []lanRule
	for rows.Next() {
		var r lanRule
		var nodeScope, nodeIDs string
		if err := rows.Scan(&r.id, &r.name, &r.typ, &r.channels, &r.sev, &r.cooldown, &nodeScope, &nodeIDs); err != nil {
			return nil, err
		}
		if _, ok := lanRules[r.typ]; !ok {
			continue
		}
		if !ruleAppliesToNode(d.currentIdentity(), nodeScope, nodeIDs) {
			continue
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

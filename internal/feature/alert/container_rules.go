package alert

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// containerPatternCache memoises shell-glob → *regexp.Regexp translations.
// matchContainerPattern is called per (rule × event), so on a busy docker
// host with N alert rules and bursty container churn the per-call
// regexp.Compile dominated CPU under load. The cache key is the raw
// operator pattern; size is bounded by the number of distinct patterns
// across all alert_rules rows, which is small.
var (
	containerPatternCacheMu sync.RWMutex
	containerPatternCache   = map[string]*regexp.Regexp{}
)

// matchContainerPattern matches shell-style wildcards (* and ?) against a
// container name. Returns true on a full match. Other regex specials are
// quoted as literals — operators frequently put `.` in container names
// (e.g. `db.example.com`) so we can't let `.` mean "any character".
//
// Empty pattern never matches anything (avoid accidental "match all" via
// a misconfigured rule with no pattern).
func matchContainerPattern(pattern, name string) bool {
	if pattern == "" {
		return false
	}
	containerPatternCacheMu.RLock()
	re, ok := containerPatternCache[pattern]
	containerPatternCacheMu.RUnlock()
	if !ok {
		var b strings.Builder
		b.WriteByte('^')
		for _, r := range pattern {
			switch r {
			case '*':
				b.WriteString(".*")
			case '?':
				b.WriteByte('.')
			default:
				b.WriteString(regexp.QuoteMeta(string(r)))
			}
		}
		b.WriteByte('$')
		compiled, err := regexp.Compile(b.String())
		if err != nil {
			return false
		}
		containerPatternCacheMu.Lock()
		containerPatternCache[pattern] = compiled
		containerPatternCacheMu.Unlock()
		re = compiled
	}
	return re.MatchString(name)
}

// evaluateRestartLoop returns true when at least `threshold` of the
// supplied restart timestamps fall within the last `windowSec` seconds.
// Caller fetches the recent restart timestamps from container_events;
// this function is pure logic for testability.
func evaluateRestartLoop(restartTimesMillis []int64, threshold, windowSec int) bool {
	if threshold <= 0 || len(restartTimesMillis) < threshold {
		return false
	}
	cutoff := time.Now().Add(-time.Duration(windowSec) * time.Second).UnixMilli()
	count := 0
	for _, t := range restartTimesMillis {
		if t >= cutoff {
			count++
		}
	}
	return count >= threshold
}

// AlertFire is the complete payload handed to ChannelDispatcher.Fire so it
// can enforce cooldown, route to channels, and write alert_history.
type AlertFire struct {
	RuleID     int
	RuleName   string
	Type       string // alert rule type, e.g. "container_down"
	Severity   string
	Message    string
	ChannelIDs string // raw JSON from alert_rules.channel_ids
	Cooldown   int    // seconds
}

// ChannelDispatcher is implemented by the existing alert manager. We
// declare the interface here to avoid a feature/alert → alert/channels
// cycle in either direction.
type ChannelDispatcher interface {
	Fire(ctx context.Context, f AlertFire)
}

// AlertContainerEvent is the slim adapter shape passed in from monitor/
// without coupling alert/ to monitor.ContainerEvent type.
type AlertContainerEvent struct {
	ID, Name, Type string
	TS             int64
	ExitCode       *int
}

// ContainerDispatcher evaluates container alert rules whenever a matching
// container event is observed. Implements monitor.EventDispatcher via
// the Dispatch shim wired in main.go.
type ContainerDispatcher struct {
	db       *sql.DB
	chDsp    ChannelDispatcher
	identity NodeIdentity // nil in single-node mode
}

func NewContainerDispatcher(db *sql.DB, ch ChannelDispatcher) *ContainerDispatcher {
	return &ContainerDispatcher{db: db, chDsp: ch}
}

// NewContainerDispatcherWithIdentity wires the cluster node identity so
// container event rules respect the per-rule node_scope/node_ids the same
// way the periodic Manager.evaluate path does.
func NewContainerDispatcherWithIdentity(db *sql.DB, ch ChannelDispatcher, identity NodeIdentity) *ContainerDispatcher {
	return &ContainerDispatcher{db: db, chDsp: ch, identity: identity}
}

// Dispatch is the entry point — translates the event to alert evaluation.
func (d *ContainerDispatcher) Dispatch(ctx context.Context, ev *AlertContainerEvent) {
	rows, err := d.db.Query(`SELECT id, name, type, condition, channel_ids, severity, cooldown, node_scope, node_ids FROM alert_rules WHERE enabled=1 AND type IN ('container_down','container_oom','container_restart_loop','container_unhealthy')`)
	if err != nil {
		slog.Warn("container alert rules query failed", "error", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var id, cooldown int
		var name, typ, condStr, channelIDs, sev, nodeScope, nodeIDs string
		if err := rows.Scan(&id, &name, &typ, &condStr, &channelIDs, &sev, &cooldown, &nodeScope, &nodeIDs); err != nil {
			continue
		}
		if !ruleAppliesToNode(d.identity, nodeScope, nodeIDs) {
			continue
		}
		var cond struct {
			ContainerPattern string `json:"container_pattern"`
			ThresholdCount   int    `json:"threshold_count"`
			WindowSeconds    int    `json:"window_seconds"`
		}
		if err := json.Unmarshal([]byte(condStr), &cond); err != nil {
			slog.Warn("invalid container alert condition JSON", "rule", name, "error", err)
			continue
		}
		if !matchContainerPattern(cond.ContainerPattern, ev.Name) {
			continue
		}
		switch typ {
		case "container_down":
			// Only fire on `die`. Docker emits `oom` immediately followed
			// by `die` for OOM-kills, so accepting only `die` covers both
			// causes without producing duplicate fires.
			if ev.Type != "die" {
				continue
			}
		case "container_oom":
			if ev.Type != "oom" {
				continue
			}
			sev = "critical"
		case "container_restart_loop":
			if ev.Type != "restart" {
				continue
			}
			times, qerr := d.recentRestartTimes(ev.ID, cond.WindowSeconds)
			if qerr != nil || !evaluateRestartLoop(times, cond.ThresholdCount, cond.WindowSeconds) {
				continue
			}
		case "container_unhealthy":
			// Docker emits `health_status: unhealthy` (parsed to "unhealthy"
			// by monitor/docker_events.go) when a container's healthcheck
			// transitions from healthy/starting to unhealthy. Recovery emits
			// "healthy" — we don't fire on that.
			if ev.Type != "unhealthy" {
				continue
			}
		}
		if d.chDsp != nil {
			d.chDsp.Fire(ctx, AlertFire{
				RuleID:     id,
				RuleName:   name,
				Type:       typ,
				Severity:   sev,
				Message:    formatAlertMessage(typ, ev),
				ChannelIDs: channelIDs,
				Cooldown:   cooldown,
			})
		}
	}
}

func (d *ContainerDispatcher) recentRestartTimes(containerID string, windowSec int) ([]int64, error) {
	cutoff := time.Now().Add(-time.Duration(windowSec)*time.Second - time.Second).UnixMilli()
	rows, err := d.db.Query(
		`SELECT ts FROM container_events WHERE container_id=? AND event_type='restart' AND ts >= ? ORDER BY ts DESC LIMIT 50`,
		containerID, cutoff,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var t int64
		rows.Scan(&t)
		out = append(out, t)
	}
	return out, nil
}

func formatAlertMessage(typ string, ev *AlertContainerEvent) string {
	switch typ {
	case "container_down":
		exit := ""
		if ev.ExitCode != nil {
			exit = " (exit " + strconv.Itoa(*ev.ExitCode) + ")"
		}
		return "Container " + ev.Name + " stopped" + exit
	case "container_oom":
		return "Container " + ev.Name + " was OOM-killed"
	case "container_restart_loop":
		return "Container " + ev.Name + " is restart-looping"
	case "container_unhealthy":
		return "Container " + ev.Name + " healthcheck failed (unhealthy)"
	}
	return "Container " + ev.Name + " event: " + typ
}

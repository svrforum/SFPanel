package alert

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"time"
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
	re, err := regexp.Compile(b.String())
	if err != nil {
		return false
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
	db    *sql.DB
	chDsp ChannelDispatcher
}

func NewContainerDispatcher(db *sql.DB, ch ChannelDispatcher) *ContainerDispatcher {
	return &ContainerDispatcher{db: db, chDsp: ch}
}

// Dispatch is the entry point — translates the event to alert evaluation.
func (d *ContainerDispatcher) Dispatch(ctx context.Context, ev *AlertContainerEvent) {
	rows, err := d.db.Query(`SELECT id, name, type, condition, channel_ids, severity, cooldown FROM alert_rules WHERE enabled=1 AND type IN ('container_down','container_oom','container_restart_loop')`)
	if err != nil {
		slog.Warn("container alert rules query failed", "error", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var id, cooldown int
		var name, typ, condStr, channelIDs, sev string
		if err := rows.Scan(&id, &name, &typ, &condStr, &channelIDs, &sev, &cooldown); err != nil {
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
	}
	return "Container " + ev.Name + " event: " + typ
}

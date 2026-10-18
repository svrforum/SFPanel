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
// supplied crash timestamps fall within the last `windowSec` seconds.
// Caller fetches the recent crashes from container_events (recentCrashTimes);
// this function is pure logic for testability.
func evaluateRestartLoop(crashTimesMillis []int64, threshold, windowSec int) bool {
	if threshold <= 0 || len(crashTimesMillis) < threshold {
		return false
	}
	cutoff := time.Now().Add(-time.Duration(windowSec) * time.Second).UnixMilli()
	count := 0
	for _, t := range crashTimesMillis {
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
	// Container is the name of the container a container rule fired for,
	// and "" for any other rule. The cooldown runs per rule and container.
	Container string
}

// stopRequestWindow is how long before a die the kill event of a stop
// someone asked for can come. Docker records a kill event for every signal it
// sends on request — docker stop, kill and restart, rm -f, compose stop and
// down, the panel's own buttons — and none for a crash, an exit of the
// container's own or an OOM kill. A stop that is not obeyed within its
// timeout, 10 seconds unless set otherwise, ends in a SIGKILL: another kill
// event, just before the die. A minute covers the default and a one-minute
// stop_grace_period. A signal the container lives through, a SIGHUP reload
// say, cannot be told from a stop for a minute after it: a crash inside that
// minute counts as a stop.
const stopRequestWindow = 60 * time.Second

// requestedStop reports whether a die at ts was a stop someone asked for,
// given the container's last kill and last start before it (0 for none): a
// kill after the last start, and at most stopRequestWindow before the die. A
// start after the kill makes the die the next run's, as after a restart.
func requestedStop(kill, start, ts int64) bool {
	return kill > start && ts-kill <= stopRequestWindow.Milliseconds()
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

// containerRule is how Dispatch evaluates one container rule type.
type containerRule struct {
	event string // the event, as monitor/docker_events.go names it, that fires the rule
	// severity, when set, is the severity every rule of the type fires with,
	// whatever it was given (ruleSeverity).
	severity string
}

// containerRules are the rule types Dispatch evaluates. Together with
// hostMetricRules this is everything that is evaluated, and the handlers
// accept no other type (ruleTypeEvaluated).
var containerRules = map[string]containerRule{
	// Only `die`. Docker emits `oom` immediately followed by `die` for
	// OOM-kills, so accepting only `die` covers both causes without
	// producing duplicate fires.
	"container_down": {event: "die"},
	"container_oom":  {event: "oom", severity: "critical"},
	// Counted over the rule's window in crashes: see recentCrashTimes.
	"container_restart_loop": {event: "die"},
	// Docker emits `health_status: unhealthy` (parsed to "unhealthy" by
	// monitor/docker_events.go) when a container's healthcheck transitions
	// from healthy/starting to unhealthy. Recovery emits "healthy" — that
	// does not fire.
	"container_unhealthy": {event: "unhealthy"},
}

// ruleSeverity is the severity a rule of type typ fires with: its own, unless
// the type fixes one. It is also what such a rule is stored and listed with.
// The form offered a severity for an OOM rule, and the rule fired critical
// whatever was picked, while listing the severity it had been given.
func ruleSeverity(typ, own string) string {
	if fixed := containerRules[typ].severity; fixed != "" {
		return fixed
	}
	return own
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
	// Worked out once, before the rules are read: it is the same for each.
	requested := ev.Type == "die" && d.stopRequested(ev)

	rows, err := d.db.Query(`SELECT id, name, type, condition, channel_ids, severity, cooldown, node_scope, node_ids FROM alert_rules WHERE enabled=1`)
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
		if rule, ok := containerRules[typ]; !ok || ev.Type != rule.event {
			continue
		}
		if !ruleAppliesToNode(d.identity, nodeScope, nodeIDs) {
			continue
		}
		var cond struct {
			ContainerPattern string `json:"container_pattern"`
			ThresholdCount   int    `json:"threshold_count"`
			WindowSeconds    int    `json:"window_seconds"`
			// container_down: alert on a stop someone asked for too.
			IncludeIntentionalStops bool `json:"include_intentional_stops"`
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
			// A stop someone asked for is not an outage, unless the rule
			// says to report those too (#135).
			if requested && !cond.IncludeIntentionalStops {
				continue
			}
		case "container_restart_loop":
			// Counted in crashes; see recentCrashTimes.
			if requested {
				continue
			}
			times, qerr := d.recentCrashTimes(ev.ID, cond.WindowSeconds)
			if qerr != nil || !evaluateRestartLoop(times, cond.ThresholdCount, cond.WindowSeconds) {
				continue
			}
		}
		sev = ruleSeverity(typ, sev)
		if d.chDsp != nil {
			d.chDsp.Fire(ctx, AlertFire{
				RuleID:     id,
				RuleName:   name,
				Type:       typ,
				Severity:   sev,
				Message:    formatAlertMessage(typ, ev, requested),
				ChannelIDs: channelIDs,
				Cooldown:   cooldown,
				Container:  ev.Name,
			})
		}
	}
}

// stopRequested reports whether ev, a die, was a stop someone asked Docker
// for, from the kill and start events the events listener stored before it
// (see requestedStop). With no history to read it is a crash, and alerts.
func (d *ContainerDispatcher) stopRequested(ev *AlertContainerEvent) bool {
	var kill, start int64
	err := d.db.QueryRow(
		`SELECT COALESCE(MAX(CASE event_type WHEN 'kill' THEN ts END), 0), COALESCE(MAX(CASE event_type WHEN 'start' THEN ts END), 0)
		FROM container_events WHERE container_id=? AND event_type IN ('kill','start') AND ts BETWEEN ? AND ?`,
		ev.ID, ev.TS-stopRequestWindow.Milliseconds(), ev.TS,
	).Scan(&kill, &start)
	if err != nil {
		slog.Warn("container stop history query failed", "container", ev.Name, "error", err)
		return false
	}
	return requestedStop(kill, start, ev.TS)
}

// recentCrashTimes returns when containerID died in the last windowSec
// seconds other than by a stop someone asked for. Docker sends a restart event
// for docker restart and none when the restart policy brings a crashed
// container back, which is a die and then a start, so a crash loop is counted
// in crashes: counting restart events found the restarts someone asked for,
// and never a loop.
func (d *ContainerDispatcher) recentCrashTimes(containerID string, windowSec int) ([]int64, error) {
	cutoff := time.Now().Add(-time.Duration(windowSec)*time.Second - time.Second).UnixMilli()
	rows, err := d.db.Query(
		`SELECT event_type, ts FROM container_events WHERE container_id=? AND event_type IN ('kill','start','die') AND ts >= ? ORDER BY ts, id`,
		containerID, cutoff-stopRequestWindow.Milliseconds(),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var kill, start int64
	var out []int64
	for rows.Next() {
		var typ string
		var ts int64
		if err := rows.Scan(&typ, &ts); err != nil {
			return nil, err
		}
		switch typ {
		case "kill":
			kill = ts
		case "start":
			start = ts
		case "die":
			if ts >= cutoff && !requestedStop(kill, start, ts) {
				out = append(out, ts)
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func formatAlertMessage(typ string, ev *AlertContainerEvent, requested bool) string {
	switch typ {
	case "container_down":
		exit := ""
		if ev.ExitCode != nil {
			exit = " (exit " + strconv.Itoa(*ev.ExitCode) + ")"
		}
		if requested {
			return "Container " + ev.Name + " was stopped on request" + exit
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

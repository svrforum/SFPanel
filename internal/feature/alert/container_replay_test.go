package alert

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/docker/docker/api/types/events"
	sfdb "github.com/svrforum/SFPanel/internal/db"
	"github.com/svrforum/SFPanel/internal/monitor"
)

// testdata/docker_events.jsonl is what `docker events --format '{{json .}}'`
// printed on Docker 29.4.2 while throwaway alpine containers were stopped,
// killed, restarted and left to crash, one container per case. The last word
// of a container's name says which:
//
//	stop          docker stop; it exits 0 on SIGTERM, as compose stop's did in #135
//	stopslow      docker stop -t 2; PID 1 ignores SIGTERM, so a SIGKILL follows
//	kill          docker kill
//	rmf           docker rm -f, running
//	restartcrash  docker restart, and the new run exits 5 three seconds in
//	restart3      docker restart three times over, and the fourth run exits 6
//	              two seconds in
//	exit          exits 3 by itself
//	oom           OOM-killed in an 8 MiB cgroup; Docker sent no oom event for it
//	crashloop     exits 1 a second in, under restart: on-failure:2
//	hupcrash      docker kill -s HUP, which it lives through; exits 1 by itself
//	              64 seconds later, and Docker, taking the HUP for a stop, does
//	              not restart it under on-failure:2
//
// Docker records a kill event for every signal it sends on request and none
// for the other ways out, which is all there is to tell the two apart by.
func loadDockerEvents(t *testing.T) []events.Message {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", "docker_events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var msgs []events.Message
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var m events.Message
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("docker_events.jsonl: %v", err)
		}
		msgs = append(msgs, m)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	// Moved, every gap kept, to end now: a restart loop's window is
	// measured back from the present.
	shift := time.Now().UnixNano() - msgs[len(msgs)-1].TimeNano
	for i := range msgs {
		msgs[i].TimeNano += shift
		msgs[i].Time = msgs[i].TimeNano / int64(time.Second)
	}
	return msgs
}

// replayDB is a database opened the way the panel opens its own.
func replayDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sfdb.Open(filepath.Join(t.TempDir(), "sfpanel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

const replaySentinel = "replay-sentinel"

type replayClient struct{ msgs chan events.Message }

func (c *replayClient) Events(context.Context, events.ListOptions) (<-chan events.Message, <-chan error) {
	return c.msgs, nil
}

// replayShim is main.go's dispShim, and it reports the sentinel.
type replayShim struct {
	d       *ContainerDispatcher
	handled chan struct{}
}

func (s *replayShim) Dispatch(ctx context.Context, ev *monitor.ContainerEvent) {
	if ev.ContainerID == replaySentinel {
		s.handled <- struct{}{}
		return
	}
	s.d.Dispatch(ctx, &AlertContainerEvent{
		ID:       ev.ContainerID,
		Name:     ev.ContainerName,
		Type:     ev.EventType,
		TS:       ev.TS,
		ExitCode: ev.ExitCode,
	})
}

// replay runs the capture through the panel's own events listener — its
// parse, the container_events row it writes, and the dispatch main.go wires
// to it — into d, one message at a time, calling after once each is handled.
func replay(t *testing.T, db *sql.DB, d *ContainerDispatcher, after func()) {
	t.Helper()
	client := &replayClient{msgs: make(chan events.Message)}
	shim := &replayShim{d: d, handled: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	monitor.StartDockerEventsListener(ctx, db, client, shim)

	send := func(m events.Message) {
		t.Helper()
		select {
		case client.msgs <- m:
		case <-time.After(5 * time.Second):
			t.Fatal("the events listener stopped reading")
		}
	}
	for _, m := range loadDockerEvents(t) {
		send(m)
		// The listener takes one message at a time, so once this start —
		// stored and dispatched like any other — reaches the shim, the
		// message before it has been dealt with.
		send(events.Message{
			Type:     events.ContainerEventType,
			Action:   events.ActionStart,
			TimeNano: m.TimeNano,
			Actor:    events.Actor{ID: replaySentinel, Attributes: map[string]string{"name": replaySentinel}},
		})
		select {
		case <-shim.handled:
		case <-time.After(5 * time.Second):
			t.Fatal("the events listener stopped dispatching")
		}
		if after != nil {
			after()
		}
	}
}

// fireLog records every fire, in order.
type fireLog struct {
	mu    sync.Mutex
	fires []AlertFire
}

func (l *fireLog) Fire(_ context.Context, f AlertFire) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.fires = append(l.fires, f)
}

func (l *fireLog) messages() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]string, 0, len(l.fires))
	for _, f := range l.fires {
		out = append(out, short(f.Message))
	}
	return out
}

// short names the container in an alert message by the last word of its
// name: "Container sfp-evtest-13468-stop stopped (exit 0)" is
// "stop stopped (exit 0)".
func short(msg string) string {
	parts := strings.SplitN(msg, " ", 3)
	if len(parts) < 3 {
		return msg
	}
	return parts[1][strings.LastIndex(parts[1], "-")+1:] + " " + parts[2]
}

func insertContainerRule(t *testing.T, db *sql.DB, typ, condition string, cooldown int) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO alert_rules (name, type, condition, channel_ids, severity, cooldown) VALUES (?, ?, ?, '[1]', 'warning', ?)`,
		typ, typ, condition, cooldown); err != nil {
		t.Fatal(err)
	}
}

// TestContainerDown_RequestedStopIsNotAnOutage: a down rule alerted on every
// die, so a stop someone asked for — docker stop, a compose stop around a
// backup, a restart — alerted as a crash does (#135). Every die missing
// below came a little after a kill event and after the container's last
// start; TestContainerDown_IncludeIntentionalStops replays the same events
// into a rule that differs by its option alone, and alerts on each of them.
func TestContainerDown_RequestedStopIsNotAnOutage(t *testing.T) {
	db := replayDB(t)
	insertContainerRule(t, db, "container_down", `{"container_pattern":"*"}`, 0)
	log := &fireLog{}
	replay(t, db, NewContainerDispatcher(db, log), nil)

	want := []string{
		"oom stopped (exit 137)",
		"crashloop stopped (exit 1)",
		"exit stopped (exit 3)",
		"crashloop stopped (exit 1)",
		"crashloop stopped (exit 1)",
		// The kills were for the runs before the restarts.
		"restartcrash stopped (exit 5)",
		"restart3 stopped (exit 6)",
		// The HUP it lived through was more than a minute before.
		"hupcrash stopped (exit 1)",
	}
	if got := log.messages(); !reflect.DeepEqual(got, want) {
		t.Errorf("container_down fired for\n  %s\nwant\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}

// TestContainerDown_IncludeIntentionalStops: the rule's option alerts on the
// requested stops as well, and the message says which they were.
func TestContainerDown_IncludeIntentionalStops(t *testing.T) {
	db := replayDB(t)
	insertContainerRule(t, db, "container_down", `{"container_pattern":"*","include_intentional_stops":true}`, 0)
	log := &fireLog{}
	replay(t, db, NewContainerDispatcher(db, log), nil)

	want := []string{
		"oom stopped (exit 137)",
		"crashloop stopped (exit 1)",
		"kill was stopped on request (exit 137)",
		"rmf was stopped on request (exit 137)",
		"exit stopped (exit 3)",
		"restartcrash was stopped on request (exit 0)",
		"stop was stopped on request (exit 0)",
		"restart3 was stopped on request (exit 0)",
		"crashloop stopped (exit 1)",
		"stopslow was stopped on request (exit 137)",
		"restart3 was stopped on request (exit 0)",
		"crashloop stopped (exit 1)",
		"restart3 was stopped on request (exit 0)",
		"restartcrash stopped (exit 5)",
		"restart3 stopped (exit 6)",
		"hupcrash stopped (exit 1)",
	}
	if got := log.messages(); !reflect.DeepEqual(got, want) {
		t.Errorf("container_down fired for\n  %s\nwant\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}

// TestRestartLoop_CountsCrashes: Docker sends a restart event for docker
// restart and none when the restart policy brings a crashed container back,
// which is a die and then a start. A rule counting restart events alerted on
// three restarts someone asked for, and never on a crash loop. It counts the
// dies a down rule alerts on: with a threshold of one it fires for each of
// those and nothing else, and with three for the one container that crashed
// three times.
func TestRestartLoop_CountsCrashes(t *testing.T) {
	db := replayDB(t)
	insertContainerRule(t, db, "container_restart_loop", `{"container_pattern":"*","threshold_count":3,"window_seconds":300}`, 0)
	insertContainerRule(t, db, "container_restart_loop", `{"container_pattern":"*","threshold_count":1,"window_seconds":300}`, 0)
	log := &fireLog{}
	replay(t, db, NewContainerDispatcher(db, log), nil)

	got := map[int][]string{}
	log.mu.Lock()
	for _, f := range log.fires {
		got[f.RuleID] = append(got[f.RuleID], short(f.Message))
	}
	log.mu.Unlock()
	want := map[int][]string{
		1: {"crashloop is restart-looping"},
		2: {
			"oom is restart-looping",
			"crashloop is restart-looping",
			"exit is restart-looping",
			"crashloop is restart-looping",
			"crashloop is restart-looping",
			"restartcrash is restart-looping",
			"restart3 is restart-looping",
			"hupcrash is restart-looping",
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("container_restart_loop fired, by rule id, for\n  %v\nwant\n  %v", got, want)
	}
}

// TestContainerDown_CooldownRunsPerContainer: the cooldown ran per rule, so
// under a "*" rule one container's alert held back every other container's
// for its length (#135). It runs per rule and container: the other
// containers' crashes are delivered, and a second crash of the same
// container inside its cooldown still is not.
func TestContainerDown_CooldownRunsPerContainer(t *testing.T) {
	db := replayDB(t)
	insertContainerRule(t, db, "container_down", `{"container_pattern":"*"}`, 300)
	m := NewManager(db)
	var delivered []string
	m.deliver = func(f AlertFire) []string {
		delivered = append(delivered, short(f.Message))
		return []string{"stub"}
	}
	// Each fire is sent before the next event arrives, as the workers would.
	drain := func() {
		for {
			select {
			case f := <-m.fireQueue:
				m.runFire(f)
			default:
				return
			}
		}
	}
	replay(t, db, NewContainerDispatcher(db, m), drain)

	want := []string{
		"oom stopped (exit 137)",
		"crashloop stopped (exit 1)",
		"exit stopped (exit 3)",
		"restartcrash stopped (exit 5)",
		"restart3 stopped (exit 6)",
		"hupcrash stopped (exit 1)",
	}
	if !reflect.DeepEqual(delivered, want) {
		t.Errorf("delivered\n  %s\nwant\n  %s", strings.Join(delivered, "\n  "), strings.Join(want, "\n  "))
	}
}

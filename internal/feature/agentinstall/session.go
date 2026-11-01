package agentinstall

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/cluster"
	"github.com/svrforum/SFPanel/internal/sshexec"
)

// The phases of a row, its run steps: the screen names them.
const (
	phaseConnect = iota
	phasePreflight
	phaseInstall
	phaseRegister
	phaseVerify
	phases
)

// What a row waits for the operator on (RunNodeRecord.Awaiting).
const (
	awaitHostKey    = "host_key"
	awaitCheck      = "check"
	awaitStandalone = "standalone"
	awaitReview     = "review"
)

// session is one install in this leader's memory: its record as it stands,
// its rows, and the credentials, which nothing else ever holds.
type session struct {
	e        *Engine
	env      Env
	id       string
	version  string
	archives string
	mode     string
	review   bool

	// ctx ends when the install is stopped or its session dropped; every
	// row and recheck runs under it.
	ctx    context.Context
	cancel context.CancelFunc
	// slots holds a token for each row being worked on: Concurrency at most.
	slots chan struct{}
	// gone is closed when the session is dropped.
	gone     chan struct{}
	goneOnce sync.Once

	// mu guards everything below and every row's rec.
	mu      sync.Mutex
	auth    sshexec.Auth
	sudoPw  []byte
	head    cluster.RunRecord
	rows    []*row
	byKey   map[string]*row
	running int
	stopped bool
	// credsGone says the credentials were zeroed; sweeping that a sweeper
	// waits for CredentialsUntil.
	credsGone bool
	sweeping  bool
	// ips and names are the addresses and node names rows have claimed, so
	// two servers of one install never register as one.
	ips, names map[string]string

	// rel guards what the release gave, fetched once per session.
	rel      sync.Mutex
	sums     []byte
	sumsErr  error
	sumsDone bool
	archive  map[string][]byte
}

func newSession(e *Engine, r Request, version, archives string) *session {
	ctx, cancel := context.WithCancel(context.Background())
	now := e.env.Now()
	s := &session{
		e: e, env: e.env, id: cluster.NewRunID(now), version: version, archives: archives,
		mode: r.Mode, review: r.Review,
		ctx: ctx, cancel: cancel, slots: make(chan struct{}, r.Concurrency), gone: make(chan struct{}),
		auth: sshexec.Auth{User: r.Account.User, Method: r.Account.Method,
			Password: r.Account.Password, Key: r.Account.Key, Passphrase: r.Account.Passphrase},
		byKey: map[string]*row{}, ips: map[string]string{}, names: map[string]string{},
		archive: map[string][]byte{},
	}
	switch {
	case r.Account.SudoSame:
		// The login password itself: zeroed with it.
		s.sudoPw = r.Account.Password
		clear(r.Account.SudoPassword)
	default:
		s.sudoPw = r.Account.SudoPassword
	}
	if archives != "" {
		// An unreleased version's archive comes from the leader's directory.
		s.mode = ModeOffline
	}
	var keys []string
	for i, srv := range r.Servers {
		key := rowKey(i)
		keys = append(keys, key)
		rw := &row{s: s, key: key, srv: srv, answer: make(chan bool, 1)}
		rw.rec = cluster.RunNodeRecord{Status: cluster.RunNodePending, Host: srv.Host, Name: srv.Name, Group: srv.Group}
		for p := 0; p < phases; p++ {
			rw.rec.Steps = append(rw.rec.Steps, cluster.RunStepRecord{Status: cluster.RunStepPending, JobID: jobID(key, p)})
			rw.logs[p] = &phaseLog{}
		}
		s.rows = append(s.rows, rw)
		s.byKey[key] = rw
	}
	s.head = cluster.RunRecord{
		ID: s.id, Kind: cluster.RunKindAgentInstall, TaskID: cluster.AgentInstallTaskID, TaskName: installTaskName,
		Inputs: map[string]string{"mode": r.Mode, "review": strconv.FormatBool(r.Review), "version": version},
		Nodes:  keys, Waves: [][]string{keys}, Batch: r.Concurrency,
		Status: cluster.RunRunning, StartedBy: r.StartedBy, StartedAt: now,
	}
	return s
}

// jobID is the job ID a row's phase records, which the output route asks
// the session for.
func jobID(key string, phase int) string { return "install/" + key + "/" + strconv.Itoa(phase) }

// active reports whether the install has rows or a recheck under way.
func (s *session) active() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running > 0
}

// record writes the run and its rows as they start.
func (s *session) record() error {
	s.mu.Lock()
	head := s.head
	rows := make(map[string]cluster.RunNodeRecord, len(s.rows))
	for _, r := range s.rows {
		rows[r.key] = r.rec
	}
	s.running = len(s.rows)
	s.mu.Unlock()
	if err := s.env.PutRun(head); err != nil {
		return err
	}
	for _, r := range s.rows {
		if err := s.env.PutRunNode(s.id, r.key, rows[r.key]); err != nil {
			return err
		}
	}
	return nil
}

// launch starts every row; Concurrency of them work at once.
func (s *session) launch() {
	for _, r := range s.rows {
		go r.run()
	}
}

// putHead and putRow write the record; a leader that lost its role can
// write nothing more, so the session stops.
func (s *session) putHead(head cluster.RunRecord) {
	s.written(s.env.PutRun(head))
}

func (s *session) putRow(key string, rec cluster.RunNodeRecord) {
	s.written(s.env.PutRunNode(s.id, key, rec))
}

func (s *session) written(err error) {
	if err == nil {
		return
	}
	if errors.Is(err, cluster.ErrNotLeader) {
		logger().Warn("agent install stops: this node no longer leads", "run", s.id)
		s.cancel()
		return
	}
	logger().Warn("agent install record not written", "run", s.id, "error", err)
}

// rowDone is a row's or a recheck's end: the last one ends the install.
func (s *session) rowDone() {
	s.mu.Lock()
	s.running--
	last := s.running == 0
	s.mu.Unlock()
	if last {
		s.finish()
	}
}

// finish ends the install's head: failed when a row failed, stopped when
// the operator stopped it, succeeded otherwise. The credentials go at once
// unless a registered row failed, for which they stay keep longer, for a
// recheck or a revert.
func (s *session) finish() {
	s.mu.Lock()
	failed, keep := false, false
	for _, r := range s.rows {
		if r.rec.Status == cluster.RunNodeFailed {
			failed = true
			if r.rec.NodeID != "" && !r.reverted {
				keep = true
			}
		}
	}
	now := s.env.Now()
	switch {
	case s.stopped:
		s.head.Status = cluster.RunStopped
	case failed:
		s.head.Status = cluster.RunFailed
	default:
		s.head.Status = cluster.RunSucceeded
	}
	s.head.EndedAt = now
	if keep && !s.stopped && s.ctx.Err() == nil && !s.credsGone {
		if s.head.CredentialsUntil.IsZero() {
			s.head.CredentialsUntil = now.Add(s.e.t.keep)
		}
		if !s.sweeping {
			s.sweeping = true
			go s.sweep()
		}
	} else {
		s.wipeLocked()
	}
	head := s.head
	s.mu.Unlock()
	s.putHead(head)
	s.env.Audit(head.StartedBy, "TASK", auditPath(s.id)+"#end:"+head.Status, 200)
}

// sweep drops the credentials once CredentialsUntil passes by the leader's
// clock.
func (s *session) sweep() {
	tick := time.NewTicker(s.e.t.sweep)
	defer tick.Stop()
	for {
		select {
		case <-s.gone:
			return
		case <-tick.C:
		}
		s.mu.Lock()
		if s.credsGone {
			s.mu.Unlock()
			return
		}
		if s.credentialsLocked() {
			s.mu.Unlock()
			continue
		}
		head := s.head
		s.mu.Unlock()
		s.putHead(head)
		return
	}
}

// credentialsLocked reports whether the credentials are still held, and
// drops them, clearing CredentialsUntil, once their time has passed.
func (s *session) credentialsLocked() bool {
	if s.credsGone {
		return false
	}
	if !s.head.CredentialsUntil.IsZero() && !s.env.Now().Before(s.head.CredentialsUntil) {
		s.wipeLocked()
		return false
	}
	return true
}

// wipeLocked zeroes the credentials and forgets when they were to go.
func (s *session) wipeLocked() {
	s.auth.Wipe()
	clear(s.sudoPw)
	s.credsGone = true
	s.head.CredentialsUntil = time.Time{}
}

// sudoPassword is the sudo password, nil once it is gone.
func (s *session) sudoPassword() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.credsGone {
		return nil
	}
	return s.sudoPw
}

// stop is the operator's stop: rows under way end stopped and the
// credentials go when they have; an install that has ended only drops what
// it kept.
func (s *session) stop(by string) {
	s.mu.Lock()
	if s.running == 0 {
		had := !s.credsGone
		s.wipeLocked()
		head := s.head
		s.mu.Unlock()
		s.cancel()
		if had {
			s.putHead(head)
		}
		return
	}
	s.stopped = true
	s.head.Status = cluster.RunStopping
	s.head.StoppedBy = by
	head := s.head
	s.mu.Unlock()
	s.putHead(head)
	s.cancel()
}

// drop is the session's end in memory: another install replaced it, or its
// record could not be written. Its credentials go now.
func (s *session) drop() {
	s.goneOnce.Do(func() { close(s.gone) })
	s.cancel()
	s.mu.Lock()
	kept := !s.head.CredentialsUntil.IsZero()
	s.wipeLocked()
	head := s.head
	s.mu.Unlock()
	if kept && !head.Active() {
		s.putHead(head)
	}
}

// login is a copy of the account for one Dial, sharing its bytes: Wipe on
// the session's zeroes them for every copy.
func (s *session) login() (sshexec.Auth, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.credsGone {
		return sshexec.Auth{}, false
	}
	return s.auth, true
}

// checksums is the release's verified checksums.txt, or the archive
// directory's for an unreleased version: fetched once per session.
func (s *session) checksums(ctx context.Context) ([]byte, error) {
	s.rel.Lock()
	defer s.rel.Unlock()
	if !s.sumsDone {
		if s.archives != "" {
			s.sums, s.sumsErr = readArchiveFile(s.archives, "checksums.txt")
		} else {
			s.sums, s.sumsErr = s.env.Checksums(ctx, s.version)
		}
		s.sumsDone = s.sumsErr == nil
	}
	return s.sums, s.sumsErr
}

// archiveFor is the release archive for arch, read from the archive
// directory for an unreleased version: one per CPU per session.
func (s *session) archiveFor(ctx context.Context, arch string, sums []byte) ([]byte, error) {
	s.rel.Lock()
	defer s.rel.Unlock()
	if a, ok := s.archive[arch]; ok {
		return a, nil
	}
	var a []byte
	var err error
	if s.archives != "" {
		a, err = readArchiveFile(s.archives, archiveName(s.version, arch))
	} else {
		a, err = s.env.Archive(ctx, s.version, arch, sums)
	}
	if err != nil {
		return nil, err
	}
	s.archive[arch] = a
	return a, nil
}

// claim records that row key takes kind value (an IP or a node name) and
// names the row that took it first, if another did.
func (s *session) claim(taken map[string]string, value, key string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if other, ok := taken[value]; ok && other != key {
		return other
	}
	taken[value] = key
	return ""
}

// rowFail is a row's end with a reason code and its details.
type rowFail struct {
	code    string
	message string
	details map[string]string
}

func (f *rowFail) Error() string { return f.code + ": " + f.message }

func fail(code, message string, details ...string) *rowFail {
	f := &rowFail{code: code, message: message}
	if len(details) > 0 {
		f.details = map[string]string{}
		for i := 0; i+1 < len(details); i += 2 {
			f.details[details[i]] = details[i+1]
		}
	}
	return f
}

var (
	// errRejected is the operator turning a server's host key down.
	errRejected = errors.New("the operator rejected the host key")
	// errDeclined is the operator declining to convert a standalone panel.
	errDeclined = errors.New("the operator declined to convert the standalone panel")
)

// phaseLog is one phase's output on one row, sanitized line by line and
// capped at logCap.
type phaseLog struct {
	mu      sync.Mutex
	buf     []byte
	partial []byte
	cut     bool
}

const (
	logCap     = 1 << 20
	logCutNote = "\n[output cut at 1 MiB]\n"
)

func (l *phaseLog) write(p []byte) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.partial = append(l.partial, p...)
	if i := bytes.LastIndexByte(l.partial, '\n'); i >= 0 {
		l.add(response.SanitizeLogPiece(string(l.partial[:i+1])))
		l.partial = append([]byte(nil), l.partial[i+1:]...)
	}
	if len(l.partial) > 64<<10 {
		l.add(response.SanitizeLogPiece(string(l.partial)))
		l.partial = nil
	}
}

func (l *phaseLog) printf(format string, args ...any) {
	l.write([]byte(fmt.Sprintf(format, args...) + "\n"))
}

// flush adds a last line that never ended.
func (l *phaseLog) flush() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.partial) > 0 {
		l.add(response.SanitizeLogPiece(string(l.partial)) + "\n")
		l.partial = nil
	}
}

func (l *phaseLog) add(s string) {
	if l.cut {
		return
	}
	if room := logCap - len(logCutNote) - len(l.buf); len(s) > room {
		l.buf = append(l.buf, s[:max(room, 0)]...)
		l.buf = append(l.buf, logCutNote...)
		l.cut = true
		return
	}
	l.buf = append(l.buf, s...)
}

// from is the output from byte skip on and the offset after it.
func (l *phaseLog) from(skip int64) (string, int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := int64(len(l.buf))
	if skip < 0 || skip > n {
		skip = n
	}
	return string(l.buf[skip:]), n
}

// mergeDetails adds more to a row's reason details.
func mergeDetails(d map[string]string, more map[string]string) map[string]string {
	out := maps.Clone(d)
	if out == nil {
		out = map[string]string{}
	}
	maps.Copy(out, more)
	return out
}

// ipKey is an IP as rows claim it.
func ipKey(ip net.IP) string { return ip.String() }

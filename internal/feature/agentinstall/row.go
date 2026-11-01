package agentinstall

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/cluster"
	"github.com/svrforum/SFPanel/internal/release"
	"github.com/svrforum/SFPanel/internal/sshexec"
)

// row is one server of an install: its record, its phase logs, and what
// its goroutine learnt on the way. A recheck or a revert reads the latter
// only once the row has ended.
type row struct {
	s   *session
	key string
	srv Server
	// rec is guarded by s.mu; wmu orders the row's writes of it.
	rec  cluster.RunNodeRecord
	wmu  sync.Mutex
	logs [phases]*phaseLog
	// answer carries the operator's answer to what the row waits on.
	answer chan bool
	// fetched is the host key the server presented, guarded by s.mu.
	fetched sshexec.HostKey

	ip       net.IP
	addr     string
	loginOut []byte
	login    Login
	joinAddr string
	name     string
	online   bool
	arch     string
	// sfpanelBefore and optStacksBefore are what the pre-flight found
	// before this install touched the server.
	sfpanelBefore, optStacksBefore bool

	tgt  Target
	held bool
	// tmp is the row's directory on the server; unit the install's
	// systemd unit, started (installStarted) and seen to end
	// (installExited).
	tmp                           string
	unit                          string
	installStarted, installExited bool
	// token is the row's join token, until it is revoked.
	token []byte

	// verifying and reverted are guarded by s.mu.
	verifying, reverted bool
}

// update changes the row's record and writes it.
func (r *row) update(fn func(rec *cluster.RunNodeRecord)) {
	r.wmu.Lock()
	defer r.wmu.Unlock()
	r.s.mu.Lock()
	fn(&r.rec)
	rec := copyRec(r.rec)
	r.s.mu.Unlock()
	r.s.putRow(r.key, rec)
}

func copyRec(rec cluster.RunNodeRecord) cluster.RunNodeRecord {
	rec.Steps = slices.Clone(rec.Steps)
	rec.ReasonDetails = maps.Clone(rec.ReasonDetails)
	return rec
}

// answerLocked hands the operator's answer to the row waiting for it.
func (r *row) answerLocked(v bool) {
	r.rec.Awaiting = ""
	select {
	case r.answer <- v:
	default:
	}
}

func (r *row) acquire(ctx context.Context) error {
	if r.held {
		return nil
	}
	select {
	case r.s.slots <- struct{}{}:
		r.held = true
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *row) release() {
	if r.held {
		<-r.s.slots
		r.held = false
	}
}

// run works the row through its phases, then ends it. The slot is held
// through the end's clean-up, which is work on the server too.
func (r *row) run() {
	defer r.s.rowDone()
	err := r.work(r.s.ctx)
	r.end(err)
	r.release()
}

func (r *row) work(ctx context.Context) error {
	if err := r.acquire(ctx); err != nil {
		return err
	}
	r.update(func(rec *cluster.RunNodeRecord) { rec.Status = cluster.RunNodeRunning })
	for p, fn := range []func(context.Context) error{r.connect, r.preflight, r.install, r.register, r.verify} {
		if err := r.phase(ctx, p, fn); err != nil {
			return err
		}
	}
	return nil
}

// phase runs one phase as the row's step p.
func (r *row) phase(ctx context.Context, p int, fn func(context.Context) error) error {
	now := r.s.env.Now()
	r.update(func(rec *cluster.RunNodeRecord) {
		rec.Steps[p].Status, rec.Steps[p].StartedAt, rec.Steps[p].EndedAt = cluster.RunStepRunning, now, time.Time{}
	})
	err := fn(ctx)
	r.logs[p].flush()
	if err != nil {
		r.logs[p].printf("%s", err)
	}
	now = r.s.env.Now()
	r.update(func(rec *cluster.RunNodeRecord) {
		rec.Steps[p].Status, rec.Steps[p].EndedAt = stepStatus(err), now
	})
	return err
}

func stepStatus(err error) string {
	var f *rowFail
	switch {
	case err == nil:
		return cluster.RunStepSucceeded
	case errors.As(err, &f):
		return cluster.RunStepFailed
	case errors.Is(err, errRejected), errors.Is(err, errDeclined), errors.Is(err, context.Canceled):
		return cluster.RunStepCancelled
	}
	return cluster.RunStepFailed
}

// end ends the row whatever came of it. It revokes the row's token first,
// so no join can come after it. A row whose install started and that ended
// short of a registration is then looked for in the cluster (unwind): an
// agent at its IP is its own and stays, and only a row with none is cleaned.
// In every case it removes the row's directory and closes its connection.
func (r *row) end(err error) {
	ctx, cancel := context.WithTimeout(context.Background(), r.s.e.t.cleanup)
	defer cancel()
	r.revokeToken()
	r.s.mu.Lock()
	registered := r.rec.NodeID != ""
	r.s.mu.Unlock()
	if err != nil && !registered && r.installStarted {
		r.unwind(ctx)
	}
	r.removeTemp(ctx)
	r.closeTarget()
	for _, l := range r.logs {
		l.flush()
	}

	r.update(func(rec *cluster.RunNodeRecord) {
		rec.Awaiting, rec.CheckURL = "", ""
		var f *rowFail
		switch {
		case err == nil:
			rec.Status = cluster.RunNodeSucceeded
		case errors.As(err, &f):
			rec.Status, rec.Reason, rec.ReasonDetails = cluster.RunNodeFailed, f.code, maps.Clone(f.details)
		case errors.Is(err, errDeclined):
			rec.Status = cluster.RunNodeSkipped
		case errors.Is(err, errRejected), errors.Is(err, context.Canceled):
			stopRecord(rec)
		default:
			rec.Status, rec.Reason = cluster.RunNodeFailed, response.ErrAgentInstallFailed
			rec.ReasonDetails = map[string]string{"error": err.Error()}
		}
		for i := range rec.Steps {
			if rec.Steps[i].Status == cluster.RunStepPending {
				rec.Steps[i].Status = cluster.RunStepSkipped
			}
		}
	})
}

// stopRecord ends a row stopped by the operator. One that registered keeps
// its node, which stays in the cluster until the row is reverted, and says
// so (AGENT_INSTALL_STOPPED).
func stopRecord(rec *cluster.RunNodeRecord) {
	rec.Status = cluster.RunNodeStopped
	if rec.NodeID != "" {
		rec.Reason, rec.ReasonDetails = response.ErrAgentInstallStopped, map[string]string{"registered": "1"}
	}
}

// connect is phase 0: the server's address, its host key approved by the
// operator before anything is sent to it, the login, and what the login can
// do.
func (r *row) connect(ctx context.Context) error {
	s, log := r.s, r.logs[phaseConnect]
	ip, err := s.e.resolve(ctx, r.srv.Host)
	if err != nil {
		return fail(response.ErrAgentInstallUnreachable, "the host does not resolve to an address", "host", r.srv.Host)
	}
	r.ip = ip
	r.addr = net.JoinHostPort(ip.String(), strconv.Itoa(r.srv.Port))
	r.update(func(rec *cluster.RunNodeRecord) { rec.IP = ip.String() })
	if other := s.claim(s.ips, ipKey(ip), r.key); other != "" {
		return fail(response.ErrAgentInstallIdentityTaken, "another server of this install has the same address", "ip", ip.String(), "row", other)
	}

	log.printf("connecting to %s", r.addr)
	auth, ok := s.login()
	if !ok {
		return fail(response.ErrAgentInstallCredentialsGone, "the credentials are gone")
	}
	hk, err := s.e.dialer.FetchHostKey(ctx, r.addr, auth.User)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fail(response.ErrAgentInstallUnreachable, err.Error(), "address", r.addr)
	}
	text := hostKeyText(hk)
	s.mu.Lock()
	r.fetched = hk
	s.mu.Unlock()
	log.printf("host key %s: waiting for the operator", text)
	accepted, err := r.wait(ctx, awaitHostKey, func(rec *cluster.RunNodeRecord) { rec.HostKey = text })
	if err != nil {
		return err
	}
	if !accepted {
		return errRejected
	}
	log.printf("host key approved")
	if err := r.dial(ctx, log); err != nil {
		return err
	}

	out, stderr, exit, err := r.tgt.Run(ctx, "sh -s", []byte(LoginCheckScript()))
	if err != nil {
		return r.runErr(ctx, err)
	}
	l, err := ParseLogin(out)
	if err != nil {
		log.write(stderr)
		return fail(response.ErrAgentInstallFailed, err.Error(), "step", "login_check", "exit", strconv.Itoa(exit))
	}
	r.login, r.loginOut = l, out
	if v := JudgeLogin(l, len(s.sudoPassword()) > 0); v.Code != "" {
		return &rowFail{code: v.Code, message: v.Message, details: v.Details}
	}
	log.printf("logged in as %s: uid %d, sudo %s", auth.User, l.UID, l.Sudo)
	return nil
}

// dial logs in to the server against its approved key. A Tailscale SSH
// check holds the login: the row shows its address and waits, up to
// checkWait.
func (r *row) dial(ctx context.Context, log *phaseLog) error {
	s := r.s
	auth, ok := s.login()
	if !ok {
		return fail(response.ErrAgentInstallCredentialsGone, "the credentials are gone")
	}
	s.mu.Lock()
	key := r.fetched
	s.mu.Unlock()
	dctx, cancel := context.WithTimeout(ctx, s.e.t.checkWait)
	defer cancel()
	var checked atomic.Bool
	notice := func(url string) {
		checked.Store(true)
		log.printf("the server asks for a check at %s", url)
		r.update(func(rec *cluster.RunNodeRecord) { rec.Awaiting, rec.CheckURL = awaitCheck, url })
	}
	t, err := s.e.dialer.Dial(dctx, r.addr, auth, key, notice)
	if checked.Load() {
		r.update(func(rec *cluster.RunNodeRecord) { rec.Awaiting, rec.CheckURL = "", "" })
	}
	if err != nil {
		switch {
		case errors.Is(err, sshexec.ErrHostKeyChanged):
			return fail(response.ErrAgentInstallHostKeyChanged, "the server presented another host key than the one approved", "host_key", hostKeyText(key))
		case errors.Is(err, sshexec.ErrAuthFailed):
			return fail(response.ErrAgentInstallAuthFailed, "the server refused the login")
		case ctx.Err() != nil:
			return ctx.Err()
		case dctx.Err() != nil:
			return fail(response.ErrAgentInstallAuthFailed, "the login was not approved in time", "check", "timeout")
		}
		return fail(response.ErrAgentInstallUnreachable, err.Error(), "address", r.addr)
	}
	r.tgt = t
	return nil
}

func (r *row) closeTarget() {
	if r.tgt != nil {
		_ = r.tgt.Close()
		r.tgt = nil
	}
}

// errSudoRefused is sudo turning the sudo password down.
var errSudoRefused = errors.New("sudo refused the password")

// sudoLine is cmd as root for this login: as it is for root, through sudo
// -n where sudo asks nothing (a password line would reach cmd's stdin
// there), and with the sudo password otherwise.
func (r *row) sudoLine(cmd string) (string, []byte) {
	switch {
	case r.login.Root():
		return cmd, nil
	case r.login.Sudo == SudoNoPassword:
		return sshexec.SudoCommand(cmd, false, nil)
	}
	return sshexec.SudoCommand(cmd, false, r.s.sudoPassword())
}

// priv runs cmd as root, stdin after the sudo password's line when there is
// one; both copies of the password are zeroed after.
func (r *row) priv(ctx context.Context, cmd string, stdin []byte) (stdout, stderr []byte, exit int, err error) {
	line, head := r.sudoLine(cmd)
	in := stdin
	if head != nil {
		in = append(head, stdin...)
		defer clear(head)
		defer clear(in)
	}
	stdout, stderr, exit, err = r.tgt.Run(ctx, line, in)
	if err == nil && head != nil && exit == 1 && sudoRefused(stderr) {
		return stdout, stderr, exit, errSudoRefused
	}
	return stdout, stderr, exit, err
}

// sudoRefused is sudo's answer to a wrong password: "Sorry, try again."
// and "N incorrect password attempts".
func sudoRefused(stderr []byte) bool {
	s := strings.ToLower(string(stderr))
	return strings.Contains(s, "incorrect password") || strings.Contains(s, "try again")
}

// runErr is a row's end for a command that could not be run.
func (r *row) runErr(ctx context.Context, err error) error {
	switch {
	case ctx.Err() != nil:
		return ctx.Err()
	case errors.Is(err, errSudoRefused):
		return fail(response.ErrAgentInstallNotRoot, "sudo refused the sudo password", "sudo", "refused")
	}
	return fail(response.ErrAgentInstallFailed, err.Error(), "step", "ssh")
}

// preflight is phase 1: the probe, the verdict, the cluster's own gates,
// and the operator's word on a standalone panel and, in a review, on going
// on.
func (r *row) preflight(ctx context.Context) error {
	s, log := r.s, r.logs[phasePreflight]
	join, err := s.env.JoinAddressFor(r.ip)
	if err != nil {
		return clusterFail(err, "", r.ip)
	}
	r.joinAddr = join
	hasSudoPw := len(s.sudoPassword()) > 0

	script := ProbeScript([]string{join}, s.version, s.mode != ModeOffline)
	start := time.Now()
	out, stderr, exit, err := r.priv(ctx, "bash -s", []byte(script))
	rtt := time.Since(start)
	leaderNow := s.env.Now()
	if err != nil {
		return r.runErr(ctx, err)
	}
	log.write(out)
	log.write(stderr)
	facts, err := ParseFacts(append(slices.Clone(r.loginOut), out...))
	if err != nil {
		return fail(response.ErrAgentInstallFailed, err.Error(), "step", "probe", "exit", strconv.Itoa(exit))
	}
	v := Judge(facts, JudgeInput{IP: r.ip, Name: r.srv.Name, Version: s.version, Managers: []string{join},
		LeaderNow: leaderNow, RTT: rtt, Mode: s.mode, SudoPassword: hasSudoPw})
	if v.Code != "" {
		return &rowFail{code: v.Code, message: v.Message, details: v.Details}
	}
	r.name, r.online, r.arch = v.Name, v.Online, facts.ReleaseArch()
	r.sfpanelBefore, r.optStacksBefore = facts.SFPanelVersion != "", facts.OptStacks
	r.update(func(rec *cluster.RunNodeRecord) { rec.Name = v.Name })
	if other := s.claim(s.names, v.Name, r.key); other != "" {
		return fail(response.ErrAgentInstallIdentityTaken, "another server of this install has the same name", "name", v.Name, "row", other)
	}
	if err := s.env.ReadyForAgentRegistration(v.Name, r.ip); err != nil {
		return clusterFail(err, v.Name, r.ip)
	}
	how := "offline"
	if r.online {
		how = "online"
	}
	log.printf("pre-flight passed: %s on %s, installing %s as %s", facts.OSID, r.arch, how, v.Name)

	if v.Standalone {
		ok, err := r.wait(ctx, awaitStandalone, nil)
		if err != nil {
			return err
		}
		if !ok {
			return errDeclined
		}
	}
	if s.review {
		if _, err := r.wait(ctx, awaitReview, nil); err != nil {
			return err
		}
	}
	return nil
}

// clusterFail is the row reason for a refusal of the cluster's: a gate of
// the registration, the agent cap, or a lost leadership.
func clusterFail(err error, name string, ip net.IP) error {
	gate := func(g string) error {
		return fail(response.ErrAgentInstallClusterNotReady, err.Error(), "gate", g)
	}
	switch {
	case errors.Is(err, cluster.ErrNotLeader):
		return fail(response.ErrAgentInstallLeaderChanged, err.Error())
	case errors.Is(err, cluster.ErrNodeIdentityTaken):
		return fail(response.ErrAgentInstallIdentityTaken, err.Error(), "name", name, "ip", ip.String())
	case errors.Is(err, cluster.ErrMaxAgentsReached):
		return gate("max_agents")
	case errors.Is(err, cluster.ErrProxyRotationPending):
		return gate("proxy_rotation")
	case errors.Is(err, cluster.ErrCARotationInProgress):
		return gate("ca_rotation")
	case errors.Is(err, cluster.ErrManagersNotReadyForAgents):
		return gate("managers")
	case errors.Is(err, cluster.ErrCAKeyUnavailable):
		return gate("ca_key")
	}
	return gate("other")
}

// wait shows kind on the row and waits for the operator's answer, without
// holding a slot or, past the login, a connection. It gives up after
// answerWait.
func (r *row) wait(ctx context.Context, kind string, set func(rec *cluster.RunNodeRecord)) (bool, error) {
	r.s.mu.Lock()
	select {
	case <-r.answer:
	default:
	}
	r.s.mu.Unlock()
	if kind == awaitStandalone || kind == awaitReview {
		r.closeTarget()
	}
	r.update(func(rec *cluster.RunNodeRecord) {
		rec.Awaiting = kind
		if set != nil {
			set(rec)
		}
	})
	r.release()
	timer := time.NewTimer(r.s.e.t.answerWait)
	defer timer.Stop()
	var ok bool
	var err error
	select {
	case ok = <-r.answer:
	case <-ctx.Done():
		err = ctx.Err()
	case <-timer.C:
		err = fail(response.ErrAgentInstallFailed, "no answer came in time", "no_answer", kind)
	}
	r.update(func(rec *cluster.RunNodeRecord) { rec.Awaiting = "" })
	if err != nil {
		return false, err
	}
	if err := r.acquire(ctx); err != nil {
		return false, err
	}
	return ok, nil
}

// install is phase 2: a join token bound to the server's IP, a private
// directory on the server holding install.sh, the verified checksums, the
// archive when offline and the token, all sent on stdin, and install.sh run
// under systemd-run as root, so it outlives the SSH connection. It returns
// once install.sh has ended.
func (r *row) install(ctx context.Context) error {
	s, log := r.s, r.logs[phaseInstall]
	if r.tgt == nil {
		if err := r.dial(ctx, log); err != nil {
			return err
		}
	}
	sums, err := s.checksums(ctx)
	if err != nil {
		return fail(response.ErrAgentInstallFailed, err.Error(), "step", "checksums")
	}
	var archive []byte
	if !r.online {
		if archive, err = s.archiveFor(ctx, r.arch, sums); err != nil {
			return fail(response.ErrAgentInstallFailed, err.Error(), "step", "archive")
		}
	}
	pin, err := s.env.CAPin()
	if err != nil {
		return fail(response.ErrAgentInstallFailed, err.Error(), "step", "ca_pin")
	}
	tok, err := s.env.CreateAgentToken(r.ip)
	if err != nil {
		return clusterFail(err, r.name, r.ip)
	}
	// The string tok cannot be zeroed; it is left to the collector. The
	// bytes are what the row keeps, until it revokes the token.
	r.token = []byte(tok)

	if err := r.mktemp(ctx); err != nil {
		return err
	}
	path := func(name string) string { return r.tmp + "/" + name }
	files := []struct {
		name string
		data []byte
	}{
		{"install.sh", s.env.InstallScript()},
		{"checksums.txt", sums},
		{"token", r.token},
		// Made by the login, so the login reads them back; install.sh,
		// running as root, only writes into them.
		{"install.log", nil},
		{"exit", nil},
	}
	args := []string{"--agent", r.joinAddr, "--token-file", path("token"), "--ca", pin,
		"--advertise", r.ip.String(), "--name", r.name, "--version", s.version, "--checksums", path("checksums.txt")}
	if archive != nil {
		name := archiveName(s.version, r.arch)
		files = append(files, struct {
			name string
			data []byte
		}{name, archive})
		args = append(args, "--archive", path(name))
	}
	for _, f := range files {
		if err := r.tgt.Upload(ctx, path(f.name), 0o600, f.data); err != nil {
			return r.runErr(ctx, err)
		}
	}
	log.printf("uploaded install.sh, checksums.txt and the join token to %s", r.tmp)

	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = sshexec.ShellQuote(a)
	}
	script := "bash " + sshexec.ShellQuote(path("install.sh")) + " " + strings.Join(quoted, " ") +
		" >" + sshexec.ShellQuote(path("install.log")) + " 2>&1; echo $? >" + sshexec.ShellQuote(path("exit"))
	r.unit = "sfpanel-agent-install-" + s.id + "-" + r.key
	cmd := "systemd-run --unit=" + r.unit + " --collect --quiet bash -c " + sshexec.ShellQuote(script)
	_, stderr, exit, err := r.priv(ctx, cmd, nil)
	if err != nil {
		return r.runErr(ctx, err)
	}
	if exit != 0 {
		log.write(stderr)
		return fail(response.ErrAgentInstallFailed, "systemd-run could not start install.sh", "step", "systemd-run", "exit", strconv.Itoa(exit))
	}
	r.installStarted = true
	log.printf("install.sh runs as %s", r.unit)
	return r.follow(ctx)
}

// mktemp makes the row's directory, mode 0700, as the login.
func (r *row) mktemp(ctx context.Context) error {
	base := r.s.e.t.tmpBase
	out, stderr, exit, err := r.tgt.Run(ctx, "mktemp -d "+sshexec.ShellQuote(base+"/sfpanel-agent.XXXXXXXXXX"), nil)
	if err != nil {
		return r.runErr(ctx, err)
	}
	dir := strings.TrimSpace(string(out))
	// The path goes into rm -rf: it must be the one asked for.
	ok, _ := regexp.MatchString(`^`+regexp.QuoteMeta(base)+`/sfpanel-agent\.[A-Za-z0-9]{10}$`, dir)
	if exit != 0 || !ok {
		r.logs[phaseInstall].write(stderr)
		return fail(response.ErrAgentInstallFailed, "mktemp made no directory", "step", "mktemp", "exit", strconv.Itoa(exit))
	}
	r.tmp = dir
	return nil
}

// follow reads the install's log and exit status every poll, until it
// ends or installLimit passes. install.sh registers the agent inside its
// join and has steps after it, so an install.sh that fails or runs out of
// time after an agent registered at the row's IP still goes on to the
// registration and the connection check. A command that cannot be run is a
// broken connection: it is closed, and dialled again at the next poll
// against the approved key, for as long as installLimit allows.
func (r *row) follow(ctx context.Context) error {
	t, log := r.s.e.t, r.logs[phaseInstall]
	deadline := time.Now().Add(t.installLimit)
	var off int64
	warned := false
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(t.poll):
		}
		var status []byte
		err := r.reconnect(ctx, log)
		if err == nil {
			// The exit status first: once it is written, the log is complete.
			status, _, _, err = r.tgt.Run(ctx, "cat "+sshexec.ShellQuote(r.tmp+"/exit"), nil)
		}
		if err == nil {
			var out []byte
			out, _, _, err = r.tgt.Run(ctx, "tail -c +"+strconv.FormatInt(off+1, 10)+" "+sshexec.ShellQuote(r.tmp+"/install.log"), nil)
			off += int64(len(out))
			log.write(out)
		}
		var f *rowFail
		switch {
		case err != nil && ctx.Err() != nil:
			return ctx.Err()
		case errors.As(err, &f) && f.code != response.ErrAgentInstallUnreachable:
			// A login the server now refuses, or a host key that changed:
			// no use dialling again.
			return err
		case err != nil:
			if !warned {
				log.printf("reading the install's progress failed: %v", err)
				warned = true
			}
			r.closeTarget()
		default:
			if n, done := exitStatus(status); done {
				r.installExited = true
				if n == 0 {
					return nil
				}
				if a, ok := r.agent(); ok {
					log.printf("install.sh exited %d after the agent registered as node %s", n, a.ID)
					return nil
				}
				return fail(response.ErrAgentInstallFailed, fmt.Sprintf("install.sh exited %d", n), "step", "install", "exit", strconv.Itoa(n))
			}
		}
		if time.Now().After(deadline) {
			if a, ok := r.agent(); ok {
				log.printf("install.sh did not end in time after the agent registered as node %s", a.ID)
				return nil
			}
			return fail(response.ErrAgentInstallFailed, "install.sh did not end in time", "step", "install",
				"timeout_minutes", strconv.Itoa(int(t.installLimit/time.Minute)))
		}
	}
}

// reconnect dials the server again when the row's connection was closed for
// a command that could not be run.
func (r *row) reconnect(ctx context.Context, log *phaseLog) error {
	if r.tgt != nil {
		return nil
	}
	if err := r.dial(ctx, log); err != nil {
		return err
	}
	log.printf("reconnected to %s", r.addr)
	return nil
}

// exitStatus reads the exit file: nothing until install.sh has ended.
func exitStatus(b []byte) (int, bool) {
	n, err := strconv.Atoi(string(bytes.TrimSpace(b)))
	return n, err == nil
}

// register is phase 3: the agent install.sh registered, found in the
// cluster by the address its token was bound to.
func (r *row) register(context.Context) error {
	a, ok := r.agent()
	if !ok {
		return fail(response.ErrAgentInstallFailed, "install.sh ended, but no agent registered at the address", "step", "register", "ip", r.ip.String())
	}
	r.adopt(a, r.logs[phaseRegister])
	return nil
}

// agent is the agent this row's install.sh registered, if there is one: the
// node that is no manager at the row's IP. The pre-flight's gates refused a
// node at that IP, so one found there after the install started is this
// row's.
func (r *row) agent() (cluster.Node, bool) {
	return cluster.AgentAt(r.s.env.Nodes(), r.ip)
}

// adopt records a as the node the row registered as.
func (r *row) adopt(a cluster.Node, log *phaseLog) {
	r.update(func(rec *cluster.RunNodeRecord) { rec.NodeID = a.ID })
	log.printf("registered as node %s (%s)", a.ID, a.Name)
}

// unwind ends an install that started and ended short of a registration as
// far as the row saw, once its token is revoked. An agent at the row's IP is
// the row's: it is adopted and the install unit, past the join, is left to
// finish. With none, the unit is stopped and the cluster looked at again,
// for a join admitted before the token went; only a row with no agent then
// has its server cleaned.
func (r *row) unwind(ctx context.Context) {
	log := r.logs[phaseInstall]
	if a, ok := r.agent(); ok {
		r.adopt(a, log)
		return
	}
	r.stopInstall(ctx)
	if a, ok := r.agent(); ok {
		r.adopt(a, log)
		return
	}
	r.cleanTarget(ctx)
}

// stopInstall stops the install unit if it is still running.
func (r *row) stopInstall(ctx context.Context) {
	if r.tgt == nil || r.installExited {
		return
	}
	if _, _, exit, err := r.priv(ctx, "systemctl stop "+sshexec.ShellQuote(r.unit+".service"), nil); err != nil || exit != 0 {
		r.logs[phaseInstall].printf("stopping %s failed (exit %d): %v", r.unit, exit, err)
	}
}

// cleanTarget takes off the server what an install that registered no agent
// put there, once its unit has stopped: SFPanel, when the server had none
// before, and the /opt/stacks the panel made, when the server had none.
func (r *row) cleanTarget(ctx context.Context) {
	if r.tgt == nil || r.sfpanelBefore {
		return
	}
	r.purge(ctx, r.tmp, r.logs[phaseInstall])
}

// purge runs the uploaded install.sh's uninstall --purge, then removes
// /opt/stacks if the server had none and it is empty.
func (r *row) purge(ctx context.Context, dir string, log *phaseLog) bool {
	ok := true
	out, stderr, exit, err := r.priv(ctx, "bash "+sshexec.ShellQuote(dir+"/install.sh")+" uninstall --purge", nil)
	log.write(out)
	log.write(stderr)
	if err != nil || exit != 0 {
		log.printf("uninstall --purge failed (exit %d): %v", exit, err)
		ok = false
	}
	if !r.optStacksBefore {
		if _, _, _, err := r.priv(ctx, "rmdir /opt/stacks 2>/dev/null || true", nil); err != nil {
			log.printf("removing /opt/stacks failed: %v", err)
		}
	}
	return ok
}

// removeTemp removes the row's directory on the server.
func (r *row) removeTemp(ctx context.Context) {
	if r.tmp == "" || r.tgt == nil {
		return
	}
	if _, _, exit, err := r.priv(ctx, "rm -rf "+sshexec.ShellQuote(r.tmp), nil); err != nil || exit != 0 {
		r.logs[phaseInstall].printf("removing %s failed (exit %d): %v", r.tmp, exit, err)
		return
	}
	r.tmp = ""
}

// revokeToken takes the row's join token back, spent or not, and zeroes it.
func (r *row) revokeToken() {
	if r.token == nil {
		return
	}
	if err := r.s.env.RevokeToken(string(r.token)); err != nil {
		logger().Warn("agent install token not revoked", "run", r.s.id, "row", r.key, "error", err)
	}
	clear(r.token)
	r.token = nil
}

func archiveName(version, arch string) string { return release.ArchiveName(version, arch) }

// readArchiveFile reads name from the unreleased archive directory, up to
// the size a release archive may have.
func readArchiveFile(dir, name string) ([]byte, error) {
	p := filepath.Join(dir, name)
	st, err := os.Stat(p)
	if err != nil {
		return nil, fmt.Errorf("SFPANEL_AGENT_ARCHIVES has no %s", name)
	}
	if !st.Mode().IsRegular() || st.Size() > release.MaxArchiveBytes {
		return nil, fmt.Errorf("SFPANEL_AGENT_ARCHIVES's %s is no file of a release's size", name)
	}
	return os.ReadFile(p)
}

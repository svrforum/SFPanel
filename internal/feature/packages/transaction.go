package packages

// A package transaction is one apt run that changes the system: an upgrade,
// or an install from the Packages page.
//
// apt used to run as the HTTP request's child. Closing the tab, a reverse
// proxy's idle timeout, the cluster relay's five-minute cap or a panel restart
// killed apt-get while dpkg was unpacking, and every apt run after it said
// "dpkg was interrupted, you must manually run dpkg --configure -a" until
// someone did that in a shell. Now apt runs as a transient unit of its own
// (sfpanel-apt-<id>), writing to a log file the page streams: a client that
// goes away stops only the streaming, and the page can come back to a run in
// progress. The unit is outside the panel's sandbox as well — the
// RestrictSUIDSGID install.sh used to set made dpkg fail on every setuid file
// it unpacked.
//
// One transaction at a time: a second is refused while one runs, and a panel
// that restarted finds a run still going by its unit.

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	osExec "os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/common/exec"
	"github.com/svrforum/SFPanel/internal/common/lifecycle"
)

const (
	txUpgrade = "upgrade"
	txInstall = "install"

	txSuccess     = "success"
	txFailed      = "failed"
	txInterrupted = "interrupted"

	txUnitPrefix = "sfpanel-apt-"

	// txExitMarker starts the last line txScript writes: how the command ended.
	txExitMarker = "[sfpanel-apt] exit="

	// txScript runs the transaction's command, given as its arguments, and
	// records the exit status in the log. The command never becomes part of
	// the script, so no package name is read by a shell.
	txScript = `"$@"; rc=$?; echo "[sfpanel-apt] exit=$rc"; exit $rc`

	// txMaxPending is the longest unterminated line held back for its end.
	txMaxPending = 64 * 1024
)

var (
	// txLogDir keeps each transaction's log (<id>.log) and what it was
	// (<id>.json). A var so tests use a temp dir.
	txLogDir = "/var/log/sfpanel/apt"
	// txKeep is how many transaction logs are kept.
	txKeep = 20
	// txPoll is how often a running transaction's unit is asked whether it is
	// still there, and its log read for new lines.
	txPoll = time.Second
	// txKeepalive is the longest a stream stays silent. A reverse proxy
	// closes an idle connection (nginx after 60 s by default), and apt says
	// nothing for minutes while it downloads or a postinst runs.
	txKeepalive = 15 * time.Second
	// txUpdateTimeout bounds the apt-get update before an upgrade.
	txUpdateTimeout = 10 * time.Minute
	// txUnknownLimit is how many answers in a row from systemctl that are
	// neither "running" nor "gone" are waited out before the log decides.
	txUnknownLimit = 30
	// txSystemctlTimeout bounds each systemctl question: one is asked with
	// txMu held, and a systemctl stuck on D-Bus would hold every package
	// request behind it for the Commander's five minutes.
	txSystemctlTimeout = 10 * time.Second

	// systemdActive and dpkgLockHeld are what they wrap; tests replace them.
	systemdActive = lifecycle.IsSystemdActive
	dpkgLockHeld  = exec.DpkgLockHeld
)

var txIDPattern = regexp.MustCompile(`^[0-9]{8}-[0-9]{6}-[0-9a-f]{4}$`)

// aptTx is one package transaction.
type aptTx struct {
	id        string
	dir       string
	kind      string
	packages  []string
	argv      []string // the apt-get command line; empty for one found running
	command   string
	startedAt time.Time

	mu       sync.Mutex
	running  bool
	result   string
	exitCode *int
	warnings []response.Warning
	summary  string // the line the stream ends with
	done     chan struct{}
}

// txMeta is written beside the log, for a panel that restarts while the
// transaction runs.
type txMeta struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	Packages  []string  `json:"packages"`
	Command   string    `json:"command"`
	StartedAt time.Time `json:"started_at"`
}

// txView is a transaction as GET /packages/transaction answers it.
type txView struct {
	txMeta
	Running  bool               `json:"running"`
	Result   string             `json:"result,omitempty"`
	ExitCode *int               `json:"exit_code,omitempty"`
	Warnings []response.Warning `json:"warnings,omitempty"`
}

func newTx(kind string, packages, argv []string) *aptTx {
	now := time.Now().UTC()
	b := make([]byte, 2)
	_, _ = rand.Read(b)
	if packages == nil {
		packages = []string{}
	}
	return &aptTx{
		id:        now.Format("20060102-150405") + "-" + hex.EncodeToString(b),
		dir:       txLogDir,
		kind:      kind,
		packages:  packages,
		argv:      argv,
		command:   strings.Join(argv, " "),
		startedAt: now,
		running:   true,
		done:      make(chan struct{}),
	}
}

func (tx *aptTx) unit() string     { return txUnitPrefix + tx.id + ".service" }
func (tx *aptTx) logPath() string  { return filepath.Join(tx.dir, tx.id+".log") }
func (tx *aptTx) metaPath() string { return filepath.Join(tx.dir, tx.id+".json") }

func (tx *aptTx) meta() txMeta {
	return txMeta{ID: tx.id, Kind: tx.kind, Packages: tx.packages, Command: tx.command, StartedAt: tx.startedAt}
}

func (tx *aptTx) view() txView {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	return txView{txMeta: tx.meta(), Running: tx.running, Result: tx.result, ExitCode: tx.exitCode, Warnings: tx.warnings}
}

func (tx *aptTx) isRunning() bool {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	return tx.running
}

// create writes the description and an empty log, which the unit appends to.
func (tx *aptTx) create() error {
	if err := os.MkdirAll(tx.dir, 0o750); err != nil {
		return err
	}
	meta, err := json.Marshal(tx.meta())
	if err != nil {
		return err
	}
	if err := os.WriteFile(tx.metaPath(), meta, 0o640); err != nil {
		return err
	}
	return os.WriteFile(tx.logPath(), nil, 0o640)
}

// note appends to the log what the panel writes there itself: its own steps,
// and apt-get update's output, which runs before the unit does.
func (tx *aptTx) note(text string) {
	if text == "" {
		return
	}
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	f, err := os.OpenFile(tx.logPath(), os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		slog.Warn("could not write the package transaction log", "component", "packages", "id", tx.id, "error", err)
		return
	}
	defer f.Close()
	_, _ = f.WriteString(text)
}

func (tx *aptTx) addWarnings(w []response.Warning) {
	tx.mu.Lock()
	tx.warnings = append(tx.warnings, w...)
	tx.mu.Unlock()
}

// finish records how the transaction ended; only the first call counts.
func (tx *aptTx) finish(result string, code *int, summary string) {
	tx.mu.Lock()
	if !tx.running {
		tx.mu.Unlock()
		return
	}
	tx.running, tx.result, tx.exitCode, tx.summary = false, result, code, summary
	tx.mu.Unlock()
	close(tx.done)
}

// logEnd puts the end of a transaction this process watched in the panel's log.
func (tx *aptTx) logEnd() {
	v := tx.view()
	attrs := []any{"component", "packages", "id", v.ID, "kind", v.Kind, "result", v.Result}
	if v.ExitCode != nil {
		attrs = append(attrs, "exit_code", *v.ExitCode)
	}
	if v.Result == txSuccess {
		slog.Info("package transaction ended", attrs...)
	} else {
		slog.Warn("package transaction ended", attrs...)
	}
}

// finishFromLog reads how the command ended from the last line txScript
// wrote. No such line means the unit was stopped or killed before apt-get
// returned, and dpkg may have been left part-way.
func (tx *aptTx) finishFromLog() {
	code, ok := exitFromLog(tx.logPath())
	switch {
	case !ok:
		tx.finish(txInterrupted, nil, "ERROR: apt stopped without saying how it ended: its unit was stopped or killed. 'dpkg --audit' lists any package it left unfinished.")
	case code == 0:
		tx.finish(txSuccess, &code, tx.successLine())
	default:
		tx.finish(txFailed, &code, fmt.Sprintf("ERROR: %s failed with exit status %d", tx.verb(), code))
	}
}

func (tx *aptTx) verb() string {
	if f := strings.Fields(tx.command); len(f) > 0 {
		return f[0]
	}
	return "apt"
}

func (tx *aptTx) successLine() string {
	switch tx.kind {
	case txUpgrade:
		return ">>> Packages upgraded successfully"
	case txInstall:
		return ">>> " + strings.Join(tx.packages, " ") + " installed successfully"
	}
	return ">>> The package transaction completed successfully"
}

func (tx *aptTx) summaryLine() string {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	return tx.summary
}

// exitFromLog is the status in the log's last line, if that is the marker.
func exitFromLog(path string) (int, bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, false
	}
	defer f.Close()
	const tail = 4096
	if st, err := f.Stat(); err == nil && st.Size() > tail {
		if _, err := f.Seek(-tail, io.SeekEnd); err != nil {
			return 0, false
		}
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return 0, false
	}
	text := strings.TrimRight(string(data), "\r\n\t ")
	last := text[strings.LastIndexByte(text, '\n')+1:]
	rest, ok := strings.CutPrefix(strings.TrimSpace(last), txExitMarker)
	if !ok {
		return 0, false
	}
	code, err := strconv.Atoi(rest)
	return code, err == nil
}

// loadTx rebuilds a transaction this process did not start, from what was
// written beside its log.
func loadTx(dir, id string) *aptTx {
	tx := &aptTx{id: id, dir: dir, packages: []string{}, done: make(chan struct{})}
	if data, err := os.ReadFile(tx.metaPath()); err == nil {
		var m txMeta
		if json.Unmarshal(data, &m) == nil && m.ID == id {
			tx.kind, tx.command, tx.startedAt = m.Kind, m.Command, m.StartedAt
			if m.Packages != nil {
				tx.packages = m.Packages
			}
		}
	}
	return tx
}

// pruneTxLogs keeps the newest txKeep logs and what was written beside them.
// The ids sort by when they started.
func pruneTxLogs(dir, keep string) {
	logs, _ := filepath.Glob(filepath.Join(dir, "*.log"))
	sort.Strings(logs)
	for len(logs) > txKeep {
		id := strings.TrimSuffix(filepath.Base(logs[0]), ".log")
		logs = logs[1:]
		if id == keep || !txIDPattern.MatchString(id) {
			continue
		}
		_ = os.Remove(filepath.Join(dir, id+".log"))
		_ = os.Remove(filepath.Join(dir, id+".json"))
	}
}

// txRunningError is beginTx refusing while another transaction runs.
type txRunningError struct{ tx txView }

func (e *txRunningError) Error() string {
	return "package transaction " + e.tx.ID + " is already running"
}

// beginTx starts the record of a transaction, or refuses: while another
// transaction runs, and while something outside the panel holds dpkg.
func (h *Handler) beginTx(kind string, packages, argv []string) (*aptTx, error) {
	h.txMu.Lock()
	defer h.txMu.Unlock()
	if cur := h.currentTxLocked(); cur != nil && cur.isRunning() {
		return nil, &txRunningError{cur.view()}
	}
	if dpkgLockHeld() {
		return nil, exec.ErrDpkgLocked
	}
	tx := newTx(kind, packages, argv)
	if err := tx.create(); err != nil {
		return nil, err
	}
	h.tx = tx
	pruneTxLogs(tx.dir, tx.id)
	slog.Info("package transaction started", "component", "packages", "id", tx.id, "kind", kind, "command", tx.command)
	return tx, nil
}

// currentTxLocked is the transaction the panel knows of: the running one,
// found by its unit when a restart made the panel forget it, or else the last
// one this process ran. Called with txMu held.
func (h *Handler) currentTxLocked() *aptTx {
	if h.tx != nil && h.tx.isRunning() {
		return h.tx
	}
	ids := h.liveTxUnits()
	if len(ids) == 0 || (h.tx != nil && h.tx.id == ids[0]) {
		// Nothing running, or the one this process watched is going away
		// as this is asked; what the watcher saw stands.
		return h.tx
	}
	tx := loadTx(txLogDir, ids[0])
	tx.running = true
	h.tx = tx
	go func() {
		h.watchUnit(tx)
		tx.logEnd()
		h.invalidateStatus()
	}()
	return tx
}

// runningTx is the transaction running now, or nil.
func (h *Handler) runningTx() *aptTx {
	h.txMu.Lock()
	defer h.txMu.Unlock()
	if tx := h.currentTxLocked(); tx != nil && tx.isRunning() {
		return tx
	}
	return nil
}

// liveTxUnits lists the ids of transaction units systemd is running.
func (h *Handler) liveTxUnits() []string {
	if !systemdActive() {
		return nil
	}
	out, err := h.Cmd.RunWithTimeout(txSystemctlTimeout, "systemctl", "list-units", "--type=service", "--plain", "--no-legend", "--no-pager", txUnitPrefix+"*")
	if err != nil {
		return nil
	}
	var ids []string
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 3 || !unitStateRunning(f[2]) {
			continue
		}
		id, ok := strings.CutPrefix(f[0], txUnitPrefix)
		if !ok {
			continue
		}
		if id, ok = strings.CutSuffix(id, ".service"); ok && txIDPattern.MatchString(id) {
			ids = append(ids, id)
		}
	}
	return ids
}

func unitStateRunning(state string) bool {
	switch state {
	case "active", "activating", "deactivating", "reloading", "refreshing":
		return true
	}
	return false
}

// watchUnit waits for the transaction's unit to go away, then reads from the
// log how it ended. --collect unloads a unit as soon as it ends, failed or
// not, so systemd no longer knows by then; the log does.
func (h *Handler) watchUnit(tx *aptTx) {
	unknown := 0
	for {
		out, _ := h.Cmd.RunWithTimeout(txSystemctlTimeout, "systemctl", "is-active", tx.unit())
		state := strings.TrimSpace(out)
		if state == "inactive" || state == "failed" {
			break
		}
		if unitStateRunning(state) {
			unknown = 0
		} else if unknown++; unknown >= txUnknownLimit {
			break // systemctl is not answering; let the log say
		}
		time.Sleep(txPoll)
	}
	tx.finishFromLog()
}

// runTx carries a transaction from its start to its end. It does not run on
// the request that started it: a client that goes away does not stop the
// upgrade it asked for.
func (h *Handler) runTx(tx *aptTx, update bool) {
	defer func() {
		tx.logEnd()
		h.invalidateStatus()
	}()
	if update && !h.txUpdate(tx) {
		return
	}
	tx.note(">>> " + tx.command)
	wait, err := h.launchTx(tx)
	if err != nil {
		tx.note("ERROR: " + err.Error())
		tx.finish(txFailed, nil, "ERROR: "+tx.verb()+" could not be started")
		return
	}
	wait()
}

// txUpdate refreshes the package lists before an upgrade. A repository apt
// could not fetch leaves the others refreshed — Proxmox's enterprise
// repository answers 401 on every host without a subscription — so the
// upgrade goes on and the repository becomes a warning. Anything else stops
// it before dpkg is touched.
func (h *Handler) txUpdate(tx *aptTx) bool {
	tx.note(">>> apt-get update")
	ctx, cancel := context.WithTimeout(context.Background(), txUpdateTimeout)
	defer cancel()
	out, err := exec.AptUpdate(ctx, h.Cmd)
	tx.note(out)
	var ue *exec.AptUpdateError
	switch {
	case err == nil:
		return true
	case errors.As(err, &ue) && ue.Partial:
		warnings := response.RepoWarnings(ue.Unauthorized, ue.Failed)
		tx.addWarnings(warnings)
		for _, w := range warnings {
			tx.note("WARNING: " + w.Message)
		}
		return true
	default:
		tx.note("ERROR: " + err.Error())
		tx.finish(txFailed, nil, "ERROR: apt-get update failed; nothing was changed")
		return false
	}
}

// launchTx starts the transaction's command and returns what waits for it
// to end.
func (h *Handler) launchTx(tx *aptTx) (func(), error) {
	if systemdActive() && h.Cmd.Exists("systemd-run") {
		out, err := h.Cmd.RunWithTimeout(30*time.Second, "systemd-run", unitArgv(tx)...)
		if err != nil {
			tx.note(strings.TrimSpace(out))
			return nil, fmt.Errorf("systemd-run: %w", err)
		}
		return func() { h.watchUnit(tx) }, nil
	}
	return launchDirect(tx)
}

// unitArgv is the systemd-run command line that runs tx:
//
//	systemd-run --unit=sfpanel-apt-<id> --collect --quiet --description=…
//	  -p StandardOutput=append:<log> -p StandardError=inherit
//	  --setenv=DEBIAN_FRONTEND=noninteractive
//	  -- /bin/sh -c <txScript> sh apt-get …
//
// A service, as internal/feature/ai starts its tmux servers: PID 1 runs it,
// so it is not the panel's child (neither a closed request nor a panel restart
// reaches it), not under the panel's sandbox, and its environment is its own
// rather than the panel's. --collect unloads it when it ends, which is why
// the script writes how it ended into the log.
func unitArgv(tx *aptTx) []string {
	argv := []string{
		"--unit=" + tx.unit(), "--collect", "--quiet",
		"--description=SFPanel package " + tx.kind,
		"-p", "StandardOutput=append:" + tx.logPath(),
		"-p", "StandardError=inherit",
	}
	for _, kv := range exec.AptEnv() {
		argv = append(argv, "--setenv="+kv)
	}
	argv = append(argv, "--", "/bin/sh", "-c", txScript, "sh")
	return append(argv, tx.argv...)
}

// launchDirect runs the transaction as the panel's own child, on a host with
// no systemd to hand it to (a container, a bare process): a panel restart ends
// it there, as it always did. os/exec rather than the Commander because the
// output has to reach the log as apt writes it.
var launchDirect = func(tx *aptTx) (func(), error) {
	f, err := os.OpenFile(tx.logPath(), os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return nil, err
	}
	cmd := osExec.Command("/bin/sh", append([]string{"-c", txScript, "sh"}, tx.argv...)...)
	cmd.Env = append(os.Environ(), exec.AptEnv()...)
	cmd.Stdout, cmd.Stderr = f, f
	if err := cmd.Start(); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		_ = cmd.Wait()
		f.Close()
		tx.finishFromLog()
	}, nil
}

// failTx answers a transaction that could not be started.
func failTx(c echo.Context, err error, code string) error {
	var running *txRunningError
	switch {
	case errors.As(err, &running):
		return txRunningFail(c, running.tx)
	case errors.Is(err, exec.ErrDpkgLocked):
		return response.Fail(c, http.StatusConflict, code,
			"Another package manager operation is in progress; try again shortly")
	default:
		return response.Fail(c, http.StatusInternalServerError, code,
			"Failed to start the package transaction: "+response.SanitizeOutput(err.Error()))
	}
}

// txRunningFail refuses a request whose apt would meet a running
// transaction's lock. The details let the page offer to follow it.
func txRunningFail(c echo.Context, tx txView) error {
	return response.FailWith(c, http.StatusConflict, response.ErrAPTTransactionRunning,
		"A package transaction ("+tx.Kind+") is already running", map[string]string{"id": tx.ID, "kind": tx.Kind})
}

// sseReady checks the response can stream, before anything is started.
func sseReady(c echo.Context) error {
	if _, ok := c.Response().Writer.(http.Flusher); !ok {
		return response.Fail(c, http.StatusInternalServerError, response.ErrSSEError, "Streaming not supported")
	}
	return nil
}

// followTx streams the transaction's log from line skip on until the
// transaction ends, then the line that says how, then [DONE]. A client that
// goes away stops the streaming and nothing else.
func (h *Handler) followTx(c echo.Context, tx *aptTx, skip int) error {
	f, err := os.Open(tx.logPath())
	if err != nil {
		return response.Fail(c, http.StatusNotFound, response.ErrNotFound, "The package transaction's log is gone")
	}
	defer f.Close()

	w := c.Response()
	flusher, _ := w.Writer.(http.Flusher)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	last := time.Now()
	write := func(s string) {
		_, _ = io.WriteString(w, s)
		if flusher != nil {
			flusher.Flush()
		}
		last = time.Now()
	}
	// The stream names its transaction first, as the SSE event id: a page
	// that follows it again after the stream was cut asks for this one,
	// not for whatever runs by then.
	write("id: " + tx.id + "\n\n")
	send := func(line string) {
		// dpkg redraws its progress with carriage returns; keep what the
		// terminal would have shown last. A CR also ends an SSE line.
		if i := strings.LastIndexByte(strings.TrimRight(line, "\r"), '\r'); i >= 0 {
			line = line[i+1:]
		}
		write("data: " + response.SanitizeOutput(line) + "\n\n")
	}

	lines := &lineReader{r: f}
	seen := 0
	emit := func(final bool) {
		for _, line := range lines.next(final) {
			if strings.HasPrefix(strings.TrimSpace(line), txExitMarker) {
				continue
			}
			if seen++; seen > skip {
				send(line)
			}
		}
	}

	ticker := time.NewTicker(txPoll)
	defer ticker.Stop()
	for {
		ended := !tx.isRunning()
		emit(ended)
		if ended {
			send(tx.summaryLine())
			send("[DONE]")
			return nil
		}
		if time.Since(last) >= txKeepalive {
			write(": keepalive\n\n")
		}
		select {
		case <-c.Request().Context().Done():
			return nil
		case <-tx.done:
		case <-ticker.C:
		}
	}
}

// lineReader hands out the lines appended to a file since it was last asked;
// with final, the unterminated rest as well.
type lineReader struct {
	r     io.Reader
	buf   []byte
	chunk []byte
}

func (l *lineReader) next(final bool) []string {
	if l.chunk == nil {
		l.chunk = make([]byte, 32*1024)
	}
	for {
		n, err := l.r.Read(l.chunk)
		l.buf = append(l.buf, l.chunk[:n]...)
		if n == 0 || err != nil {
			break
		}
	}
	var out []string
	for {
		i := bytes.IndexByte(l.buf, '\n')
		if i < 0 {
			break
		}
		out = append(out, string(l.buf[:i]))
		l.buf = l.buf[i+1:]
	}
	if len(l.buf) > 0 && (final || len(l.buf) > txMaxPending) {
		out = append(out, string(l.buf))
		l.buf = nil
	}
	return out
}

// ---------- Transaction routes ----------

// GetTransaction answers the package transaction the panel knows of — the
// one running, or else the last one it ran — or null.
// GET /packages/transaction
func (h *Handler) GetTransaction(c echo.Context) error {
	h.txMu.Lock()
	tx := h.currentTxLocked()
	h.txMu.Unlock()
	if tx == nil {
		return response.OK(c, map[string]interface{}{"transaction": nil})
	}
	return response.OK(c, map[string]interface{}{"transaction": tx.view()})
}

// FollowTransaction streams a package transaction the way the request that
// started it does, so a page that was closed, reloaded or cut off by a proxy
// can come back to it. ?id= picks one (by default the current one), and
// ?skip=N leaves out the first N lines, which the page already has.
// GET /packages/transaction/stream
func (h *Handler) FollowTransaction(c echo.Context) error {
	if err := sseReady(c); err != nil {
		return err
	}
	id := c.QueryParam("id")
	if id != "" && !txIDPattern.MatchString(id) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidID, "Invalid transaction id")
	}
	skip, _ := strconv.Atoi(c.QueryParam("skip"))
	if skip < 0 {
		skip = 0
	}

	h.txMu.Lock()
	tx := h.currentTxLocked()
	if id != "" && (tx == nil || tx.id != id) {
		// An earlier one: its log says how it ended.
		tx = nil
		if _, err := os.Stat(filepath.Join(txLogDir, id+".log")); err == nil {
			tx = loadTx(txLogDir, id)
			tx.running = true
			tx.finishFromLog()
		}
	}
	h.txMu.Unlock()
	if tx == nil {
		return response.Fail(c, http.StatusNotFound, response.ErrNotFound, "No package transaction")
	}
	return h.followTx(c, tx, skip)
}

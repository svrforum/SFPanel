package packages

// A task job (internal/feature/taskjob) changes packages through the same
// transactions the Packages page starts, behind the same guards: the
// functions here are what both call, so a refusal carries the reason code the
// page would have shown.

import (
	"bufio"
	"errors"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strconv"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/common/exec"
)

// Refusal is a package operation turned down before anything ran, worded as
// the endpoints answer it: the HTTP status, the error code and its details.
type Refusal struct {
	Status  int
	Code    string
	Message string
	Details map[string]string
}

func (r *Refusal) Error() string { return r.Message }

// write answers the request with the refusal.
func (r *Refusal) write(c echo.Context) error {
	return response.FailWith(c, r.Status, r.Code, r.Message, r.Details)
}

// ErrNoTransaction is JobTransaction asked about a transaction there is no
// record of.
var ErrNoTransaction = errors.New("no such package transaction")

// txFailCodes is the code each kind of transaction is refused with when dpkg
// is held, the one its endpoint uses.
var txFailCodes = map[string]string{
	txLists:   response.ErrAPTUpdateError,
	txUpgrade: response.ErrAPTUpgradeError,
	txInstall: response.ErrAPTInstallError,
	txRemove:  response.ErrAPTRemoveError,
}

// txRefusal is beginTx's refusal as a Refusal, or nil when err is something
// else (the log could not be written).
func txRefusal(err error, code string) *Refusal {
	var running *txRunningError
	switch {
	case errors.As(err, &running):
		return txRunningRefusal(running.tx)
	case errors.Is(err, exec.ErrDpkgLocked):
		return &Refusal{Status: http.StatusConflict, Code: code,
			Message: "Another package manager operation is in progress; try again shortly"}
	}
	return nil
}

// txRunningRefusal refuses an operation whose apt would meet a running
// transaction's lock. The details let the page offer to follow it.
func txRunningRefusal(tx txView) *Refusal {
	return &Refusal{Status: http.StatusConflict, Code: response.ErrAPTTransactionRunning,
		Message: "A package transaction (" + tx.Kind + ") is already running",
		Details: map[string]string{"id": tx.ID, "kind": tx.Kind}}
}

// dockerOnPVERefusal refuses to put a Docker engine on a Proxmox VE host.
func dockerOnPVERefusal(engine string) *Refusal {
	return &Refusal{Status: http.StatusConflict, Code: response.ErrDockerOnPVE,
		Message: "Docker is not installed on a Proxmox VE host (" + engine + "): its FORWARD DROP policy can cut the guests off the network. Install it in a VM or LXC container.",
		Details: map[string]string{"package": engine}}
}

// StartJobTransaction starts a package transaction for a task job and returns
// its id; the transaction goes on without the caller, which reads how it
// ended with JobTransaction. kind is one of
//
//   - "update": apt-get update alone, run in the panel as before an upgrade,
//     so a repository that refused stays a warning;
//   - "upgrade": every package with the platform's verb, or only those named;
//   - "install": every named package in one apt-get install;
//   - "remove": every named package in one apt-get remove.
//
// update refreshes the lists first, as the Packages page's upgrade does.
// What the endpoints refuse is refused here for the same reason, as a
// *Refusal: a name apt-get could read as a flag, a transaction already
// running, dpkg held by something else, and on a Proxmox VE host an install
// that would bring a Docker engine — apt is asked once about every package
// together, since one of them can bring the engine for another.
func (h *Handler) StartJobTransaction(kind string, packages []string, update bool) (string, error) {
	code, ok := txFailCodes[kind]
	if !ok {
		return "", &Refusal{Status: http.StatusBadRequest, Code: response.ErrInvalidRequest,
			Message: "Unknown package transaction " + strconv.Quote(kind)}
	}
	switch {
	case kind == txLists && len(packages) > 0:
		return "", &Refusal{Status: http.StatusBadRequest, Code: response.ErrInvalidRequest,
			Message: "Updating the package lists takes no package names"}
	case (kind == txInstall || kind == txRemove) && len(packages) == 0:
		return "", &Refusal{Status: http.StatusBadRequest, Code: response.ErrMissingFields,
			Message: "Package name is required"}
	}
	for _, name := range packages {
		if !validatePackageName(name) {
			return "", &Refusal{Status: http.StatusBadRequest, Code: response.ErrInvalidPackageName,
				Message: "Package name contains invalid characters (allowed: a-zA-Z0-9._+-)"}
		}
	}

	var argv []string
	switch kind {
	case txLists:
		argv, update = []string{"apt-get", "update"}, true
	case txUpgrade:
		argv = upgradeArgv(packages)
	case txInstall:
		if engine := h.dockerEngineOnPVE(packages...); engine != "" {
			return "", dockerOnPVERefusal(engine)
		}
		argv = installArgv(packages...)
	case txRemove:
		argv = removeArgv(packages...)
	}

	tx, err := h.beginTx(kind, packages, argv)
	if err != nil {
		if r := txRefusal(err, code); r != nil {
			return "", r
		}
		return "", fmt.Errorf("start the package transaction: %w", err)
	}
	go h.runTx(tx, update)
	return tx.id, nil
}

// JobTransaction says how the transaction id stands: running, or how it
// ended (result is "success", "failed" or "interrupted"; exitCode is apt's
// status, -1 when there is none), and where its log is. A transaction this
// process no longer remembers is read from its log, as the page's
// FollowTransaction reads it.
func (h *Handler) JobTransaction(id string) (running bool, result string, exitCode int, logPath string, err error) {
	if !txIDPattern.MatchString(id) {
		return false, "", -1, "", ErrNoTransaction
	}
	h.txMu.Lock()
	tx := h.txLocked(id)
	h.txMu.Unlock()
	if tx == nil {
		return false, "", -1, "", ErrNoTransaction
	}
	v := tx.view()
	exitCode = -1
	if v.ExitCode != nil {
		exitCode = *v.ExitCode
	}
	return v.Running, v.Result, exitCode, tx.logPath(), nil
}

// aptSummary is the line apt-get prints before it changes anything:
// "3 upgraded, 1 newly installed, 2 to remove and 5 not upgraded."
var aptSummary = regexp.MustCompile(`^(\d+) upgraded, (\d+) newly installed, (\d+) to remove\b`)

// ChangedPackages is how many packages the transaction logged at logPath
// upgraded, installed and removed, from apt-get's summary line; false when
// the log has none (a lists refresh, a run that stopped before it, or apt
// speaking a language other than English).
func (h *Handler) ChangedPackages(logPath string) (int, bool) {
	f, err := os.Open(logPath)
	if err != nil {
		return 0, false
	}
	defer f.Close()
	n, found := 0, false
	r := bufio.NewReader(f)
	for {
		line, err := r.ReadString('\n')
		if m := aptSummary.FindStringSubmatch(line); m != nil {
			n, found = 0, true
			for _, s := range m[1:] {
				v, _ := strconv.Atoi(s)
				n += v
			}
		}
		if err != nil {
			return n, found
		}
	}
}

package cluster

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sort"
	"strings"
	"time"
)

// Saved logins of the agent install (internal/feature/agentinstall): SSH
// logins an operator named and kept, so the install form can use one again
// without typing it. They live in the replicated config map, one entry each,
// written with CmdSetConfig and CmdDeleteConfig only so that a manager of any
// version applies them, as the task store's are:
//
//	installlogin/<id>   a saved login, JSON InstallLogin
//
// The store lists by prefix (FSM.ConfigWithPrefix) and never copies the whole
// state. A saved login's secrets are plaintext in the FSM, as jwt_secret,
// cluster_ca_key and the agents' keys are: in every manager's Raft log and
// snapshots, under the cluster's data directory, which only root reads. A key
// to encrypt them would sit beside them and protect nothing. Agents hold no
// Raft, so no agent has them, and no route ever answers them: the install API
// says whether each is set, never what it is.
const installLoginKeyPrefix = "installlogin/"

// MaxInstallLogins is how many saved logins a cluster keeps.
const MaxInstallLogins = 32

var (
	ErrInstallLoginNotFound  = errors.New("saved login not found")
	ErrInstallLoginNameTaken = errors.New("another saved login already has this name")
	ErrInstallLoginLimit     = fmt.Errorf("a cluster keeps at most %d saved logins", MaxInstallLogins)
)

// InstallLogin is a saved SSH login for the agent install. Name is what the
// install form shows; the rest is an account as an install takes one: User,
// Method in the engine's names ("password", "key", or "none" for Tailscale
// SSH), the secrets that method uses, and the sudo password, or SudoSame for
// the login password. The secrets are plaintext in every manager's
// replicated state, like jwt_secret; agents hold none, and no route answers
// them.
type InstallLogin struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	User         string    `json:"user"`
	Method       string    `json:"method"` // the engine's names: "password", "key", "none"
	Password     string    `json:"password,omitempty"`
	Key          string    `json:"key,omitempty"`
	Passphrase   string    `json:"passphrase,omitempty"`
	SudoPassword string    `json:"sudo_password,omitempty"`
	SudoSame     bool      `json:"sudo_same"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	UpdatedBy    string    `json:"updated_by,omitempty"`
}

func installLoginKey(id string) string { return installLoginKeyPrefix + id }

// InstallLogins returns the saved logins by name without regard to case,
// from this manager's own replicated state.
func (m *Manager) InstallLogins() ([]InstallLogin, error) {
	if _, err := m.taskFSM(); err != nil {
		return nil, err
	}
	return m.installLogins(), nil
}

// installLogins reads every saved login, by name without regard to case. One
// that does not decode is logged by its key and left out: it must not hide
// the others. The decoder's error is not logged, as it can quote the value.
func (m *Manager) installLogins() []InstallLogin {
	var out []InstallLogin
	for k, v := range m.raft.GetFSM().ConfigWithPrefix(installLoginKeyPrefix) {
		var l InstallLogin
		if err := json.Unmarshal([]byte(v), &l); err != nil {
			slog.Warn("unreadable saved login in replicated state", "component", "cluster", "key", k)
			continue
		}
		out = append(out, l)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := strings.ToLower(out[i].Name), strings.ToLower(out[j].Name)
		if a != b {
			return a < b
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// InstallLogin returns saved login id, from this manager's own replicated
// state.
func (m *Manager) InstallLogin(id string) (InstallLogin, error) {
	fsm, err := m.taskFSM()
	if err != nil {
		return InstallLogin{}, err
	}
	if !taskIDPattern.MatchString(id) {
		return InstallLogin{}, ErrInstallLoginNotFound
	}
	v, ok := fsm.GetConfigValue(installLoginKey(id))
	if !ok {
		return InstallLogin{}, ErrInstallLoginNotFound
	}
	var l InstallLogin
	if err := json.Unmarshal([]byte(v), &l); err != nil {
		// Not wrapped: the decoder's error can quote the value.
		return InstallLogin{}, fmt.Errorf("saved login %s does not decode", id)
	}
	return l, nil
}

// PutInstallLogin stores l and returns it as stored (leader only). An l with
// no ID is a new login: it gets a new ID and is created now, and is refused
// with ErrInstallLoginLimit once MaxInstallLogins are kept. One with an ID
// replaces that login and keeps when it was created; ErrInstallLoginNotFound
// when there is none. Either way it is updated now, and no other login may
// have its name without regard to case (ErrInstallLoginNameTaken). The store
// checks no account: the API does before it saves one.
func (m *Manager) PutInstallLogin(l InstallLogin) (InstallLogin, error) {
	if !m.taskLeader() {
		return InstallLogin{}, ErrNotLeader
	}
	create := l.ID == ""
	if !create && !taskIDPattern.MatchString(l.ID) {
		return InstallLogin{}, ErrInstallLoginNotFound
	}

	// The checks and the write under one lock: checked apart, two logins
	// saved at once could both take a free name, or the last free place.
	m.installLoginMu.Lock()
	defer m.installLoginMu.Unlock()
	all := m.installLogins()
	now := time.Now().UTC()
	if create {
		if len(all) >= MaxInstallLogins {
			return InstallLogin{}, ErrInstallLoginLimit
		}
		l.ID = hex.EncodeToString(randomBytes(8))
		l.CreatedAt = now
	} else {
		i := slices.IndexFunc(all, func(o InstallLogin) bool { return o.ID == l.ID })
		if i < 0 {
			return InstallLogin{}, ErrInstallLoginNotFound
		}
		l.CreatedAt = all[i].CreatedAt
	}
	for _, o := range all {
		if o.ID != l.ID && strings.EqualFold(o.Name, l.Name) {
			return InstallLogin{}, ErrInstallLoginNameTaken
		}
	}
	l.UpdatedAt = now
	data, err := json.Marshal(l)
	if err != nil {
		return InstallLogin{}, err
	}
	if err := m.putTaskValue(installLoginKey(l.ID), string(data)); err != nil {
		return InstallLogin{}, err
	}
	return l, nil
}

// DeleteInstallLogin removes saved login id (leader only);
// ErrInstallLoginNotFound when there is none. An install under way that uses
// it keeps its own copy of the account until it ends.
func (m *Manager) DeleteInstallLogin(id string) error {
	if !m.taskLeader() {
		return ErrNotLeader
	}
	if !taskIDPattern.MatchString(id) {
		return ErrInstallLoginNotFound
	}
	m.installLoginMu.Lock()
	defer m.installLoginMu.Unlock()
	if _, ok := m.raft.GetFSM().GetConfigValue(installLoginKey(id)); !ok {
		return ErrInstallLoginNotFound
	}
	return m.deleteTaskValue(installLoginKey(id))
}

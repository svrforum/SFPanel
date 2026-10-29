package cluster

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"
)

// AgentKeyFile is where, in its cert dir, an agent keeps an agent key
// rotation: the generation of agent.key, and the key it moves to until the
// managers sign with that one. agent.key stays the key the managers sign
// with as far as the agent knows — all an older release reads.
const AgentKeyFile = "agent-key-rotation.json"

// agentKeyState is what AgentKeyFile holds.
type agentKeyState struct {
	Generation     int64  `json:"generation"`     // of agent.key
	Next           string `json:"next,omitempty"` // issued; the managers do not sign with it yet
	NextGeneration int64  `json:"next_generation,omitempty"`
}

// AgentKeys are an agent's internal-proxy keys: the one key its managers sign
// with (agent.key), and in a rotation the key it moves to — accepted beside it
// once on disk — then the key it left, accepted for legacyProxyGrace in memory
// only: a restart in that window shortens it, and only for a manager whose
// FSM lags the leader's. Keys are never logged.
type AgentKeys struct {
	tm      *TLSManager
	install func(sign string, accept []string)

	mu            sync.Mutex
	current       string // agent.key
	state         agentKeyState
	previous      string // the key left at the last switch
	previousUntil time.Time
	installed     []string // what install was last handed to accept
}

func (k *AgentKeys) statePath() string { return filepath.Join(k.tm.certDir, AgentKeyFile) }

func (k *AgentKeys) saveState(s agentKeyState) error {
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return replaceFile(k.statePath(), data)
}

// LoadAgentKeys reads the agent's keys from tm's cert dir; install is what
// hands the process the key to sign with and those to accept (Install). A
// missing or empty agent.key is an error: the agent has no other key its
// managers sign with, and never comes up standalone instead. A rotation file
// that cannot be read or does not parse is warned and left out: agent.key is
// then the managers' key, as for an older release. One whose next key is
// agent.key already is a switch a crash cut between its two writes, which
// this finishes.
func LoadAgentKeys(tm *TLSManager, install func(sign string, accept []string)) (*AgentKeys, error) {
	key, err := tm.LoadAgentKey()
	if err != nil {
		return nil, err
	}
	k := &AgentKeys{tm: tm, install: install, current: key}
	data, err := os.ReadFile(k.statePath())
	switch {
	case errors.Is(err, os.ErrNotExist):
		return k, nil
	case err != nil:
		slog.Warn("could not read the agent key rotation file; going by agent.key alone", "component", "agent", "path", k.statePath(), "error", err)
		return k, nil
	}
	var s agentKeyState
	if err := json.Unmarshal(data, &s); err != nil {
		slog.Warn("the agent key rotation file does not parse; going by agent.key alone", "component", "agent", "path", k.statePath(), "error", err)
		return k, nil
	}
	if s.Next != "" && s.Next == key {
		s = agentKeyState{Generation: s.NextGeneration}
		if err := k.saveState(s); err != nil {
			// Finished in memory all the same; the next boot finishes it again.
			slog.Warn("could not save the agent key rotation file", "component", "agent", "path", k.statePath(), "error", err)
		}
		slog.Info("finished a switch to the agent's new key that a restart cut", "component", "agent", "generation", s.Generation)
	}
	k.state = s
	return k, nil
}

// Install hands install the key to sign with and the keys to accept, when
// they differ from what it handed last: agent.key, the key a rotation issued
// once it is on disk, and the key left at a switch until legacyProxyGrace
// after it. Held under the lock throughout, so two calls cannot hand install
// their lists in the wrong order.
func (k *AgentKeys) Install(now time.Time) {
	k.mu.Lock()
	defer k.mu.Unlock()
	accept := []string{k.current}
	if k.state.Next != "" {
		accept = append(accept, k.state.Next)
	}
	if k.previous != "" {
		if now.Before(k.previousUntil) {
			accept = append(accept, k.previous)
		} else {
			k.previous, k.previousUntil = "", time.Time{}
		}
	}
	if k.installed != nil && slices.Equal(accept, k.installed) {
		return
	}
	k.installed = accept
	k.install(k.current, slices.Clone(accept))
	slog.Info("the agent's internal-proxy keys changed", "component", "agent", "generation", k.state.Generation, "accepted", len(accept))
}

// Current is the key the managers sign with as far as the agent knows, and
// its generation.
func (k *AgentKeys) Current() (key string, generation int64) {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.current, k.state.Generation
}

// Pending is the key a rotation issued that the managers do not sign with
// yet, and its generation: "" and 0 with none.
func (k *AgentKeys) Pending() (key string, generation int64) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.state.Next == "" {
		return "", 0
	}
	return k.state.Next, k.state.NextGeneration
}

// Due reports whether a hint's generation is newer than agent.key's.
func (k *AgentKeys) Due(generation int64) bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	return generation > k.state.Generation
}

// Take keeps key, the key rotation generation issued, beside agent.key. It is
// on disk before it is accepted, and accepted before the agent confirms it:
// the leader moves the managers to it on the confirm.
func (k *AgentKeys) Take(key string, generation int64) error {
	k.mu.Lock()
	if key == "" || generation <= k.state.Generation {
		g := k.state.Generation
		k.mu.Unlock()
		return fmt.Errorf("no key to take for generation %d: agent.key is generation %d", generation, g)
	}
	next := k.state
	next.Next, next.NextGeneration = key, generation
	if err := k.saveState(next); err != nil {
		k.mu.Unlock()
		return fmt.Errorf("save the agent's next key: %w", err)
	}
	k.state = next
	k.mu.Unlock()
	k.Install(time.Now())
	return nil
}

// Switch makes the key generation issued agent.key, once the managers sign
// with it. agent.key is written first and the rotation file after: a crash
// between leaves the new key in both, which LoadAgentKeys finishes. The key
// left is accepted for legacyProxyGrace more — a manager whose FSM lags the
// leader's still signs with it — and dropped by a timer of its own, not by
// the link's ticks, which stop while no manager answers.
func (k *AgentKeys) Switch(generation int64, now time.Time) error {
	k.mu.Lock()
	if k.state.Next == "" || k.state.NextGeneration != generation {
		k.mu.Unlock()
		return fmt.Errorf("the agent holds no key of generation %d to move to", generation)
	}
	if err := k.tm.SaveAgentKey(k.state.Next); err != nil {
		k.mu.Unlock()
		return fmt.Errorf("save agent.key: %w", err)
	}
	k.previous, k.previousUntil = k.current, now.Add(legacyProxyGrace)
	k.current = k.state.Next
	k.state = agentKeyState{Generation: generation}
	if err := k.saveState(k.state); err != nil {
		// The boot finishes it: agent.key is the file's next key.
		slog.Warn("could not save the agent key rotation file", "component", "agent", "path", k.statePath(), "error", err)
	}
	k.mu.Unlock()
	k.Install(now)
	time.AfterFunc(legacyProxyGrace, func() { k.Install(time.Now()) })
	return nil
}

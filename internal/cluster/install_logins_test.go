package cluster

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

// A saved login is created under a new ID with its times, read back whole
// from the replicated config map, replaced keeping when it was created, and
// deleted.
func TestASavedLoginIsCreatedReadReplacedAndDeleted(t *testing.T) {
	m := taskLeader(t)
	before := time.Now().UTC()
	in := InstallLogin{Name: "기본 계정", User: "ubuntu", Method: "password", Password: "pw-1", SudoSame: true, UpdatedBy: "admin",
		// A caller's own ID-less times are not the store's to keep.
		CreatedAt: time.Unix(0, 0).UTC(), UpdatedAt: time.Unix(0, 0).UTC()}
	got, err := m.PutInstallLogin(in)
	if err != nil {
		t.Fatal(err)
	}
	if !taskIDPattern.MatchString(got.ID) || got.CreatedAt.Before(before) || !got.UpdatedAt.Equal(got.CreatedAt) {
		t.Fatalf("created %+v, want a new ID and created now", got)
	}
	if got.Name != in.Name || got.User != in.User || got.Method != in.Method || got.Password != in.Password || !got.SudoSame || got.UpdatedBy != "admin" {
		t.Fatalf("created %+v, want the login given", got)
	}
	read, err := m.InstallLogin(got.ID)
	if err != nil || !reflect.DeepEqual(read, got) {
		t.Fatalf("InstallLogin = %+v, %v; want %+v", read, err, got)
	}
	// It is the config map's entry under its prefix, and nothing else is.
	entries := m.raft.GetFSM().ConfigWithPrefix(installLoginKeyPrefix)
	if v, ok := entries[installLoginKey(got.ID)]; !ok || len(entries) != 1 {
		t.Fatalf("installlogin/ entries %v, want the one login", keysOf(entries))
	} else if b, _ := json.Marshal(got); v != string(b) {
		t.Fatalf("stored %s, want %s", v, b)
	}

	time.Sleep(2 * time.Millisecond)
	next := got
	next.Name, next.Password, next.SudoSame, next.SudoPassword = "기본 계정 2", "pw-2", false, "sudo-2"
	next.CreatedAt = time.Unix(0, 0).UTC()
	replaced, err := m.PutInstallLogin(next)
	if err != nil {
		t.Fatal(err)
	}
	if replaced.ID != got.ID || !replaced.CreatedAt.Equal(got.CreatedAt) || !replaced.UpdatedAt.After(got.UpdatedAt) {
		t.Fatalf("replaced %+v, want ID %s created %v and updated after %v", replaced, got.ID, got.CreatedAt, got.UpdatedAt)
	}
	if read, _ := m.InstallLogin(got.ID); !reflect.DeepEqual(read, replaced) || read.Password != "pw-2" || read.SudoPassword != "sudo-2" {
		t.Fatalf("read back %+v, want %+v", read, replaced)
	}

	if err := m.DeleteInstallLogin(got.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.InstallLogin(got.ID); !errors.Is(err, ErrInstallLoginNotFound) {
		t.Fatalf("after the delete: %v, want ErrInstallLoginNotFound", err)
	}
	if err := m.DeleteInstallLogin(got.ID); !errors.Is(err, ErrInstallLoginNotFound) {
		t.Fatalf("deleting it again: %v, want ErrInstallLoginNotFound", err)
	}
	if _, err := m.PutInstallLogin(replaced); !errors.Is(err, ErrInstallLoginNotFound) {
		t.Fatalf("replacing a deleted login: %v, want ErrInstallLoginNotFound", err)
	}
	if list, err := m.InstallLogins(); err != nil || len(list) != 0 {
		t.Fatalf("logins after the delete: %+v, %v; want none", list, err)
	}
}

// The list is by name without regard to case: "alpha" before "Beta", which
// a byte order puts after it.
func TestSavedLoginsAreListedByNameWithoutRegardToCase(t *testing.T) {
	m := taskLeader(t)
	for _, name := range []string{"Gamma", "alpha", "Beta"} {
		if _, err := m.PutInstallLogin(InstallLogin{Name: name, User: "ops", Method: "key", Key: "k"}); err != nil {
			t.Fatal(err)
		}
	}
	list, err := m.InstallLogins()
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, l := range list {
		names = append(names, l.Name)
	}
	if want := []string{"alpha", "Beta", "Gamma"}; !slices.Equal(names, want) {
		t.Fatalf("listed %v, want %v", names, want)
	}
}

// No two logins share a name, whatever its case; a login keeps its own when
// it is saved again, in another case too.
func TestASavedLoginsNameIsUniqueWithoutRegardToCase(t *testing.T) {
	m := taskLeader(t)
	ops, err := m.PutInstallLogin(InstallLogin{Name: "Ops", User: "ops", Method: "key", Key: "k"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.PutInstallLogin(InstallLogin{Name: "oPS", User: "ops", Method: "key", Key: "k"}); !errors.Is(err, ErrInstallLoginNameTaken) {
		t.Fatalf("a second login named oPS: %v, want ErrInstallLoginNameTaken", err)
	}
	other, err := m.PutInstallLogin(InstallLogin{Name: "web", User: "ops", Method: "key", Key: "k"})
	if err != nil {
		t.Fatal(err)
	}
	other.Name = "OPS"
	if _, err := m.PutInstallLogin(other); !errors.Is(err, ErrInstallLoginNameTaken) {
		t.Fatalf("renaming web to OPS: %v, want ErrInstallLoginNameTaken", err)
	}
	ops.Name = "ops"
	if _, err := m.PutInstallLogin(ops); err != nil {
		t.Fatalf("saving Ops again as ops: %v", err)
	}
	if list, _ := m.InstallLogins(); len(list) != 2 || list[0].Name != "ops" || list[1].Name != "web" {
		t.Fatalf("logins %+v, want ops and web", list)
	}
}

// A cluster keeps 32 logins: the 33rd is refused and not stored, while one
// of the 32 can still be changed, and a delete frees a place.
func TestAtMost32LoginsAreSaved(t *testing.T) {
	m := taskLeader(t)
	var first InstallLogin
	for i := range MaxInstallLogins {
		l, err := m.PutInstallLogin(InstallLogin{Name: fmt.Sprintf("login-%02d", i), User: "ops", Method: "key", Key: "k"})
		if err != nil {
			t.Fatalf("login %d: %v", i+1, err)
		}
		if i == 0 {
			first = l
		}
	}
	if _, err := m.PutInstallLogin(InstallLogin{Name: "one too many", User: "ops", Method: "key", Key: "k"}); !errors.Is(err, ErrInstallLoginLimit) {
		t.Fatalf("the 33rd login: %v, want ErrInstallLoginLimit", err)
	}
	if list, _ := m.InstallLogins(); len(list) != MaxInstallLogins {
		t.Fatalf("%d logins kept, want %d", len(list), MaxInstallLogins)
	}
	first.User = "root"
	if _, err := m.PutInstallLogin(first); err != nil {
		t.Fatalf("changing one of 32: %v", err)
	}
	if err := m.DeleteInstallLogin(first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.PutInstallLogin(InstallLogin{Name: "one too many", User: "ops", Method: "key", Key: "k"}); err != nil {
		t.Fatalf("a login once one of 32 is deleted: %v", err)
	}
}

// An ID is a key segment: one that is not names no login, and a caller's ID
// never creates one. Entries planted under the prefix at keys no ID makes
// (as a hand edit or another build could leave) are found by none of the
// store's calls: only the segment check tells them apart, as no lookup
// misses them.
func TestASavedLoginIDMustBeAKeySegment(t *testing.T) {
	m := taskLeader(t)
	planted := map[string]string{}
	for _, id := range []string{"a/b", "../x", "taskrun/x", strings.Repeat("a", 65)} {
		b, _ := json.Marshal(InstallLogin{ID: id, Name: "planted " + id, User: "ops", Method: "key", Key: "k"})
		if err := m.SetConfig(installLoginKey(id), string(b)); err != nil {
			t.Fatal(err)
		}
		planted[installLoginKey(id)] = string(b)
	}
	for _, id := range []string{"a/b", "../x", "taskrun/x", strings.Repeat("a", 65)} {
		if _, err := m.InstallLogin(id); !errors.Is(err, ErrInstallLoginNotFound) {
			t.Errorf("InstallLogin(%q): %v, want ErrInstallLoginNotFound", id, err)
		}
		if _, err := m.PutInstallLogin(InstallLogin{ID: id, Name: "x" + id, User: "ops", Method: "key", Key: "k"}); !errors.Is(err, ErrInstallLoginNotFound) {
			t.Errorf("PutInstallLogin(ID %q): %v, want ErrInstallLoginNotFound", id, err)
		}
		if err := m.DeleteInstallLogin(id); !errors.Is(err, ErrInstallLoginNotFound) {
			t.Errorf("DeleteInstallLogin(%q): %v, want ErrInstallLoginNotFound", id, err)
		}
	}
	if _, err := m.PutInstallLogin(InstallLogin{ID: "0123456789abcdef", Name: "x", User: "ops", Method: "key", Key: "k"}); !errors.Is(err, ErrInstallLoginNotFound) {
		t.Fatalf("an unknown ID: %v, want ErrInstallLoginNotFound", err)
	}
	if left := m.raft.GetFSM().ConfigWithPrefix(installLoginKeyPrefix); !reflect.DeepEqual(left, planted) {
		t.Fatalf("installlogin/ entries %v, want only the planted ones, untouched", keysOf(left))
	}
}

// Writes go through the leader, each manager reads its own replicated copy,
// and a node with no Raft is not the leader.
func TestSavedLoginWritesNeedTheLeader(t *testing.T) {
	mgrs := inmemManagers(t, 2)
	var leader, follower *Manager
	waitFor(t, "a leader", func() bool {
		for i, m := range mgrs {
			if m.raft.IsLeader() {
				leader, follower = m, mgrs[1-i]
				return true
			}
		}
		return false
	})
	l, err := leader.PutInstallLogin(InstallLogin{Name: "ops", User: "ops", Method: "password", Password: "pw"})
	if err != nil {
		t.Fatal(err)
	}
	writes := map[string]func() error{
		"create": func() error {
			_, err := follower.PutInstallLogin(InstallLogin{Name: "x", User: "ops", Method: "key", Key: "k"})
			return err
		},
		"replace": func() error { _, err := follower.PutInstallLogin(l); return err },
		"delete":  func() error { return follower.DeleteInstallLogin(l.ID) },
	}
	for name, write := range writes {
		if err := write(); !errors.Is(err, ErrNotLeader) {
			t.Errorf("%s on a follower: %v, want ErrNotLeader", name, err)
		}
	}
	waitFor(t, "the follower to apply the write", func() bool {
		got, err := follower.InstallLogin(l.ID)
		return err == nil && got.Password == "pw"
	})

	none := &Manager{}
	if _, err := none.PutInstallLogin(InstallLogin{Name: "x", User: "ops", Method: "key", Key: "k"}); !errors.Is(err, ErrNotLeader) {
		t.Fatalf("a create on a node with no Raft: %v, want ErrNotLeader", err)
	}
	if err := none.DeleteInstallLogin(l.ID); !errors.Is(err, ErrNotLeader) {
		t.Fatalf("a delete on a node with no Raft: %v, want ErrNotLeader", err)
	}
	if _, err := none.InstallLogins(); !errors.Is(err, ErrNotInitialized) {
		t.Fatalf("a list on a node with no Raft: %v, want ErrNotInitialized", err)
	}
	if _, err := none.InstallLogin(l.ID); !errors.Is(err, ErrNotInitialized) {
		t.Fatalf("a read on a node with no Raft: %v, want ErrNotInitialized", err)
	}
}

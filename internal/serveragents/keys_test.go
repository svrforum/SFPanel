package serveragents

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func readTestdata(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func mustDearmor(t *testing.T, file []byte) []byte {
	t.Helper()
	b, err := dearmor(file)
	if err != nil {
		t.Fatalf("dearmor: %v", err)
	}
	return b
}

func mustFingerprints(t *testing.T, keyring []byte) []string {
	t.Helper()
	fps, err := keyFingerprints(keyring)
	if err != nil {
		t.Fatalf("keyFingerprints: %v", err)
	}
	return fps
}

func armor(b []byte) []byte {
	enc := base64.StdEncoding.EncodeToString(b)
	var out strings.Builder
	out.WriteString("-----BEGIN PGP PUBLIC KEY BLOCK-----\n\n")
	for len(enc) > 64 {
		out.WriteString(enc[:64] + "\n")
		enc = enc[64:]
	}
	out.WriteString(enc + "\n-----END PGP PUBLIC KEY BLOCK-----\n")
	return []byte(out.String())
}

func TestTheZabbixBundleGivesOnlyThePinnedKey(t *testing.T) {
	file := readTestdata(t, "zabbix-official-repo.key")
	keyring := mustDearmor(t, file)
	all := mustFingerprints(t, keyring)
	if len(all) != 4 || !slices.Contains(all, ZabbixKeyFingerprint) || all[0] == ZabbixKeyFingerprint {
		t.Fatalf("the bundle's keys = %q; want four, the pinned one not first", all)
	}

	out, err := SelectKey(file, ZabbixKeyFingerprint)
	if err != nil {
		t.Fatalf("SelectKey: %v", err)
	}
	if got := mustFingerprints(t, out); !slices.Equal(got, []string{ZabbixKeyFingerprint}) {
		t.Fatalf("selected keys = %q, want only %s", got, ZabbixKeyFingerprint)
	}
	if !bytes.Contains(keyring, out) {
		t.Fatal("the selected key is not the bundle's own bytes")
	}
}

func TestTheWazuhFileGivesItsKey(t *testing.T) {
	file := readTestdata(t, "GPG-KEY-WAZUH")
	out, err := SelectKey(file, WazuhKeyFingerprint)
	if err != nil {
		t.Fatalf("SelectKey: %v", err)
	}
	if got := mustFingerprints(t, out); !slices.Equal(got, []string{WazuhKeyFingerprint}) {
		t.Fatalf("selected keys = %q, want only %s", got, WazuhKeyFingerprint)
	}
	if !bytes.Equal(out, mustDearmor(t, file)) {
		t.Fatal("the file holds one key, so all of it should come back; some of it did not")
	}
	binary, err := SelectKey(out, WazuhKeyFingerprint)
	if err != nil || !bytes.Equal(binary, out) {
		t.Fatalf("SelectKey on the binary key: err %v, same bytes %v", err, bytes.Equal(binary, out))
	}
}

func TestAFileWithoutThePinnedKeyIsRefused(t *testing.T) {
	zabbix := readTestdata(t, "zabbix-official-repo.key")
	wazuh := readTestdata(t, "GPG-KEY-WAZUH")
	cases := []struct {
		name        string
		file        []byte
		fingerprint string
	}{
		{"the Wazuh file asked for the Zabbix key", wazuh, ZabbixKeyFingerprint},
		{"the Zabbix bundle asked for the Wazuh key", zabbix, WazuhKeyFingerprint},
		{"the pinned key's long ID instead of its fingerprint", zabbix, ZabbixKeyFingerprint[24:]},
		{"no fingerprint at all", zabbix, ""},
		{"no fingerprint, and a key of another version", []byte{0xC6, 0x01, 0x06}, ""},
	}
	for _, c := range cases {
		out, err := SelectKey(c.file, c.fingerprint)
		if !errors.Is(err, ErrKeyNotFound) || out != nil {
			t.Errorf("%s: out %d bytes, err %v; want nil, ErrKeyNotFound", c.name, len(out), err)
		}
	}
}

func TestFingerprintSpacingAndCaseDoNotMatter(t *testing.T) {
	file := readTestdata(t, "zabbix-official-repo.key")
	want, err := SelectKey(file, ZabbixKeyFingerprint)
	if err != nil {
		t.Fatalf("SelectKey: %v", err)
	}
	got, err := SelectKey(file, " 4c3d 6f2c c75f 5146 754f  c374 d913 219a b533 3005\n")
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("spaced lowercase fingerprint: err %v, same bytes %v", err, bytes.Equal(got, want))
	}
}

func TestNotAKeyIsRefused(t *testing.T) {
	wazuh := readTestdata(t, "GPG-KEY-WAZUH")
	wazuhKey := mustDearmor(t, wazuh)
	noise := sha256.Sum256([]byte("not a key"))
	more := sha256.Sum256(noise[:])
	random := append(noise[:], more[:]...)

	cases := []struct {
		name, reason string
		file         []byte
	}{
		{"empty", "holds no key", []byte{}},
		{"hello", "not a packet", []byte("hello")},
		{"an armored block of random bytes", "partial length", armor(random)},
		{"an armored block that is not base64", "illegal base64",
			[]byte("-----BEGIN PGP PUBLIC KEY BLOCK-----\n\n!!!!\n-----END PGP PUBLIC KEY BLOCK-----\n")},
		{"an armored block cut before its end line", "no end line", wazuh[:len(wazuh)/2]},
		{"a binary key cut inside a packet", "runs past the end", wazuhKey[:len(wazuhKey)-10]},
		{"a secret-key packet", "secret key material", []byte{0x95, 0x00, 0x01, 0x04}},
		{"the key followed by a secret-key packet", "secret key material", append(slices.Clone(wazuhKey), 0x95, 0x00, 0x01, 0x04)},
		{"the key followed by a secret-subkey packet", "secret key material", append(slices.Clone(wazuhKey), 0x9D, 0x00, 0x01, 0x04)},
		{"a user ID before any key", "does not start with a public key", append([]byte{0xCD, 0x01, 'x'}, wazuhKey...)},
		{"a packet of indeterminate length", "indeterminate length", []byte{0x9B, 0x04}},
		{"an old-format header cut short", "header is cut short", []byte{0x99, 0x01}},
		{"a new-format header cut short", "header is cut short", []byte{0xC6, 0xFF, 0x00, 0x00}},
		{"a packet of partial length", "partial length", []byte{0xC6, 0xE0, 0x04}},
	}
	for _, c := range cases {
		out, err := SelectKey(c.file, WazuhKeyFingerprint)
		if !errors.Is(err, ErrKeyUnreadable) || !strings.Contains(err.Error(), c.reason) || out != nil {
			t.Errorf("%s: out %d bytes, err %v; want nil, ErrKeyUnreadable for %q", c.name, len(out), err, c.reason)
		}
	}
}

// gpgListing returns gpg's colon listing of the keys in file, one entry per
// primary key, each the lines from its "pub" record up to the next one.
func gpgListing(t *testing.T, gpg, file string) [][]string {
	t.Helper()
	cmd := exec.Command(gpg, "--show-keys", "--with-colons", file)
	cmd.Env = append(os.Environ(), "GNUPGHOME="+t.TempDir())
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("gpg --show-keys %s: %v", filepath.Base(file), err)
	}
	var keys [][]string
	for line := range strings.Lines(string(out)) {
		line = strings.TrimSuffix(line, "\n")
		if strings.HasPrefix(line, "pub:") {
			keys = append(keys, nil)
		}
		if len(keys) > 0 {
			keys[len(keys)-1] = append(keys[len(keys)-1], line)
		}
	}
	return keys
}

func TestGPGReadsTheSelectedKey(t *testing.T) {
	gpg, err := exec.LookPath("gpg")
	if err != nil {
		t.Skip("gpg is not installed")
	}
	for _, c := range []struct{ file, fingerprint string }{
		{"zabbix-official-repo.key", ZabbixKeyFingerprint},
		{"GPG-KEY-WAZUH", WazuhKeyFingerprint},
	} {
		out, err := SelectKey(readTestdata(t, c.file), c.fingerprint)
		if err != nil {
			t.Fatalf("%s: SelectKey: %v", c.file, err)
		}
		path := filepath.Join(t.TempDir(), "selected.gpg")
		if err := os.WriteFile(path, out, 0o644); err != nil {
			t.Fatal(err)
		}

		got := gpgListing(t, gpg, path)
		if len(got) != 1 || len(got[0]) < 2 || got[0][1] != "fpr:::::::::"+c.fingerprint+":" {
			t.Fatalf("%s: gpg lists %q; want one key with fingerprint %s", c.file, got, c.fingerprint)
		}
		var original []string
		for _, key := range gpgListing(t, gpg, "testdata/"+c.file) {
			if len(key) > 1 && key[1] == got[0][1] {
				original = key
			}
		}
		if !slices.Equal(got[0], original) {
			t.Fatalf("%s: gpg reads the selected key as\n%s\nbut the vendor's file as\n%s",
				c.file, strings.Join(got[0], "\n"), strings.Join(original, "\n"))
		}
	}
}

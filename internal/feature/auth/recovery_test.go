package featureauth

import (
	"strings"
	"testing"
)

func TestGenerateRecoveryCodes(t *testing.T) {
	plain, hashes, err := generateRecoveryCodes()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(plain) != recoveryCodeCount || len(hashes) != recoveryCodeCount {
		t.Fatalf("expected %d codes, got %d/%d", recoveryCodeCount, len(plain), len(hashes))
	}
	seen := map[string]bool{}
	for i, code := range plain {
		if seen[code] {
			t.Errorf("duplicate code %q", code)
		}
		seen[code] = true
		if hashRecoveryCode(code) != hashes[i] {
			t.Errorf("hash[%d] does not match its code", i)
		}
	}
}

func TestHashRecoveryCode_Normalization(t *testing.T) {
	base := hashRecoveryCode("abcde-fghij")
	for _, variant := range []string{"ABCDE-FGHIJ", "abcdefghij", " abcde fghij ", "Abcde-Fghij"} {
		if hashRecoveryCode(variant) != base {
			t.Errorf("variant %q hashed differently from canonical form", variant)
		}
	}
	if hashRecoveryCode("zzzzz-zzzzz") == base {
		t.Error("different codes hashed equal")
	}
}

func TestRecoveryCodes_PersistLoadConsume(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.Exec(`INSERT INTO admin (username, password, totp_secret) VALUES (?, ?, ?)`,
		"alice", "phash", "secret"); err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	h := &Handler{DB: db} // no cluster manager → local (DB) path

	plain, hashes, err := generateRecoveryCodes()
	if err != nil {
		t.Fatal(err)
	}
	if err := h.persistRecoveryCodes("alice", hashes, false); err != nil {
		t.Fatalf("persist: %v", err)
	}

	got, fromCluster, err := h.loadRecoveryCodes("alice")
	if err != nil || fromCluster || len(got) != recoveryCodeCount {
		t.Fatalf("load: %d codes fromCluster=%v err=%v", len(got), fromCluster, err)
	}

	// Consume a valid code: succeeds and shrinks the set.
	ok, err := h.consumeRecoveryCode("alice", plain[3])
	if err != nil || !ok {
		t.Fatalf("consume valid: ok=%v err=%v", ok, err)
	}
	got, _, _ = h.loadRecoveryCodes("alice")
	if len(got) != recoveryCodeCount-1 {
		t.Errorf("after consume: %d codes, want %d", len(got), recoveryCodeCount-1)
	}

	// Same code again: rejected (already consumed — one-time use).
	if ok, _ := h.consumeRecoveryCode("alice", plain[3]); ok {
		t.Error("a consumed code was accepted a second time")
	}
	// A code formatted differently but still valid is accepted + consumed.
	if ok, _ := h.consumeRecoveryCode("alice", strings.ToUpper(plain[0])); !ok {
		t.Error("uppercase variant of a valid code was rejected")
	}
	// Garbage is rejected.
	if ok, _ := h.consumeRecoveryCode("alice", "nope0-nope0"); ok {
		t.Error("an invalid code was accepted")
	}

	got, _, _ = h.loadRecoveryCodes("alice")
	if len(got) != recoveryCodeCount-2 {
		t.Errorf("final count: %d, want %d", len(got), recoveryCodeCount-2)
	}
}

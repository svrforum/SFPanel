package release

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

// hashOf returns the hex-encoded SHA-256 of b. Helper for tests only.
func hashOf(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func TestVerifyInstaller_EnvUnsetPassesWithWarning(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "installer.sh")
	if err := os.WriteFile(tmp, []byte("echo hello\n"), 0644); err != nil {
		t.Fatal(err)
	}
	envVar := "SFPANEL_TEST_INSTALLER_SHA256"
	t.Setenv(envVar, "") // explicit empty

	if err := VerifyInstaller(tmp, envVar, "test"); err != nil {
		t.Fatalf("expected nil error when env unset, got %v", err)
	}
}

func TestVerifyInstaller_MatchPasses(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "installer.sh")
	content := []byte("echo hello\n")
	if err := os.WriteFile(tmp, content, 0644); err != nil {
		t.Fatal(err)
	}
	envVar := "SFPANEL_TEST_INSTALLER_SHA256"
	t.Setenv(envVar, hashOf(content))

	if err := VerifyInstaller(tmp, envVar, "test"); err != nil {
		t.Fatalf("expected nil error on hash match, got %v", err)
	}
}

func TestVerifyInstaller_MismatchFails(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "installer.sh")
	if err := os.WriteFile(tmp, []byte("echo hello\n"), 0644); err != nil {
		t.Fatal(err)
	}
	envVar := "SFPANEL_TEST_INSTALLER_SHA256"
	t.Setenv(envVar, hashOf([]byte("echo evil\n")))

	err := VerifyInstaller(tmp, envVar, "test")
	if err == nil {
		t.Fatalf("expected mismatch error, got nil")
	}
}

func TestVerifyInstaller_CaseInsensitiveAndTrimsWhitespace(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "installer.sh")
	content := []byte("payload\n")
	if err := os.WriteFile(tmp, content, 0644); err != nil {
		t.Fatal(err)
	}
	envVar := "SFPANEL_TEST_INSTALLER_SHA256"
	// Pad with spaces and use upper case — both should be tolerated.
	t.Setenv(envVar, "  "+hashUpper(hashOf(content))+"  ")

	if err := VerifyInstaller(tmp, envVar, "test"); err != nil {
		t.Fatalf("expected accept on padded/upper hash, got %v", err)
	}
}

func TestVerifyInstaller_MissingFileFails(t *testing.T) {
	envVar := "SFPANEL_TEST_INSTALLER_SHA256"
	t.Setenv(envVar, "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")

	err := VerifyInstaller(filepath.Join(t.TempDir(), "nonexistent"), envVar, "test")
	if err == nil {
		t.Fatalf("expected error for missing file, got nil")
	}
}

// hashUpper uppercases the a-f hex digits without dragging in strings.ToUpper.
func hashUpper(h string) string {
	out := make([]byte, len(h))
	for i, c := range h {
		if c >= 'a' && c <= 'f' {
			out[i] = byte(c) - 32
		} else {
			out[i] = byte(c)
		}
	}
	return string(out)
}

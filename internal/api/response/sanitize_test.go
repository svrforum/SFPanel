package response

import (
	"strings"
	"testing"
)

func TestSanitizeOutput_StripsPaths(t *testing.T) {
	input := "Error: /home/dalso/config.yaml not found"
	result := SanitizeOutput(input)
	if strings.Contains(result, "dalso") {
		t.Fatalf("expected username stripped, got: %s", result)
	}
}

func TestSanitizeOutput_StripsSecrets(t *testing.T) {
	input := "password=mysecret123 connected"
	result := SanitizeOutput(input)
	if strings.Contains(result, "mysecret123") {
		t.Fatalf("expected secret stripped, got: %s", result)
	}
}

func TestSanitizeOutput_TruncatesLong(t *testing.T) {
	input := strings.Repeat("x", 1000)
	result := SanitizeOutput(input)
	if len(result) > 520 {
		t.Fatalf("expected truncated output, got length %d", len(result))
	}
}

func TestSanitizeOutput_ShortPassthrough(t *testing.T) {
	input := "Service started successfully"
	result := SanitizeOutput(input)
	if result != input {
		t.Fatalf("expected passthrough, got: %s", result)
	}
}

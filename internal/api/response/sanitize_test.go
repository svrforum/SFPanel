package response

import (
	"strings"
	"testing"
)

func TestSanitizeOutput_StripsPaths(t *testing.T) {
	input := "Error: /home/devuser/config.yaml not found"
	result := SanitizeOutput(input)
	if strings.Contains(result, "devuser") {
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

// Secrets keep being hidden in every shape they reach a response in — env
// dumps, YAML/JSON, headers, query strings, command-line flags — while the
// words that merely end in "key" no longer hide the word after them.
func TestRedactSecrets(t *testing.T) {
	hidden := map[string]string{
		"password=mysecret123 connected":      "password=*** connected",
		"PASSWORD: hunter2":                   "PASSWORD: ***",
		"DB_PASSWORD=hunter2":                 "DB_PASSWORD=***",
		"MYSQL_ROOT_PASSWORD=hunter2":         "MYSQL_ROOT_PASSWORD=***",
		"dbpassword=hunter2":                  "dbpassword=***",
		"SECRET_KEY=abc":                      "SECRET_KEY=***",
		"SECRET_KEY_BASE=abc":                 "SECRET_KEY_BASE=***",
		"AWS_SECRET_ACCESS_KEY=abc":           "AWS_SECRET_ACCESS_KEY=***",
		"client_secret: abc":                  "client_secret: ***",
		"clientsecret=abc":                    "clientsecret=***",
		"api_key=abc":                         "api_key=***",
		"apikey=abc":                          "apikey=***",
		"x-api-key: abc":                      "x-api-key: ***",
		"private_key=abc":                     "private_key=***",
		"GITHUB_TOKEN=ghp_abc":                "GITHUB_TOKEN=***",
		"auth_token: abc":                     "auth_token: ***",
		`{"password": "hunter2"}`:             `{"password": ***`,
		"GET /hook?token=abc&x=1 200":         "GET /hook?token=*** 200",
		"run --password hunter2 now":          "run --password *** now",
		"run --token=abc":                     "run --token=***",
		"tailscale up --authkey tskey-auth-x": "tailscale up --authkey ***",
		"--passphrase hunter2":                "--passphrase ***",
	}
	for in, want := range hidden {
		if got := SanitizeLog(in); got != want {
			t.Errorf("SanitizeLog(%q) = %q, want %q", in, got, want)
		}
		if got := SanitizeOutput(in); got != want {
			t.Errorf("SanitizeOutput(%q) = %q, want %q", in, got, want)
		}
	}

	kept := []string{
		"Container npg-valkey Started",
		"Container npg-valkey  Healthy",
		"valkey=ready",
		"monkey business",
		"hotkey list",
		"apikey list",
		"turkey: 3",
		"keyboard: us",
		"max_tokens: 4096",
		"Service started successfully",
		"npg-valkey --appendonly yes",
	}
	for _, in := range kept {
		if got := SanitizeLog(in); got != in {
			t.Errorf("SanitizeLog(%q) = %q, want it unchanged", in, got)
		}
	}
}

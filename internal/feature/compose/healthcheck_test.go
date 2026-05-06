package compose

import (
	"errors"
	"strings"
	"testing"
)

const sampleCompose = `services:
  jellyfin:
    image: jellyfin/jellyfin:latest
    container_name: jellyfin
    ports:
      - '8096:8096/tcp'
    restart: unless-stopped
`

func TestApplyHealthcheck_AddCMDSHELL(t *testing.T) {
	spec := HealthcheckSpec{
		TestType:    "CMD-SHELL",
		TestValue:   "curl -f http://localhost:8096/health || exit 1",
		Interval:    "30s",
		Timeout:     "10s",
		Retries:     3,
		StartPeriod: "30s",
	}
	got, err := ApplyHealthcheck(sampleCompose, "jellyfin", spec, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "healthcheck:") {
		t.Fatalf("missing healthcheck block:\n%s", got)
	}
	if !strings.Contains(got, "CMD-SHELL") {
		t.Errorf("missing CMD-SHELL marker:\n%s", got)
	}
	if !strings.Contains(got, "interval: 30s") {
		t.Errorf("missing interval:\n%s", got)
	}
	if !strings.Contains(got, "retries: 3") {
		t.Errorf("missing retries:\n%s", got)
	}
}

const composeWithHealthcheck = `services:
  jellyfin:
    image: jellyfin/jellyfin:latest
    healthcheck:
      test: ["CMD-SHELL", "echo old"]
      interval: 60s
      timeout: 5s
      retries: 5
      start_period: 10s
    restart: unless-stopped
`

func TestApplyHealthcheck_RejectsExistingWithoutReplace(t *testing.T) {
	spec := HealthcheckSpec{
		TestType: "CMD-SHELL", TestValue: "echo new",
		Interval: "30s", Timeout: "10s", Retries: 3, StartPeriod: "30s",
	}
	_, err := ApplyHealthcheck(composeWithHealthcheck, "jellyfin", spec, false)
	if !errors.Is(err, ErrHealthcheckExists) {
		t.Fatalf("got %v want ErrHealthcheckExists", err)
	}
}

func TestApplyHealthcheck_ReplaceExisting(t *testing.T) {
	spec := HealthcheckSpec{
		TestType: "CMD-SHELL", TestValue: "echo new",
		Interval: "30s", Timeout: "10s", Retries: 3, StartPeriod: "30s",
	}
	got, err := ApplyHealthcheck(composeWithHealthcheck, "jellyfin", spec, true)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "echo old") {
		t.Errorf("old test value still present:\n%s", got)
	}
	if !strings.Contains(got, "echo new") {
		t.Errorf("new test value missing:\n%s", got)
	}
}

func TestApplyHealthcheck_ServiceMissing(t *testing.T) {
	spec := HealthcheckSpec{
		TestType: "CMD-SHELL", TestValue: "x",
		Interval: "30s", Timeout: "10s", Retries: 3, StartPeriod: "30s",
	}
	_, err := ApplyHealthcheck(sampleCompose, "nonexistent", spec, false)
	if !errors.Is(err, ErrServiceNotFound) {
		t.Fatalf("got %v want ErrServiceNotFound", err)
	}
}

func TestApplyHealthcheck_NONE(t *testing.T) {
	spec := HealthcheckSpec{TestType: "NONE"}
	got, err := ApplyHealthcheck(sampleCompose, "jellyfin", spec, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "test:") || !strings.Contains(got, "NONE") {
		t.Errorf("missing test: [NONE]:\n%s", got)
	}
	// NONE block must NOT include interval/timeout/retries/start_period.
	if strings.Contains(got, "interval:") {
		t.Errorf("NONE block should not include interval:\n%s", got)
	}
}

func TestApplyHealthcheck_CMDArgv(t *testing.T) {
	spec := HealthcheckSpec{
		TestType: "CMD", TestValue: "curl|-f|http://localhost:8096/health",
		Interval: "30s", Timeout: "10s", Retries: 3, StartPeriod: "30s",
	}
	got, err := ApplyHealthcheck(sampleCompose, "jellyfin", spec, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "CMD") || !strings.Contains(got, "curl") || !strings.Contains(got, "/health") {
		t.Errorf("CMD argv not flattened correctly:\n%s", got)
	}
}

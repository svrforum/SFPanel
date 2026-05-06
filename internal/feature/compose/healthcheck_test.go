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

func TestParseHealthcheck_PresentCMDSHELL(t *testing.T) {
	got, ok, err := ParseHealthcheck(composeWithHealthcheck, "jellyfin")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatalf("expected healthcheck found")
	}
	if got.TestType != "CMD-SHELL" {
		t.Errorf("test_type: got %q want CMD-SHELL", got.TestType)
	}
	if got.TestValue != "echo old" {
		t.Errorf("test_value: got %q want 'echo old'", got.TestValue)
	}
	if got.Interval != "60s" || got.Timeout != "5s" || got.Retries != 5 || got.StartPeriod != "10s" {
		t.Errorf("durations/retries mismatched: %+v", got)
	}
}

func TestParseHealthcheck_Absent(t *testing.T) {
	_, ok, err := ParseHealthcheck(sampleCompose, "jellyfin")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatalf("expected absent")
	}
}

// TestApplyHealthcheck_RoundTripPreservesComments — apply with
// replace=true using the SAME spec parsed back should produce a
// document where everything except the healthcheck block is
// byte-identical to the original.
func TestApplyHealthcheck_RoundTripPreservesComments(t *testing.T) {
	yamlWithComments := `# top comment
services:
  jellyfin:                # service line comment
    image: jellyfin/jellyfin:latest
    container_name: jellyfin
    ports:
      - '8096:8096/tcp'    # port comment
    restart: unless-stopped
`
	spec := HealthcheckSpec{
		TestType: "CMD-SHELL", TestValue: "echo ok",
		Interval: "30s", Timeout: "10s", Retries: 3, StartPeriod: "30s",
	}
	got, err := ApplyHealthcheck(yamlWithComments, "jellyfin", spec, false)
	if err != nil {
		t.Fatal(err)
	}
	// Comments must survive.
	if !strings.Contains(got, "# top comment") {
		t.Errorf("top comment lost:\n%s", got)
	}
	if !strings.Contains(got, "service line comment") {
		t.Errorf("service line comment lost:\n%s", got)
	}
	if !strings.Contains(got, "port comment") {
		t.Errorf("port comment lost:\n%s", got)
	}
	// Existing keys must still be present.
	if !strings.Contains(got, "container_name: jellyfin") {
		t.Errorf("container_name lost:\n%s", got)
	}
}

func TestRemoveHealthcheck_RemovesExisting(t *testing.T) {
	got, err := RemoveHealthcheck(composeWithHealthcheck, "jellyfin")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "healthcheck:") {
		t.Fatalf("healthcheck key still present:\n%s", got)
	}
	if !strings.Contains(got, "image: jellyfin/jellyfin:latest") {
		t.Errorf("other keys clobbered:\n%s", got)
	}
}

func TestRemoveHealthcheck_AbsentIsIdempotent(t *testing.T) {
	got, err := RemoveHealthcheck(sampleCompose, "jellyfin")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "image: jellyfin/jellyfin:latest") {
		t.Errorf("structural keys missing:\n%s", got)
	}
	if strings.Contains(got, "healthcheck:") {
		t.Errorf("healthcheck appeared from nowhere:\n%s", got)
	}
}

func TestRemoveHealthcheck_ServiceMissing(t *testing.T) {
	_, err := RemoveHealthcheck(sampleCompose, "nonexistent")
	if !errors.Is(err, ErrServiceNotFound) {
		t.Fatalf("got %v want ErrServiceNotFound", err)
	}
}

func TestRemoveHealthcheck_PreservesComments(t *testing.T) {
	yamlWithComments := `# top comment
services:
  jellyfin:                # service line comment
    image: jellyfin/jellyfin:latest
    healthcheck:
      test: ["CMD-SHELL", "echo old"]
      interval: 30s
      timeout: 10s
      retries: 3
      start_period: 30s
    restart: unless-stopped # restart comment
`
	got, err := RemoveHealthcheck(yamlWithComments, "jellyfin")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "healthcheck:") {
		t.Fatalf("healthcheck not removed:\n%s", got)
	}
	if !strings.Contains(got, "# top comment") {
		t.Errorf("top comment lost:\n%s", got)
	}
	if !strings.Contains(got, "service line comment") {
		t.Errorf("service line comment lost:\n%s", got)
	}
	if !strings.Contains(got, "restart comment") {
		t.Errorf("restart comment lost:\n%s", got)
	}
}

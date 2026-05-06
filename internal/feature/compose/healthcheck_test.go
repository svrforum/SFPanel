package compose

import (
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

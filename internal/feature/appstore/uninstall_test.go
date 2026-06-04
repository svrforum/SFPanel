package appstore

import (
	"strings"
	"testing"
)

func TestComposeDownArgs(t *testing.T) {
	withVol := strings.Join(composeDownArgs("/x/docker-compose.yml", false), " ")
	if !strings.Contains(withVol, " -v") {
		t.Errorf("default uninstall must remove volumes: %q", withVol)
	}
	keep := strings.Join(composeDownArgs("/x/docker-compose.yml", true), " ")
	if strings.Contains(keep, " -v") {
		t.Errorf("keep_data uninstall must NOT remove volumes: %q", keep)
	}
	if !strings.Contains(keep, "--remove-orphans") {
		t.Errorf("uninstall must always remove orphans: %q", keep)
	}
}

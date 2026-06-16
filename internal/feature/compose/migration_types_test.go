package compose

import (
	"encoding/json"
	"testing"
)

func TestManifestRoundTrip(t *testing.T) {
	m := MigrationManifest{
		SchemaVersion:      1,
		StackID:            "jellyfin",
		ComposeProjectName: "jellyfin",
		Source:             NodeRef{NodeID: "a", Arch: "amd64"},
		Target:             NodeRef{NodeID: "b", Arch: "amd64"},
		ComposeFile:        "docker-compose.yml",
		HasEnv:             true,
		Disposition:        DispositionRetain,
	}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got MigrationManifest
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.StackID != "jellyfin" || got.Disposition != DispositionRetain || got.Source.Arch != "amd64" {
		t.Fatalf("round trip mismatch: %+v", got)
	}
}

func TestDispositionValid(t *testing.T) {
	for _, d := range []Disposition{DispositionRetain, DispositionDelete, DispositionClone} {
		if !d.Valid() {
			t.Errorf("%q should be valid", d)
		}
	}
	if Disposition("wipe").Valid() {
		t.Error("unknown disposition must be invalid")
	}
}

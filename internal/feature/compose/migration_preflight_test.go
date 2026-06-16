package compose

import "testing"

func TestPreflightBlocks(t *testing.T) {
	in := PreflightInput{
		SourceArch: "amd64", TargetArch: "arm64",
		TargetFreeBytes: 1000, EstimatedBytes: 2000,
		TargetPortsInUse: []int{8096}, StackPorts: []int{8096, 9000},
		TargetStackExists: true, OverwriteAcked: false,
		SourceNodeID: "a", TargetNodeID: "b",
		HasSystemBind: true, HasExternalVolume: true, HasDevice: true,
		Disposition: DispositionRetain,
	}
	r := BuildPreflightReport(in)
	wantBlocks := map[string]bool{"arch-mismatch": true, "insufficient-disk": true, "port-conflict": true, "stack-exists": true}
	got := map[string]bool{}
	for _, b := range r.Blocks {
		got[b.Code] = true
	}
	for code := range wantBlocks {
		if !got[code] {
			t.Errorf("expected block %q, blocks=%+v", code, r.Blocks)
		}
	}
	wantWarn := map[string]bool{"system-bind": true, "external-volume": true, "device-required": true}
	gotW := map[string]bool{}
	for _, w := range r.Warnings {
		gotW[w.Code] = true
	}
	for code := range wantWarn {
		if !gotW[code] {
			t.Errorf("expected warning %q, warnings=%+v", code, r.Warnings)
		}
	}
}

func TestPreflightCleanPasses(t *testing.T) {
	in := PreflightInput{
		SourceArch: "amd64", TargetArch: "amd64",
		TargetFreeBytes: 10_000, EstimatedBytes: 1000,
		StackPorts: []int{8096}, TargetPortsInUse: []int{22},
		SourceNodeID: "a", TargetNodeID: "b", Disposition: DispositionRetain,
	}
	r := BuildPreflightReport(in)
	if len(r.Blocks) != 0 {
		t.Fatalf("expected no blocks, got %+v", r.Blocks)
	}
}

func TestPreflightSameNodeBlocks(t *testing.T) {
	r := BuildPreflightReport(PreflightInput{SourceNodeID: "a", TargetNodeID: "a", Disposition: DispositionRetain})
	found := false
	for _, b := range r.Blocks {
		if b.Code == "same-node" {
			found = true
		}
	}
	if !found {
		t.Fatal("source==target must block")
	}
}

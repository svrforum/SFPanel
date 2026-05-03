package disk

import (
	"reflect"
	"testing"
)

// /proc/mdstat fixtures.
const mdstatClean = `Personalities : [raid1] [raid6] [raid5] [raid4] [linear]
md0 : active raid1 sdb1[1] sda1[0]
      976630464 blocks super 1.2 [2/2] [UU]

unused devices: <none>
`

const mdstatDegraded = `md1 : active raid5 sdc1[0] sdd1[1] sde1[2](F) sdf1[3](S)
      1953506304 blocks super 1.2 level 5, 64k chunk, algorithm 2 [3/2] [_UU]
`

func TestParseMdstat_Clean(t *testing.T) {
	got := parseMdstat(mdstatClean)
	if len(got) != 1 {
		t.Fatalf("expected 1 array, got %d", len(got))
	}
	want := RAIDArray{
		Name:  "md0",
		State: "active",
		Level: "raid1",
		Devices: []RAIDDisk{
			{Device: "sdb1", Index: 1, State: "active"},
			{Device: "sda1", Index: 0, State: "active"},
		},
	}
	if !reflect.DeepEqual(got[0], want) {
		t.Errorf("got %+v\nwant %+v", got[0], want)
	}
}

func TestParseMdstat_Degraded(t *testing.T) {
	got := parseMdstat(mdstatDegraded)
	if len(got) != 1 {
		t.Fatalf("expected 1 array, got %d", len(got))
	}
	a := got[0]
	if a.Name != "md1" || a.Level != "raid5" {
		t.Errorf("name/level: %+v", a)
	}
	if len(a.Devices) != 4 {
		t.Fatalf("expected 4 devices, got %d", len(a.Devices))
	}

	// Build a map for state lookup; order from mdstat may shuffle.
	stateByDev := map[string]string{}
	for _, d := range a.Devices {
		stateByDev[d.Device] = d.State
	}
	if stateByDev["sde1"] != "faulty" {
		t.Errorf("sde1 should be faulty, got %q", stateByDev["sde1"])
	}
	if stateByDev["sdf1"] != "spare" {
		t.Errorf("sdf1 should be spare, got %q", stateByDev["sdf1"])
	}
	if stateByDev["sdc1"] != "active" || stateByDev["sdd1"] != "active" {
		t.Errorf("sdc1/sdd1 should be active, got %v", stateByDev)
	}
}

func TestParseMdstat_NoArrays(t *testing.T) {
	got := parseMdstat(`Personalities :
unused devices: <none>
`)
	if len(got) != 0 {
		t.Errorf("expected 0 arrays, got %d", len(got))
	}
}

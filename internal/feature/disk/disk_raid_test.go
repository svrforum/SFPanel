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

// Each array's first line as the kernel writes it (drivers/md/md.c,
// md_seq_show): the state, then a read-only flag in parentheses, then the
// personality — which an inactive array does not have — then the members,
// each with its flags ((W)rite-mostly, (J)ournal, (F)aulty, (S)pare,
// (R)eplacement). A line the parser did not take left the array out of the
// list altogether.
func TestParseMdstat_KernelLines(t *testing.T) {
	cases := []struct {
		name string
		line string
		want RAIDArray
	}{
		{"active", "md0 : active raid1 sdb1[1] sda1[0]", RAIDArray{
			Name: "md0", State: "active", Level: "raid1",
			Devices: []RAIDDisk{{Device: "sdb1", Index: 1, State: "active"}, {Device: "sda1", Index: 0, State: "active"}},
		}},
		{"auto-read-only", "md127 : active (auto-read-only) raid1 sdb1[1] sda1[0]", RAIDArray{
			Name: "md127", State: "active, auto-read-only", Level: "raid1",
			Devices: []RAIDDisk{{Device: "sdb1", Index: 1, State: "active"}, {Device: "sda1", Index: 0, State: "active"}},
		}},
		{"read-only", "md127 : active (read-only) raid1 sdb1[1] sda1[0]", RAIDArray{
			Name: "md127", State: "active, read-only", Level: "raid1",
			Devices: []RAIDDisk{{Device: "sdb1", Index: 1, State: "active"}, {Device: "sda1", Index: 0, State: "active"}},
		}},
		// No personality: the word after the state is a member, not a level.
		{"inactive", "md127 : inactive sdb1[1](S) sdc1[0](S)", RAIDArray{
			Name: "md127", State: "inactive", Level: "",
			Devices: []RAIDDisk{{Device: "sdb1", Index: 1, State: "spare"}, {Device: "sdc1", Index: 0, State: "spare"}},
		}},
		{"inactive without members", "md126 : inactive", RAIDArray{
			Name: "md126", State: "inactive", Level: "", Devices: []RAIDDisk{},
		}},
		// A member's flags come one after another, and a device-mapper member's name has a dash.
		{"member flags and names", "md2 : active raid1 dm-3[1](W)(F) nvme0n1p2[0] sdc1[2](S)(R)", RAIDArray{
			Name: "md2", State: "active", Level: "raid1",
			Devices: []RAIDDisk{{Device: "dm-3", Index: 1, State: "faulty"}, {Device: "nvme0n1p2", Index: 0, State: "active"}, {Device: "sdc1", Index: 2, State: "spare"}},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseMdstat("Personalities : [raid1]\n" + tc.line + "\n      976630464 blocks super 1.2 [2/2] [UU]\n\nunused devices: <none>\n")
			if len(got) != 1 {
				t.Fatalf("%q: expected 1 array, got %d", tc.line, len(got))
			}
			if !reflect.DeepEqual(got[0], tc.want) {
				t.Errorf("%q:\n got %+v\nwant %+v", tc.line, got[0], tc.want)
			}
		})
	}
}

// mdadm --detail's state replaces the kernel's, but mdadm does not say an
// array is read-only (for a mirror it says "clean"), so the kernel's flag is
// kept on it; a state that already says so is left as mdadm wrote it.
func TestMergeMdadmDetail_KeepsReadOnly(t *testing.T) {
	cases := []struct{ mdstat, detail, want string }{
		{"md127 : active (auto-read-only) raid1 sdb1[1] sda1[0]", "clean, degraded", "clean, degraded, auto-read-only"},
		{"md127 : active (read-only) raid1 sdb1[1] sda1[0]", "clean", "clean, read-only"},
		{"md127 : active (auto-read-only) raid0 sdb1[1] sda1[0]", "read-auto", "read-auto"},
		{"md0 : active raid1 sdb1[1] sda1[0]", "clean, degraded", "clean, degraded"},
		{"md127 : inactive sdb1[1](S) sdc1[0](S)", "inactive", "inactive"},
	}
	for _, tc := range cases {
		a := parseMdstat(tc.mdstat + "\n")
		if len(a) != 1 {
			t.Fatalf("%q: expected 1 array, got %d", tc.mdstat, len(a))
		}
		mergeMdadmDetail(&a[0], &RAIDArray{Name: a[0].Name, Level: "raid1", State: tc.detail, Total: 2})
		if a[0].State != tc.want {
			t.Errorf("%q + %q: state %q, want %q", tc.mdstat, tc.detail, a[0].State, tc.want)
		}
		if a[0].Level != "raid1" || a[0].Total != 2 {
			t.Errorf("%q: detail not laid over: %+v", tc.mdstat, a[0])
		}
	}
}

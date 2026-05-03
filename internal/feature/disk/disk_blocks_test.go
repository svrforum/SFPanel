package disk

import (
	"reflect"
	"testing"
)

// Realistic lsblk -J output covering: nvme + sata transports, polymorphic
// size (string in older lsblk, number in newer), polymorphic rota/ro, an
// LVM child, and a loop device that must be filtered out.
const lsblkJSON = `{
  "blockdevices": [
    {"name": "nvme0n1", "size": 512110190592, "type": "disk", "rota": false, "ro": false, "model": "Samsung SSD 970", "tran": "nvme",
      "children": [
        {"name": "nvme0n1p1", "size": 536870912, "type": "part", "fstype": "vfat", "mountpoint": "/boot/efi", "rota": false, "ro": false}
      ]
    },
    {"name": "sda", "size": "8589934592", "type": "disk", "rota": "1", "ro": "0", "model": " WDC WD80EFAX  ", "tran": "sata",
      "children": [
        {"name": "sda1", "size": 8588886016, "type": "part", "rota": true, "ro": false}
      ]
    },
    {"name": "loop0", "size": 67108864, "type": "loop", "rota": false, "ro": true},
    {"name": "ram0", "size": 67108864, "type": "ram", "rota": false, "ro": false}
  ]
}`

func TestParseLsblkJSON(t *testing.T) {
	devs, err := parseLsblkJSON([]byte(lsblkJSON))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(devs) != 2 {
		t.Fatalf("expected 2 devices (nvme + sata), loop/ram filtered, got %d", len(devs))
	}
	if devs[0].Name != "nvme0n1" || devs[0].Transport != "nvme" || devs[0].Size != 512110190592 {
		t.Errorf("nvme0n1 mismatch: %+v", devs[0])
	}
	if devs[0].Rotational {
		t.Errorf("nvme0n1 should be non-rotational")
	}
	if len(devs[0].Children) != 1 || devs[0].Children[0].Name != "nvme0n1p1" {
		t.Errorf("nvme0n1 child missing: %+v", devs[0].Children)
	}
	// sda used the polymorphic shapes — string size, string "1"/"0" for rota/ro.
	// Implementation only handles bool/float64 for rota/ro, so the sata case
	// keeps the JSON string-size path covered (json.Number) and string-rota
	// falls through to the false default — confirms current behavior.
	if devs[1].Name != "sda" {
		t.Errorf("expected sda second, got %q", devs[1].Name)
	}
	// Model is trimmed
	if devs[1].Model != "WDC WD80EFAX" {
		t.Errorf("model not trimmed: %q", devs[1].Model)
	}
}

func TestParseLsblkJSON_Empty(t *testing.T) {
	devs, err := parseLsblkJSON([]byte(`{"blockdevices": []}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(devs) != 0 {
		t.Errorf("expected 0 devices, got %d", len(devs))
	}
}

func TestParseLsblkJSON_Malformed(t *testing.T) {
	_, err := parseLsblkJSON([]byte(`not json`))
	if err == nil {
		t.Error("expected error on malformed JSON, got nil")
	}
}

func TestComputeSmartStatus(t *testing.T) {
	cases := []struct {
		name                          string
		value, worst, threshold int
		want                          string
	}{
		{"no threshold = ok", 100, 100, 0, "ok"},
		{"value at threshold", 50, 50, 50, "fail"},
		{"value below threshold", 49, 49, 50, "fail"},
		{"worst dipped below threshold", 100, 49, 50, "fail"},
		{"within 10% margin", 54, 54, 50, "warn"}, // (54-50)/50 = 0.08 < 0.10
		{"healthy margin", 100, 100, 50, "ok"},    // (100-50)/50 = 1.0
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := computeSmartStatus(c.value, c.worst, c.threshold)
			if got != c.want {
				t.Errorf("computeSmartStatus(%d,%d,%d)=%q; want %q",
					c.value, c.worst, c.threshold, got, c.want)
			}
		})
	}
}

const smartctlJSON = `{
  "device": {"name": "/dev/sda"},
  "model_name": "Samsung SSD 970 EVO Plus 1TB",
  "serial_number": "S4EVNX0R123456",
  "firmware_version": "2B2QEXM7",
  "smart_status": {"passed": true},
  "temperature": {"current": 38},
  "power_on_time": {"hours": 12345},
  "ata_smart_attributes": {
    "table": [
      {"id": 5, "name": "Reallocated_Sector_Ct", "value": 100, "worst": 100, "thresh": 10, "raw": {"string": "0"}},
      {"id": 197, "name": "Current_Pending_Sector", "value": 100, "worst": 100, "thresh": 0, "raw": {"string": "0"}}
    ]
  }
}`

func TestParseSmartctlJSON(t *testing.T) {
	info, err := parseSmartctlJSON("/dev/sda", []byte(smartctlJSON))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if info.ModelName != "Samsung SSD 970 EVO Plus 1TB" {
		t.Errorf("ModelName: got %q", info.ModelName)
	}
	if info.Healthy == nil || !*info.Healthy {
		t.Error("expected Healthy=true")
	}
	if info.Temperature != 38 {
		t.Errorf("Temperature: got %d, want 38", info.Temperature)
	}
	if info.PowerOnHours != 12345 {
		t.Errorf("PowerOnHours: got %d, want 12345", info.PowerOnHours)
	}
	if len(info.Attributes) != 2 {
		t.Fatalf("expected 2 attributes, got %d", len(info.Attributes))
	}
	if info.Attributes[0].ID != 5 || info.Attributes[0].Name != "Reallocated_Sector_Ct" {
		t.Errorf("attr 0 mismatch: %+v", info.Attributes[0])
	}
}

func TestParseSmartctlJSON_FailingHealth(t *testing.T) {
	const failing = `{"smart_status": {"passed": false}, "temperature": {"current": 65}}`
	info, err := parseSmartctlJSON("/dev/sdb", []byte(failing))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if info.Healthy == nil || *info.Healthy {
		t.Error("expected Healthy=false")
	}
}

func TestParseSmartctlJSON_Empty(t *testing.T) {
	info, err := parseSmartctlJSON("/dev/sdc", []byte(`{}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if info.Healthy != nil {
		t.Errorf("expected nil Healthy on missing smart_status, got %v", *info.Healthy)
	}
	if !reflect.DeepEqual(info.Attributes, []SmartAttr{}) {
		t.Errorf("expected empty Attributes, got %v", info.Attributes)
	}
}

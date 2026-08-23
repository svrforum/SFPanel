package network

import (
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// withNetplanDir points netplan discovery at a temp dir for one test
// (cleaned up via t.Cleanup). Tests using this must not call t.Parallel —
// the override is a package-level var and would race across goroutines.
func withNetplanDir(t *testing.T) string {
	t.Helper()
	prev := netplanDir
	dir := t.TempDir()
	netplanDir = dir
	t.Cleanup(func() { netplanDir = prev })
	return dir
}

// writeNetplan writes a fixture into the given directory and returns its path.
func writeNetplan(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func boolPtr(b bool) *bool { return &b }
func intPtr(i int) *int    { return &i }

// ---------- netplan fixtures (placeholder addressing only — public repo) ----------

// DHCP ethernet with unknown keys (optional/match/set-name) that must survive
// via the Extra inline map. The MAC is a Docker-bridge-style example address.
const fixtureDHCPEthernet = `network:
  version: 2
  renderer: networkd
  ethernets:
    eth0:
      dhcp4: true
      optional: true
      match:
        macaddress: "02:42:ac:11:00:02"
      set-name: eth0
`

// Static ethernet with routes carrying int (metric) and bool (on-link)
// scalars — yaml.v3 decodes them leniently into the []map[string]string.
const fixtureStaticWithRoutes = `network:
  version: 2
  ethernets:
    eth0:
      dhcp4: false
      addresses:
        - 192.168.1.10/24
      gateway4: 192.168.1.1
      nameservers:
        addresses:
          - 8.8.8.8
        search:
          - lan
      routes:
        - to: 10.0.0.0/8
          via: 192.168.1.254
          metric: 100
          on-link: true
`

const fixtureBond = `network:
  version: 2
  bonds:
    bond0:
      dhcp4: true
      interfaces:
        - eth1
        - eth2
      parameters:
        mode: active-backup
        primary: eth1
        mii-monitor-interval: 100
`

// Everything in one file: ethernet with unknown keys and typed route scalars,
// bond with an unknown parameter, plus bridges/vlans blocks that only exist
// through the Extra inline maps.
const fixtureFull = `network:
  version: 2
  renderer: networkd
  ethernets:
    eth0:
      dhcp4: false
      addresses:
        - 192.168.1.10/24
      gateway4: 192.168.1.1
      optional: true
      match:
        macaddress: "02:42:ac:11:00:02"
      set-name: eth0
      routes:
        - to: 10.0.0.0/8
          via: 192.168.1.254
          metric: 100
          on-link: true
  bonds:
    bond0:
      dhcp4: true
      interfaces:
        - eth1
        - eth2
      parameters:
        mode: active-backup
        primary: eth1
        mii-monitor-interval: 100
  bridges:
    br0:
      dhcp4: true
      interfaces:
        - eth3
  vlans:
    vlan10:
      id: 10
      link: eth0
`

func TestConfigureInterface_HappyPath(t *testing.T) {
	t.Run("static config applied and unknown keys preserved", func(t *testing.T) {
		dir := withNetplanDir(t)
		path := writeNetplan(t, dir, "01-netcfg.yaml", fixtureDHCPEthernet)

		body := `{"dhcp4":false,"addresses":["192.168.1.20/24"],"gateway4":"192.168.1.1","dns":["1.1.1.1"],"mtu":1500}`
		rec := doConfigureInterface(t, "eth0", body)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
		}

		np, err := loadNetplanFile(path)
		if err != nil {
			t.Fatalf("reload: %v", err)
		}
		eth := np.Network.Ethernets["eth0"]
		if eth == nil {
			t.Fatal("eth0 missing after update")
		}
		if eth.DHCP4 == nil || *eth.DHCP4 {
			t.Errorf("dhcp4 = %v, want false", eth.DHCP4)
		}
		if !reflect.DeepEqual(eth.Addresses, []string{"192.168.1.20/24"}) {
			t.Errorf("addresses = %v, want [192.168.1.20/24]", eth.Addresses)
		}
		if eth.Gateway4 != "192.168.1.1" {
			t.Errorf("gateway4 = %q, want 192.168.1.1", eth.Gateway4)
		}
		if eth.Nameservers == nil || !reflect.DeepEqual(eth.Nameservers.Addresses, []string{"1.1.1.1"}) {
			t.Errorf("nameservers = %+v, want addresses [1.1.1.1]", eth.Nameservers)
		}
		if eth.MTU == nil || *eth.MTU != 1500 {
			t.Errorf("mtu = %v, want 1500", eth.MTU)
		}
		for _, key := range []string{"optional", "match", "set-name"} {
			if _, ok := eth.Extra[key]; !ok {
				t.Errorf("unknown key %q lost after update (extra: %v)", key, eth.Extra)
			}
		}
	})

	t.Run("MTU boundary 576 accepted", func(t *testing.T) {
		dir := withNetplanDir(t)
		path := writeNetplan(t, dir, "01-netcfg.yaml", fixtureDHCPEthernet)
		rec := doConfigureInterface(t, "eth0", `{"mtu":576}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
		}
		np, err := loadNetplanFile(path)
		if err != nil {
			t.Fatalf("reload: %v", err)
		}
		if mtu := np.Network.Ethernets["eth0"].MTU; mtu == nil || *mtu != 576 {
			t.Errorf("mtu = %v, want 576", mtu)
		}
	})

	t.Run("MTU boundary 9216 accepted", func(t *testing.T) {
		dir := withNetplanDir(t)
		path := writeNetplan(t, dir, "01-netcfg.yaml", fixtureDHCPEthernet)
		rec := doConfigureInterface(t, "eth0", `{"mtu":9216}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
		}
		np, err := loadNetplanFile(path)
		if err != nil {
			t.Fatalf("reload: %v", err)
		}
		if mtu := np.Network.Ethernets["eth0"].MTU; mtu == nil || *mtu != 9216 {
			t.Errorf("mtu = %v, want 9216", mtu)
		}
	})

	t.Run("IPv4-mapped gateway6 accepted", func(t *testing.T) {
		// The gateway6 validator only requires net.ParseIP success plus a
		// colon, so IPv4-mapped forms like ::ffff:1.2.3.4 pass (frozen spec).
		dir := withNetplanDir(t)
		path := writeNetplan(t, dir, "01-netcfg.yaml", fixtureDHCPEthernet)
		rec := doConfigureInterface(t, "eth0", `{"gateway6":"::ffff:1.2.3.4"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
		}
		np, err := loadNetplanFile(path)
		if err != nil {
			t.Fatalf("reload: %v", err)
		}
		if gw6 := np.Network.Ethernets["eth0"].Gateway6; gw6 != "::ffff:1.2.3.4" {
			t.Errorf("gateway6 = %q, want ::ffff:1.2.3.4", gw6)
		}
	})
}

func TestSaveNetplanFile(t *testing.T) {
	t.Run("round-trip with atomic-write contract", func(t *testing.T) {
		dir := t.TempDir()
		path := writeNetplan(t, dir, "01-netcfg.yaml", fixtureStaticWithRoutes)

		np, err := loadNetplanFile(path)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if err := saveNetplanFile(path, np); err != nil {
			t.Fatalf("save: %v", err)
		}

		np2, err := loadNetplanFile(path)
		if err != nil {
			t.Fatalf("reload: %v", err)
		}
		if !reflect.DeepEqual(np, np2) {
			t.Errorf("round-trip mismatch:\n got %+v\nwant %+v", np2, np)
		}

		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat: %v", err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("file mode = %o, want 600", perm)
		}

		// No .netplan-* temp file may remain after a successful save.
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read dir: %v", err)
		}
		if len(entries) != 1 {
			t.Errorf("expected only the target file in dir, got %d entries: %v", len(entries), entries)
		}
	})

	t.Run("nonexistent directory fails without creating target", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(dir, "missing", "out.yaml")
		np := &netplanData{Network: &netplanNetwork{Version: 2}}
		if err := saveNetplanFile(target, np); err == nil {
			t.Fatal("expected error for nonexistent directory")
		}
		if _, err := os.Stat(target); !os.IsNotExist(err) {
			t.Errorf("target file should not exist, stat err = %v", err)
		}
	})
}

func TestLoadNetplanFile(t *testing.T) {
	t.Run("valid fixture parses", func(t *testing.T) {
		dir := t.TempDir()
		path := writeNetplan(t, dir, "01-netcfg.yaml", fixtureDHCPEthernet)
		np, err := loadNetplanFile(path)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if np.Network == nil || np.Network.Version != 2 || np.Network.Renderer != "networkd" {
			t.Fatalf("network header = %+v, want version 2 renderer networkd", np.Network)
		}
		eth := np.Network.Ethernets["eth0"]
		if eth == nil || eth.DHCP4 == nil || !*eth.DHCP4 {
			t.Errorf("eth0 = %+v, want dhcp4 true", eth)
		}
		if _, ok := eth.Extra["optional"]; !ok {
			t.Errorf("unknown key optional not captured in Extra: %v", eth.Extra)
		}
	})

	t.Run("missing file errors", func(t *testing.T) {
		if _, err := loadNetplanFile(filepath.Join(t.TempDir(), "nope.yaml")); err == nil {
			t.Fatal("expected error for missing file")
		}
	})

	t.Run("invalid YAML errors", func(t *testing.T) {
		dir := t.TempDir()
		path := writeNetplan(t, dir, "broken.yaml", "network: [broken")
		if _, err := loadNetplanFile(path); err == nil {
			t.Fatal("expected error for invalid YAML")
		}
	})

	t.Run("route int and bool scalars decode as strings", func(t *testing.T) {
		// yaml.v3 (v3.0.1) decodes `metric: 100` and `on-link: true` into
		// the []map[string]string leniently as "100"/"true" — frozen spec,
		// not an unmarshal failure.
		dir := t.TempDir()
		path := writeNetplan(t, dir, "01-netcfg.yaml", fixtureStaticWithRoutes)
		np, err := loadNetplanFile(path)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		routes := np.Network.Ethernets["eth0"].Routes
		if len(routes) != 1 {
			t.Fatalf("expected 1 route, got %d", len(routes))
		}
		if routes[0]["metric"] != "100" {
			t.Errorf("metric = %q, want \"100\"", routes[0]["metric"])
		}
		if routes[0]["on-link"] != "true" {
			t.Errorf("on-link = %q, want \"true\"", routes[0]["on-link"])
		}
	})
}

func TestNetplanRoundTrip_PreservesUnknownKeys(t *testing.T) {
	dir := t.TempDir()
	path := writeNetplan(t, dir, "01-full.yaml", fixtureFull)

	np, err := loadNetplanFile(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := saveNetplanFile(path, np); err != nil {
		t.Fatalf("save: %v", err)
	}
	np2, err := loadNetplanFile(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}

	eth := np2.Network.Ethernets["eth0"]
	if eth == nil {
		t.Fatal("eth0 missing after round-trip")
	}
	if v, ok := eth.Extra["optional"]; !ok || v != true {
		t.Errorf("optional = %v (present=%v), want true", v, ok)
	}
	if v, ok := eth.Extra["set-name"]; !ok || v != "eth0" {
		t.Errorf("set-name = %v (present=%v), want eth0", v, ok)
	}
	match, ok := eth.Extra["match"].(map[string]interface{})
	if !ok || match["macaddress"] != "02:42:ac:11:00:02" {
		t.Errorf("match = %v, want macaddress 02:42:ac:11:00:02", eth.Extra["match"])
	}

	bond := np2.Network.Bonds["bond0"]
	if bond == nil || bond.Parameters == nil {
		t.Fatalf("bond0 = %+v, want parameters present", bond)
	}
	if v, ok := bond.Parameters.Extra["mii-monitor-interval"]; !ok || v != 100 {
		t.Errorf("mii-monitor-interval = %v (present=%v), want 100", v, ok)
	}

	// bridges/vlans blocks live entirely in Extra inline maps and must survive.
	if br := np2.Network.Bridges["br0"]; br == nil || br.Extra["interfaces"] == nil {
		t.Errorf("br0 = %+v, want interfaces preserved", np2.Network.Bridges["br0"])
	}
	if vl := np2.Network.Vlans["vlan10"]; vl == nil || vl.Extra["id"] != 10 || vl.Extra["link"] != "eth0" {
		t.Errorf("vlan10 = %+v, want id 10 link eth0", np2.Network.Vlans["vlan10"])
	}

	// Frozen notation change (verified against yaml.v3 v3.0.1): the lenient
	// string decoding above means `metric: 100` / `on-link: true` re-marshal
	// as quoted strings. netplan parses scalars by content, so this is
	// harmless — but if `netplan generate` ever rejects a saved file, this
	// assertion is the diagnostic baseline.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read saved file: %v", err)
	}
	if !strings.Contains(string(data), `metric: "100"`) {
		t.Errorf("saved YAML lost the quoted metric notation:\n%s", data)
	}
	if !strings.Contains(string(data), `on-link: "true"`) {
		t.Errorf("saved YAML lost the quoted on-link notation:\n%s", data)
	}
}

func TestFindNetplanFiles(t *testing.T) {
	t.Run("yaml and yml merged sorted", func(t *testing.T) {
		dir := withNetplanDir(t)
		writeNetplan(t, dir, "b.yaml", "network:\n  version: 2\n")
		writeNetplan(t, dir, "a.yaml", "network:\n  version: 2\n")
		writeNetplan(t, dir, "c.yml", "network:\n  version: 2\n")

		files, err := findNetplanFiles()
		if err != nil {
			t.Fatalf("findNetplanFiles: %v", err)
		}
		want := []string{
			filepath.Join(dir, "a.yaml"),
			filepath.Join(dir, "b.yaml"),
			filepath.Join(dir, "c.yml"),
		}
		if !reflect.DeepEqual(files, want) {
			t.Errorf("files = %v, want %v", files, want)
		}
	})

	t.Run("empty directory yields no files and no error", func(t *testing.T) {
		withNetplanDir(t)
		files, err := findNetplanFiles()
		if err != nil {
			t.Fatalf("findNetplanFiles: %v", err)
		}
		if len(files) != 0 {
			t.Errorf("files = %v, want empty", files)
		}
	})
}

func TestUpdateNetplanInterface(t *testing.T) {
	t.Run("existing ethernet modified in place", func(t *testing.T) {
		dir := withNetplanDir(t)
		path := writeNetplan(t, dir, "01-netcfg.yaml", fixtureDHCPEthernet)

		req := &ConfigureInterfaceRequest{
			DHCP4:     boolPtr(false),
			Addresses: []string{"192.168.1.30/24"},
			Gateway4:  strPtr("192.168.1.1"),
		}
		if err := updateNetplanInterface("eth0", req); err != nil {
			t.Fatalf("updateNetplanInterface: %v", err)
		}
		np, err := loadNetplanFile(path)
		if err != nil {
			t.Fatalf("reload: %v", err)
		}
		eth := np.Network.Ethernets["eth0"]
		if eth.DHCP4 == nil || *eth.DHCP4 {
			t.Errorf("dhcp4 = %v, want false", eth.DHCP4)
		}
		if !reflect.DeepEqual(eth.Addresses, []string{"192.168.1.30/24"}) {
			t.Errorf("addresses = %v, want [192.168.1.30/24]", eth.Addresses)
		}
	})

	t.Run("unknown interface added to first file", func(t *testing.T) {
		dir := withNetplanDir(t)
		first := writeNetplan(t, dir, "01-first.yaml", fixtureDHCPEthernet)
		second := writeNetplan(t, dir, "02-second.yaml", fixtureBond)

		req := &ConfigureInterfaceRequest{Addresses: []string{"192.168.1.40/24"}}
		if err := updateNetplanInterface("eth5", req); err != nil {
			t.Fatalf("updateNetplanInterface: %v", err)
		}
		np, err := loadNetplanFile(first)
		if err != nil {
			t.Fatalf("reload first: %v", err)
		}
		eth := np.Network.Ethernets["eth5"]
		if eth == nil || !reflect.DeepEqual(eth.Addresses, []string{"192.168.1.40/24"}) {
			t.Errorf("eth5 in first file = %+v, want addresses [192.168.1.40/24]", eth)
		}
		np2, err := loadNetplanFile(second)
		if err != nil {
			t.Fatalf("reload second: %v", err)
		}
		if np2.Network.Ethernets != nil {
			t.Errorf("second file gained ethernets: %+v", np2.Network.Ethernets)
		}
	})

	t.Run("empty file gets a version 2 network", func(t *testing.T) {
		dir := withNetplanDir(t)
		path := writeNetplan(t, dir, "01-empty.yaml", "")

		req := &ConfigureInterfaceRequest{DHCP4: boolPtr(true)}
		if err := updateNetplanInterface("eth0", req); err != nil {
			t.Fatalf("updateNetplanInterface: %v", err)
		}
		np, err := loadNetplanFile(path)
		if err != nil {
			t.Fatalf("reload: %v", err)
		}
		if np.Network == nil || np.Network.Version != 2 {
			t.Fatalf("network = %+v, want version 2", np.Network)
		}
		if eth := np.Network.Ethernets["eth0"]; eth == nil || eth.DHCP4 == nil || !*eth.DHCP4 {
			t.Errorf("eth0 = %+v, want dhcp4 true", np.Network.Ethernets["eth0"])
		}
	})

	t.Run("bond interface updated via bonds path", func(t *testing.T) {
		dir := withNetplanDir(t)
		path := writeNetplan(t, dir, "01-bond.yaml", fixtureBond)

		req := &ConfigureInterfaceRequest{MTU: intPtr(1400)}
		if err := updateNetplanInterface("bond0", req); err != nil {
			t.Fatalf("updateNetplanInterface: %v", err)
		}
		np, err := loadNetplanFile(path)
		if err != nil {
			t.Fatalf("reload: %v", err)
		}
		bond := np.Network.Bonds["bond0"]
		if bond == nil || bond.MTU == nil || *bond.MTU != 1400 {
			t.Errorf("bond0 = %+v, want mtu 1400", bond)
		}
		if !reflect.DeepEqual(bond.Interfaces, []string{"eth1", "eth2"}) {
			t.Errorf("bond interfaces = %v, want [eth1 eth2]", bond.Interfaces)
		}
		if _, ok := np.Network.Ethernets["bond0"]; ok {
			t.Error("bond0 duplicated into ethernets section")
		}
	})

	t.Run("dhcp4 true clears static fields", func(t *testing.T) {
		dir := withNetplanDir(t)
		path := writeNetplan(t, dir, "01-static.yaml", fixtureStaticWithRoutes)

		req := &ConfigureInterfaceRequest{DHCP4: boolPtr(true)}
		if err := updateNetplanInterface("eth0", req); err != nil {
			t.Fatalf("updateNetplanInterface: %v", err)
		}
		np, err := loadNetplanFile(path)
		if err != nil {
			t.Fatalf("reload: %v", err)
		}
		eth := np.Network.Ethernets["eth0"]
		if eth.DHCP4 == nil || !*eth.DHCP4 {
			t.Errorf("dhcp4 = %v, want true", eth.DHCP4)
		}
		if eth.Addresses != nil || eth.Gateway4 != "" || eth.Gateway6 != "" || eth.Nameservers != nil {
			t.Errorf("static fields not cleared: %+v", eth)
		}
	})

	t.Run("no netplan files errors", func(t *testing.T) {
		withNetplanDir(t)
		req := &ConfigureInterfaceRequest{}
		if err := updateNetplanInterface("eth0", req); err == nil {
			t.Fatal("expected error with no netplan files")
		}
	})

	t.Run("interface in second file leaves first untouched", func(t *testing.T) {
		dir := withNetplanDir(t)
		first := writeNetplan(t, dir, "01-first.yaml", fixtureDHCPEthernet)
		// The static fixture's interface is renamed so only the second file has it.
		second := writeNetplan(t, dir, "02-second.yaml",
			strings.Replace(fixtureStaticWithRoutes, "eth0:", "eth1:", 1))

		before, err := os.ReadFile(first)
		if err != nil {
			t.Fatalf("read first: %v", err)
		}
		req := &ConfigureInterfaceRequest{MTU: intPtr(9000)}
		if err := updateNetplanInterface("eth1", req); err != nil {
			t.Fatalf("updateNetplanInterface: %v", err)
		}
		after, err := os.ReadFile(first)
		if err != nil {
			t.Fatalf("re-read first: %v", err)
		}
		if string(before) != string(after) {
			t.Error("first file changed although the interface lives in the second")
		}
		np, err := loadNetplanFile(second)
		if err != nil {
			t.Fatalf("reload second: %v", err)
		}
		if mtu := np.Network.Ethernets["eth1"].MTU; mtu == nil || *mtu != 9000 {
			t.Errorf("mtu = %v, want 9000", mtu)
		}
	})
}

func TestApplyConfigToEthernet(t *testing.T) {
	cases := []struct {
		name string
		eth  *netplanEthernet
		req  ConfigureInterfaceRequest
		want *netplanEthernet
	}{
		{
			name: "nil DHCP4 keeps existing dhcp4",
			eth:  &netplanEthernet{DHCP4: boolPtr(true)},
			req:  ConfigureInterfaceRequest{},
			want: &netplanEthernet{DHCP4: boolPtr(true)},
		},
		{
			// Was a frozen quirk: the gateways were assigned unconditionally
			// while Addresses, DNS and MTU beside them were nil-guarded, so a
			// partial update — an MTU change, or any save from a dialog that
			// never prefilled the field — deleted the host's default route.
			name: "omitted gateway keeps the existing gateway",
			eth: &netplanEthernet{
				DHCP4:     boolPtr(false),
				Addresses: []string{"192.168.1.10/24"},
				Gateway4:  "192.168.1.1",
				Gateway6:  "fd00::1",
			},
			req: ConfigureInterfaceRequest{DHCP4: boolPtr(false)},
			want: &netplanEthernet{
				DHCP4:     boolPtr(false),
				Addresses: []string{"192.168.1.10/24"},
				Gateway4:  "192.168.1.1",
				Gateway6:  "fd00::1",
			},
		},
		{
			// Clearing still has to be possible, and an explicit empty string
			// is how the caller asks for it. If this passed while the case
			// above also passed under the old code, the guard would be
			// meaningless.
			name: "explicit empty gateway clears it",
			eth: &netplanEthernet{
				DHCP4:     boolPtr(false),
				Addresses: []string{"192.168.1.10/24"},
				Gateway4:  "192.168.1.1",
			},
			req: ConfigureInterfaceRequest{DHCP4: boolPtr(false), Gateway4: strPtr("")},
			want: &netplanEthernet{
				DHCP4:     boolPtr(false),
				Addresses: []string{"192.168.1.10/24"},
			},
		},
		{
			name: "nil DNS keeps existing nameservers",
			eth: &netplanEthernet{
				Nameservers: &netplanNameservers{Addresses: []string{"8.8.8.8"}, Search: []string{"lan"}},
			},
			req: ConfigureInterfaceRequest{Gateway4: strPtr("192.168.1.1")},
			want: &netplanEthernet{
				Gateway4:    "192.168.1.1",
				Nameservers: &netplanNameservers{Addresses: []string{"8.8.8.8"}, Search: []string{"lan"}},
			},
		},
		{
			name: "nil Addresses keeps existing addresses",
			eth:  &netplanEthernet{Addresses: []string{"192.168.1.10/24"}},
			req:  ConfigureInterfaceRequest{Gateway4: strPtr("192.168.1.1")},
			want: &netplanEthernet{
				Addresses: []string{"192.168.1.10/24"},
				Gateway4:  "192.168.1.1",
			},
		},
		{
			name: "MTU applied",
			eth:  &netplanEthernet{},
			req:  ConfigureInterfaceRequest{MTU: intPtr(9000)},
			want: &netplanEthernet{MTU: intPtr(9000)},
		},
		{
			// Only DHCP4=true triggers the static-field clear;
			// DHCP6=true leaves addresses and nameservers in place
			// (frozen current behavior).
			name: "DHCP6 true does not clear static fields",
			eth: &netplanEthernet{
				Addresses:   []string{"192.168.1.10/24"},
				Nameservers: &netplanNameservers{Addresses: []string{"8.8.8.8"}},
			},
			req: ConfigureInterfaceRequest{DHCP6: boolPtr(true)},
			want: &netplanEthernet{
				DHCP6:       boolPtr(true),
				Addresses:   []string{"192.168.1.10/24"},
				Nameservers: &netplanNameservers{Addresses: []string{"8.8.8.8"}},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			applyConfigToEthernet(tc.eth, &tc.req)
			if !reflect.DeepEqual(tc.eth, tc.want) {
				t.Errorf("after apply:\n got %+v\nwant %+v", tc.eth, tc.want)
			}
		})
	}
}

func TestCreateNetplanBond(t *testing.T) {
	t.Run("new bond with dhcp4 default", func(t *testing.T) {
		dir := withNetplanDir(t)
		path := writeNetplan(t, dir, "01-netcfg.yaml", fixtureDHCPEthernet)

		req := &CreateBondRequest{
			Name:    "bond1",
			Mode:    "active-backup",
			Slaves:  []string{"eth1", "eth2"},
			Primary: "eth1",
		}
		if err := createNetplanBond(req); err != nil {
			t.Fatalf("createNetplanBond: %v", err)
		}
		np, err := loadNetplanFile(path)
		if err != nil {
			t.Fatalf("reload: %v", err)
		}
		bond := np.Network.Bonds["bond1"]
		if bond == nil {
			t.Fatal("bond1 missing")
		}
		if !reflect.DeepEqual(bond.Interfaces, []string{"eth1", "eth2"}) {
			t.Errorf("interfaces = %v, want [eth1 eth2]", bond.Interfaces)
		}
		if bond.Parameters == nil || bond.Parameters.Mode != "active-backup" || bond.Parameters.Primary != "eth1" {
			t.Errorf("parameters = %+v, want mode active-backup primary eth1", bond.Parameters)
		}
		if bond.DHCP4 == nil || !*bond.DHCP4 {
			t.Errorf("dhcp4 = %v, want default true for new bonds", bond.DHCP4)
		}

		// Creating the same bond again is rejected.
		if err := createNetplanBond(req); err == nil {
			t.Fatal("expected duplicate-bond error")
		}
	})

	t.Run("no netplan files errors", func(t *testing.T) {
		withNetplanDir(t)
		req := &CreateBondRequest{Name: "bond1", Mode: "active-backup", Slaves: []string{"eth1"}}
		if err := createNetplanBond(req); err == nil {
			t.Fatal("expected error with no netplan files")
		}
	})
}

func TestDeleteNetplanBond(t *testing.T) {
	t.Run("delete prunes empty bonds map and keeps ethernets", func(t *testing.T) {
		dir := withNetplanDir(t)
		path := writeNetplan(t, dir, "01-full.yaml", fixtureFull)

		if err := deleteNetplanBond("bond0"); err != nil {
			t.Fatalf("deleteNetplanBond: %v", err)
		}
		np, err := loadNetplanFile(path)
		if err != nil {
			t.Fatalf("reload: %v", err)
		}
		if np.Network.Bonds != nil {
			t.Errorf("bonds = %+v, want nil after last bond removed", np.Network.Bonds)
		}
		eth := np.Network.Ethernets["eth0"]
		if eth == nil || !reflect.DeepEqual(eth.Addresses, []string{"192.168.1.10/24"}) {
			t.Errorf("eth0 = %+v, want untouched addresses [192.168.1.10/24]", eth)
		}
	})

	t.Run("missing bond errors", func(t *testing.T) {
		dir := withNetplanDir(t)
		writeNetplan(t, dir, "01-netcfg.yaml", fixtureDHCPEthernet)
		if err := deleteNetplanBond("bond9"); err == nil {
			t.Fatal("expected error for missing bond")
		}
	})
}

func TestUpdateNetplanDNS(t *testing.T) {
	// QUIRK (frozen current behavior, reported separately): updateNetplanDNS
	// ranges over the ethernets map and writes to the "first" entry — Go map
	// iteration is randomized, so with 2+ ethernets in one file the target
	// interface is nondeterministic. Fixtures here MUST stay single-ethernet.
	t.Run("replaces addresses and keeps existing search", func(t *testing.T) {
		dir := withNetplanDir(t)
		path := writeNetplan(t, dir, "01-static.yaml", fixtureStaticWithRoutes)

		if err := updateNetplanDNS([]string{"1.1.1.1", "9.9.9.9"}); err != nil {
			t.Fatalf("updateNetplanDNS: %v", err)
		}
		np, err := loadNetplanFile(path)
		if err != nil {
			t.Fatalf("reload: %v", err)
		}
		ns := np.Network.Ethernets["eth0"].Nameservers
		if ns == nil || !reflect.DeepEqual(ns.Addresses, []string{"1.1.1.1", "9.9.9.9"}) {
			t.Errorf("nameserver addresses = %+v, want [1.1.1.1 9.9.9.9]", ns)
		}
		// Only Addresses is assigned; an existing search list survives.
		if !reflect.DeepEqual(ns.Search, []string{"lan"}) {
			t.Errorf("search = %v, want [lan] preserved", ns.Search)
		}
	})

	t.Run("creates nameservers block when absent", func(t *testing.T) {
		dir := withNetplanDir(t)
		path := writeNetplan(t, dir, "01-dhcp.yaml", fixtureDHCPEthernet)

		if err := updateNetplanDNS([]string{"1.1.1.1"}); err != nil {
			t.Fatalf("updateNetplanDNS: %v", err)
		}
		np, err := loadNetplanFile(path)
		if err != nil {
			t.Fatalf("reload: %v", err)
		}
		ns := np.Network.Ethernets["eth0"].Nameservers
		if ns == nil || !reflect.DeepEqual(ns.Addresses, []string{"1.1.1.1"}) {
			t.Errorf("nameservers = %+v, want addresses [1.1.1.1]", ns)
		}
		if len(ns.Search) != 0 {
			t.Errorf("search = %v, want empty for freshly created block", ns.Search)
		}
	})

	t.Run("no ethernets anywhere errors", func(t *testing.T) {
		dir := withNetplanDir(t)
		writeNetplan(t, dir, "01-bond.yaml", fixtureBond)
		if err := updateNetplanDNS([]string{"1.1.1.1"}); err == nil {
			t.Fatal("expected error when no file has an ethernets section")
		}
	})
}

func TestReadNetplanConfigForInterface(t *testing.T) {
	t.Run("ethernet converts to InterfaceConfig", func(t *testing.T) {
		dir := withNetplanDir(t)
		writeNetplan(t, dir, "01-static.yaml", fixtureStaticWithRoutes)

		got := readNetplanConfigForInterface("eth0")
		want := &InterfaceConfig{
			DHCP4:     false,
			Addresses: []string{"192.168.1.10/24"},
			Gateway4:  "192.168.1.1",
			DNS:       []string{"8.8.8.8"},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("config = %+v, want %+v", got, want)
		}
	})

	t.Run("bare ethernet yields non-nil empty slices", func(t *testing.T) {
		dir := withNetplanDir(t)
		writeNetplan(t, dir, "01-bare.yaml", "network:\n  version: 2\n  ethernets:\n    eth7: {}\n")

		got := readNetplanConfigForInterface("eth7")
		want := &InterfaceConfig{Addresses: []string{}, DNS: []string{}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("config = %+v, want %+v", got, want)
		}
	})

	t.Run("bond converts via bondToConfig", func(t *testing.T) {
		dir := withNetplanDir(t)
		writeNetplan(t, dir, "01-bond.yaml", fixtureBond)

		got := readNetplanConfigForInterface("bond0")
		want := &InterfaceConfig{DHCP4: true, Addresses: []string{}, DNS: []string{}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("config = %+v, want %+v", got, want)
		}
	})

	t.Run("unknown interface yields nil", func(t *testing.T) {
		dir := withNetplanDir(t)
		writeNetplan(t, dir, "01-netcfg.yaml", fixtureDHCPEthernet)
		if got := readNetplanConfigForInterface("eth99"); got != nil {
			t.Errorf("config = %+v, want nil", got)
		}
	})
}

// strPtr is the gateway fields' equivalent of boolPtr: nil means "leave it
// alone", a pointer to "" means "clear it".
func strPtr(s string) *string { return &s }

// A netplan file holds more than this struct models. saveNetplanFile marshals
// the whole struct over the file, so anything unmodelled and uncaught is
// deleted — permanently, since the write is atomic and keeps no backup.
// Reproduced before the fix: an MTU-only edit removed the operator's wifis and
// tunnels sections outright.
func TestNetplanRoundTripKeepsUnmodelledSections(t *testing.T) {
	dir := withNetplanDir(t)
	const src = `network:
  version: 2
  renderer: networkd
  ethernets:
    eth0:
      dhcp4: true
  wifis:
    wlan0:
      dhcp4: true
      access-points:
        homenet:
          password: hunter2
  tunnels:
    wg0:
      mode: wireguard
      addresses:
      - 10.9.0.1/24
`
	path := writeNetplan(t, dir, "01-netcfg.yaml", src)

	mtu := 1400
	if err := updateNetplanInterface("eth0", &ConfigureInterfaceRequest{MTU: &mtu}); err != nil {
		t.Fatalf("updateNetplanInterface: %v", err)
	}

	out, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	// Assert the content, not just the key: a section that survives as an
	// empty map would satisfy a substring check on "wifis" alone.
	for _, want := range []string{"wifis:", "wlan0:", "homenet:", "hunter2", "tunnels:", "wg0:", "wireguard", "10.9.0.1/24"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q was lost by the rewrite:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "mtu: 1400") {
		t.Errorf("the change that prompted the write is missing:\n%s", got)
	}
}

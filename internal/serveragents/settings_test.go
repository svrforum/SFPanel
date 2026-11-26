package serveragents

import (
	"strings"
	"testing"
)

func TestValidateRefusesWhatCouldBreakAConfigFile(t *testing.T) {
	ok := Settings{Zabbix: ZabbixSettings{Server: "192.0.2.10", ServerActive: "192.0.2.10"}, Wazuh: WazuhSettings{Manager: "198.51.100.5", Version: "4.14.10"}}
	if err := ok.Validate(); err != nil {
		t.Fatalf("the values every case starts from are refused: %v", err)
	}
	for _, tc := range []struct {
		field  string
		why    string
		change func(*Settings)
	}{
		{"zabbix.server", "not an IP address", func(s *Settings) { s.Zabbix.Server = "192.0.2.10\nUserParameter=x,id" }},
		{"zabbix.server", "zone", func(s *Settings) { s.Zabbix.Server = "fe80::1%x\nUserParameter=y" }},
		{"zabbix.server", "no port", func(s *Settings) { s.Zabbix.Server = "192.0.2.10:10051" }},
		{"zabbix.server", "at most 16", func(s *Settings) { s.Zabbix.Server = strings.Repeat("192.0.2.10,", 16) + "192.0.2.11" }},
		{"zabbix.server_active", "1-65535", func(s *Settings) { s.Zabbix.ServerActive = "192.0.2.10:70000" }},
		{"zabbix.server_active", "one host", func(s *Settings) { s.Zabbix.ServerActive = "192.0.2.10,192.0.2.11" }},
		{"zabbix.server_active", "required", func(s *Settings) { s.Zabbix.ServerActive = "" }},
		{"zabbix.server", "required", func(s *Settings) { s.Zabbix.Server = "" }},
		{"zabbix.host_metadata", "control", func(s *Settings) { s.Zabbix.HostMetadata = "linux\nHostname=x" }},
		{"zabbix.host_metadata", "255 bytes", func(s *Settings) { s.Zabbix.HostMetadata = strings.Repeat("m", 256) }},
		{"wazuh.manager", "not an IP address", func(s *Settings) { s.Wazuh.Manager = "198.51.100.5</address>" }},
		{"wazuh.groups", "comma-separated", func(s *Settings) { s.Wazuh.Groups = "web servers" }},
		{"wazuh.version", "x.y.z", func(s *Settings) { s.Wazuh.Version = "4.14" }},
		{"wazuh.version", "x.y.z", func(s *Settings) { s.Wazuh.Version = "" }},
		{"wazuh.manager", "required", func(s *Settings) { s.Wazuh.Manager, s.Wazuh.Groups = "", "linux" }},
	} {
		s := ok
		tc.change(&s)
		err := s.Validate()
		if err == nil || err.Field != tc.field {
			t.Errorf("%+v: Validate = %v, want field %s", s, err, tc.field)
			continue
		}
		if !strings.Contains(err.Message, tc.why) {
			t.Errorf("%+v: Validate = %v, want a message about %q", s, err, tc.why)
		}
	}
}

func TestValidateTakesTheOperatorsValues(t *testing.T) {
	s := Settings{Zabbix: ZabbixSettings{Server: "192.0.2.10,zbx.example.net,198.51.100.0/24", ServerActive: "[2001:db8::1]:10051", HostMetadata: "linux-fleet-auto"},
		Wazuh: WazuhSettings{Manager: "198.51.100.5", Groups: "linux,web", Version: "4.14.10"}}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, active := range []string{"zbx.example.net", "zbx.example.net:10052", "192.0.2.10:10051", "2001:db8::1"} {
		s.Zabbix.ServerActive = active
		if err := s.Validate(); err != nil {
			t.Errorf("server_active %q: %v", active, err)
		}
	}
	if err := (Settings{}).Validate(); err != nil {
		t.Fatalf("nothing set: %v", err)
	}
	if err := (Settings{Zabbix: ZabbixSettings{Server: "192.0.2.10", ServerActive: "192.0.2.10"}}).Validate(); err != nil {
		t.Fatalf("Zabbix alone, no Wazuh version: %v", err)
	}
}

func TestNormalizeTrimsAndDefaultsTheVersion(t *testing.T) {
	n := Settings{
		Zabbix: ZabbixSettings{Server: " 192.0.2.10 ,, 198.51.100.7 ", ServerActive: " zbx.example.net ", HostMetadata: " linux-fleet-auto "},
		Wazuh:  WazuhSettings{Manager: " 198.51.100.5 ", Groups: "linux, ,web,"},
	}.Normalize()
	want := Settings{
		Zabbix: ZabbixSettings{Server: "192.0.2.10,198.51.100.7", ServerActive: "zbx.example.net", HostMetadata: "linux-fleet-auto"},
		Wazuh:  WazuhSettings{Manager: "198.51.100.5", Groups: "linux,web", Version: DefaultWazuhVersion},
	}
	if n != want {
		t.Errorf("Normalize = %+v, want %+v", n, want)
	}
	if DefaultWazuhVersion != "4.14.10" {
		t.Errorf("DefaultWazuhVersion = %q, want the manager's 4.14.10", DefaultWazuhVersion)
	}
	if v := (Settings{Wazuh: WazuhSettings{Version: " 4.13.1 "}}).Normalize().Wazuh.Version; v != "4.13.1" {
		t.Errorf("a chosen version became %q", v)
	}
	if err := n.Validate(); err != nil {
		t.Errorf("normalized values are refused: %v", err)
	}
}

func TestConfiguredNeedsTheAddresses(t *testing.T) {
	both := Settings{Zabbix: ZabbixSettings{Server: "192.0.2.10", ServerActive: "zbx.example.net"}, Wazuh: WazuhSettings{Manager: "198.51.100.5"}}
	if !both.Configured(Zabbix) || !both.Configured(Wazuh) {
		t.Errorf("both set: Configured = %v, %v", both.Configured(Zabbix), both.Configured(Wazuh))
	}
	if both.Configured("nagios") {
		t.Error("an unknown agent reads as configured")
	}
	noActive := both
	noActive.Zabbix.ServerActive = ""
	if noActive.Configured(Zabbix) {
		t.Error("Zabbix without server_active reads as configured")
	}
	noPassive := both
	noPassive.Zabbix.Server = ""
	if noPassive.Configured(Zabbix) {
		t.Error("Zabbix without server reads as configured")
	}
	groupsOnly := Settings{Wazuh: WazuhSettings{Groups: "linux", Version: "4.14.10"}}
	if groupsOnly.Configured(Wazuh) {
		t.Error("Wazuh without a manager reads as configured")
	}
	if len(Names) != 2 || Names[0] != Zabbix || Names[1] != Wazuh {
		t.Errorf("Names = %v", Names)
	}
}

func TestActiveAddressDefaultsThePort(t *testing.T) {
	for _, tc := range []struct {
		in   string
		host string
		port int
	}{
		{"zbx.example.net", "zbx.example.net", 10051},
		{"zbx.example.net:10052", "zbx.example.net", 10052},
		{"192.0.2.10", "192.0.2.10", 10051},
		{"2001:db8::1", "2001:db8::1", 10051},
		{"[2001:db8::1]:10052", "2001:db8::1", 10052},
		{"", "", 0},
		{"192.0.2.10:70000", "", 0},
	} {
		host, port := ZabbixSettings{ServerActive: tc.in}.ActiveAddress()
		if host != tc.host || port != tc.port {
			t.Errorf("ActiveAddress(%q) = (%q, %d), want (%q, %d)", tc.in, host, port, tc.host, tc.port)
		}
	}
	if ZabbixActivePort != 10051 {
		t.Errorf("ZabbixActivePort = %d", ZabbixActivePort)
	}
}

func TestSameHostIgnoresCaseAndSpaces(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		same bool
	}{
		{" ZBX.example.net", "zbx.example.net", true},
		{"192.0.2.10", "192.0.2.10 ", true},
		{"2001:db8::1", "2001:DB8:0:0::1", true},
		{"192.0.2.10", "192.0.2.11", false},
		{"zbx.example.net", "zbx2.example.net", false},
	} {
		if got := SameHost(tc.a, tc.b); got != tc.same {
			t.Errorf("SameHost(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.same)
		}
	}
}

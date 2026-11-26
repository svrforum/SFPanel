package serveragents

import "testing"

const (
	osUbuntu2510 = `PRETTY_NAME="Ubuntu 25.10"
NAME="Ubuntu"
VERSION_ID="25.10"
VERSION="25.10 (Questing Quokka)"
VERSION_CODENAME=questing
ID=ubuntu
ID_LIKE=debian
HOME_URL="https://www.ubuntu.com/"
UBUNTU_CODENAME=questing
LOGO=ubuntu-logo
`
	osDebian13 = `PRETTY_NAME="Debian GNU/Linux 13 (trixie)"
NAME="Debian GNU/Linux"
VERSION_ID="13"
VERSION="13 (trixie)"
VERSION_CODENAME=trixie
DEBIAN_VERSION_FULL=13.2
ID=debian
HOME_URL="https://www.debian.org/"
`
	osMint = `NAME="Linux Mint"
VERSION="22.1 (Xia)"
ID=linuxmint
ID_LIKE="ubuntu debian"
PRETTY_NAME="Linux Mint 22.1"
VERSION_ID="22.1"
VERSION_CODENAME=xia
UBUNTU_CODENAME=noble
`
	osFedora = `NAME="Fedora Linux"
VERSION="41 (Server Edition)"
ID=fedora
VERSION_ID=41
VERSION_CODENAME=""
PLATFORM_ID="platform:f41"
PRETTY_NAME='Fedora Linux 41 (Server Edition)'
`
)

func TestParseOSReleaseReadsQuotedValues(t *testing.T) {
	for _, tc := range []struct {
		name      string
		in        string
		want      OS
		debianish bool
	}{
		{"ubuntu 25.10", osUbuntu2510, OS{ID: "ubuntu", IDLike: "debian", VersionID: "25.10", Codename: "questing", Pretty: "Ubuntu 25.10"}, true},
		{"debian 13 (PVE)", osDebian13, OS{ID: "debian", VersionID: "13", Codename: "trixie", Pretty: "Debian GNU/Linux 13 (trixie)"}, true},
		{"linux mint", osMint, OS{ID: "linuxmint", IDLike: "ubuntu debian", VersionID: "22.1", Codename: "xia", Pretty: "Linux Mint 22.1"}, true},
		{"fedora", osFedora, OS{ID: "fedora", VersionID: "41", Pretty: "Fedora Linux 41 (Server Edition)"}, false},
		{"empty", "", OS{}, false},
	} {
		got := ParseOSRelease([]byte(tc.in))
		if got != tc.want {
			t.Errorf("%s: ParseOSRelease = %+v, want %+v", tc.name, got, tc.want)
		}
		if got.Debianish() != tc.debianish {
			t.Errorf("%s: Debianish = %v, want %v", tc.name, got.Debianish(), tc.debianish)
		}
	}
}

func TestParseOSReleaseUnquotesEscapesAndFallsBackToTheUbuntuCodename(t *testing.T) {
	got := ParseOSRelease([]byte("# a comment\r\nID=ubuntu\r\nPRETTY_NAME=\"Ubuntu \\\"LTS\\\"\"\r\nUBUNTU_CODENAME=noble\r\n"))
	want := OS{ID: "ubuntu", Codename: "noble", Pretty: `Ubuntu "LTS"`}
	if got != want {
		t.Errorf("ParseOSRelease = %+v, want %+v", got, want)
	}
}

func TestZabbixRepoLineCoversTheVendorsCodenames(t *testing.T) {
	for _, tc := range []struct {
		os   OS
		line string
		ok   bool
	}{
		{OS{ID: "ubuntu", Codename: "noble"}, "deb [signed-by=/k] https://repo.zabbix.com/zabbix/7.0/ubuntu noble main", true},
		{OS{ID: "ubuntu", Codename: "jammy"}, "deb [signed-by=/k] https://repo.zabbix.com/zabbix/7.0/ubuntu jammy main", true},
		{OS{ID: "ubuntu", Codename: "focal"}, "deb [signed-by=/k] https://repo.zabbix.com/zabbix/7.0/ubuntu focal main", true},
		{OS{ID: "ubuntu", Codename: "resolute"}, "deb [signed-by=/k] https://repo.zabbix.com/zabbix/7.0/ubuntu resolute main", true},
		{OS{ID: "debian", Codename: "trixie"}, "deb [signed-by=/k] https://repo.zabbix.com/zabbix/7.0/debian trixie main", true},
		{OS{ID: "debian", Codename: "bookworm"}, "deb [signed-by=/k] https://repo.zabbix.com/zabbix/7.0/debian bookworm main", true},
		{OS{ID: "debian", Codename: "bullseye"}, "deb [signed-by=/k] https://repo.zabbix.com/zabbix/7.0/debian bullseye main", true},
		{OS{ID: "ubuntu", Codename: "questing"}, "", false},
		{OS{ID: "ubuntu", Codename: "plucky"}, "", false},
		{OS{ID: "debian", Codename: "noble"}, "", false},
		{OS{ID: "ubuntu", Codename: "trixie"}, "", false},
		{OS{ID: "linuxmint", IDLike: "ubuntu debian", Codename: "xia"}, "", false},
		{OS{ID: "fedora"}, "", false},
	} {
		line, ok := ZabbixRepoLine(tc.os, "/k")
		if line != tc.line || ok != tc.ok {
			t.Errorf("ZabbixRepoLine(%+v) = %q, %v; want %q, %v", tc.os, line, ok, tc.line, tc.ok)
		}
	}
}

func TestWazuhRepoLine(t *testing.T) {
	want := "deb [signed-by=/usr/share/keyrings/k.gpg] https://packages.wazuh.com/4.x/apt/ stable main"
	if got := WazuhRepoLine("/usr/share/keyrings/k.gpg"); got != want {
		t.Errorf("WazuhRepoLine = %q, want %q", got, want)
	}
}

func TestParseDpkg(t *testing.T) {
	for _, tc := range []struct {
		out  string
		want Package
	}{
		{"install ok installed|1:7.0.22+dfsg-1\n", Package{Installed: true, Version: "1:7.0.22+dfsg-1"}},
		{"hold ok installed|4.14.10-1\n", Package{Installed: true, Held: true, Version: "4.14.10-1"}},
		{"install ok triggers-pending|4.14.10-1\n", Package{Installed: true, Version: "4.14.10-1"}},
		{"deinstall ok config-files|4.14.10-1\n", Package{}},
		{"install ok half-configured|4.14.10-1\n", Package{}},
		{"unknown ok not-installed|\n", Package{}},
		{"dpkg-query: no packages found matching wazuh-agent\n", Package{}},
		{"", Package{}},
	} {
		if got := ParseDpkg(tc.out); got != tc.want {
			t.Errorf("ParseDpkg(%q) = %+v, want %+v", tc.out, got, tc.want)
		}
	}
	if DpkgFormat != `${Status}|${Version}\n` {
		t.Errorf("DpkgFormat = %q; dpkg-query must get a backslash and an n, not a newline", DpkgFormat)
	}
}

func TestZabbixSourceReadsTheVersion(t *testing.T) {
	for _, tc := range []struct{ version, want string }{
		{"1:7.0.32-2+ubuntu24.04", SourceVendor},
		{"7.0.32-2+ubuntu26.04", SourceVendor},
		{"1:7.0.32-2+debian13", SourceVendor},
		{"1:7.0.22+dfsg-1", SourceDistro},
		{"1:7.0.22+dfsg-1ubuntu1", SourceDistro},
		{"1:6.0.14+dfsg-1+deb12u1", SourceDistro},
		{"", SourceOther},
	} {
		if got := ZabbixSource(tc.version); got != tc.want {
			t.Errorf("ZabbixSource(%q) = %q, want %q", tc.version, got, tc.want)
		}
	}
}

func TestUpstreamVersion(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"1:7.0.22+dfsg-1", "7.0.22+dfsg"},
		{"4.14.10-1", "4.14.10"},
		{"1:7.0.32-2+ubuntu24.04", "7.0.32"},
		{"4.14.10", "4.14.10"},
		{" 4.14.10-1 ", "4.14.10"},
	} {
		if got := UpstreamVersion(tc.in); got != tc.want {
			t.Errorf("UpstreamVersion(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestCompareVersions(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{
		{"4.14.11-1", "4.14.10", 1},
		{"4.14.10-1", "4.14.10", 0},
		{"4.13.1-1", "4.14.10", -1},
		{"1:7.0.22+dfsg-1", "7.0.32", -1},
		{"4.14.10", "4.9.99", 1},
		{"4.14", "4.14.0", 0},
		{"10.0.0", "9.99.99", 1},
	} {
		if got := CompareVersions(tc.a, tc.b); got != tc.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
		if got := CompareVersions(tc.b, tc.a); got != -tc.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d", tc.b, tc.a, got, -tc.want)
		}
	}
}

func TestAptCandidate(t *testing.T) {
	for _, tc := range []struct{ policy, want string }{
		{"zabbix-agent2:\n  Installed: (none)\n  Candidate: 1:7.0.27+dfsg-1\n  Version table:\n     1:7.0.27+dfsg-1 500\n", "1:7.0.27+dfsg-1"},
		{"zabbix-agent2:\n  Installed: (none)\n  Candidate: (none)\n  Version table:\n", ""},
		{"N: Unable to locate package zabbix-agent2\n", ""},
		{"", ""},
	} {
		if got := AptCandidate(tc.policy); got != tc.want {
			t.Errorf("AptCandidate(%q) = %q, want %q", tc.policy, got, tc.want)
		}
	}
}

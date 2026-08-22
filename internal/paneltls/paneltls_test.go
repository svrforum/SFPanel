package paneltls

import (
	"crypto/x509"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func ips(list ...string) []net.IP {
	out := make([]net.IP, 0, len(list))
	for _, s := range list {
		out = append(out, net.ParseIP(s))
	}
	return out
}

func TestDesiredSANs(t *testing.T) {
	cases := []struct {
		name     string
		hostname string
		addrs    []net.IP
		wantDNS  []string
		wantIPs  []string
	}{
		{
			name:     "hostname and a LAN address",
			hostname: "panel",
			addrs:    ips("192.168.1.10"),
			wantDNS:  []string{"localhost", "panel"},
			wantIPs:  []string{"127.0.0.1", "::1", "192.168.1.10"},
		},
		{
			name:     "loopback in the input is not duplicated",
			hostname: "panel",
			addrs:    ips("127.0.0.1", "::1", "10.0.0.5"),
			wantDNS:  []string{"localhost", "panel"},
			wantIPs:  []string{"127.0.0.1", "::1", "10.0.0.5"},
		},
		{
			// A certificate that promises 169.254.x is noise: nothing reaches the
			// panel on a link-local address, and it would churn the SAN set every
			// time an interface came up.
			name:     "link-local addresses are dropped",
			hostname: "panel",
			addrs:    ips("169.254.1.1", "fe80::1", "192.168.1.10"),
			wantDNS:  []string{"localhost", "panel"},
			wantIPs:  []string{"127.0.0.1", "::1", "192.168.1.10"},
		},
		{
			name:     "duplicate addresses collapse",
			hostname: "panel",
			addrs:    ips("192.168.1.10", "192.168.1.10"),
			wantDNS:  []string{"localhost", "panel"},
			wantIPs:  []string{"127.0.0.1", "::1", "192.168.1.10"},
		},
		{
			name:     "empty hostname yields localhost only",
			hostname: "",
			addrs:    ips("192.168.1.10"),
			wantDNS:  []string{"localhost"},
			wantIPs:  []string{"127.0.0.1", "::1", "192.168.1.10"},
		},
		{
			name:     "hostname localhost is not repeated",
			hostname: "localhost",
			addrs:    nil,
			wantDNS:  []string{"localhost"},
			wantIPs:  []string{"127.0.0.1", "::1"},
		},
		{
			name:     "IPv6 global address is kept",
			hostname: "panel",
			addrs:    ips("2001:db8::5"),
			wantDNS:  []string{"localhost", "panel"},
			wantIPs:  []string{"127.0.0.1", "::1", "2001:db8::5"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DesiredSANs(tc.hostname, tc.addrs)
			if !got.Equal(SANs{DNS: tc.wantDNS, IPs: ips(tc.wantIPs...)}) {
				t.Errorf("DesiredSANs(%q, %v) = %v / %v, want %v / %v",
					tc.hostname, tc.addrs, got.DNS, ipStrings(got.IPs), tc.wantDNS, tc.wantIPs)
			}
		})
	}
}

// Loopback must always be covered: the cluster's gRPC handler re-issues a
// proxied request against its own panel port over 127.0.0.1 and verifies the
// certificate like any other client. Dropping it would break ?node= proxying
// in a way that only shows up on a live cluster.
func TestDesiredSANsAlwaysCoversLoopback(t *testing.T) {
	for _, host := range []string{"", "panel", "localhost"} {
		got := DesiredSANs(host, nil)
		var haveV4, haveV6 bool
		for _, ip := range got.IPs {
			if ip.Equal(net.ParseIP("127.0.0.1")) {
				haveV4 = true
			}
			if ip.Equal(net.ParseIP("::1")) {
				haveV6 = true
			}
		}
		if !haveV4 || !haveV6 {
			t.Errorf("hostname %q: loopback coverage v4=%v v6=%v, want both", host, haveV4, haveV6)
		}
	}
}

// Docker creates and destroys a bridge per compose project. If those addresses
// reached the certificate, every `docker compose up` would change the SAN set
// and re-issue the certificate — on a host with a dozen stacks, constantly.
func TestEphemeralInterface(t *testing.T) {
	cases := map[string]bool{
		"br-0873e8f86664": true,  // docker compose project network
		"br-d3c9fcdc1f1e": true,
		"veth19e8e18":     true,
		"docker0":         false, // stable, and containers reach the host on it
		"br0":             false, // hand-configured host bridge — the hyphen is the tell
		"enp2s0":          false,
		"tailscale0":      false,
		"eth0":            false,
		"lo":              false,
		"wlan0":           false,
	}
	for name, want := range cases {
		if got := EphemeralInterface(name); got != want {
			t.Errorf("EphemeralInterface(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestSANsEqualIgnoresOrder(t *testing.T) {
	a := SANs{DNS: []string{"localhost", "panel"}, IPs: ips("127.0.0.1", "192.168.1.10")}
	b := SANs{DNS: []string{"panel", "localhost"}, IPs: ips("192.168.1.10", "127.0.0.1")}
	if !a.Equal(b) {
		t.Error("SAN sets differing only in order compared unequal — the leaf would be re-issued on every boot")
	}
	c := SANs{DNS: []string{"localhost", "panel"}, IPs: ips("127.0.0.1", "192.168.1.11")}
	if a.Equal(c) {
		t.Error("SAN sets with a different address compared equal — an address change would go unnoticed")
	}
}

func TestReissueReason(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	want := DesiredSANs("panel", ips("192.168.1.10"))

	good := func() *x509.Certificate {
		return &x509.Certificate{
			NotBefore:   now.Add(-24 * time.Hour),
			NotAfter:    now.Add(300 * 24 * time.Hour),
			DNSNames:    want.DNS,
			IPAddresses: want.IPs,
		}
	}

	cases := []struct {
		name    string
		cert    *x509.Certificate
		wantRe  bool
		because string
	}{
		{"healthy certificate is left alone", good(), false, ""},
		{"nil certificate", nil, true, "missing"},
		{
			"expired",
			&x509.Certificate{NotBefore: now.Add(-400 * 24 * time.Hour), NotAfter: now.Add(-time.Hour), DNSNames: want.DNS, IPAddresses: want.IPs},
			true, "expired",
		},
		{
			"inside the renewal window",
			&x509.Certificate{NotBefore: now.Add(-24 * time.Hour), NotAfter: now.Add(30 * 24 * time.Hour), DNSNames: want.DNS, IPAddresses: want.IPs},
			true, "expiring",
		},
		{
			"just outside the renewal window is left alone",
			&x509.Certificate{NotBefore: now.Add(-24 * time.Hour), NotAfter: now.Add(RenewWindow + time.Hour), DNSNames: want.DNS, IPAddresses: want.IPs},
			false, "",
		},
		{
			"not yet valid (clock went backwards)",
			&x509.Certificate{NotBefore: now.Add(time.Hour), NotAfter: now.Add(300 * 24 * time.Hour), DNSNames: want.DNS, IPAddresses: want.IPs},
			true, "not yet valid",
		},
		{
			// The DHCP case this whole check exists for.
			"address changed",
			&x509.Certificate{NotBefore: now.Add(-24 * time.Hour), NotAfter: now.Add(300 * 24 * time.Hour), DNSNames: want.DNS, IPAddresses: ips("127.0.0.1", "::1", "192.168.1.99")},
			true, "addresses changed",
		},
		{
			"hostname changed",
			&x509.Certificate{NotBefore: now.Add(-24 * time.Hour), NotAfter: now.Add(300 * 24 * time.Hour), DNSNames: []string{"localhost", "renamed"}, IPAddresses: want.IPs},
			true, "addresses changed",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ReissueReason(tc.cert, want, now, RenewWindow)
			if tc.wantRe && got == "" {
				t.Errorf("ReissueReason = \"\" (keep), want a reason (%s)", tc.because)
			}
			if !tc.wantRe && got != "" {
				t.Errorf("ReissueReason = %q, want \"\" (keep as-is)", got)
			}
		})
	}
}

// Ensure is the boot path. These cases are the ones that decide whether a
// panel comes back up after a restart.
func TestEnsure(t *testing.T) {
	newManager := func(dir string, now time.Time) *Manager {
		m := New(dir)
		m.now = func() time.Time { return now }
		m.hostname = func() (string, error) { return "panel", nil }
		m.localIPs = func() ([]net.IP, error) { return ips("192.168.1.10"), nil }
		return m
	}
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)

	t.Run("empty directory creates CA and leaf", func(t *testing.T) {
		dir := t.TempDir()
		res, err := newManager(dir, now).Ensure()
		if err != nil {
			t.Fatalf("Ensure: %v", err)
		}
		if res.CAAction != ActionIssuedCA || res.LeafAction != ActionIssuedLeaf {
			t.Errorf("actions = %s / %s, want issued / issued", res.CAAction, res.LeafAction)
		}
		for _, f := range []string{CACertFile, CAKeyFile, ServerCertFile, ServerKeyFile} {
			if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
				t.Errorf("%s not created: %v", f, err)
			}
		}
	})

	// Private keys readable by any local process would undo the point of the
	// exercise, so the modes are asserted rather than assumed.
	t.Run("keys are not world or group readable", func(t *testing.T) {
		dir := t.TempDir()
		if _, err := newManager(dir, now).Ensure(); err != nil {
			t.Fatalf("Ensure: %v", err)
		}
		for _, f := range []string{CAKeyFile, ServerKeyFile, CACertFile, ServerCertFile} {
			info, err := os.Stat(filepath.Join(dir, f))
			if err != nil {
				t.Fatalf("stat %s: %v", f, err)
			}
			if mode := info.Mode().Perm(); mode != 0600 {
				t.Errorf("%s mode = %04o, want 0600", f, mode)
			}
		}
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatal(err)
		}
		if mode := info.Mode().Perm(); mode != 0700 {
			t.Errorf("tls dir mode = %04o, want 0700", mode)
		}
	})

	t.Run("second run changes nothing", func(t *testing.T) {
		dir := t.TempDir()
		first, err := newManager(dir, now).Ensure()
		if err != nil {
			t.Fatalf("first Ensure: %v", err)
		}
		before, err := os.ReadFile(filepath.Join(dir, ServerCertFile))
		if err != nil {
			t.Fatal(err)
		}
		second, err := newManager(dir, now).Ensure()
		if err != nil {
			t.Fatalf("second Ensure: %v", err)
		}
		if second.CAAction != ActionNone || second.LeafAction != ActionNone {
			t.Errorf("second run actions = %s / %s, want none / none (reason %q)",
				second.CAAction, second.LeafAction, second.LeafReason)
		}
		after, err := os.ReadFile(filepath.Join(dir, ServerCertFile))
		if err != nil {
			t.Fatal(err)
		}
		if string(before) != string(after) {
			t.Error("certificate was rewritten on an unchanged second run")
		}
		if !first.NotAfter.Equal(second.NotAfter) {
			t.Error("NotAfter drifted between identical runs")
		}
	})

	// The DHCP case. The CA must survive, or every device the operator set up
	// would have to be visited again.
	t.Run("address change re-issues the leaf but keeps the CA", func(t *testing.T) {
		dir := t.TempDir()
		if _, err := newManager(dir, now).Ensure(); err != nil {
			t.Fatalf("Ensure: %v", err)
		}
		caBefore, err := os.ReadFile(filepath.Join(dir, CACertFile))
		if err != nil {
			t.Fatal(err)
		}

		moved := newManager(dir, now)
		moved.localIPs = func() ([]net.IP, error) { return ips("192.168.1.77"), nil }
		res, err := moved.Ensure()
		if err != nil {
			t.Fatalf("Ensure after address change: %v", err)
		}
		if res.LeafAction != ActionIssuedLeaf {
			t.Error("leaf was not re-issued after the address changed")
		}
		if res.CAAction != ActionNone {
			t.Errorf("CA action = %s, want none — replacing the CA invalidates every device's trust", res.CAAction)
		}
		caAfter, err := os.ReadFile(filepath.Join(dir, CACertFile))
		if err != nil {
			t.Fatal(err)
		}
		if string(caBefore) != string(caAfter) {
			t.Error("CA certificate changed on an address change")
		}
		var covered bool
		for _, ip := range res.IPs {
			if ip == "192.168.1.77" {
				covered = true
			}
		}
		if !covered {
			t.Errorf("new address not in re-issued certificate: %v", res.IPs)
		}
	})

	t.Run("expiring leaf is renewed from the existing CA", func(t *testing.T) {
		dir := t.TempDir()
		if _, err := newManager(dir, now).Ensure(); err != nil {
			t.Fatalf("Ensure: %v", err)
		}
		caBefore, err := os.ReadFile(filepath.Join(dir, CACertFile))
		if err != nil {
			t.Fatal(err)
		}
		// Ten months on: inside the 90-day renewal window of a 1-year leaf.
		later := now.Add(300 * 24 * time.Hour)
		res, err := newManager(dir, later).Ensure()
		if err != nil {
			t.Fatalf("Ensure near expiry: %v", err)
		}
		if res.LeafAction != ActionIssuedLeaf {
			t.Error("leaf was not renewed inside the renewal window")
		}
		if res.CAAction != ActionNone {
			t.Errorf("CA action = %s, want none", res.CAAction)
		}
		caAfter, err := os.ReadFile(filepath.Join(dir, CACertFile))
		if err != nil {
			t.Fatal(err)
		}
		if string(caBefore) != string(caAfter) {
			t.Error("CA was replaced during a routine leaf renewal")
		}
	})

	t.Run("truncated leaf is replaced rather than fatal", func(t *testing.T) {
		dir := t.TempDir()
		if _, err := newManager(dir, now).Ensure(); err != nil {
			t.Fatalf("Ensure: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, ServerCertFile), []byte("garbage"), 0600); err != nil {
			t.Fatal(err)
		}
		res, err := newManager(dir, now).Ensure()
		if err != nil {
			t.Fatalf("Ensure with a corrupt leaf must not fail: %v", err)
		}
		if res.LeafAction != ActionIssuedLeaf {
			t.Error("corrupt leaf was not replaced")
		}
	})

	// Losing only the key leaves a certificate that cannot complete a
	// handshake. Catching it at boot beats failing per-connection later.
	t.Run("leaf without its key is replaced", func(t *testing.T) {
		dir := t.TempDir()
		if _, err := newManager(dir, now).Ensure(); err != nil {
			t.Fatalf("Ensure: %v", err)
		}
		if err := os.Remove(filepath.Join(dir, ServerKeyFile)); err != nil {
			t.Fatal(err)
		}
		res, err := newManager(dir, now).Ensure()
		if err != nil {
			t.Fatalf("Ensure: %v", err)
		}
		if res.LeafAction != ActionIssuedLeaf {
			t.Error("leaf was not re-issued after its key went missing")
		}
	})

	// A leaf signed by a CA that no longer exists cannot verify, so replacing
	// the CA has to replace the leaf too.
	t.Run("replaced CA forces a new leaf", func(t *testing.T) {
		dir := t.TempDir()
		if _, err := newManager(dir, now).Ensure(); err != nil {
			t.Fatalf("Ensure: %v", err)
		}
		leafBefore, err := os.ReadFile(filepath.Join(dir, ServerCertFile))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(filepath.Join(dir, CACertFile)); err != nil {
			t.Fatal(err)
		}
		res, err := newManager(dir, now).Ensure()
		if err != nil {
			t.Fatalf("Ensure: %v", err)
		}
		if res.CAAction != ActionIssuedCA {
			t.Error("CA was not recreated")
		}
		if res.LeafAction != ActionIssuedLeaf {
			t.Fatal("leaf was not re-issued after the CA changed — it would fail verification")
		}
		leafAfter, err := os.ReadFile(filepath.Join(dir, ServerCertFile))
		if err != nil {
			t.Fatal(err)
		}
		if string(leafBefore) == string(leafAfter) {
			t.Error("leaf is byte-identical after the CA was replaced")
		}
	})
}

// The generated pair must actually chain and actually satisfy hostname
// verification — the properties a browser checks. Asserting the files exist
// proves nothing about whether a client would accept them.
func TestIssuedCertificateVerifies(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	m := New(dir)
	m.now = func() time.Time { return now }
	m.hostname = func() (string, error) { return "panel", nil }
	m.localIPs = func() ([]net.IP, error) { return ips("192.168.1.10"), nil }
	if _, err := m.Ensure(); err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	caPEM, err := m.CACertPEM()
	if err != nil {
		t.Fatalf("CACertPEM: %v", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		t.Fatal("CA PEM was not accepted into a cert pool")
	}
	leaf, err := parseCertFile(filepath.Join(dir, ServerCertFile))
	if err != nil {
		t.Fatalf("parse leaf: %v", err)
	}

	for _, host := range []string{"panel", "localhost", "192.168.1.10", "127.0.0.1", "::1"} {
		t.Run(host, func(t *testing.T) {
			if _, err := leaf.Verify(x509.VerifyOptions{
				Roots:       pool,
				DNSName:     host,
				CurrentTime: now.Add(time.Hour),
				KeyUsages:   []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
			}); err != nil {
				t.Errorf("verify against %s: %v", host, err)
			}
		})
	}

	t.Run("a host it does not cover is rejected", func(t *testing.T) {
		if _, err := leaf.Verify(x509.VerifyOptions{
			Roots:       pool,
			DNSName:     "192.168.1.99",
			CurrentTime: now.Add(time.Hour),
			KeyUsages:   []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		}); err == nil {
			t.Error("verification succeeded for an address not in the certificate")
		}
	})

	t.Run("an unrelated CA does not verify it", func(t *testing.T) {
		other := New(t.TempDir())
		other.now = func() time.Time { return now }
		other.hostname = func() (string, error) { return "panel", nil }
		other.localIPs = func() ([]net.IP, error) { return ips("192.168.1.10"), nil }
		if _, err := other.Ensure(); err != nil {
			t.Fatal(err)
		}
		otherPEM, err := other.CACertPEM()
		if err != nil {
			t.Fatal(err)
		}
		otherPool := x509.NewCertPool()
		otherPool.AppendCertsFromPEM(otherPEM)
		if _, err := leaf.Verify(x509.VerifyOptions{
			Roots:       otherPool,
			DNSName:     "panel",
			CurrentTime: now.Add(time.Hour),
			KeyUsages:   []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		}); err == nil {
			t.Error("a certificate from a different CA verified — peer trust would be meaningless")
		}
	})
}

// Lifetimes are a deliberate, load-bearing choice: ten years belongs on the
// anchor users install, and the leaf must stay under the 398-day ceiling
// platforms enforce. A well-meaning edit to either constant should fail here.
func TestLifetimes(t *testing.T) {
	if CALifetime < 9*365*24*time.Hour {
		t.Errorf("CALifetime = %v, want ~10 years — users re-install this by hand on every device", CALifetime)
	}
	const platformCeiling = 398 * 24 * time.Hour
	if LeafLifetime >= platformCeiling {
		t.Errorf("LeafLifetime = %v, must stay under the %v platforms enforce", LeafLifetime, platformCeiling)
	}
	if RenewWindow >= LeafLifetime {
		t.Error("RenewWindow >= LeafLifetime would re-issue the certificate on every boot")
	}
	if RenewWindow < 30*24*time.Hour {
		t.Errorf("RenewWindow = %v is too short — renewal only runs at boot, and panels stay up for months", RenewWindow)
	}
}

func TestCACertPEMReturnsOnlyTheCertificate(t *testing.T) {
	dir := t.TempDir()
	m := New(dir)
	m.hostname = func() (string, error) { return "panel", nil }
	m.localIPs = func() ([]net.IP, error) { return ips("192.168.1.10"), nil }
	if _, err := m.Ensure(); err != nil {
		t.Fatal(err)
	}
	pem, err := m.CACertPEM()
	if err != nil {
		t.Fatal(err)
	}
	// This bytestream is served to any authenticated caller, so it must never
	// contain key material.
	for _, forbidden := range []string{"PRIVATE KEY", "EC PRIVATE KEY", "RSA PRIVATE KEY"} {
		if contains(string(pem), forbidden) {
			t.Fatalf("CACertPEM output contains %q", forbidden)
		}
	}
	if !contains(string(pem), "BEGIN CERTIFICATE") {
		t.Error("CACertPEM output is not a certificate")
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	}()
}

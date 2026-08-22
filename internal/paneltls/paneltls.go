// Package paneltls manages the certificate the panel presents to browsers.
//
// It is deliberately separate from internal/cluster's TLSManager. That one
// exists for mutual TLS between nodes: both ends authenticate, the leaf lives
// five years, and every file is trust material for a closed set of machines.
// This one faces web browsers, which authenticate only the server, police leaf
// lifetime, and ignore CommonName in favour of SANs. Sharing a type between
// the two would mean one struct whose every method needed a "which mode" flag.
//
// The split of lifetimes is the design's one non-obvious choice. The operator
// asked for a ten-year certificate so that installing trust on a phone or
// laptop is a one-time chore. Ten years on the *leaf* is what platforms object
// to — Apple rejects server certificates valid for more than 398 days, and the
// public limit keeps ratcheting down. But the file a user installs on a device
// is the CA, not the leaf. So the CA carries the ten years and the leaf is
// short and renewed automatically: the promised "install once" property is
// kept, without betting the panel's reachability on a lifetime browsers are
// actively tightening.
package paneltls

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	// CALifetime is what the operator installs on their devices, so it is long
	// on purpose: re-installing a trust anchor across every phone and laptop is
	// exactly the chore this feature exists to avoid.
	CALifetime = 10 * 365 * 24 * time.Hour

	// LeafLifetime stays inside the 398-day ceiling Apple enforces (and that the
	// CA/Browser Forum keeps lowering) so no platform has grounds to reject the
	// handshake.
	LeafLifetime = 365 * 24 * time.Hour

	// RenewWindow is how long before expiry a leaf is replaced. Renewal happens
	// at boot, so this has to comfortably exceed the longest realistic uptime
	// between restarts — a panel left running for two months must not wake up
	// to an expired certificate.
	RenewWindow = 90 * 24 * time.Hour
)

// File names inside the TLS directory. Exported because install.sh and the
// config defaults have to name the same paths.
const (
	CACertFile     = "ca.crt"
	CAKeyFile      = "ca.key"
	ServerCertFile = "server.crt"
	ServerKeyFile  = "server.key"
)

// Action reports what Ensure had to do, so the caller can log it and the
// settings UI can explain itself.
type Action string

const (
	ActionNone       Action = "none"
	ActionIssuedCA   Action = "issued_ca"
	ActionIssuedLeaf Action = "issued_leaf"
)

// Result describes the outcome of Ensure.
type Result struct {
	CAAction   Action
	LeafAction Action
	// LeafReason is why the leaf was re-issued (empty when it was not). It is
	// surfaced in logs because "your certificate changed" is something an
	// operator debugging a client-side trust error needs to be able to see.
	LeafReason string
	CANotAfter time.Time
	NotAfter   time.Time
	DNSNames   []string
	IPs        []string
}

// Manager owns the panel's certificate directory.
type Manager struct {
	dir string

	// now and localIPs are injection points for tests. Production always uses
	// the real clock and the real interface list.
	now      func() time.Time
	localIPs func() ([]net.IP, error)
	hostname func() (string, error)
}

// New returns a Manager rooted at dir.
func New(dir string) *Manager {
	return &Manager{
		dir:      dir,
		now:      time.Now,
		localIPs: LocalIPs,
		hostname: os.Hostname,
	}
}

func (m *Manager) path(name string) string { return filepath.Join(m.dir, name) }

// CACertPath is where the trust anchor lives — the one file a user copies to
// their devices.
func (m *Manager) CACertPath() string { return m.path(CACertFile) }

// ServerCertPath and ServerKeyPath are the pair handed to the HTTP server.
func (m *Manager) ServerCertPath() string { return m.path(ServerCertFile) }
func (m *Manager) ServerKeyPath() string  { return m.path(ServerKeyFile) }

// Ensure brings the on-disk material up to date: it creates the CA if absent,
// then issues a leaf if one is missing, expiring, or no longer covers the
// addresses this host answers on.
//
// It is intentionally forgiving about a missing or unparseable leaf — a panel
// that refuses to boot because a certificate file got truncated is worse than
// one that quietly mints a replacement, since the replacement is still signed
// by the CA every device already trusts. A damaged *CA* is the one case with a
// real human cost, and the caller is told about it through Result so it can be
// logged loudly.
func (m *Manager) Ensure() (*Result, error) {
	if err := os.MkdirAll(m.dir, 0700); err != nil {
		return nil, fmt.Errorf("create tls dir: %w", err)
	}
	// MkdirAll leaves an EXISTING directory's mode alone, so a directory that
	// some earlier install created 0755 would stay 0755 and let any local user
	// list the trust material. The keys inside are 0600 regardless, but the
	// directory holding private keys has no business being traversable.
	if err := os.Chmod(m.dir, 0700); err != nil {
		return nil, fmt.Errorf("tighten tls dir permissions: %w", err)
	}

	res := &Result{CAAction: ActionNone, LeafAction: ActionNone}

	caCert, caKey, err := m.loadCA()
	if err != nil {
		if err := m.createCA(); err != nil {
			return nil, err
		}
		res.CAAction = ActionIssuedCA
		caCert, caKey, err = m.loadCA()
		if err != nil {
			return nil, fmt.Errorf("load freshly created CA: %w", err)
		}
	}
	res.CANotAfter = caCert.NotAfter

	want, err := m.desiredSANs()
	if err != nil {
		return nil, err
	}

	leaf, leafErr := m.loadLeaf()
	reason := ""
	switch {
	case leafErr != nil:
		reason = "missing or unreadable"
	case res.CAAction == ActionIssuedCA:
		// A leaf signed by the CA we just replaced would not verify.
		reason = "certificate authority was replaced"
	default:
		reason = ReissueReason(leaf, want, m.now(), RenewWindow)
	}

	if reason != "" {
		if err := m.issueLeaf(caCert, caKey, want); err != nil {
			return nil, err
		}
		res.LeafAction = ActionIssuedLeaf
		res.LeafReason = reason
		leaf, err = m.loadLeaf()
		if err != nil {
			return nil, fmt.Errorf("load freshly issued certificate: %w", err)
		}
	}

	res.NotAfter = leaf.NotAfter
	res.DNSNames = leaf.DNSNames
	for _, ip := range leaf.IPAddresses {
		res.IPs = append(res.IPs, ip.String())
	}
	return res, nil
}

// SANs is the set of names and addresses a certificate must cover.
type SANs struct {
	DNS []string
	IPs []net.IP
}

// Equal compares two SAN sets order-insensitively. Certificate SAN order is
// not meaningful, and x509 parsing does not promise to preserve it, so an
// order-sensitive comparison would re-issue the leaf on every boot.
func (s SANs) Equal(other SANs) bool {
	if len(s.DNS) != len(other.DNS) || len(s.IPs) != len(other.IPs) {
		return false
	}
	a, b := append([]string(nil), s.DNS...), append([]string(nil), other.DNS...)
	sort.Strings(a)
	sort.Strings(b)
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	ai, bi := ipStrings(s.IPs), ipStrings(other.IPs)
	for i := range ai {
		if ai[i] != bi[i] {
			return false
		}
	}
	return true
}

func ipStrings(ips []net.IP) []string {
	out := make([]string, 0, len(ips))
	for _, ip := range ips {
		out = append(out, ip.String())
	}
	sort.Strings(out)
	return out
}

// DesiredSANs builds the SAN set for a host. It is pure so the address-drift
// logic can be tested without touching real interfaces.
//
// Loopback is always included: the cluster's gRPC handler re-issues proxied
// requests against its own panel port over 127.0.0.1, and that hop verifies
// the certificate like any other client.
func DesiredSANs(hostname string, addrs []net.IP) SANs {
	dns := []string{"localhost"}
	if hostname != "" && hostname != "localhost" {
		dns = append(dns, hostname)
	}

	ips := []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}
	seen := map[string]bool{"127.0.0.1": true, "::1": true}
	for _, ip := range addrs {
		if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
			continue
		}
		key := ip.String()
		if seen[key] {
			continue
		}
		seen[key] = true
		ips = append(ips, ip)
	}
	return SANs{DNS: dns, IPs: ips}
}

func (m *Manager) desiredSANs() (SANs, error) {
	host, err := m.hostname()
	if err != nil {
		host = ""
	}
	addrs, err := m.localIPs()
	if err != nil {
		return SANs{}, fmt.Errorf("enumerate local addresses: %w", err)
	}
	return DesiredSANs(host, addrs), nil
}

// EphemeralInterface reports whether an interface's addresses are too
// short-lived to belong in a certificate.
//
// Docker creates one bridge per compose network, named "br-" + 12 hex digits,
// and destroys it when the stack comes down. On a host running a dozen stacks
// that is a dozen addresses that appear and vanish as projects are started and
// stopped — and since a changed address set triggers re-issue, including them
// would churn the certificate on ordinary container activity. Nothing reaches
// the panel on one of them anyway.
//
// docker0 is deliberately NOT excluded: it is created once and stays, and a
// container legitimately reaches the host panel at its gateway address.
// The hyphen matters — a hand-configured host bridge called "br0" is a real
// interface and is kept.
func EphemeralInterface(name string) bool {
	switch {
	case strings.HasPrefix(name, "br-"):
		return true // docker compose per-project network
	case strings.HasPrefix(name, "veth"):
		return true // container side of a veth pair
	default:
		return false
	}
}

// LocalIPs returns every unicast address on a stable, up interface.
func LocalIPs() ([]net.IP, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var out []net.IP
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || EphemeralInterface(iface.Name) {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			if ipnet, ok := a.(*net.IPNet); ok {
				out = append(out, ipnet.IP)
			}
		}
	}
	return out, nil
}

// ReissueReason reports why cert must be replaced, or "" when it is still
// good. Pure, and the single place the renewal policy lives.
func ReissueReason(cert *x509.Certificate, want SANs, now time.Time, window time.Duration) string {
	if cert == nil {
		return "missing or unreadable"
	}
	if now.After(cert.NotAfter) {
		return "expired"
	}
	if now.Add(window).After(cert.NotAfter) {
		return "expiring within the renewal window"
	}
	if now.Before(cert.NotBefore) {
		// Clock moved backwards, or the file came from another machine.
		return "not yet valid"
	}
	have := SANs{DNS: cert.DNSNames, IPs: cert.IPAddresses}
	if !have.Equal(want) {
		// The common cause is a DHCP lease change: the address people type into
		// the browser is no longer in the certificate.
		return "host names or addresses changed"
	}
	return ""
}

func (m *Manager) createCA() error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("generate CA key: %w", err)
	}
	serial, err := randomSerial()
	if err != nil {
		return err
	}
	host, _ := m.hostname()
	if host == "" {
		host = "sfpanel"
	}
	now := m.now()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			Organization: []string{"SFPanel"},
			CommonName:   fmt.Sprintf("SFPanel Local CA - %s", host),
		},
		NotBefore:             now.Add(-5 * time.Minute),
		NotAfter:              now.Add(CALifetime),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return fmt.Errorf("create CA cert: %w", err)
	}
	if err := writePEM(m.path(CACertFile), "CERTIFICATE", der, 0600); err != nil {
		return err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return fmt.Errorf("marshal CA key: %w", err)
	}
	return writePEM(m.path(CAKeyFile), "EC PRIVATE KEY", keyDER, 0600)
}

func (m *Manager) issueLeaf(caCert *x509.Certificate, caKey *ecdsa.PrivateKey, want SANs) error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("generate server key: %w", err)
	}
	serial, err := randomSerial()
	if err != nil {
		return err
	}
	cn := "sfpanel"
	if len(want.DNS) > 1 {
		cn = want.DNS[1] // the hostname; DNS[0] is always "localhost"
	}
	now := m.now()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			Organization: []string{"SFPanel"},
			CommonName:   cn,
		},
		NotBefore:             now.Add(-5 * time.Minute),
		NotAfter:              now.Add(LeafLifetime),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              want.DNS,
		IPAddresses:           want.IPs,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
	if err != nil {
		return fmt.Errorf("create server cert: %w", err)
	}
	if err := writePEM(m.path(ServerCertFile), "CERTIFICATE", der, 0600); err != nil {
		return err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return fmt.Errorf("marshal server key: %w", err)
	}
	return writePEM(m.path(ServerKeyFile), "EC PRIVATE KEY", keyDER, 0600)
}

func (m *Manager) loadCA() (*x509.Certificate, *ecdsa.PrivateKey, error) {
	cert, err := parseCertFile(m.path(CACertFile))
	if err != nil {
		return nil, nil, err
	}
	if !cert.IsCA {
		return nil, nil, errors.New("ca.crt is not a CA certificate")
	}
	keyPEM, err := os.ReadFile(m.path(CAKeyFile))
	if err != nil {
		return nil, nil, err
	}
	block, _ := pem.Decode(keyPEM)
	if block == nil {
		return nil, nil, errors.New("ca.key is not valid PEM")
	}
	key, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("parse CA key: %w", err)
	}
	return cert, key, nil
}

func (m *Manager) loadLeaf() (*x509.Certificate, error) {
	cert, err := parseCertFile(m.path(ServerCertFile))
	if err != nil {
		return nil, err
	}
	// A certificate without its key is useless, and silently keeping it would
	// make the server fail at handshake time instead of at boot.
	if _, err := os.Stat(m.path(ServerKeyFile)); err != nil {
		return nil, fmt.Errorf("server key missing: %w", err)
	}
	return cert, nil
}

// Status describes the material on disk for the settings UI.
type Status struct {
	CANotBefore   time.Time `json:"ca_not_before"`
	CANotAfter    time.Time `json:"ca_not_after"`
	CASubject     string    `json:"ca_subject"`
	CAFingerprint string    `json:"ca_fingerprint"`
	NotBefore     time.Time `json:"not_before"`
	NotAfter      time.Time `json:"not_after"`
	DNSNames      []string  `json:"dns_names"`
	IPAddresses   []string  `json:"ip_addresses"`
}

// Status reads the current material. It never returns key material.
func (m *Manager) Status() (*Status, error) {
	ca, err := parseCertFile(m.path(CACertFile))
	if err != nil {
		return nil, err
	}
	leaf, err := parseCertFile(m.path(ServerCertFile))
	if err != nil {
		return nil, err
	}
	s := &Status{
		CANotBefore:   ca.NotBefore,
		CANotAfter:    ca.NotAfter,
		CASubject:     ca.Subject.CommonName,
		CAFingerprint: Fingerprint(ca),
		NotBefore:     leaf.NotBefore,
		NotAfter:      leaf.NotAfter,
		DNSNames:      leaf.DNSNames,
	}
	for _, ip := range leaf.IPAddresses {
		s.IPAddresses = append(s.IPAddresses, ip.String())
	}
	return s, nil
}

// CACertPEM returns the trust anchor, for download. Certificate only — the key
// is never read by any code path that can reach a request handler.
func (m *Manager) CACertPEM() ([]byte, error) {
	return os.ReadFile(m.path(CACertFile))
}

// Fingerprint renders a certificate's SHA-256 fingerprint in the colon-
// separated hex form every OS certificate viewer displays, so an operator can
// compare what they installed against what the panel reports.
func Fingerprint(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.Raw)
	out := make([]byte, 0, len(sum)*3)
	const hexDigits = "0123456789ABCDEF"
	for i, b := range sum {
		if i > 0 {
			out = append(out, ':')
		}
		out = append(out, hexDigits[b>>4], hexDigits[b&0x0f])
	}
	return string(out)
}

func parseCertFile(path string) (*x509.Certificate, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(raw)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("%s is not a PEM certificate", filepath.Base(path))
	}
	return x509.ParseCertificate(block.Bytes)
}

func randomSerial() (*big.Int, error) {
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, fmt.Errorf("generate serial: %w", err)
	}
	return serial, nil
}

// writePEM writes atomically: a half-written certificate that survives a crash
// would keep the panel down until someone deleted it by hand.
func writePEM(path, blockType string, der []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return fmt.Errorf("open %s: %w", filepath.Base(tmp), err)
	}
	if err := pem.Encode(f, &pem.Block{Type: blockType, Bytes: der}); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("close %s: %w", filepath.Base(path), err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename %s: %w", filepath.Base(path), err)
	}
	return nil
}

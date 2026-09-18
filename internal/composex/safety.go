// Package composex holds compose-related helpers shared across feature
// modules. Today that's just the safety analyser used by both the App
// Store one-click installer and the plain compose CRUD endpoints — both
// take operator-supplied YAML and feed it directly to `docker compose`,
// so they need the same gate.
package composex

import (
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Finding is one pattern the analyser recognised in a compose document.
type Finding struct {
	Service string // the compose service the pattern sits in
	Rule    string // stable identifier: "bind", "privileged", "namespace", "capability", "group", "security-opt", "device"
	Detail  string // the value that tripped it, e.g. "/var/run/docker.sock"
	Message string // the sentence the operator reads
}

// Report is everything Analyze found in one document, split into the two
// tiers: Forbidden is refused whatever the caller says, Risky is refused
// until the operator acknowledges it. Both empty means nothing was found.
type Report struct {
	Forbidden []Finding // the panel's own secrets; never acceptable
	Risky     []Finding // acceptable once the operator says so
}

// Analyze reports every container-escape pattern a compose document asks for.
// It is not a complete container-escape sandbox — a caller with write access to
// these endpoints already has admin credentials — and for most of what it finds
// it is not a security boundary at all: the panel hands that same operator a
// root terminal in the next tab, so refusing `privileged: true` stops a
// mistake, not an attacker. Those findings land in Report.Risky, for a caller
// that can name them and proceed once the operator says yes. The narrow set in
// forbiddenBind is the exception and lands in Report.Forbidden.
//
// Every finding is reported, not the first: a dialog has to list what a stack
// asks for, and a save that fails six times in a row teaches nothing.
//
// The error return means the document could not be read at all; a document that
// parses always yields a Report.
func Analyze(content string) (Report, error) {
	var report Report
	var doc map[string]interface{}
	if err := yaml.Unmarshal([]byte(content), &doc); err != nil {
		return report, fmt.Errorf("compose YAML is invalid: %w", err)
	}
	servicesRaw, ok := doc["services"].(map[string]interface{})
	if !ok {
		return report, fmt.Errorf("compose YAML must contain a top-level 'services' map")
	}
	names := make([]string, 0, len(servicesRaw))
	for name := range servicesRaw {
		names = append(names, name)
	}
	// Map iteration order is random; the findings are listed to the operator,
	// so keep two analyses of the same document in the same order.
	sort.Strings(names)
	for _, svcName := range names {
		svc, ok := servicesRaw[svcName].(map[string]interface{})
		if !ok {
			continue
		}
		report.analyzeService(svcName, svc)
	}
	return report, nil
}

// Error reports what blocks a request carrying this report: forbidden findings
// always, risky findings only while the operator has not acknowledged them.
// The messages are joined so the caller can show every one of them. Returns nil
// when nothing blocks the request.
func (r Report) Error(acknowledged bool) error {
	msgs := make([]string, 0, len(r.Forbidden)+len(r.Risky))
	for _, f := range r.Forbidden {
		msgs = append(msgs, f.Message)
	}
	if !acknowledged {
		for _, f := range r.Risky {
			msgs = append(msgs, f.Message)
		}
	}
	if len(msgs) == 0 {
		return nil
	}
	return errors.New(strings.Join(msgs, "; "))
}

// ValidateAdvancedCompose is the entry point for callers that cannot express an
// acknowledgement — today the migration import, which copies a stack that is
// already installed and already running on another node. It refuses the
// forbidden tier only; the two tiers are explained at forbiddenBind. Returns
// nil if the compose content is acceptable.
func ValidateAdvancedCompose(content string) error {
	report, err := Analyze(content)
	if err != nil {
		return err
	}
	return report.Error(true)
}

func (r *Report) analyzeService(svcName string, svc map[string]interface{}) {
	if isPrivileged(svc["privileged"]) {
		r.risky(Finding{
			Service: svcName, Rule: "privileged", Detail: "true",
			Message: fmt.Sprintf("service %q sets privileged: true", svcName),
		})
	}
	for _, hostModeKey := range []string{
		"pid", "network", "ipc", "uts",
		// Long-form aliases. Compose accepts both shapes; we must too.
		"pid_mode", "network_mode", "ipc_mode", "userns_mode",
	} {
		if v, ok := svc[hostModeKey].(string); ok && strings.EqualFold(v, "host") {
			r.risky(Finding{
				Service: svcName, Rule: "namespace", Detail: hostModeKey + ": host",
				Message: fmt.Sprintf("service %q sets %s: host", svcName, hostModeKey),
			})
		}
	}
	if caps, ok := svc["cap_add"].([]interface{}); ok {
		for _, c := range caps {
			s, _ := c.(string)
			// Strip optional CAP_ prefix — Docker accepts both "SYS_ADMIN"
			// and "CAP_SYS_ADMIN" (the kernel-canonical form). Compare on
			// the bare form so canonical names can't bypass the blocklist.
			canon := strings.TrimPrefix(strings.ToUpper(strings.TrimSpace(s)), "CAP_")
			if canon == "ALL" || canon == "SYS_ADMIN" || canon == "SYS_MODULE" || canon == "SYS_PTRACE" {
				r.risky(Finding{
					Service: svcName, Rule: "capability", Detail: canon,
					Message: fmt.Sprintf("service %q requests disallowed capability %s", svcName, canon),
				})
			}
		}
	}
	if groups, ok := svc["group_add"].([]interface{}); ok {
		for _, g := range groups {
			s, _ := g.(string)
			name := strings.ToLower(strings.TrimSpace(s))
			// Flag membership in groups that gate sensitive host
			// resources: docker socket (docker), raw disks (disk),
			// sudoers (sudo/wheel), uid-0 (root), kernel virtualisation
			// (kvm). Numeric GIDs and unknown names pass through.
			switch name {
			case "docker", "disk", "sudo", "wheel", "root", "kvm":
				r.risky(Finding{
					Service: svcName, Rule: "group", Detail: name,
					Message: fmt.Sprintf("service %q joins host group %q via group_add", svcName, name),
				})
			}
		}
	}
	if security, ok := svc["security_opt"].([]interface{}); ok {
		for _, s := range security {
			ss, _ := s.(string)
			trimmed := strings.TrimSpace(ss)
			// Docker accepts both ':' and '=' as key/value separator
			// (e.g. apparmor=unconfined); split on whichever comes first.
			key, val := trimmed, ""
			if i := strings.IndexAny(trimmed, ":="); i >= 0 {
				key, val = strings.TrimSpace(trimmed[:i]), strings.TrimSpace(trimmed[i+1:])
			}
			switch strings.ToLower(key) {
			case "apparmor", "seccomp", "systempaths":
				if strings.EqualFold(val, "unconfined") {
					r.risky(Finding{
						Service: svcName, Rule: "security-opt", Detail: trimmed,
						Message: fmt.Sprintf("service %q disables %q sandbox", svcName, trimmed),
					})
				}
			}
		}
	}
	for _, hostPath := range bindHostPaths(svc["volumes"]) {
		finding := Finding{
			Service: svcName, Rule: "bind", Detail: hostPath,
			Message: fmt.Sprintf("service %q binds sensitive host path %q", svcName, hostPath),
		}
		switch {
		case forbiddenBind(hostPath):
			r.forbidden(finding)
		case isDangerousBind(hostPath):
			r.risky(finding)
		}
	}
	if devices, ok := svc["devices"].([]interface{}); ok && len(devices) > 0 {
		listed := make([]string, 0, len(devices))
		for _, d := range devices {
			if s, ok := d.(string); ok {
				listed = append(listed, strings.TrimSpace(s))
			}
		}
		r.risky(Finding{
			Service: svcName, Rule: "device", Detail: strings.Join(listed, ", "),
			Message: fmt.Sprintf("service %q declares devices: passthrough not allowed", svcName),
		})
	}
}

func (r *Report) risky(f Finding)     { r.Risky = append(r.Risky, f) }
func (r *Report) forbidden(f Finding) { r.Forbidden = append(r.Forbidden, f) }

// isPrivileged resolves the shapes compose accepts for `privileged`.
func isPrivileged(v interface{}) bool {
	switch priv := v.(type) {
	case bool:
		return priv
	case string:
		// Quoted ("true") and YAML-1.1 ("yes"/"on") forms decode as
		// strings here but compose still resolves them to a truthy bool.
		switch strings.ToLower(strings.TrimSpace(priv)) {
		case "true", "1", "yes", "y", "on":
			return true
		}
	}
	return false
}

// bindHostPaths returns the host side of every bind entry in a service's
// volumes list. Named volumes (no leading '/') are not binds and are skipped.
func bindHostPaths(v interface{}) []string {
	list, ok := v.([]interface{})
	if !ok {
		return nil
	}
	var paths []string
	for _, entry := range list {
		var hostPath string
		switch e := entry.(type) {
		case string:
			hostPath = strings.TrimSpace(strings.SplitN(e, ":", 2)[0])
		case map[string]interface{}:
			if t, _ := e["type"].(string); !strings.EqualFold(t, "bind") && t != "" {
				continue
			}
			hostPath, _ = e["source"].(string)
		}
		if hostPath == "" || !strings.HasPrefix(hostPath, "/") {
			continue
		}
		paths = append(paths, hostPath)
	}
	return paths
}

// forbiddenBind reports the binds no acknowledgement lifts. Everything else
// this file finds stops an operator's mistake, not an attacker — the panel
// hands that operator a root terminal in the next tab — but these four paths
// are a real boundary, because an attacker who found a bug in *this* handler
// does not have that terminal:
//
//   - /etc/sfpanel holds the JWT signing secret and the cluster CA key, so a
//     hole here must not become the ability to mint tokens and node certs
//   - /var/lib/sfpanel holds the panel database
//   - /root/.ssh holds the host's SSH keys
//   - /etc/sudoers.d turns one written file into passwordless root
//
// Matching is on the cleaned path, exactly or under it, so a subpath such as
// /etc/sfpanel/config.yaml is covered too.
func forbiddenBind(p string) bool {
	clean := cleanBindPath(p)
	for _, b := range []string{
		"/etc/sfpanel", "/var/lib/sfpanel", "/root/.ssh", "/etc/sudoers.d",
	} {
		if clean == b || strings.HasPrefix(clean, b+"/") {
			return true
		}
	}
	return false
}

// isDangerousBind reports host paths that hand a container the machine. These
// are risky, not forbidden: the App Store's own catalog binds the docker socket
// and one app binds /, so an operator who says yes has to be able to proceed.
func isDangerousBind(p string) bool {
	clean := cleanBindPath(p)
	if clean == "/" {
		return true
	}
	blocked := []string{
		"/etc", "/root", "/home", "/boot", "/proc", "/sys", "/dev",
		"/var/lib/sfpanel", "/etc/sfpanel", "/usr", "/bin", "/sbin",
		"/lib", "/lib64",
		// Daemon state dirs: write access there is host takeover even
		// without the socket (image layers, containerd runtime state).
		"/var/lib/docker", "/run/containerd",
	}
	for _, b := range blocked {
		if clean == b || strings.HasPrefix(clean, b+"/") {
			return true
		}
	}
	// /var/run/docker.sock normalizes to /run/docker.sock above.
	if clean == "/run/docker.sock" {
		return true
	}
	return false
}

// cleanBindPath normalises a host path so the tiers match one spelling of it.
// path.Clean collapses //, resolves . and .. and drops the trailing slash: the
// kernel resolves /etc//sfpanel, /etc/./sfpanel and /etc/foo/../sfpanel to the
// same directory and Docker cleans the mount source too, so a tier matching the
// string as written would refuse /etc/sfpanel and wave its respellings through
// to the same files.
//
// Then the /var/run alias: on the target platform /var/run is a symlink to
// /run, so /var/run/containerd reaches the same runtime state as
// /run/containerd. Both tiers match on the result, so neither needs to list the
// alias — and /var/run/… cannot slip past the forbidden check.
func cleanBindPath(p string) string {
	clean := path.Clean(strings.TrimSpace(p))
	// path.Clean answers "." for the empty string; a bind with no host side
	// is not a path the tiers can reason about, so treat it as the root.
	if clean == "" || clean == "." {
		clean = "/"
	}
	if clean == "/var/run" || strings.HasPrefix(clean, "/var/run/") {
		clean = clean[len("/var"):]
	}
	return clean
}

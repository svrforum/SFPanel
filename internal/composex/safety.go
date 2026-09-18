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
	Service string // the compose service the pattern sits in, or the top-level secret/config/volume name
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
	report.analyzeTopLevel(doc)
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

// AnalyzeBinds puts the bind mounts of a standalone container through the same
// two tiers Analyze applies to a service's `volumes:` list.
//
// POST /docker/containers has no compose document for Analyze to read: its
// volumes are Docker's own "source:target[:mode]" strings and land verbatim in
// HostConfig.Binds. The route is reached by the same session as the compose
// editor, so without this the forbidden tier would be a boundary on one route
// and an open door on its sibling — and the tier exists precisely for the
// attacker who found a bug in a handler and has no root terminal.
//
// Binds only. A standalone create carries no privileged flag, no host
// namespace, no capability list and no devices for the rest of the analyser to
// read; the panel's create API is a small explicit subset that never exposed
// them.
//
// name is the requested container name, empty when the daemon will assign one.
func AnalyzeBinds(name string, binds []string) Report {
	subject := "the new container"
	if trimmed := strings.TrimSpace(name); trimmed != "" {
		subject = fmt.Sprintf("container %q", trimmed)
	}
	entries := make([]interface{}, 0, len(binds))
	for _, b := range binds {
		entries = append(entries, b)
	}
	var report Report
	for _, mount := range bindMounts(entries) {
		finding := Finding{
			Service: name, Rule: "bind", Detail: mount.Source,
			Message: mount.message(subject),
		}
		switch {
		case forbiddenBind(mount.Source):
			report.forbidden(finding)
		case isDangerousBind(mount.Source):
			report.risky(finding)
		}
	}
	return report
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
	for _, mount := range bindMounts(svc["volumes"]) {
		finding := Finding{
			Service: svcName, Rule: "bind", Detail: mount.Source,
			Message: mount.message(fmt.Sprintf("service %q", svcName)),
		}
		switch {
		case forbiddenBind(mount.Source):
			r.forbidden(finding)
		case isDangerousBind(mount.Source):
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

// analyzeTopLevel walks the two shapes that put a host path inside a container
// without ever appearing in a service's `volumes:` list, so analyzeService
// cannot see them. Both are ordinary compose and `docker compose config`
// echoes both unchanged, which means a tier reading only services.*.volumes
// was blind at the save-time check and at the deploy guard alike:
//
//   - top-level `secrets:` / `configs:` with `file: /etc/sfpanel/config.yaml` —
//     compose bind-mounts that file into the container read-only
//   - a top-level named volume whose `driver_opts` are `{type: none, o: bind,
//     device: /etc/sfpanel}` — the service references it by name, so the
//     service side carries no host path at all
//
// The forbidden tier only. The risky tier is a sentence the operator reads
// about one service asking for one thing, and these entries belong to no
// service; widening them would newly demand an acknowledgement for the
// ordinary home-server named volume (`device: /home/<user>/data`) while the
// boundary — the part that must not be reachable at all — is what is missing.
func (r *Report) analyzeTopLevel(doc map[string]interface{}) {
	for _, kind := range []string{"secrets", "configs"} {
		entries, ok := doc[kind].(map[string]interface{})
		if !ok {
			continue
		}
		for _, name := range sortedKeys(entries) {
			entry, ok := entries[name].(map[string]interface{})
			if !ok {
				continue
			}
			file, _ := entry["file"].(string)
			r.forbiddenTopLevel(kind, name, file)
		}
	}
	volumes, ok := doc["volumes"].(map[string]interface{})
	if !ok {
		return
	}
	for _, name := range sortedKeys(volumes) {
		vol, ok := volumes[name].(map[string]interface{})
		if !ok {
			continue
		}
		opts, ok := vol["driver_opts"].(map[string]interface{})
		if !ok {
			continue
		}
		device, _ := opts["device"].(string)
		r.forbiddenTopLevel("volume", name, device)
	}
}

// forbiddenTopLevel records a bind finding when a top-level entry names one of
// the panel's own directories. A value that is not an absolute host path is
// skipped: an NFS volume's device is ":/export" and a CIFS one is a UNC share,
// neither of which is a path on this machine.
func (r *Report) forbiddenTopLevel(kind, name, hostPath string) {
	p := strings.TrimSpace(hostPath)
	if !strings.HasPrefix(p, "/") || !forbiddenBind(p) {
		return
	}
	r.forbidden(Finding{
		Service: name,
		Rule:    "bind",
		Detail:  p,
		Message: fmt.Sprintf("top-level %s %q binds sensitive host path %q", kind, name, p),
	})
}

// sortedKeys lists a map's keys in a stable order. Map iteration is random and
// the findings are read as a list, so two analyses of one document have to
// produce the same one.
func sortedKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
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

// bindMount is one bind entry: the host path the tiers match on, and the
// container path it lands at. The target is carried because the findings are
// read as a list — two mounts of the same source at different targets are two
// separate things the stack asks for, and a message naming only the source
// would print the same line twice.
type bindMount struct {
	Source string
	Target string
}

// message is the sentence the operator reads for this mount. subject names the
// thing that asked for it and arrives already quoted by the caller — `service
// "web"` for a compose document, `container "web"` for the standalone create
// route — so one wording serves both.
func (b bindMount) message(subject string) string {
	if b.Target == "" {
		return fmt.Sprintf("%s binds sensitive host path %q", subject, b.Source)
	}
	return fmt.Sprintf("%s binds sensitive host path %q at %q", subject, b.Source, b.Target)
}

// bindMounts returns every bind entry in a service's volumes list. Named
// volumes (no leading '/') are not binds and are skipped.
func bindMounts(v interface{}) []bindMount {
	list, ok := v.([]interface{})
	if !ok {
		return nil
	}
	var mounts []bindMount
	for _, entry := range list {
		var mount bindMount
		switch e := entry.(type) {
		case string:
			// "source:target[:mode]" — the mode is not part of either half.
			parts := strings.SplitN(e, ":", 3)
			mount.Source = strings.TrimSpace(parts[0])
			if len(parts) > 1 {
				mount.Target = strings.TrimSpace(parts[1])
			}
		case map[string]interface{}:
			if t, _ := e["type"].(string); !strings.EqualFold(t, "bind") && t != "" {
				continue
			}
			src, _ := e["source"].(string)
			// TrimSpace like the short form above: " /etc/sfpanel" is the
			// same directory, and the tiers must not miss it because the
			// long form was written with a stray space.
			mount.Source = strings.TrimSpace(src)
			tgt, _ := e["target"].(string)
			mount.Target = strings.TrimSpace(tgt)
		}
		if mount.Source == "" || !strings.HasPrefix(mount.Source, "/") {
			continue
		}
		mounts = append(mounts, mount)
	}
	return mounts
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

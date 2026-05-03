// Package composex holds compose-related helpers shared across feature
// modules. Today that's just the safety validator used by both the App
// Store one-click installer and the plain compose CRUD endpoints — both
// take operator-supplied YAML and feed it directly to `docker compose`,
// so they need the same gate.
package composex

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// ValidateAdvancedCompose blocks docker-compose.yml patterns that let a service
// break out of its container and take over the host. It is not a complete
// container-escape sandbox — an attacker with write access to this endpoint
// already has admin credentials — but it raises the bar from "one HTTP POST"
// to "requires physical console / different path", which matches the rest of
// the panel's trust model. Returns nil if the compose content is acceptable.
func ValidateAdvancedCompose(content string) error {
	var doc map[string]interface{}
	if err := yaml.Unmarshal([]byte(content), &doc); err != nil {
		return fmt.Errorf("compose YAML is invalid: %w", err)
	}
	servicesRaw, ok := doc["services"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("compose YAML must contain a top-level 'services' map")
	}
	for svcName, svcRaw := range servicesRaw {
		svc, ok := svcRaw.(map[string]interface{})
		if !ok {
			continue
		}
		if priv, _ := svc["privileged"].(bool); priv {
			return fmt.Errorf("service %q sets privileged: true", svcName)
		}
		for _, hostModeKey := range []string{"pid", "network", "ipc", "uts", "userns_mode"} {
			if v, ok := svc[hostModeKey].(string); ok && strings.EqualFold(v, "host") {
				return fmt.Errorf("service %q sets %s: host", svcName, hostModeKey)
			}
		}
		if caps, ok := svc["cap_add"].([]interface{}); ok {
			for _, c := range caps {
				s, _ := c.(string)
				upper := strings.ToUpper(strings.TrimSpace(s))
				if upper == "ALL" || upper == "SYS_ADMIN" || upper == "SYS_MODULE" || upper == "SYS_PTRACE" {
					return fmt.Errorf("service %q requests disallowed capability %s", svcName, upper)
				}
			}
		}
		if security, ok := svc["security_opt"].([]interface{}); ok {
			for _, s := range security {
				ss, _ := s.(string)
				trimmed := strings.TrimSpace(ss)
				if strings.EqualFold(trimmed, "apparmor:unconfined") ||
					strings.EqualFold(trimmed, "seccomp:unconfined") ||
					strings.EqualFold(trimmed, "systempaths=unconfined") {
					return fmt.Errorf("service %q disables %q sandbox", svcName, trimmed)
				}
			}
		}
		if err := checkVolumes(svcName, svc["volumes"]); err != nil {
			return err
		}
		if devices, ok := svc["devices"].([]interface{}); ok && len(devices) > 0 {
			return fmt.Errorf("service %q declares devices: passthrough not allowed", svcName)
		}
	}
	return nil
}

// checkVolumes rejects bind mounts that target sensitive host paths. Named
// volumes (no leading '/') are allowed. Docker socket access is specifically
// blocked because it is a full container-escape primitive on its own.
func checkVolumes(svcName string, v interface{}) error {
	list, ok := v.([]interface{})
	if !ok {
		return nil
	}
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
		if isDangerousBind(hostPath) {
			return fmt.Errorf("service %q binds sensitive host path %q", svcName, hostPath)
		}
	}
	return nil
}

func isDangerousBind(p string) bool {
	clean := strings.TrimRight(p, "/")
	if clean == "" {
		clean = "/"
	}
	if clean == "/" {
		return true
	}
	blocked := []string{
		"/etc", "/root", "/home", "/boot", "/proc", "/sys", "/dev",
		"/var/lib/sfpanel", "/etc/sfpanel", "/usr", "/bin", "/sbin",
		"/lib", "/lib64",
	}
	for _, b := range blocked {
		if clean == b || strings.HasPrefix(clean, b+"/") {
			return true
		}
	}
	if clean == "/var/run/docker.sock" || clean == "/run/docker.sock" {
		return true
	}
	return false
}

package portmap

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/common/exec"
	"github.com/svrforum/SFPanel/internal/docker"
)

// Handler exposes the unified port map endpoint.
type Handler struct {
	Cmd    exec.Commander
	Docker *docker.Client // nil-safe; nil means Docker source returns empty
}

const portmapCmdTimeout = 5 * time.Second

// GetPortMap aggregates UFW + Docker DNAT + ss listening into a single response.
//
//	GET /api/v1/system/portmap
func (h *Handler) GetPortMap(c echo.Context) error {
	ctx := c.Request().Context()

	var (
		mu sync.Mutex
		wg sync.WaitGroup

		ufwByPort = map[int]FirewallInfo{}
		dnat      []PortBinding
		listeners []SsEntry
	)

	wg.Add(3)

	// ss (tcp + udp).
	go func() {
		defer wg.Done()
		out, err := h.Cmd.RunWithTimeout(portmapCmdTimeout, "ss", "-tlnp", "-H")
		var entries []SsEntry
		if err != nil {
			slog.Warn("portmap: ss tcp failed", "error", err)
		} else {
			entries = ParseSs(out, "tcp")
		}
		out2, err2 := h.Cmd.RunWithTimeout(portmapCmdTimeout, "ss", "-ulnp", "-H")
		if err2 != nil {
			slog.Warn("portmap: ss udp failed", "error", err2)
		} else {
			entries = append(entries, ParseSs(out2, "udp")...)
		}
		mu.Lock()
		listeners = entries
		mu.Unlock()
	}()

	// UFW.
	go func() {
		defer wg.Done()
		out, err := h.Cmd.RunWithTimeout(portmapCmdTimeout, "ufw", "status", "numbered")
		if err != nil {
			slog.Warn("portmap: ufw status failed", "error", err)
			return
		}
		parsed := parseUFWForPortMap(out)
		mu.Lock()
		ufwByPort = parsed
		mu.Unlock()
	}()

	// Docker DNAT.
	go func() {
		defer wg.Done()
		if h.Docker == nil {
			return
		}
		bindings, err := collectDockerBindings(ctx, h.Docker)
		if err != nil {
			slog.Warn("portmap: docker bindings failed", "error", err)
			return
		}
		mu.Lock()
		dnat = bindings
		mu.Unlock()
	}()

	wg.Wait()

	rows := Aggregate(ufwByPort, dnat, listeners)
	return response.OK(c, rows)
}

// parseUFWForPortMap is a minimal UFW status numbered parser scoped to the
// fields portmap needs. Single-port rules ("22/tcp") yield one entry; ranges
// like "4000:4010/tcp" yield one entry for the range start.
func parseUFWForPortMap(output string) map[int]FirewallInfo {
	out := map[int]FirewallInfo{}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "[") {
			continue
		}
		closeIdx := strings.Index(line, "]")
		if closeIdx < 0 {
			continue
		}
		ruleNumStr := strings.TrimSpace(strings.Trim(line[1:closeIdx], " "))
		ruleID, _ := strconv.Atoi(ruleNumStr)
		body := strings.TrimSpace(line[closeIdx+1:])
		// Strip trailing "# comment".
		if hashIdx := strings.LastIndex(body, "#"); hashIdx >= 0 {
			body = strings.TrimSpace(body[:hashIdx])
		}
		fields := strings.Fields(body)
		if len(fields) < 2 {
			continue
		}
		toField := fields[0]
		var action, scope string
		actionIdx := -1
		for i, f := range fields {
			switch f {
			case "ALLOW", "DENY", "REJECT", "LIMIT":
				actionIdx = i
				action = f
			}
			if actionIdx >= 0 {
				break
			}
		}
		if actionIdx < 0 {
			continue
		}
		scopeStart := actionIdx + 1
		if scopeStart < len(fields) {
			switch fields[scopeStart] {
			case "IN", "OUT", "FWD":
				scopeStart++
			}
		}
		if scopeStart < len(fields) {
			scope = strings.Join(fields[scopeStart:], " ")
		}
		port, _ := splitPortProto(toField)
		if port == 0 {
			continue
		}
		out[port] = FirewallInfo{Action: action, Scope: scope, RuleID: ruleID, Source: "ufw"}
	}
	return out
}

// splitPortProto parses "22/tcp" → (22, "tcp"); "22" → (22, ""); "4000:4010/tcp" → (4000, "tcp").
func splitPortProto(s string) (int, string) {
	proto := ""
	if slash := strings.Index(s, "/"); slash >= 0 {
		proto = s[slash+1:]
		s = s[:slash]
	}
	if colon := strings.Index(s, ":"); colon >= 0 {
		s = s[:colon] // range start
	}
	port, _ := strconv.Atoi(s)
	return port, proto
}

// collectDockerBindings flattens container port bindings into PortBinding slice.
func collectDockerBindings(ctx context.Context, dc *docker.Client) ([]PortBinding, error) {
	containers, err := dc.ListContainers(ctx)
	if err != nil {
		return nil, err
	}
	out := []PortBinding{}
	for _, c := range containers {
		name := ""
		if len(c.Names) > 0 {
			name = strings.TrimPrefix(c.Names[0], "/")
		}
		stack := c.Labels["com.docker.compose.project"]
		for _, p := range c.Ports {
			if p.PublicPort == 0 {
				continue
			}
			out = append(out, PortBinding{
				HostPort:      int(p.PublicPort),
				ContainerID:   c.ID,
				ContainerName: name,
				Stack:         stack,
			})
		}
	}
	return out, nil
}

package serveragents

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// ZabbixVendorHostname is the Hostname the vendor's default config ships
// with. It names the Zabbix server's own host, so an agent left with it has
// no name of its own yet.
const ZabbixVendorHostname = "Zabbix server"

// zabbixManagedKeys are the config keys a deployment sets, in the order they
// are reported.
var zabbixManagedKeys = []string{"Server", "ServerActive", "Hostname", "HostMetadata"}

// ZabbixConf is what a Zabbix Agent 2 config file says about the keys a
// deployment sets.
type ZabbixConf struct {
	Server       string `json:"server"`
	ServerActive string `json:"server_active"`
	Hostname     string `json:"hostname"`
	HostMetadata string `json:"host_metadata"`
	// Duplicates are the keys above with more than one active line, in file
	// order.
	Duplicates []string `json:"duplicates,omitempty"`
	// HostnameSet reports an active Hostname line, even an empty one.
	HostnameSet bool `json:"-"`
	// HostnameItemSet reports an active HostnameItem line, which names the
	// agent when Hostname does not.
	HostnameItemSet bool `json:"-"`
	// Includes are the Include= values as written: a path or a glob,
	// relative ones against the config file's directory.
	Includes []string `json:"-"`
	// LogType, LogFile and RefreshActiveChecks as the file sets them, ""
	// when it does not: where the agent writes its log, and how often it asks
	// its server for its active checks. LogPath and Refresh read them with
	// the agent's defaults.
	LogType             string `json:"-"`
	LogFile             string `json:"-"`
	RefreshActiveChecks string `json:"-"`
}

const (
	// ZabbixDefaultLogFile is where agent 2 writes its log when its config
	// names no LogFile.
	ZabbixDefaultLogFile = "/tmp/zabbix_agent2.log"
	// ZabbixDefaultRefresh is how often agent 2 asks its server for its
	// active checks when its config does not say.
	ZabbixDefaultRefresh = 5 * time.Second
)

// LogPath is the file the agent writes its log to — LogFile, or the agent's
// default — or "" when it writes none: LogType system or console.
func (c ZabbixConf) LogPath() string {
	switch c.LogType {
	case "", "file":
		if c.LogFile != "" {
			return c.LogFile
		}
		return ZabbixDefaultLogFile
	}
	return ""
}

// Refresh is RefreshActiveChecks, or the agent's default when the file sets
// none or one the agent refuses (1 to 86400 seconds).
func (c ZabbixConf) Refresh() time.Duration {
	n, err := strconv.Atoi(c.RefreshActiveChecks)
	if err != nil || n < 1 || n > 86400 {
		return ZabbixDefaultRefresh
	}
	return time.Duration(n) * time.Second
}

// ReadZabbixConf reads a Zabbix Agent 2 config file. A key's value is that of
// its first active line — "Key=value", not commented out — with the key
// matched whole and case-sensitively, and the spaces, tabs and carriage
// return around the value left out, as the agent reads it.
func ReadZabbixConf(src []byte) ZabbixConf {
	var c ZabbixConf
	fields := map[string]*string{
		"Server":       &c.Server,
		"ServerActive": &c.ServerActive,
		"Hostname":     &c.Hostname,
		"HostMetadata": &c.HostMetadata,
	}
	// What the agent itself reads, its first non-empty value.
	agentFields := map[string]*string{"LogType": &c.LogType, "LogFile": &c.LogFile, "RefreshActiveChecks": &c.RefreshActiveChecks}
	seen := map[string]int{}
	for _, line := range bytes.Split(src, []byte("\n")) {
		for _, key := range zabbixManagedKeys {
			v, ok := activeValue(line, key)
			if !ok {
				continue
			}
			seen[key]++
			switch seen[key] {
			case 1:
				*fields[key] = v
			case 2:
				c.Duplicates = append(c.Duplicates, key)
			}
		}
		if v, ok := activeValue(line, "Include"); ok {
			c.Includes = append(c.Includes, v)
		}
		if _, ok := activeValue(line, "HostnameItem"); ok {
			c.HostnameItemSet = true
		}
		for key, field := range agentFields {
			if v, ok := activeValue(line, key); ok && *field == "" {
				*field = v
			}
		}
	}
	c.HostnameSet = seen["Hostname"] > 0
	return c
}

// KeyValue is a config key and the value to give it.
type KeyValue struct{ Key, Value string }

// ErrValueHasNewline refuses a value that would end its line and start
// another: through it any key could be added to the agent's config.
var ErrValueHasNewline = errors.New("a value holds a line break")

// EditZabbixConf sets each key to its value, in the order given, and leaves
// every other line as it was, byte for byte:
//
//   - the key's first active line becomes "Key=value" in place (left alone
//     when it already holds the value), and every later active line of the
//     key is commented out with "# ", so the agent reads one value;
//   - a key with no active line goes right below its last commented-out line
//     ("# Key=", the vendor's example), or else at the end of the file.
//
// The spaces and tabs around a value are not part of it, as the agent reads
// it. A line keeps its carriage return, and a line written anew takes the one
// of the line above it. A value holding a line break refuses the whole edit
// with ErrValueHasNewline. changed reports whether out differs from src; when
// it does not, out is src.
func EditZabbixConf(src []byte, edits []KeyValue) (out []byte, changed bool, err error) {
	for _, e := range edits {
		if strings.ContainsAny(e.Value, "\r\n") {
			return nil, false, fmt.Errorf("%s: %w", e.Key, ErrValueHasNewline)
		}
	}
	lines := bytes.Split(src, []byte("\n"))
	for _, e := range edits {
		lines = setKey(lines, e.Key, strings.Trim(e.Value, " \t"))
	}
	out = bytes.Join(lines, []byte("\n"))
	if bytes.Equal(out, src) {
		return src, false, nil
	}
	return out, true, nil
}

// SetsManagedKey lists the keys a deployment sets — Server, ServerActive,
// Hostname, HostMetadata, in that order — that have an active line in src;
// nil when none has. It is for a file the agent's config includes, where such
// a key would stand beside the value written to the main file.
func SetsManagedKey(src []byte) []string {
	lines := bytes.Split(src, []byte("\n"))
	var keys []string
	for _, key := range zabbixManagedKeys {
		if slices.ContainsFunc(lines, func(l []byte) bool { _, ok := activeValue(l, key); return ok }) {
			keys = append(keys, key)
		}
	}
	return keys
}

// setKey applies one edit to lines, the file split at "\n": the last element
// is what follows the last line break, empty when the file ends with one.
func setKey(lines [][]byte, key, value string) [][]byte {
	first, lastExample := -1, -1
	for i, line := range lines {
		if v, ok := activeValue(line, key); ok {
			if first < 0 {
				first = i
				if v != value {
					lines[i] = keyLine(key, value, hasCR(line))
				}
			} else {
				lines[i] = append([]byte("# "), line...)
			}
			continue
		}
		if commentedOut(line, key) {
			lastExample = i
		}
	}
	switch {
	case first >= 0:
		return lines
	case lastExample >= 0:
		return insertLine(lines, lastExample+1, key, value)
	case len(lines[len(lines)-1]) == 0:
		return insertLine(lines, len(lines)-1, key, value)
	default:
		return insertLine(lines, len(lines), key, value)
	}
}

// insertLine puts "key=value" before lines[i]. Past the end — after a last
// line with no line break — it ends that line first and gives the new one a
// line break of its own.
func insertLine(lines [][]byte, i int, key, value string) [][]byte {
	l := keyLine(key, value, i > 0 && hasCR(lines[i-1]))
	if i == len(lines) {
		return append(lines, l, nil)
	}
	return slices.Insert(lines, i, l)
}

func keyLine(key, value string, cr bool) []byte {
	l := key + "=" + value
	if cr {
		l += "\r"
	}
	return []byte(l)
}

func hasCR(line []byte) bool { return bytes.HasSuffix(line, []byte("\r")) }

// activeValue reports whether line sets key — `^[ \t]*Key[ \t]*=` — and the
// value it sets.
func activeValue(line []byte, key string) (string, bool) {
	rest := bytes.TrimLeft(line, " \t")
	if !bytes.HasPrefix(rest, []byte(key)) {
		return "", false
	}
	rest = bytes.TrimLeft(rest[len(key):], " \t")
	if len(rest) == 0 || rest[0] != '=' {
		return "", false
	}
	return string(bytes.Trim(rest[1:], " \t\r")), true
}

// commentedOut reports whether line is key commented out —
// `^[ \t]*#[ \t]*Key[ \t]*=`.
func commentedOut(line []byte, key string) bool {
	rest := bytes.TrimLeft(line, " \t")
	if len(rest) == 0 || rest[0] != '#' {
		return false
	}
	_, ok := activeValue(rest[1:], key)
	return ok
}

package lanscan

import (
	"encoding/xml"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"strings"
	"time"
)

const (
	// hostsTimeout bounds a host scan (nmap -sn). One /24 answered in 7
	// seconds on the LAN it was measured on; a /20 is sixteen of those.
	hostsTimeout = 2 * time.Minute
	// portsTimeout bounds a port scan. Each host gives up after 30 seconds
	// (--host-timeout) and nmap checks many hosts at once: a /24 of 28
	// hosts took 64 seconds with one host at 52.
	portsTimeout = 6 * time.Minute
)

// nmapArgs is the command line for a scan of cidr written as XML to
// xmlFile. cidr is a Network.CIDR from this node's own list, never a string
// from a request. A host scan finds who answers (ARP on the LAN); a port
// scan also SYN-checks nmap's 1000 most common TCP ports on each, retrying
// a port at most twice and giving up on a host after 30 seconds.
func nmapArgs(cidr string, ports bool, xmlFile string) []string {
	if ports {
		return []string{"-sS", "-T4", "--max-retries", "2", "--host-timeout", "30s", "-oX", xmlFile, cidr}
	}
	return []string{"-sn", "-oX", xmlFile, cidr}
}

// Host is one device a scan found.
type Host struct {
	IP string `json:"ip"`
	// MAC is lower case; "" for this node itself until the handler fills in
	// the scanned interface's (nmap does not learn its own over ARP).
	MAC string `json:"mac"`
	// Vendor is nmap's name for the MAC's manufacturer, "" when unknown.
	Vendor string `json:"vendor"`
	// Hostname is the first name nmap found (reverse DNS), "" for none.
	Hostname string `json:"hostname"`
	// Self is the node that ran the scan.
	Self bool `json:"self"`
	// TimedOut is a host the port scan gave up on (--host-timeout): it is
	// up, and its port list is incomplete or empty.
	TimedOut bool `json:"timed_out"`
	// Ports are the open ones only, by number. Never null: [] for a host
	// scan and for a host with none open. nmap's service names are left
	// out: they come from a table of usual ports, not from the service.
	Ports []Port `json:"ports"`
}

// Port is an open port.
type Port struct {
	Port  int    `json:"port"`
	Proto string `json:"proto"`
}

// errNotFinished is an nmap output that is not a whole, successful run. It
// is never stored: the earlier result of the network stays.
var errNotFinished = errors.New("nmap did not finish the scan")

// The part of nmap's XML (nmap.dtd) a scan reads. Only <host> elements
// directly under <nmaprun> are hosts: <hosthint>, which nmap writes for a
// host it is about to port-scan, repeats one and is not read.
type xmlRun struct {
	Hosts    []xmlHost `xml:"host"`
	Finished *struct {
		Exit string `xml:"exit,attr"`
	} `xml:"runstats>finished"`
}

type xmlHost struct {
	TimedOut string `xml:"timedout,attr"`
	Status   struct {
		State  string `xml:"state,attr"`
		Reason string `xml:"reason,attr"`
	} `xml:"status"`
	Addresses []struct {
		Addr     string `xml:"addr,attr"`
		AddrType string `xml:"addrtype,attr"`
		Vendor   string `xml:"vendor,attr"`
	} `xml:"address"`
	Hostnames []struct {
		Name string `xml:"name,attr"`
	} `xml:"hostnames>hostname"`
	Ports []struct {
		Protocol string `xml:"protocol,attr"`
		PortID   int    `xml:"portid,attr"`
		State    struct {
			State string `xml:"state,attr"`
		} `xml:"state"`
	} `xml:"ports>port"`
}

// parseScan reads nmap's XML output into the hosts that are up, by address.
// An output that does not end in <runstats><finished exit="success"> is
// refused with errNotFinished: nmap writes the file as it goes, so one that
// was killed or timed out mid-scan is a well-begun file with the end
// missing, and storing it would replace a complete result with part of one.
func parseScan(data []byte) ([]Host, error) {
	var run xmlRun
	if err := xml.Unmarshal(data, &run); err != nil {
		return nil, fmt.Errorf("%w: its XML output ends early or is not XML: %v", errNotFinished, err)
	}
	if run.Finished == nil || run.Finished.Exit != "success" {
		return nil, fmt.Errorf(`%w: its XML output has no <runstats><finished exit="success">`, errNotFinished)
	}

	type found struct {
		addr netip.Addr
		host Host
	}
	list := []found{}
	for _, xh := range run.Hosts {
		if xh.Status.State != "up" {
			continue
		}
		h := Host{
			Self:     xh.Status.Reason == "localhost-response",
			TimedOut: xh.TimedOut == "true",
			Ports:    []Port{},
		}
		var addr netip.Addr
		for _, a := range xh.Addresses {
			switch a.AddrType {
			case "ipv4":
				if ip, err := netip.ParseAddr(a.Addr); err == nil && ip.Is4() {
					addr = ip
				}
			case "mac":
				if hw, err := net.ParseMAC(a.Addr); err == nil {
					h.MAC = hw.String()
				}
				h.Vendor = a.Vendor
			}
		}
		if !addr.IsValid() {
			continue
		}
		h.IP = addr.String()
		for _, hn := range xh.Hostnames {
			if hn.Name != "" {
				h.Hostname = hn.Name
				break
			}
		}
		for _, p := range xh.Ports {
			if p.State.State == "open" && p.PortID > 0 {
				h.Ports = append(h.Ports, Port{Port: p.PortID, Proto: strings.ToLower(p.Protocol)})
			}
		}
		sort.Slice(h.Ports, func(i, j int) bool {
			if h.Ports[i].Port != h.Ports[j].Port {
				return h.Ports[i].Port < h.Ports[j].Port
			}
			return h.Ports[i].Proto < h.Ports[j].Proto
		})
		list = append(list, found{addr: addr, host: h})
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].addr.Less(list[j].addr) })
	hosts := make([]Host, 0, len(list))
	for _, f := range list {
		hosts = append(hosts, f.host)
	}
	return hosts, nil
}

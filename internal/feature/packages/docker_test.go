package packages

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/svrforum/SFPanel/internal/api/response"
)

// What `apt-get -s install` prints under LC_ALL=C, cut to its Inst lines.
const (
	simDockerIO = `NOTE: This is only a simulation!
Inst containerd (1.7.24~ds1-6+b4 Debian:13.1/stable [amd64])
Inst docker.io (26.1.5+dfsg1-9+b9 Debian:13.1/stable [amd64])
Conf containerd (1.7.24~ds1-6+b4 Debian:13.1/stable [amd64])
`
	simBuildxBringsDocker = `Inst docker-buildx (0.13.1+ds1-3 Debian:13.1/stable [amd64])
Inst docker.io (26.1.5+dfsg1-9+b9 Debian:13.1/stable [amd64])
`
	simDockerIOUpgrade = `Inst docker.io [26.1.5+dfsg1-9+b9] (26.1.5+dfsg1-9+b10 Debian:13.1/stable [amd64])
`
	simHtop = `Inst htop (3.4.1-4 Debian:13.1/stable [amd64])
`
)

// On a Proxmox VE host the package list does not install a Docker engine,
// whether it is asked for by name or arrives with something else: Docker
// sets the host's FORWARD policy to DROP under every guest. apt is asked
// what an install would bring, so the refusal names the engine apt chose.
func TestADockerEngineIsNotInstalledOnAProxmoxHost(t *testing.T) {
	for _, tc := range []struct {
		label   string
		name    string
		sim     string
		simErr  error
		refused string // the engine named, or "" when the install goes ahead
	}{
		{"asked for by name", "docker.io", simDockerIO, nil, "docker.io"},
		{"brought by another package", "docker-buildx", simBuildxBringsDocker, nil, "docker.io"},
		{"not in the lists", "docker-ce", "E: Unable to locate package docker-ce\n", errors.New("exit status 100"), "docker-ce"},
		{"something else", "htop", simHtop, nil, ""},
		// An engine already there is managed as usual.
		{"an upgrade of one installed", "docker.io", simDockerIOUpgrade, nil, ""},
	} {
		t.Run(tc.label, func(t *testing.T) {
			h, f, _ := txSetup(t, true)
			f.setArgs("apt-get -s install -- "+tc.name, tc.sim, tc.simErr)

			rec := call(t, h.InstallPackageStream, http.MethodPost, "/", `{"name":"`+tc.name+`"}`)
			settle(t, h)
			if tc.refused == "" {
				if rec.Code != http.StatusOK || len(f.calls("systemd-run")) != 1 {
					t.Errorf("%s was not installed: %d %s", tc.name, rec.Code, rec.Body.String())
				}
				return
			}
			code, details := failure(t, rec)
			if rec.Code != http.StatusConflict || code != response.ErrDockerOnPVE || details["package"] != tc.refused {
				t.Errorf("got %d %s %v, want 409 %s naming %s", rec.Code, code, details, response.ErrDockerOnPVE, tc.refused)
			}
			if len(f.calls("systemd-run")) != 0 || currentTx(t, h) != nil {
				t.Errorf("a transaction started: %+v", f.all())
			}

			// The install the terminal's prompt uses is refused the same way.
			rec = call(t, h.InstallPackage, http.MethodPost, "/", `{"name":"`+tc.name+`"}`)
			if code, details := failure(t, rec); rec.Code != http.StatusConflict || code != response.ErrDockerOnPVE || details["package"] != tc.refused {
				t.Errorf("unary install: %d %s %v", rec.Code, code, details)
			}
			for _, c := range f.calls("apt-get") {
				if slices.Contains(c.Args, "install") && !slices.Contains(c.Args, "-s") {
					t.Errorf("apt-get installed: %v", c.Args)
				}
			}
		})
	}
}

// Elsewhere Docker installs from the package list as before, and apt is not
// even asked.
func TestADockerEngineInstallsAwayFromProxmox(t *testing.T) {
	h, f, _ := txSetup(t, false)
	rec := call(t, h.InstallPackageStream, http.MethodPost, "/", `{"name":"docker.io"}`)
	settle(t, h)
	if rec.Code != http.StatusOK || len(f.calls("systemd-run")) != 1 {
		t.Errorf("docker.io was not installed: %d %s", rec.Code, rec.Body.String())
	}
	for _, c := range f.calls("apt-get") {
		if slices.Contains(c.Args, "-s") {
			t.Errorf("simulated an install on a host that is not Proxmox: %v", c.Args)
		}
	}
}

// The get.docker.com installer is refused on a Proxmox VE host before
// anything is downloaded; elsewhere it goes ahead.
func TestInstallDockerIsRefusedOnAProxmoxHost(t *testing.T) {
	var fetched []string
	prev := downloadInstaller
	downloadInstaller = func(_ context.Context, url string) (string, string, error) {
		fetched = append(fetched, url)
		return "", "", errors.New("not in a test")
	}
	t.Cleanup(func() { downloadInstaller = prev })

	h, _, _ := txSetup(t, true)
	rec := call(t, h.InstallDocker, http.MethodPost, "/", "")
	if code, _ := failure(t, rec); rec.Code != http.StatusConflict || code != response.ErrDockerOnPVE {
		t.Errorf("got %d %s, want 409 %s", rec.Code, code, response.ErrDockerOnPVE)
	}
	if len(fetched) != 0 {
		t.Errorf("fetched %v on a Proxmox host", fetched)
	}

	h, _, _ = txSetup(t, false)
	rec = call(t, h.InstallDocker, http.MethodPost, "/", "")
	if rec.Code != http.StatusOK || len(fetched) != 1 {
		t.Errorf("away from Proxmox: %d, fetched %v", rec.Code, fetched)
	}
}

func dockerStatus(t *testing.T, h *Handler) map[string]any {
	t.Helper()
	rec := call(t, h.GetDockerStatus, http.MethodGet, "/", "")
	var body struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("%v: %s", err, rec.Body.String())
	}
	return body.Data
}

func withBridgeNF(t *testing.T, value string) {
	t.Helper()
	prev := bridgeNFPath
	bridgeNFPath = filepath.Join(t.TempDir(), "bridge-nf-call-iptables")
	if value != "" {
		if err := os.WriteFile(bridgeNFPath, []byte(value), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { bridgeNFPath = prev })
}

// Where Docker is installed the card shows what it did to the host's
// forwarding; on a Proxmox VE host that decides whether the guests' bridged
// traffic meets a DROP policy.
func TestDockerStatusShowsTheForwardPolicyAndBridgeFiltering(t *testing.T) {
	h, f, _ := txSetup(t, true)
	withBridgeNF(t, "1\n")

	st := dockerStatus(t, h)
	if st["proxmox_host"] != true || st["installed"] != false {
		t.Errorf("no Docker on a Proxmox host: %v", st)
	}
	if _, ok := st["forward_policy"]; ok || len(f.calls("iptables")) != 0 {
		t.Errorf("read the forward policy with no Docker: %v", st)
	}

	f.set("exists:docker", "", nil)
	f.setArgs("docker --version", "Docker version 26.1.5+dfsg1, build a72d7cd", nil)
	f.setArgs("iptables -S FORWARD", "-P INPUT ACCEPT\n-P FORWARD DROP\n-P OUTPUT ACCEPT\n-N DOCKER-USER\n-A FORWARD -j DOCKER-USER\n", nil)
	st = dockerStatus(t, h)
	if st["forward_policy"] != "DROP" || st["bridge_nf_call_iptables"] != true || st["proxmox_host"] != true {
		t.Errorf("status %v, want FORWARD DROP with bridge filtering on, on a Proxmox host", st)
	}

	// Without br_netfilter bridged frames never reach iptables: null, not false.
	withBridgeNF(t, "")
	st = dockerStatus(t, h)
	if v, ok := st["bridge_nf_call_iptables"]; !ok || v != nil {
		t.Errorf("bridge_nf_call_iptables = %v (present %v), want null", v, ok)
	}
}

// The panel's stacks run on `docker compose`. A host with only the old
// docker-compose binary was told it had Compose, and every stack failed.
func TestComposeMeansTheV2Plugin(t *testing.T) {
	for _, tc := range []struct {
		name       string
		v2         error
		v1         bool
		available  bool
		v1OnlyFlag bool
	}{
		{"plugin", nil, false, true, false},
		{"plugin and the old binary", nil, true, true, false},
		{"the old binary alone", errors.New("docker: 'compose' is not a docker command"), true, false, true},
		{"neither", errors.New("docker: 'compose' is not a docker command"), false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, f, _ := txSetup(t, false)
			withBridgeNF(t, "")
			f.set("exists:docker", "", nil)
			f.setArgs("docker compose version", "Docker Compose version v2.33.1", tc.v2)
			if tc.v1 {
				f.set("exists:docker-compose", "", nil)
			}
			st := dockerStatus(t, h)
			if st["compose_available"] != tc.available || st["compose_v1_only"] != tc.v1OnlyFlag {
				t.Errorf("compose_available=%v compose_v1_only=%v, want %v %v", st["compose_available"], st["compose_v1_only"], tc.available, tc.v1OnlyFlag)
			}
		})
	}
}

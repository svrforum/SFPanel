package packages

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/svrforum/SFPanel/internal/api/response"
)

const upgradableC = `Listing...
nginx/stable-security 1.26.3-3+deb13u1 amd64 [upgradable from: 1.26.3-3]
pve-manager/stable 9.2.4 all [upgradable from: 9.2.3]
`

// A Proxmox VE host without a subscription: the check used to fail on
// every click with "exit status 100". The other repositories were refreshed,
// so it lists what they offer and names the one that refused.
func TestCheckUpdatesGoesOnPastARepositoryThatRefusedIt(t *testing.T) {
	h, f, _ := txSetup(t, true)
	f.setArgs("apt-get update", updateEnterprise401, errors.New("exit status 100"))
	f.setArgs("apt list --upgradable", upgradableC, nil)

	rec := call(t, h.CheckUpdates, http.MethodGet, "/", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Data struct {
			Updates  []PackageInfo      `json:"updates"`
			Warnings []response.Warning `json:"warnings"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data.Updates) != 2 || body.Data.Updates[1].Name != "pve-manager" || body.Data.Updates[1].CurrentVersion != "9.2.3" {
		t.Errorf("updates %+v", body.Data.Updates)
	}
	w := body.Data.Warnings
	if len(w) != 1 || w[0].Code != response.ErrAPTRepoUnauthorized || w[0].Details["uri"] != "https://enterprise.proxmox.com/debian/pve" {
		t.Errorf("warnings %+v, want APT_REPO_UNAUTHORIZED for the enterprise repository", w)
	}
}

// An update that refreshed nothing is still a failure, with apt's reason.
func TestCheckUpdatesFailsWhenTheListsCannotBeRead(t *testing.T) {
	h, f, _ := txSetup(t, false)
	f.setArgs("apt-get update", "E: The list of sources could not be read.\n", errors.New("exit status 100"))
	rec := call(t, h.CheckUpdates, http.MethodGet, "/", "")
	code, _ := failure(t, rec)
	if rec.Code != http.StatusInternalServerError || code != response.ErrAPTUpdateError || !strings.Contains(rec.Body.String(), "The list of sources could not be read") {
		t.Errorf("got %d %s: %s", rec.Code, code, rec.Body.String())
	}
	if len(f.calls("apt")) != 0 {
		t.Error("listed upgrades from lists that were not refreshed")
	}
}

// Every apt call whose output is read runs in C. "upgradable from:" is
// translated on a host whose language is not English, and the current
// version came back empty for every row.
func TestParsedAptOutputIsRequestedInC(t *testing.T) {
	h, f, _ := txSetup(t, true)
	f.setArgs("apt list --upgradable", upgradableC, nil)
	call(t, h.CheckUpdates, http.MethodGet, "/", "")
	call(t, h.SearchPackages, http.MethodGet, "/?q=nginx", "")
	call(t, h.InstallPackage, http.MethodPost, "/", `{"name":"htop"}`)

	for _, cmd := range []string{"apt-get update", "apt list --upgradable", "apt-cache search nginx", "apt-get -s install -- htop"} {
		f.mu.Lock()
		env, ran := f.envs[cmd]
		f.mu.Unlock()
		if !ran {
			t.Errorf("%s did not run", cmd)
			continue
		}
		if !slices.Contains(env, "LC_ALL=C") {
			t.Errorf("%s ran with %q, want LC_ALL=C", cmd, env)
		}
	}
}

// The parser itself, on the C-locale line.
func TestParseUpgradableLine(t *testing.T) {
	got := parseUpgradableLine("nginx/stable-security 1.26.3-3+deb13u1 amd64 [upgradable from: 1.26.3-3]")
	want := PackageInfo{Name: "nginx", CurrentVersion: "1.26.3-3", NewVersion: "1.26.3-3+deb13u1", Architecture: "amd64"}
	if got == nil || *got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

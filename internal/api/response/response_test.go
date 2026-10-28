package response

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
)

func failBody(t *testing.T, fail func(echo.Context) error) (int, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	if err := fail(echo.New().NewContext(httptest.NewRequest(http.MethodGet, "/", nil), rec)); err != nil {
		t.Fatal(err)
	}
	var body struct {
		Error map[string]any `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return rec.Code, body.Error
}

// The page words a code itself and fills in the specifics from details:
// "VM {{vmid}}" needs the vmid, or i18next prints the placeholder.
func TestFailWithCarriesTheDetailsTheMessageNames(t *testing.T) {
	status, e := failBody(t, func(c echo.Context) error {
		return FailWith(c, http.StatusConflict, ErrProcessIsGuest, "process is VM 100", map[string]string{"vmid": "100"})
	})
	if status != http.StatusConflict || e["code"] != ErrProcessIsGuest || e["message"] != "process is VM 100" {
		t.Fatalf("got %d %v", status, e)
	}
	details, _ := e["details"].(map[string]any)
	if details["vmid"] != "100" {
		t.Errorf("details %v, want vmid 100", e["details"])
	}
}

// Fail keeps its shape: no details key at all, rather than an empty one.
func TestFailHasNoDetails(t *testing.T) {
	_, e := failBody(t, func(c echo.Context) error {
		return Fail(c, http.StatusBadRequest, ErrInvalidRequest, "bad")
	})
	if _, ok := e["details"]; ok {
		t.Errorf("Fail sent details: %v", e)
	}
}

// A refused repository is worded by its own code and names the repository.
func TestRepoWarningsNameEachRepository(t *testing.T) {
	w := RepoWarnings([]string{"https://enterprise.proxmox.com/debian/pve"}, []string{"http://mirror.example.lan/debian"})
	if len(w) != 2 || w[0].Code != ErrAPTRepoUnauthorized || w[0].Details["uri"] != "https://enterprise.proxmox.com/debian/pve" ||
		w[1].Code != ErrAPTRepoFailed || w[1].Details["uri"] != "http://mirror.example.lan/debian" {
		t.Errorf("got %+v", w)
	}
	if RepoWarnings(nil, nil) != nil {
		t.Error("nothing failed, yet there are warnings")
	}
}

// A request that starts what its answer does not wait for — a node leaving the
// cluster to come back as an agent — is answered 202, in OK's envelope.
func TestAcceptedAnswersInOKsEnvelope(t *testing.T) {
	rec := httptest.NewRecorder()
	if err := Accepted(echo.New().NewContext(httptest.NewRequest(http.MethodPost, "/", nil), rec), map[string]string{"message": "leaving"}); err != nil {
		t.Fatal(err)
	}
	var body struct {
		Success bool              `json:"success"`
		Data    map[string]string `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusAccepted || !body.Success || body.Data["message"] != "leaving" {
		t.Fatalf("got %d %+v", rec.Code, body)
	}
}

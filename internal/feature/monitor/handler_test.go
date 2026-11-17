package featuremonitor

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
)

// The new-stack dialog and the App Store named /opt/stacks, the default, on a
// node whose server.stacks_path says otherwise. The overview every page
// already has carries the configured folder, without the trailing slash an
// operator may have written, which would show as "//" before a stack's name.
func TestGetOverviewCarriesStacksPath(t *testing.T) {
	for _, tc := range []struct {
		configured string
		want       *string
	}{
		{"/srv/stacks", ptr("/srv/stacks")},
		{"/srv/stacks/", ptr("/srv/stacks")},
		{"", nil}, // a handler without it sends no field, as an older node does
	} {
		rec := httptest.NewRecorder()
		c := echo.New().NewContext(httptest.NewRequest(http.MethodGet, "/system/overview?range=1h", nil), rec)
		if err := (&Handler{Version: "v0.0.0", StacksPath: tc.configured}).GetOverview(c); err != nil {
			t.Fatal(err)
		}
		var body struct {
			Data map[string]json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("%v: %s", err, rec.Body.String())
		}
		raw, ok := body.Data["stacks_path"]
		switch {
		case tc.want == nil && ok:
			t.Errorf("%q: stacks_path %s, want no field", tc.configured, raw)
		case tc.want != nil && !ok:
			t.Errorf("%q: no stacks_path, want %q", tc.configured, *tc.want)
		case tc.want != nil:
			var got string
			if err := json.Unmarshal(raw, &got); err != nil || got != *tc.want {
				t.Errorf("%q: stacks_path %s, want %q", tc.configured, raw, *tc.want)
			}
		}
	}
}

func ptr(s string) *string { return &s }

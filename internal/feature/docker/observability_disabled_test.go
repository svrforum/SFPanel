package featuredocker

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
)

// With observability off the endpoints must answer with the same shape they
// answer with when it is on and there is nothing recorded: an array.
//
// They used to wrap it in an object carrying an observability_disabled flag,
// which is what ObservabilityConfig's comment described but not what any
// caller expects — the client types these as slices and iterates them, so a
// documented, supported setting broke two Docker tabs instead of showing them
// empty.
func TestObservabilityDisabledReturnsArrays(t *testing.T) {
	h := &ObservabilityHandler{ObservabilityEnabled: false}

	cases := []struct {
		name   string
		path   string
		params map[string]string
		call   func(echo.Context) error
	}{
		{"history", "/docker/containers/abc/history?range=1h", map[string]string{"id": "abc"}, h.GetMetrics},
		{"events", "/docker/containers/abc/events", map[string]string{"id": "abc"}, h.GetEvents},
		{"recent events", "/docker/events/recent", nil, h.GetRecentEvents},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			rec := httptest.NewRecorder()
			c := echo.New().NewContext(req, rec)
			for k, v := range tc.params {
				c.SetParamNames(k)
				c.SetParamValues(v)
			}
			if err := tc.call(c); err != nil {
				t.Fatalf("handler: %v", err)
			}

			var envelope struct {
				Success bool            `json:"success"`
				Data    json.RawMessage `json:"data"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
				t.Fatalf("response is not the standard envelope: %v (%s)", err, rec.Body.String())
			}
			// Decoding into a slice is the assertion: an object would fail
			// here, which is exactly how the client failed.
			var items []map[string]any
			if err := json.Unmarshal(envelope.Data, &items); err != nil {
				t.Fatalf("data is not an array: %v (%s)", err, string(envelope.Data))
			}
			if len(items) != 0 {
				t.Errorf("got %d items, want an empty array", len(items))
			}
		})
	}
}

package lanscan

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/require"

	"github.com/svrforum/SFPanel/internal/api/response"
)

// serve sends one request through the routes as router.go registers them,
// so path parameters reach the handler as Echo hands them over.
func serve(t *testing.T, h *Handler, method, path, body string) (int, envelope) {
	t.Helper()
	e := echo.New()
	g := e.Group("/api/v1/network")
	g.GET("/scan", h.GetScan)
	g.POST("/scan", h.StartScan)
	g.DELETE("/scan", h.CancelScan)
	g.PUT("/devices/:mac", h.PutDevice)
	g.POST("/devices/:mac/ack", h.AckDevice)
	g.DELETE("/devices/:mac", h.DeleteDevice)

	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	var env envelope
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env), rec.Body.String())
	return rec.Code, env
}

func deviceCount(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM lan_devices`).Scan(&n))
	return n
}

func decodeDevice(t *testing.T, env envelope) Device {
	t.Helper()
	var d Device
	require.NoError(t, json.Unmarshal(env.Data, &d))
	return d
}

// A label for a MAC this node has never seen creates it, the MAC read in
// any form and stored lower case, the name and note trimmed, labelled_at
// now.
func TestPutDevice_LabelsAnUnseenMAC(t *testing.T) {
	db := openDB(t)
	h := newHandler(t, db, nodeCommander(true), nil)
	h.now = newClock(t0).now

	code, env := serve(t, h, http.MethodPut, "/api/v1/network/devices/02-42-AC-14-1E-2A",
		`{"name":"  Living room TV ","note":"\n  wall mount, HDMI 2\n","kind":"tv"}`)
	require.Equal(t, http.StatusOK, code, env.Error)
	require.JSONEq(t, `{"mac":"02:42:ac:14:1e:2a","name":"Living room TV","note":"wall mount, HDMI 2","kind":"tv",
		"labelled_at":"2026-11-22T03:00:00Z","acked":false,"network":"","ip":"","vendor":"","hostname":"",
		"first_seen":"","last_seen":""}`, string(env.Data))

	st := getStatus(t, h)
	require.Len(t, st.Devices, 1)
	require.Equal(t, "Living room TV", st.Devices[0].Name)
}

// The page may escape the colons; the MAC is the same.
func TestPutDevice_EscapedMAC(t *testing.T) {
	db := openDB(t)
	h := newHandler(t, db, nodeCommander(true), nil)
	code, env := serve(t, h, http.MethodPut, "/api/v1/network/devices/02%3A42%3Aac%3A14%3A1e%3A2a", `{"name":"NAS","kind":"nas"}`)
	require.Equal(t, http.StatusOK, code, env.Error)
	require.Equal(t, "02:42:ac:14:1e:2a", decodeDevice(t, env).MAC)
}

// The limits are characters, not bytes, and a note may run over lines.
func TestPutDevice_AtTheLimits(t *testing.T) {
	db := openDB(t)
	h := newHandler(t, db, nodeCommander(true), nil)
	name := strings.Repeat("가", 64)            // 192 bytes
	note := strings.Repeat("메모\n", 166) + "끝끝" // 500 characters with the last line break trimmed away
	body, err := json.Marshal(map[string]string{"name": name, "note": note + "\n", "kind": ""})
	require.NoError(t, err)
	code, env := serve(t, h, http.MethodPut, "/api/v1/network/devices/02:42:ac:14:1e:2a", string(body))
	require.Equal(t, http.StatusOK, code, env.Error)
	d := decodeDevice(t, env)
	require.Equal(t, name, d.Name)
	require.Equal(t, note, d.Note)
}

// Each guard refuses for its own reason, names its field, and writes
// nothing.
func TestPutDevice_Refused(t *testing.T) {
	cases := []struct {
		name, mac, body, field string
	}{
		{"a MAC of five bytes", "02:42:ac:14:1e", `{"name":"x"}`, "mac"},
		{"an EUI-64", "02:42:ac:14:1e:2a:00:01", `{"name":"x"}`, "mac"},
		{"not a MAC", "living-room-tv", `{"name":"x"}`, "mac"},
		{"a name of 65 characters", "02:42:ac:14:1e:2a", `{"name":"` + strings.Repeat("가", 65) + `"}`, "name"},
		{"a name over two lines", "02:42:ac:14:1e:2a", `{"name":"Living\nroom"}`, "name"},
		{"a name with a tab", "02:42:ac:14:1e:2a", `{"name":"Living\troom"}`, "name"},
		{"a name with an escape", "02:42:ac:14:1e:2a", `{"name":"TV\u001b[31m"}`, "name"},
		{"a note of 501 characters", "02:42:ac:14:1e:2a", `{"note":"` + strings.Repeat("a", 501) + `"}`, "note"},
		{"a note with a bell", "02:42:ac:14:1e:2a", `{"note":"ring\u0007"}`, "note"},
		{"an unknown kind", "02:42:ac:14:1e:2a", `{"kind":"fridge"}`, "kind"},
		{"a kind in capitals", "02:42:ac:14:1e:2a", `{"kind":"Router"}`, "kind"},
		{"labelled_at not a time", "02:42:ac:14:1e:2a", `{"name":"x","labelled_at":"yesterday"}`, "labelled_at"},
		{"labelled_at empty", "02:42:ac:14:1e:2a", `{"name":"x","labelled_at":""}`, "labelled_at"},
		{"labelled_at without a zone", "02:42:ac:14:1e:2a", `{"name":"x","labelled_at":"2026-11-22T03:00:00"}`, "labelled_at"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := openDB(t)
			h := newHandler(t, db, nodeCommander(true), nil)
			code, env := serve(t, h, http.MethodPut, "/api/v1/network/devices/"+tc.mac, tc.body)
			require.Equal(t, http.StatusBadRequest, code)
			require.NotNil(t, env.Error)
			require.Equal(t, response.ErrLanDeviceInvalid, env.Error.Code)
			require.Equal(t, map[string]string{"field": tc.field}, env.Error.Details)
			require.Zero(t, deviceCount(t, db), "nothing is written")
		})
	}
}

func TestPutDevice_BodyNotJSON(t *testing.T) {
	db := openDB(t)
	h := newHandler(t, db, nodeCommander(true), nil)
	code, env := serve(t, h, http.MethodPut, "/api/v1/network/devices/02:42:ac:14:1e:2a", `{"name":42}`)
	require.Equal(t, http.StatusBadRequest, code)
	require.Equal(t, response.ErrInvalidRequest, env.Error.Code)
	require.Zero(t, deviceCount(t, db))
}

// One label written to several nodes carries its time; a write older than
// the stored label changes nothing, whether its time is given or now.
func TestPutDevice_OlderLabelChangesNothing(t *testing.T) {
	db := openDB(t)
	clock := newClock(t0)
	h := newHandler(t, db, nodeCommander(true), nil)
	h.now = clock.now
	path := "/api/v1/network/devices/02:42:ac:14:1e:2a"

	// The page's time, with milliseconds and a zone, stored as given.
	code, env := serve(t, h, http.MethodPut, path, `{"name":"NAS","kind":"nas","labelled_at":"2026-11-22T12:30:00.250+09:00"}`)
	require.Equal(t, http.StatusOK, code, env.Error)
	require.Equal(t, "2026-11-22T12:30:00.250+09:00", decodeDevice(t, env).LabelledAt)

	// Older by a second: the stored label is answered, unchanged.
	code, env = serve(t, h, http.MethodPut, path, `{"name":"Old name","kind":"pc","labelled_at":"2026-11-22T03:29:59Z"}`)
	require.Equal(t, http.StatusOK, code, env.Error)
	d := decodeDevice(t, env)
	require.Equal(t, "NAS", d.Name)
	require.Equal(t, "nas", d.Kind)
	require.Equal(t, "2026-11-22T12:30:00.250+09:00", d.LabelledAt)
	require.Equal(t, "NAS", device(t, db, "02:42:ac:14:1e:2a").Name)

	// No time given: now, which is 03:00Z, older still.
	code, env = serve(t, h, http.MethodPut, path, `{"name":"Older name"}`)
	require.Equal(t, http.StatusOK, code, env.Error)
	require.Equal(t, "NAS", decodeDevice(t, env).Name)

	// The same time again is not older.
	code, env = serve(t, h, http.MethodPut, path, `{"name":"NAS 2","kind":"nas","labelled_at":"2026-11-22T03:30:00.250Z"}`)
	require.Equal(t, http.StatusOK, code, env.Error)
	require.Equal(t, "NAS 2", decodeDevice(t, env).Name)

	// Later: written.
	clock.advance(time.Hour)
	code, env = serve(t, h, http.MethodPut, path, `{"name":"Backup NAS","note":"rack 1","kind":"nas"}`)
	require.Equal(t, http.StatusOK, code, env.Error)
	d = decodeDevice(t, env)
	require.Equal(t, "Backup NAS", d.Name)
	require.Equal(t, "rack 1", d.Note)
	require.Equal(t, "2026-11-22T04:00:00Z", d.LabelledAt)
}

// A label leaves what the scans saw, and the acknowledgement, alone.
func TestPutDevice_KeepsTheSighting(t *testing.T) {
	db := openDB(t)
	h := newHandler(t, db, nodeCommander(true), nil)
	h.now = newClock(t0.Add(time.Hour)).now
	record(t, db, t0, Host{IP: "10.20.30.31", MAC: "02:42:ac:14:1e:1f", Vendor: "Espressif", Ports: []Port{}})
	_, err := ackDevice(db, "02:42:ac:14:1e:1f")
	require.NoError(t, err)

	code, env := serve(t, h, http.MethodPut, "/api/v1/network/devices/02:42:ac:14:1e:1f", `{"name":"Plug","kind":"iot"}`)
	require.Equal(t, http.StatusOK, code, env.Error)
	require.Equal(t, Device{MAC: "02:42:ac:14:1e:1f", Name: "Plug", Kind: "iot", LabelledAt: stamp(t0.Add(time.Hour)),
		Acked: true, Network: "10.20.30.0/24", IP: "10.20.30.31", Vendor: "Espressif",
		FirstSeen: stamp(t0), LastSeen: stamp(t0)}, decodeDevice(t, env))
}

func TestAckDevice(t *testing.T) {
	db := openDB(t)
	h := newHandler(t, db, nodeCommander(true), nil)

	code, env := serve(t, h, http.MethodPost, "/api/v1/network/devices/02:42:ac:14:1e:1f/ack", "")
	require.Equal(t, http.StatusNotFound, code)
	require.Equal(t, response.ErrLanDeviceNotFound, env.Error.Code)
	require.Zero(t, deviceCount(t, db), "an unknown MAC is not created")

	code, env = serve(t, h, http.MethodPost, "/api/v1/network/devices/02:42:ac:14:1e/ack", "")
	require.Equal(t, http.StatusBadRequest, code)
	require.Equal(t, response.ErrLanDeviceInvalid, env.Error.Code)
	require.Equal(t, map[string]string{"field": "mac"}, env.Error.Details)

	record(t, db, t0, sighting("10.20.30.31", "02:42:ac:14:1e:1f"))
	require.False(t, device(t, db, "02:42:ac:14:1e:1f").Acked)
	code, env = serve(t, h, http.MethodPost, "/api/v1/network/devices/02:42:AC:14:1E:1F/ack", "")
	require.Equal(t, http.StatusOK, code, env.Error)
	require.JSONEq(t, `{"acked":true}`, string(env.Data))
	require.True(t, device(t, db, "02:42:ac:14:1e:1f").Acked)
}

// Deleting forgets the device and the addresses it held.
func TestDeleteDevice(t *testing.T) {
	db := openDB(t)
	h := newHandler(t, db, nodeCommander(true), nil)
	a, b := "02:42:ac:14:1e:4a", "02:42:ac:14:1e:4b"
	record(t, db, t0, sighting("10.20.30.12", a))
	record(t, db, t0.Add(time.Hour), sighting("10.20.30.12", b))
	record(t, db, t0.Add(2*time.Hour), sighting("10.20.30.12", a))
	require.Len(t, conflicts(t, db), 1)

	code, env := serve(t, h, http.MethodDelete, "/api/v1/network/devices/"+b, "")
	require.Equal(t, http.StatusOK, code, env.Error)
	require.JSONEq(t, `{"deleted":true}`, string(env.Data))
	_, ok, err := getDevice(db, b)
	require.NoError(t, err)
	require.False(t, ok)
	require.Len(t, claims(t, db), 1, "its claims are gone, the other device's stay")
	require.Equal(t, []Conflict{}, conflicts(t, db))

	code, env = serve(t, h, http.MethodDelete, "/api/v1/network/devices/"+b, "")
	require.Equal(t, http.StatusOK, code, env.Error)
	require.JSONEq(t, `{"deleted":false}`, string(env.Data))

	code, env = serve(t, h, http.MethodDelete, "/api/v1/network/devices/nonsense", "")
	require.Equal(t, http.StatusBadRequest, code)
	require.Equal(t, response.ErrLanDeviceInvalid, env.Error.Code)
	require.Equal(t, map[string]string{"field": "mac"}, env.Error.Details)
	require.Equal(t, 1, deviceCount(t, db))
}

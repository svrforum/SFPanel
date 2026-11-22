package lanscan

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/svrforum/SFPanel/internal/api/response"
	sfdb "github.com/svrforum/SFPanel/internal/db"
)

// testNode is a cluster node id for a node item; made up.
const testNode = "3c1f6a2e-7b4d-4f0a-9e8c-2d5b6a7c8e9f"

// rackBody is a PUT body with doc, written at 2026-11-22T10:00:00Z.
func rackBody(doc string) string {
	return `{"doc":` + doc + `,"updated_at":"2026-11-22T10:00:00Z"}`
}

// rackAt is a PUT body with doc, written at updatedAt.
func rackAt(doc, updatedAt string) string {
	return `{"doc":` + doc + `,"updated_at":"` + updatedAt + `"}`
}

// liveRack is a valid rack named name, 12 units tall, with one text item.
func liveRack(name string) string {
	return `{"name":"` + name + `","units":12,"items":[{"id":"a1","u":1,"height":1,"kind":"text","text":"Patch panel"}]}`
}

func putRack(t *testing.T, h *Handler, id, body string) (int, envelope) {
	t.Helper()
	return serve(t, h, http.MethodPut, "/api/v1/network/racks/"+id, body)
}

// putRackOK writes a rack and answers it as stored.
func putRackOK(t *testing.T, h *Handler, id, body string) Rack {
	t.Helper()
	code, env := putRack(t, h, id, body)
	require.Equal(t, http.StatusOK, code, env.Error)
	var r Rack
	require.NoError(t, json.Unmarshal(env.Data, &r))
	return r
}

func getRacks(t *testing.T, h *Handler) []Rack {
	t.Helper()
	code, env := serve(t, h, http.MethodGet, "/api/v1/network/racks", "")
	require.Equal(t, http.StatusOK, code, env.Error)
	var out struct {
		Racks []Rack `json:"racks"`
	}
	require.NoError(t, json.Unmarshal(env.Data, &out))
	return out.Racks
}

func rackIDs(t *testing.T, h *Handler) []string {
	t.Helper()
	ids := []string{}
	for _, r := range getRacks(t, h) {
		ids = append(ids, r.ID)
	}
	return ids
}

func rackCount(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM lan_racks`).Scan(&n))
	return n
}

// A rack is answered and listed as stored: normalised, its doc an object.
func TestPutRack_RoundTrip(t *testing.T) {
	db := openDB(t)
	h := newHandler(t, db, nodeCommander(true), nil)

	code, env := serve(t, h, http.MethodGet, "/api/v1/network/racks", "")
	require.Equal(t, http.StatusOK, code, env.Error)
	require.JSONEq(t, `{"racks":[]}`, string(env.Data))

	// Untrimmed strings, a MAC in capitals with dashes, fields a kind does
	// not use, and a field no rack has.
	code, env = putRack(t, h, "main-1", rackAt(`{"name":"  Main rack ","units":12,"deleted":false,"colour":"red",
		"items":[{"id":" a1 ","u":1,"height":2,"kind":"node","node":" `+testNode+` ","mac":"02:42:ac:14:1e:29","text":"x"},
		         {"id":"a2","u":3,"height":1,"kind":"device","mac":"02-42-AC-14-1E-29","node":"n","text":"x"},
		         {"id":"a3","u":12,"height":1,"kind":"text","text":"  Patch panel  ","node":"n","mac":"02:42:ac:14:1e:2a"}]}`,
		"2026-11-22T10:00:00.000Z"))
	require.Equal(t, http.StatusOK, code, env.Error)
	want := `{"id":"main-1","updated_at":"2026-11-22T10:00:00.000Z","doc":{"name":"Main rack","units":12,"deleted":false,"items":[
		{"id":"a1","u":1,"height":2,"kind":"node","node":"` + testNode + `"},
		{"id":"a2","u":3,"height":1,"kind":"device","mac":"02:42:ac:14:1e:29"},
		{"id":"a3","u":12,"height":1,"kind":"text","text":"Patch panel"}]}}`
	require.JSONEq(t, want, string(env.Data))

	code, env = serve(t, h, http.MethodGet, "/api/v1/network/racks", "")
	require.Equal(t, http.StatusOK, code, env.Error)
	require.JSONEq(t, `{"racks":[`+want+`]}`, string(env.Data))

	// An empty rack has items [], not null; racks are listed by id.
	putRackOK(t, h, "Aisle_2", rackBody(`{"name":"Empty","units":1}`))
	code, env = serve(t, h, http.MethodGet, "/api/v1/network/racks", "")
	require.Equal(t, http.StatusOK, code, env.Error)
	require.JSONEq(t, `{"racks":[{"id":"Aisle_2","updated_at":"2026-11-22T10:00:00Z",
		"doc":{"name":"Empty","units":1,"deleted":false,"items":[]}},`+want+`]}`, string(env.Data))
}

// A write older than the stored rack changes nothing and answers the stored
// one; the same time again, however it is spelled, is written; a later one
// is written.
func TestPutRack_OlderWriteChangesNothing(t *testing.T) {
	db := openDB(t)
	h := newHandler(t, db, nodeCommander(true), nil)

	r := putRackOK(t, h, "main", rackAt(liveRack("First"), "2026-11-22T19:00:00.250+09:00"))
	require.Equal(t, "First", r.Doc.Name)
	require.Equal(t, "2026-11-22T19:00:00.250+09:00", r.UpdatedAt, "stored as sent")

	r = putRackOK(t, h, "main", rackAt(liveRack("Older"), "2026-11-22T10:00:00Z"))
	require.Equal(t, "First", r.Doc.Name, "older by 250 ms: the stored rack is answered")
	require.Equal(t, "2026-11-22T19:00:00.250+09:00", r.UpdatedAt)
	require.Equal(t, "First", getRacks(t, h)[0].Doc.Name)

	r = putRackOK(t, h, "main", rackAt(liveRack("Same"), "2026-11-22T10:00:00.250Z"))
	require.Equal(t, "Same", r.Doc.Name, "the same time again is written")
	require.Equal(t, "2026-11-22T10:00:00.250Z", r.UpdatedAt)

	r = putRackOK(t, h, "main", rackAt(liveRack("Later"), "2026-11-22T10:00:01Z"))
	require.Equal(t, "Later", r.Doc.Name)
	racks := getRacks(t, h)
	require.Len(t, racks, 1)
	require.Equal(t, "Later", racks[0].Doc.Name)
	require.Equal(t, "2026-11-22T10:00:01Z", racks[0].UpdatedAt)
}

// A deleted rack is kept as a mark, with no name or items, so that the rack
// as a node that was down still has it does not come back; a later write
// drops a mark more than 30 days older than itself.
func TestPutRack_DeletedMark(t *testing.T) {
	db := openDB(t)
	h := newHandler(t, db, nodeCommander(true), nil)

	putRackOK(t, h, "main", rackAt(liveRack("Main rack"), "2026-11-22T10:00:00Z"))
	// The name and items of a deleted rack are dropped unread.
	r := putRackOK(t, h, "main", rackAt(`{"name":"bad\u0007","units":12,"deleted":true,
		"items":[{"id":"x y","u":0,"height":9,"kind":"shelf"}]}`, "2026-11-22T10:01:00Z"))
	want := Rack{ID: "main", Doc: RackDoc{Units: 12, Deleted: true, Items: []RackItem{}}, UpdatedAt: "2026-11-22T10:01:00Z"}
	require.Equal(t, want, r)

	// The rack as it was, from a node that was down: older, so nothing.
	r = putRackOK(t, h, "main", rackAt(liveRack("Main rack"), "2026-11-22T10:00:00Z"))
	require.Equal(t, want, r)

	// Without units, a mark is 1 unit tall.
	putRackOK(t, h, "spare", rackAt(`{"deleted":true}`, "2026-11-22T19:02:00+09:00"))
	// A rack that is not deleted is never dropped, however old.
	putRackOK(t, h, "old", rackAt(liveRack("Old rack"), "2026-10-01T00:00:00Z"))

	racks := getRacks(t, h)
	require.Equal(t, []Rack{
		{ID: "main", Doc: RackDoc{Units: 12, Deleted: true, Items: []RackItem{}}, UpdatedAt: "2026-11-22T10:01:00Z"},
		{ID: "old", Doc: RackDoc{Name: "Old rack", Units: 12,
			Items: []RackItem{{ID: "a1", U: 1, Height: 1, Kind: "text", Text: "Patch panel"}}}, UpdatedAt: "2026-10-01T00:00:00Z"},
		{ID: "spare", Doc: RackDoc{Units: 1, Deleted: true, Items: []RackItem{}}, UpdatedAt: "2026-11-22T19:02:00+09:00"},
	}, racks)

	// A mark's age is told by this node's clock. Exactly 30 days after
	// main's mark: kept.
	clock := newClock(time.Date(2026, 12, 22, 10, 1, 0, 0, time.UTC))
	h.now = clock.now
	putRackOK(t, h, "other", rackAt(liveRack("Other"), "2026-12-22T10:01:00Z"))
	require.Equal(t, []string{"main", "old", "other", "spare"}, rackIDs(t, h))

	// A second more: main's mark goes; spare's, a minute younger, stays.
	clock.advance(time.Second)
	putRackOK(t, h, "other", rackAt(liveRack("Other"), "2026-12-22T10:01:01Z"))
	require.Equal(t, []string{"old", "other", "spare"}, rackIDs(t, h))
}

// A mark's age is not told by the write's own time, which is whatever the
// device that sent it believes — and the page stamps a change later than the
// copy it changes, so one device with a clock months ahead moves every later
// write of that rack ahead too. Such a write must not drop the mark of a rack
// deleted a minute ago: a node that was down then would bring the rack back.
func TestPutRack_MarkAgeIsByThisNodesClock(t *testing.T) {
	db := openDB(t)
	h := newHandler(t, db, nodeCommander(true), nil)
	clock := newClock(time.Date(2026, 11, 22, 10, 5, 0, 0, time.UTC))
	h.now = clock.now

	putRackOK(t, h, "gone", rackAt(`{"deleted":true}`, "2026-11-22T10:04:00Z"))
	// A write that says it is from next March.
	putRackOK(t, h, "other", rackAt(liveRack("Other"), "2027-03-01T00:00:00Z"))
	require.Equal(t, []string{"gone", "other"}, rackIDs(t, h))

	// A mark whose own time is ahead of the clock is kept, however long.
	putRackOK(t, h, "ahead", rackAt(`{"deleted":true}`, "2027-03-01T00:00:00Z"))
	clock.advance(31 * 24 * time.Hour)
	// A write that says it is from long ago is as good as any to sweep by:
	// gone's mark is 31 days old now.
	putRackOK(t, h, "claims-2020", rackAt(liveRack("Says it is 2020"), "2020-01-01T00:00:00Z"))
	require.Equal(t, []string{"ahead", "claims-2020", "other"}, rackIDs(t, h))
}

// The fullest rack the rules allow is stored: 60 one-unit items, each id and
// text as long as it may be.
func TestPutRack_FullestRackFits(t *testing.T) {
	db := openDB(t)
	h := newHandler(t, db, nodeCommander(true), nil)
	for _, wide := range []string{"랙", "𝌆"} {
		items := make([]string, 60)
		for i := range items {
			items[i] = fmt.Sprintf(`{"id":"%064d","u":%d,"height":1,"kind":"text","text":%q}`, i, i+1, strings.Repeat(wide, 64))
		}
		body := rackBody(`{"name":"` + strings.Repeat(wide, 64) + `","units":60,"items":[` + strings.Join(items, ",") + `]}`)
		require.Greater(t, len(body), 16<<10, "what a 16 KiB limit refused")
		r := putRackOK(t, h, "full", body)
		require.Len(t, r.Doc.Items, 60)
	}
}

// Each guard refuses for its own reason: the code, the field and the
// message, which tells apart the refusals of one field. Nothing is written.
func TestPutRack_Refused(t *testing.T) {
	item := func(fields string) string {
		return `{"name":"Main rack","units":12,"items":[{"id":"a1","u":1,"height":1,` + fields + `}]}`
	}
	long := strings.Repeat("a", 65)
	const (
		msgID       = "The rack id is 1 to 64 of A-Z, a-z, 0-9, _ and -"
		msgUpdated  = "updated_at must be an RFC 3339 time"
		msgNoDoc    = "The request has no doc"
		msgName     = "The name is 1 to 64 characters, on one line"
		msgUnits    = "units is a whole number from 1 to 60"
		msgItemID   = "An item id is 1 to 64 of A-Z, a-z, 0-9, _ and -"
		msgU        = "Item a1: u is a whole number of 1 or more"
		msgHeight   = "Item a1: height is a whole number from 1 to 8"
		msgAbove    = "Item a1 reaches above the rack's 12 units"
		msgKind     = "Item a1: kind is node, device or text"
		msgNode     = "Item a1: node is 1 to 64 characters, on one line"
		msgMAC      = "Item a1: mac must be six bytes, as 02:42:ac:14:1e:1f"
		msgText     = "Item a1: text is 1 to 64 characters, on one line"
		textItem    = `"kind":"text","text":"x"`
		updatedAtOK = `"updated_at":"2026-11-22T10:00:00Z"`
	)
	cases := []struct {
		name, id, body, field, message string
	}{
		{"an id of 65 characters", long, rackBody(liveRack("x")), "id", msgID},
		{"an id with a dot", "rack.1", rackBody(liveRack("x")), "id", msgID},
		{"an id with an escaped space", "rack%201", rackBody(liveRack("x")), "id", msgID},
		{"an id in Hangul", "%EB%9E%99", rackBody(liveRack("x")), "id", msgID},

		{"no updated_at", "main", `{"doc":` + liveRack("x") + `}`, "updated_at", msgUpdated},
		{"updated_at empty", "main", rackAt(liveRack("x"), ""), "updated_at", msgUpdated},
		{"updated_at not a time", "main", rackAt(liveRack("x"), "yesterday"), "updated_at", msgUpdated},
		{"updated_at without a zone", "main", rackAt(liveRack("x"), "2026-11-22T10:00:00"), "updated_at", msgUpdated},

		{"no doc", "main", `{` + updatedAtOK + `}`, "doc", msgNoDoc},
		{"doc null", "main", `{"doc":null,` + updatedAtOK + `}`, "doc", msgNoDoc},

		{"a name of spaces", "main", rackBody(liveRack("   ")), "name", msgName},
		{"no name", "main", rackBody(`{"units":12}`), "name", msgName},
		{"a name of 65 characters", "main", rackBody(liveRack(strings.Repeat("가", 65))), "name", msgName},
		{"a name over two lines", "main", rackBody(liveRack(`Main\nrack`)), "name", msgName},
		{"a name with an escape", "main", rackBody(liveRack(`Main\u001b[31m`)), "name", msgName},

		{"no units", "main", rackBody(`{"name":"Main rack"}`), "units", msgUnits},
		{"0 units", "main", rackBody(`{"name":"Main rack","units":0}`), "units", msgUnits},
		{"61 units", "main", rackBody(`{"name":"Main rack","units":61}`), "units", msgUnits},
		{"1.5 units", "main", rackBody(`{"name":"Main rack","units":1.5}`), "units", msgUnits},
		{"a deleted rack of 61 units", "main", rackBody(`{"deleted":true,"units":61}`), "units", msgUnits},

		{"an item id empty", "main", rackBody(`{"name":"Main rack","units":12,"items":[{"id":" ","u":1,"height":1,` + textItem + `}]}`), "items", msgItemID},
		{"an item id of 65 characters", "main", rackBody(`{"name":"Main rack","units":12,"items":[{"id":"` + long + `","u":1,"height":1,` + textItem + `}]}`), "items", msgItemID},
		{"an item id with a space", "main", rackBody(`{"name":"Main rack","units":12,"items":[{"id":"a 1","u":1,"height":1,` + textItem + `}]}`), "items", msgItemID},
		{"an item id used twice", "main", rackBody(`{"name":"Main rack","units":12,"items":[{"id":"a1","u":1,"height":1,` + textItem + `},
			{"id":"a1","u":2,"height":1,` + textItem + `}]}`), "items", "Item id a1 is used twice"},
		{"u 0", "main", rackBody(`{"name":"Main rack","units":12,"items":[{"id":"a1","u":0,"height":1,` + textItem + `}]}`), "items", msgU},
		{"u -1", "main", rackBody(`{"name":"Main rack","units":12,"items":[{"id":"a1","u":-1,"height":1,` + textItem + `}]}`), "items", msgU},
		{"u 1.5", "main", rackBody(`{"name":"Main rack","units":12,"items":[{"id":"a1","u":1.5,"height":1,` + textItem + `}]}`), "items", msgU},
		{"height 0", "main", rackBody(`{"name":"Main rack","units":12,"items":[{"id":"a1","u":1,"height":0,` + textItem + `}]}`), "items", msgHeight},
		{"height 9", "main", rackBody(`{"name":"Main rack","units":12,"items":[{"id":"a1","u":1,"height":9,` + textItem + `}]}`), "items", msgHeight},
		{"height 1.5", "main", rackBody(`{"name":"Main rack","units":12,"items":[{"id":"a1","u":1,"height":1.5,` + textItem + `}]}`), "items", msgHeight},
		{"an item past the top", "main", rackBody(`{"name":"Main rack","units":12,"items":[{"id":"a1","u":12,"height":2,` + textItem + `}]}`), "items", msgAbove},
		{"an item above the top", "main", rackBody(`{"name":"Main rack","units":12,"items":[{"id":"a1","u":13,"height":1,` + textItem + `}]}`), "items", msgAbove},
		{"an item far above the top", "main", rackBody(`{"name":"Main rack","units":12,"items":[{"id":"a1","u":1e300,"height":1,` + textItem + `}]}`), "items", msgAbove},
		{"an unknown kind", "main", rackBody(item(`"kind":"shelf","text":"x"`)), "items", msgKind},
		{"a kind in capitals", "main", rackBody(item(`"kind":"Text","text":"x"`)), "items", msgKind},
		{"no kind", "main", rackBody(item(`"text":"x"`)), "items", msgKind},
		{"a node item with no node", "main", rackBody(item(`"kind":"node"`)), "items", msgNode},
		{"a node item with a node of spaces", "main", rackBody(item(`"kind":"node","node":"   "`)), "items", msgNode},
		{"a node item with a node of 65 characters", "main", rackBody(item(`"kind":"node","node":"` + long + `"`)), "items", msgNode},
		{"a node item with a node over two lines", "main", rackBody(item(`"kind":"node","node":"node\none"`)), "items", msgNode},
		{"a device item with no MAC", "main", rackBody(item(`"kind":"device"`)), "items", msgMAC},
		{"a device item with five bytes", "main", rackBody(item(`"kind":"device","mac":"02:42:ac:14:1e"`)), "items", msgMAC},
		{"a device item with an EUI-64", "main", rackBody(item(`"kind":"device","mac":"02:42:ac:14:1e:29:00:01"`)), "items", msgMAC},
		{"a device item with a name for a MAC", "main", rackBody(item(`"kind":"device","mac":"router"`)), "items", msgMAC},
		{"a text item with no text", "main", rackBody(item(`"kind":"text"`)), "items", msgText},
		{"a text item of spaces", "main", rackBody(item(`"kind":"text","text":"   "`)), "items", msgText},
		{"a text item of 65 characters", "main", rackBody(item(`"kind":"text","text":"` + strings.Repeat("가", 65) + `"`)), "items", msgText},
		{"a text item over two lines", "main", rackBody(item(`"kind":"text","text":"Patch\npanel"`)), "items", msgText},
		{"two items in one unit", "main", rackBody(`{"name":"Main rack","units":12,"items":[{"id":"a1","u":1,"height":2,` + textItem + `},
			{"id":"a2","u":2,"height":1,` + textItem + `}]}`), "items", "Items a1 and a2 both take unit 2"},
		{"an item in the unit of one below it", "main", rackBody(`{"name":"Main rack","units":12,"items":[{"id":"a2","u":5,"height":1,` + textItem + `},
			{"id":"a1","u":3,"height":3,` + textItem + `}]}`), "items", "Items a2 and a1 both take unit 5"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := openDB(t)
			h := newHandler(t, db, nodeCommander(true), nil)
			code, env := putRack(t, h, tc.id, tc.body)
			require.Equal(t, http.StatusBadRequest, code)
			require.NotNil(t, env.Error)
			require.Equal(t, response.ErrLanRackInvalid, env.Error.Code)
			require.Equal(t, map[string]string{"field": tc.field}, env.Error.Details)
			require.Equal(t, tc.message, env.Error.Message)
			require.Zero(t, rackCount(t, db), "nothing is written")
		})
	}
}

// Up to 64 KiB is read; one byte more is refused, unread.
func TestPutRack_BodySize(t *testing.T) {
	db := openDB(t)
	h := newHandler(t, db, nodeCommander(true), nil)
	body := rackBody(liveRack("Main rack"))
	pad := func(n int) string { return body + strings.Repeat(" ", n-len(body)) }

	code, env := putRack(t, h, "exact", pad(64<<10))
	require.Equal(t, http.StatusOK, code, env.Error)

	code, env = putRack(t, h, "over", pad(64<<10+1))
	require.Equal(t, http.StatusBadRequest, code)
	require.Equal(t, response.ErrLanRackInvalid, env.Error.Code)
	require.Equal(t, map[string]string{"field": "doc"}, env.Error.Details)
	require.Equal(t, "The request is larger than 64 KiB", env.Error.Message)
	require.Equal(t, []string{"exact"}, rackIDs(t, h), "nothing is written")
}

// A body that is not JSON, or not of the shape, is the generic refusal.
func TestPutRack_BodyNotJSON(t *testing.T) {
	for _, body := range []string{
		"",
		`{"doc":`,
		`[]`,
		`{"doc":"Main rack","updated_at":"2026-11-22T10:00:00Z"}`,
		rackBody(`{"name":"Main rack","units":"12"}`),
		rackBody(`{"name":"Main rack","units":12,"items":[{"id":"a1","u":"1","height":1,"kind":"text","text":"x"}]}`),
	} {
		db := openDB(t)
		h := newHandler(t, db, nodeCommander(true), nil)
		code, env := putRack(t, h, "main", body)
		require.Equal(t, http.StatusBadRequest, code, body)
		require.Equal(t, response.ErrInvalidRequest, env.Error.Code, body)
		require.Zero(t, rackCount(t, db))
	}
}

// A node keeps at most 32 racks that are not deleted; deleted marks do not
// count, and a write that adds no rack is not held to it.
func TestPutRack_RackLimit(t *testing.T) {
	db := openDB(t)
	h := newHandler(t, db, nodeCommander(true), nil)
	for i := 0; i < 32; i++ {
		putRackOK(t, h, fmt.Sprintf("r%02d", i), rackBody(liveRack(fmt.Sprintf("Rack %d", i))))
	}
	putRackOK(t, h, "d1", rackAt(`{"deleted":true,"units":12}`, "2026-11-22T11:00:00Z"))
	putRackOK(t, h, "d2", rackAt(`{"deleted":true,"units":12}`, "2026-11-22T11:00:00Z"))
	refused := func(id, body string) {
		t.Helper()
		before := getRacks(t, h)
		code, env := putRack(t, h, id, body)
		require.Equal(t, http.StatusBadRequest, code)
		require.Equal(t, response.ErrLanRackInvalid, env.Error.Code)
		require.Equal(t, map[string]string{"field": "racks"}, env.Error.Details)
		require.Equal(t, "A node keeps at most 32 racks that are not deleted", env.Error.Message)
		require.Equal(t, before, getRacks(t, h), "nothing is written")
	}

	refused("r32", rackBody(liveRack("One too many")))
	// Bringing a deleted rack back adds one too.
	refused("d1", rackAt(liveRack("Back"), "2026-11-22T12:00:00Z"))

	// Changing a rack, deleting one, or an older write that changes
	// nothing, adds none.
	require.Equal(t, "Renamed", putRackOK(t, h, "r05", rackAt(liveRack("Renamed"), "2026-11-22T10:00:01Z")).Doc.Name)
	require.True(t, putRackOK(t, h, "d3", rackBody(`{"deleted":true}`)).Doc.Deleted)
	require.True(t, putRackOK(t, h, "d2", rackAt(liveRack("Back"), "2026-11-22T10:59:59Z")).Doc.Deleted)

	// One deleted makes room for one, and only one.
	putRackOK(t, h, "r00", rackAt(`{"deleted":true}`, "2026-11-22T10:00:01Z"))
	require.Equal(t, "One more", putRackOK(t, h, "r32", rackBody(liveRack("One more"))).Doc.Name)
	refused("r33", rackBody(liveRack("Again one too many")))
}

// The racks are in the database: a new handler on it, after the panel
// restarts, answers them.
func TestRacks_KeptAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "panel.db")
	db, err := sfdb.Open(path)
	require.NoError(t, err)
	h := newHandler(t, db, nodeCommander(true), nil)
	stored := putRackOK(t, h, "main", rackBody(liveRack("Main rack")))
	require.NoError(t, db.Close())

	db, err = sfdb.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	h = newHandler(t, db, nodeCommander(true), nil)
	require.Equal(t, []Rack{stored}, getRacks(t, h))
}

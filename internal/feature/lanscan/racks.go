package lanscan

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/labstack/echo/v4"

	"github.com/svrforum/SFPanel/internal/api/response"
)

const (
	// maxRackBody is the most a PUT /network/racks/:id body may be, in bytes:
	// well over the fullest rack the rules allow (60 one-unit items, every
	// id and text at its length, is about 19 KB in Hangul and 25 KB at four
	// bytes a character).
	maxRackBody = 64 << 10
	// maxRackID is the longest a rack's or an item's id may be.
	maxRackID = 64
	// maxRackUnits is the tallest a rack may be, in units.
	maxRackUnits = 60
	// maxItemHeight is the tallest an item may be, in units.
	maxItemHeight = 8
	// maxRacks is the most racks a node keeps that are not deleted.
	maxRacks = 32
	// rackMarkKeep is how long a deleted rack's mark is kept: a write drops
	// the marks written more than this before this node's clock.
	rackMarkKeep = 30 * 24 * time.Hour
)

// Rack is one rack the operator drew, as lan_racks keeps it and the rack
// routes answer it. Per node, never replicated: the page writes one rack to
// several nodes with one updated_at, and each keeps the newer.
type Rack struct {
	ID  string  `json:"id"`
	Doc RackDoc `json:"doc"`
	// UpdatedAt is when the page wrote the rack, RFC 3339, as it sent it.
	UpdatedAt string `json:"updated_at"`
}

// RackDoc is the drawing itself. A rack the operator deleted is kept as a
// mark, with no name and no items, so that a node that was down when it was
// deleted does not bring it back.
type RackDoc struct {
	Name    string     `json:"name"`
	Units   int        `json:"units"`
	Deleted bool       `json:"deleted"`
	Items   []RackItem `json:"items"`
}

// RackItem is one thing in a rack: from unit U (1 is the bottom) up,
// Height units tall. A node item names a cluster node, a device item a MAC
// and a text item says what is there; each carries only its own field.
type RackItem struct {
	ID     string `json:"id"`
	U      int    `json:"u"`
	Height int    `json:"height"`
	Kind   string `json:"kind"`
	Node   string `json:"node,omitempty"`
	MAC    string `json:"mac,omitempty"`
	Text   string `json:"text,omitempty"`
}

type rackRequest struct {
	Doc       *rackDocIn `json:"doc"`
	UpdatedAt string     `json:"updated_at"`
}

// rackDocIn and rackItemIn are a doc as the page sends it. Their numbers
// read any JSON number, so that one that is not a whole number is refused by
// its field, not as a body that does not read.
type rackDocIn struct {
	Name    string       `json:"name"`
	Units   *float64     `json:"units"`
	Deleted bool         `json:"deleted"`
	Items   []rackItemIn `json:"items"`
}

type rackItemIn struct {
	ID     string  `json:"id"`
	U      float64 `json:"u"`
	Height float64 `json:"height"`
	Kind   string  `json:"kind"`
	Node   string  `json:"node"`
	MAC    string  `json:"mac"`
	Text   string  `json:"text"`
}

// errTooManyRacks is a write that would leave more than maxRacks racks that
// are not deleted.
var errTooManyRacks = errors.New("too many racks")

// GetRacks answers every rack this node keeps, deleted marks included, by
// id.
//
//	GET /api/v1/network/racks
func (h *Handler) GetRacks(c echo.Context) error {
	racks, err := loadRacks(h.DB)
	if err != nil {
		slog.Warn("LAN racks unreadable", "component", "lanscan", "error", err)
		return response.Fail(c, http.StatusInternalServerError, response.ErrDBError, "Cannot read the racks")
	}
	return response.OK(c, map[string][]Rack{"racks": racks})
}

// PutRack writes one rack, normalised. A write older than the stored rack
// changes nothing and the stored rack is answered; the same time again is
// written. A write drops the deleted marks more than 30 days old by this
// node's clock — not by the write's own time, which is whatever the device
// that sent it believes: one whose clock runs months ahead would otherwise
// drop the mark of a rack deleted a minute ago.
//
//	PUT /api/v1/network/racks/:id {"doc":{…},"updated_at":"<RFC 3339>"}
func (h *Handler) PutRack(c echo.Context) error {
	id := c.Param("id")
	if !validRackID(id) {
		return invalidRack(c, "id", "The rack id is 1 to 64 of A-Z, a-z, 0-9, _ and -")
	}
	// One byte past the limit is enough to know the body is over it.
	raw, err := io.ReadAll(io.LimitReader(c.Request().Body, maxRackBody+1))
	if err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Cannot read the request body")
	}
	if len(raw) > maxRackBody {
		return invalidRack(c, "doc", "The request is larger than 64 KiB")
	}
	var req rackRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest,
			"Body must be {\"doc\": {…}, \"updated_at\": \"<RFC 3339>\"}")
	}
	if _, err := time.Parse(time.RFC3339, req.UpdatedAt); err != nil {
		return invalidRack(c, "updated_at", "updated_at must be an RFC 3339 time")
	}
	if req.Doc == nil {
		return invalidRack(c, "doc", "The request has no doc")
	}
	doc, bad := readRackDoc(*req.Doc)
	if bad != nil {
		return invalidRack(c, bad.field, bad.message)
	}

	// One write at a time, so that two new racks written at once cannot
	// both find room for one.
	h.racksMu.Lock()
	defer h.racksMu.Unlock()
	r, err := writeRack(h.DB, Rack{ID: id, Doc: doc, UpdatedAt: req.UpdatedAt}, h.clock())
	if errors.Is(err, errTooManyRacks) {
		return invalidRack(c, "racks", "A node keeps at most 32 racks that are not deleted")
	}
	if err != nil {
		slog.Warn("LAN rack not stored", "component", "lanscan", "id", id, "error", err)
		return response.Fail(c, http.StatusInternalServerError, response.ErrDBError, "Cannot store the rack")
	}
	return response.OK(c, r)
}

// rackRefusal is why a doc is refused: the field the page shows it on, and
// the message.
type rackRefusal struct {
	field, message string
}

// readRackDoc checks a doc and gives it as stored: strings trimmed, MACs
// lower case, each item with only its kind's field, and a deleted rack with
// no name and no items, its units as sent or 1. A deleted rack's name and
// items are dropped unread.
func readRackDoc(in rackDocIn) (RackDoc, *rackRefusal) {
	name := strings.TrimSpace(in.Name)
	if !in.Deleted && (name == "" || utf8.RuneCountInString(name) > maxNameLen || strings.IndexFunc(name, unicode.IsControl) >= 0) {
		return RackDoc{}, &rackRefusal{"name", "The name is 1 to 64 characters, on one line"}
	}
	units := 1
	if in.Units != nil || !in.Deleted {
		if in.Units == nil || !wholeIn(*in.Units, 1, maxRackUnits) {
			return RackDoc{}, &rackRefusal{"units", "units is a whole number from 1 to 60"}
		}
		units = int(*in.Units)
	}
	if in.Deleted {
		return RackDoc{Units: units, Deleted: true, Items: []RackItem{}}, nil
	}

	items := make([]RackItem, 0, len(in.Items))
	ids := map[string]bool{}
	// taken is, for each unit, the id of the item in it.
	taken := make([]string, units+1)
	for _, it := range in.Items {
		item, bad := readRackItem(it, units)
		if bad != nil {
			return RackDoc{}, bad
		}
		if ids[item.ID] {
			return RackDoc{}, &rackRefusal{"items", fmt.Sprintf("Item id %s is used twice", item.ID)}
		}
		ids[item.ID] = true
		for unit := item.U; unit < item.U+item.Height; unit++ {
			if other := taken[unit]; other != "" {
				return RackDoc{}, &rackRefusal{"items", fmt.Sprintf("Items %s and %s both take unit %d", other, item.ID, unit)}
			}
			taken[unit] = item.ID
		}
		items = append(items, item)
	}
	return RackDoc{Name: name, Units: units, Items: items}, nil
}

// readRackItem checks one item of a rack units tall and gives it as stored.
func readRackItem(in rackItemIn, units int) (RackItem, *rackRefusal) {
	id := strings.TrimSpace(in.ID)
	if !validRackID(id) {
		return RackItem{}, &rackRefusal{"items", "An item id is 1 to 64 of A-Z, a-z, 0-9, _ and -"}
	}
	if !wholeIn(in.U, 1, math.MaxFloat64) {
		return RackItem{}, &rackRefusal{"items", fmt.Sprintf("Item %s: u is a whole number of 1 or more", id)}
	}
	if !wholeIn(in.Height, 1, maxItemHeight) {
		return RackItem{}, &rackRefusal{"items", fmt.Sprintf("Item %s: height is a whole number from 1 to 8", id)}
	}
	// In floats: a u past what an int holds is only too high.
	if in.U+in.Height-1 > float64(units) {
		return RackItem{}, &rackRefusal{"items", fmt.Sprintf("Item %s reaches above the rack's %d units", id, units)}
	}
	item := RackItem{ID: id, U: int(in.U), Height: int(in.Height), Kind: strings.TrimSpace(in.Kind)}
	switch item.Kind {
	case "node":
		node := strings.TrimSpace(in.Node)
		if node == "" || utf8.RuneCountInString(node) > maxRackID || strings.IndexFunc(node, unicode.IsControl) >= 0 {
			return RackItem{}, &rackRefusal{"items", fmt.Sprintf("Item %s: node is 1 to 64 characters, on one line", id)}
		}
		item.Node = node
	case "device":
		mac, ok := parseDeviceMAC(strings.TrimSpace(in.MAC))
		if !ok {
			return RackItem{}, &rackRefusal{"items", fmt.Sprintf("Item %s: mac must be six bytes, as 02:42:ac:14:1e:1f", id)}
		}
		item.MAC = mac
	case "text":
		text := strings.TrimSpace(in.Text)
		if text == "" || utf8.RuneCountInString(text) > maxNameLen || strings.IndexFunc(text, unicode.IsControl) >= 0 {
			return RackItem{}, &rackRefusal{"items", fmt.Sprintf("Item %s: text is 1 to 64 characters, on one line", id)}
		}
		item.Text = text
	default:
		return RackItem{}, &rackRefusal{"items", fmt.Sprintf("Item %s: kind is node, device or text", id)}
	}
	return item, nil
}

// validRackID says whether s is 1 to 64 of A-Z, a-z, 0-9, _ and -.
func validRackID(s string) bool {
	if s == "" || len(s) > maxRackID {
		return false
	}
	for _, r := range s {
		if !rackIDChar(r) {
			return false
		}
	}
	return true
}

// rackIDChar says whether r may be in a rack's or an item's id.
func rackIDChar(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-'
}

// wholeIn says whether f is a whole number from lo to hi.
func wholeIn(f, lo, hi float64) bool {
	return f == math.Trunc(f) && f >= lo && f <= hi
}

func invalidRack(c echo.Context, field, message string) error {
	return response.FailWith(c, http.StatusBadRequest, response.ErrLanRackInvalid, message,
		map[string]string{"field": field})
}

// loadRacks is every rack kept, deleted marks included, by id. Never nil. A
// row whose doc does not read is left out (and logged) rather than failing
// the others.
func loadRacks(db querier) ([]Rack, error) {
	rows, err := db.Query(`SELECT id, doc, updated_at FROM lan_racks ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Rack{}
	for rows.Next() {
		var r Rack
		var raw string
		if err := rows.Scan(&r.ID, &raw, &r.UpdatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(raw), &r.Doc); err != nil {
			slog.Warn("stored LAN rack unreadable", "component", "lanscan", "id", r.ID, "error", err)
			continue
		}
		if r.Doc.Items == nil {
			r.Doc.Items = []RackItem{}
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// writeRack stores r, unless the rack stored under its id is newer: then it
// changes nothing and answers the stored one. A write that would leave more
// than maxRacks racks that are not deleted is errTooManyRacks. A write drops
// the deleted marks written more than rackMarkKeep before now, the node's
// clock; a mark whose time is ahead of the clock stays.
func writeRack(db *sql.DB, r Rack, now time.Time) (Rack, error) {
	tx, err := db.Begin()
	if err != nil {
		return Rack{}, err
	}
	defer func() { _ = tx.Rollback() }()
	stored, err := loadRacks(tx)
	if err != nil {
		return Rack{}, err
	}
	live := 0
	for _, s := range stored {
		if s.ID == r.ID {
			if newer(s.UpdatedAt, r.UpdatedAt) {
				return s, nil
			}
			continue
		}
		if !s.Doc.Deleted {
			live++
		}
	}
	if !r.Doc.Deleted && live >= maxRacks {
		return Rack{}, errTooManyRacks
	}
	doc, err := json.Marshal(r.Doc)
	if err != nil {
		return Rack{}, err
	}
	if _, err := tx.Exec(`INSERT INTO lan_racks (id, doc, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET doc = excluded.doc, updated_at = excluded.updated_at`,
		r.ID, string(doc), r.UpdatedAt); err != nil {
		return Rack{}, err
	}
	for _, s := range stored {
		if s.ID == r.ID || !s.Doc.Deleted {
			continue
		}
		at, err := time.Parse(time.RFC3339, s.UpdatedAt)
		if err != nil || now.Sub(at) <= rackMarkKeep {
			continue
		}
		if _, err := tx.Exec(`DELETE FROM lan_racks WHERE id = ?`, s.ID); err != nil {
			return Rack{}, err
		}
	}
	return r, tx.Commit()
}

package lanscan

import (
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/labstack/echo/v4"

	"github.com/svrforum/SFPanel/internal/api/response"
)

const (
	// maxNameLen and maxNoteLen are in characters, not bytes.
	maxNameLen = 64
	maxNoteLen = 500
)

// kinds are the device kinds a label may name; "" is none chosen.
var kinds = map[string]bool{
	"": true, "router": true, "switch": true, "ap": true, "server": true, "nas": true, "pc": true,
	"phone": true, "tv": true, "camera": true, "printer": true, "iot": true, "other": true,
}

type labelRequest struct {
	Name string `json:"name"`
	Note string `json:"note"`
	Kind string `json:"kind"`
	// LabelledAt, when given, is the label's time as the page set it, so
	// that every node it writes one label to stores the same time.
	LabelledAt *string `json:"labelled_at"`
}

// PutDevice writes a device's label: its name, note and kind. A MAC this
// node has never seen is added (another node saw it). A label older than
// the one stored changes nothing, and the stored device is answered.
//
//	PUT /api/v1/network/devices/:mac {"name","note","kind","labelled_at"?}
func (h *Handler) PutDevice(c echo.Context) error {
	mac, ok := deviceMAC(c)
	if !ok {
		return invalidDevice(c, "mac", "The MAC address must be six bytes, as 02:42:ac:14:1e:1f")
	}
	var req labelRequest
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest,
			"Body must be {\"name\": \"…\", \"note\": \"…\", \"kind\": \"…\"}")
	}
	name := strings.TrimSpace(req.Name)
	if utf8.RuneCountInString(name) > maxNameLen || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return invalidDevice(c, "name", "The name is at most 64 characters, on one line")
	}
	note := strings.TrimSpace(req.Note)
	if utf8.RuneCountInString(note) > maxNoteLen || strings.IndexFunc(note, controlInNote) >= 0 {
		return invalidDevice(c, "note", "The note is at most 500 characters")
	}
	if !kinds[req.Kind] {
		return invalidDevice(c, "kind", "Unknown device kind")
	}
	labelledAt := stamp(h.clock())
	if req.LabelledAt != nil {
		if _, err := time.Parse(time.RFC3339, *req.LabelledAt); err != nil {
			return invalidDevice(c, "labelled_at", "labelled_at must be an RFC 3339 time")
		}
		labelledAt = *req.LabelledAt
	}

	d, err := labelDevice(h.DB, mac, name, note, req.Kind, labelledAt)
	if err != nil {
		slog.Warn("device label not stored", "component", "lanscan", "mac", mac, "error", err)
		return response.Fail(c, http.StatusInternalServerError, response.ErrDBError, "Cannot store the label")
	}
	return response.OK(c, d)
}

// AckDevice marks a device as seen, so it is no longer shown as new.
//
//	POST /api/v1/network/devices/:mac/ack
func (h *Handler) AckDevice(c echo.Context) error {
	mac, ok := deviceMAC(c)
	if !ok {
		return invalidDevice(c, "mac", "The MAC address must be six bytes, as 02:42:ac:14:1e:1f")
	}
	found, err := ackDevice(h.DB, mac)
	if err != nil {
		slog.Warn("device not acknowledged", "component", "lanscan", "mac", mac, "error", err)
		return response.Fail(c, http.StatusInternalServerError, response.ErrDBError, "Cannot store the acknowledgement")
	}
	if !found {
		return response.Fail(c, http.StatusNotFound, response.ErrLanDeviceNotFound, "This node has no device with that MAC")
	}
	return response.OK(c, map[string]bool{"acked": true})
}

// DeleteDevice forgets a device, its label and the addresses it held. A
// device still on the network is found again by the next scan, unlabelled.
//
//	DELETE /api/v1/network/devices/:mac
func (h *Handler) DeleteDevice(c echo.Context) error {
	mac, ok := deviceMAC(c)
	if !ok {
		return invalidDevice(c, "mac", "The MAC address must be six bytes, as 02:42:ac:14:1e:1f")
	}
	deleted, err := deleteDevice(h.DB, mac)
	if err != nil {
		slog.Warn("device not deleted", "component", "lanscan", "mac", mac, "error", err)
		return response.Fail(c, http.StatusInternalServerError, response.ErrDBError, "Cannot delete the device")
	}
	return response.OK(c, map[string]bool{"deleted": deleted})
}

// deviceMAC is the route's :mac. Echo hands a path parameter back as the
// request spelled it, so a page that escapes the colons (02%3A42%3A…), as
// encodeURIComponent does, is unescaped first; a MAC holds no %.
func deviceMAC(c echo.Context) (string, bool) {
	s, err := url.PathUnescape(c.Param("mac"))
	if err != nil {
		return "", false
	}
	return parseDeviceMAC(s)
}

// parseDeviceMAC reads a MAC in any form net.ParseMAC takes, and answers it
// as the registry keys it: six bytes, lower case, colon-separated.
func parseDeviceMAC(s string) (string, bool) {
	hw, err := net.ParseMAC(s)
	if err != nil || len(hw) != 6 {
		return "", false
	}
	return hw.String(), true
}

// controlInNote is a control character other than the line breaks and tabs
// a note may hold.
func controlInNote(r rune) bool {
	return unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t'
}

func invalidDevice(c echo.Context, field, message string) error {
	return response.FailWith(c, http.StatusBadRequest, response.ErrLanDeviceInvalid, message,
		map[string]string{"field": field})
}

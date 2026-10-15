package alert

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	sfdb "github.com/svrforum/SFPanel/internal/db"
)

// pageContract is testdata/page_contract.json: what the Settings → Alerts page
// sends to these handlers and what it reads back. e2e/tests/alert-settings.spec.ts
// checks the page against the same file, so the two sides answer to one copy
// of the shape instead of each to its own idea of it. They had different ideas
// from the day the page shipped — it sent config as an object, CreateChannel
// bound a string — and adding a channel failed for every type until #116.
type pageContract struct {
	ChannelCreates []json.RawMessage `json:"channel_creates"`
	RuleRow        map[string]any    `json:"rule_row"`
}

func loadPageContract(t *testing.T) pageContract {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "page_contract.json"))
	if err != nil {
		t.Fatal(err)
	}
	var pc pageContract
	if err := json.Unmarshal(raw, &pc); err != nil {
		t.Fatalf("page_contract.json: %v", err)
	}
	if len(pc.ChannelCreates) == 0 {
		t.Fatal("page_contract.json has no channel_creates")
	}
	return pc
}

// openMigratedDB opens a database with the real schema, so a row shape the
// handlers depend on comes from the migrations rather than from a test's
// copy of them.
func openMigratedDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if err := sfdb.RunMigrations(db); err != nil {
		t.Fatalf("migrations: %v", err)
	}
	return db
}

// callJSON runs one handler with a JSON body, as the page's api client sends
// it, and an optional :id route parameter.
func callJSON(t *testing.T, fn echo.HandlerFunc, method, body string, id int) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "/", strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)
	if id > 0 {
		c.SetParamNames("id")
		c.SetParamValues(strconv.Itoa(id))
	}
	if err := fn(c); err != nil {
		t.Fatalf("handler returned %v", err)
	}
	return rec
}

// storedChannelConfig reads a channel's config column back the way the
// manager and TestChannel do: as the text of a JSON object.
func storedChannelConfig(t *testing.T, db *sql.DB, id int64) map[string]string {
	t.Helper()
	var raw string
	if err := db.QueryRow("SELECT config FROM alert_channels WHERE id=?", id).Scan(&raw); err != nil {
		t.Fatalf("channel %d: %v", id, err)
	}
	var cfg map[string]string
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatalf("channel %d config %s is not the JSON object the manager reads: %v", id, raw, err)
	}
	return cfg
}

func TestCreateChannel_AcceptsWhatThePageSends(t *testing.T) {
	h := &Handler{DB: openMigratedDB(t)}
	for _, body := range loadPageContract(t).ChannelCreates {
		var sent struct {
			Type   string            `json:"type"`
			Config map[string]string `json:"config"`
		}
		if err := json.Unmarshal(body, &sent); err != nil {
			t.Fatal(err)
		}
		t.Run(sent.Type, func(t *testing.T) {
			rec := callJSON(t, h.CreateChannel, http.MethodPost, string(body), 0)
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d, want 200: %s", rec.Code, rec.Body)
			}
			var env struct {
				Data struct {
					ID int64 `json:"id"`
				} `json:"data"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil || env.Data.ID == 0 {
				t.Fatalf("no channel id in %s (%v)", rec.Body, err)
			}
			if got := storedChannelConfig(t, h.DB, env.Data.ID); !reflect.DeepEqual(got, sent.Config) {
				t.Errorf("stored config %v, want %v", got, sent.Config)
			}
		})
	}
}

// TestListRules_RowIsWhatThePageReads pins the row ListRules returns to
// rule_row, which the e2e spec serves to the page as its rules list. The
// page's row type had a threshold field this handler never returned — the row
// carries the condition the rule was created with — so every host-metric rule
// listed itself as "Threshold: %".
func TestListRules_RowIsWhatThePageReads(t *testing.T) {
	db := openMigratedDB(t)
	if _, err := db.Exec(`INSERT INTO alert_rules
		(name, type, condition, channel_ids, severity, cooldown, node_scope, node_ids, enabled, created_at)
		VALUES ('cpu-high', 'cpu', '{"operator":">","threshold":90}', '[1]', 'warning', 300, 'all', '[]', 1, '2026-01-01 00:00:00')`); err != nil {
		t.Fatal(err)
	}
	rec := callJSON(t, (&Handler{DB: db}).ListRules, http.MethodGet, "", 0)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var env struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil || len(env.Data) != 1 {
		t.Fatalf("want one rule in %s (%v)", rec.Body, err)
	}
	if want := loadPageContract(t).RuleRow; !reflect.DeepEqual(env.Data[0], want) {
		got, _ := json.Marshal(env.Data[0])
		t.Errorf("ListRules row\n got %s\nwant the rule_row in testdata/page_contract.json, and the page reads that", got)
	}
}

package main

import (
	"reflect"
	"testing"
	"time"

	featureAlert "github.com/svrforum/SFPanel/internal/feature/alert"
	featureLanScan "github.com/svrforum/SFPanel/internal/feature/lanscan"
)

// Every field of a stored scan's event reaches the alert package: a field
// the adapter forgot would leave its rule quiet with nothing failing.
func TestLanAlertEventCarriesEveryField(t *testing.T) {
	at := time.Date(2026, 11, 22, 4, 0, 0, 0, time.UTC)
	got := lanAlertEvent(featureLanScan.ScanEvent{
		Network: "10.20.30.0/24", FinishedAt: at, Scheduled: true,
		New:  []featureLanScan.SeenDevice{{IP: "10.20.30.51", MAC: "02:42:ac:14:1e:33", Vendor: "Espressif", Hostname: "plug.lan"}},
		Down: []featureLanScan.KnownDevice{{MAC: "02:42:ac:14:1e:32", Name: "Front door", IP: "10.20.30.50", Vendor: "Example Electronics", Hostname: "door.lan", LastSeen: "2026-11-22T02:13:08Z"}},
		Back: []featureLanScan.KnownDevice{{MAC: "02:42:ac:14:1e:37", Name: "Garage", IP: "10.20.30.55", Vendor: "Example Electronics", Hostname: "garage.lan", LastSeen: "2026-11-22T04:00:00Z"}},
		Conflicts: []featureLanScan.AddressConflict{
			{IP: "10.20.30.12", MACs: []string{"02:42:ac:14:1e:4d"}, Vendor: "Example Electronics", Certain: true},
			{IP: "10.20.30.13", MACs: []string{"02:42:ac:14:1e:0d", "02:42:ac:14:1e:4f"}},
		},
	})
	want := &featureAlert.AlertLanEvent{
		Network: "10.20.30.0/24", FinishedAt: at, Scheduled: true,
		New:  []featureAlert.LanSeenDevice{{IP: "10.20.30.51", MAC: "02:42:ac:14:1e:33", Vendor: "Espressif", Hostname: "plug.lan"}},
		Down: []featureAlert.LanKnownDevice{{MAC: "02:42:ac:14:1e:32", Name: "Front door", IP: "10.20.30.50", Vendor: "Example Electronics", Hostname: "door.lan", LastSeen: "2026-11-22T02:13:08Z"}},
		Back: []featureAlert.LanKnownDevice{{MAC: "02:42:ac:14:1e:37", Name: "Garage", IP: "10.20.30.55", Vendor: "Example Electronics", Hostname: "garage.lan", LastSeen: "2026-11-22T04:00:00Z"}},
		Conflicts: []featureAlert.LanAddressConflict{
			{IP: "10.20.30.12", MACs: []string{"02:42:ac:14:1e:4d"}, Vendor: "Example Electronics", Certain: true},
			{IP: "10.20.30.13", MACs: []string{"02:42:ac:14:1e:0d", "02:42:ac:14:1e:4f"}},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("lanAlertEvent\n got %+v\nwant %+v", got, want)
	}
	// The two packages' types match field for field, so a field added to one
	// side is a field this adapter has to carry.
	for _, pair := range [][2]any{
		{featureLanScan.ScanEvent{}, featureAlert.AlertLanEvent{}},
		{featureLanScan.SeenDevice{}, featureAlert.LanSeenDevice{}},
		{featureLanScan.KnownDevice{}, featureAlert.LanKnownDevice{}},
		{featureLanScan.AddressConflict{}, featureAlert.LanAddressConflict{}},
	} {
		a, b := reflect.TypeOf(pair[0]), reflect.TypeOf(pair[1])
		if a.NumField() != b.NumField() {
			t.Errorf("%s has %d fields and %s %d", a, a.NumField(), b, b.NumField())
			continue
		}
		for i := 0; i < a.NumField(); i++ {
			if a.Field(i).Name != b.Field(i).Name {
				t.Errorf("%s field %d is %s, and %s's is %s", a, i, a.Field(i).Name, b, b.Field(i).Name)
			}
		}
	}
}

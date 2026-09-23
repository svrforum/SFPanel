package featurecluster

import "testing"

func TestParseUpdateAvailable(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		body    string
		want    bool
		wantErr bool
	}{
		{"newer release", 200, `{"success":true,"data":{"current_version":"0.77.4","latest_version":"0.77.5","update_available":true}}`, true, false},
		{"already current", 200, `{"success":true,"data":{"current_version":"0.77.5","latest_version":"0.77.5","update_available":false}}`, false, false},
		// An unknown must not read as "no update needed" by accident: the
		// caller treats it as a reason to leave the leader alone, and it has
		// to be able to tell the two apart in its log line.
		{"github down", 502, `{"success":false,"error":{"code":"UPDATE_FAILED"}}`, false, true},
		{"field missing", 200, `{"success":true,"data":{}}`, false, true},
		{"not json", 200, `<html>`, false, true},
	}
	for _, c := range cases {
		got, err := parseUpdateAvailable(c.status, []byte(c.body))
		if (err != nil) != c.wantErr || got != c.want {
			t.Errorf("%s: got (%v, %v), want (%v, err=%v)", c.name, got, err, c.want, c.wantErr)
		}
	}
}

package main

import "testing"

// The watchdog rolls the binary AND the database back when its probe fails for
// the whole grace window. An upgrade that flips the panel between HTTP and
// HTTPS hands the probe a URL for the old scheme, so the fallback is what keeps
// a healthy upgrade from being reverted.
func TestOtherSchemeURL(t *testing.T) {
	cases := map[string]string{
		"http://127.0.0.1:3628/api/v1/system/info":  "https://127.0.0.1:3628/api/v1/system/info",
		"https://127.0.0.1:3628/api/v1/system/info": "http://127.0.0.1:3628/api/v1/system/info",
		"http://127.0.0.1:19443/x":                  "https://127.0.0.1:19443/x",
		"ftp://127.0.0.1/":                          "",
		"":                                          "",
		"127.0.0.1:3628":                            "",
	}
	for in, want := range cases {
		if got := otherSchemeURL(in); got != want {
			t.Errorf("otherSchemeURL(%q) = %q, want %q", in, got, want)
		}
	}
}

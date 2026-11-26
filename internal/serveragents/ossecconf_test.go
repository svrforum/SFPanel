package serveragents

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
)

func ossecFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func mustReadOssec(t *testing.T, src []byte) OssecConf {
	t.Helper()
	c, err := ReadOssecConf(src)
	if err != nil {
		t.Fatalf("ReadOssecConf: %v", err)
	}
	return c
}

func mustEditOssec(t *testing.T, src []byte, manager, groups string) []byte {
	t.Helper()
	out, changed, err := EditOssecConf(src, manager, groups)
	if err != nil {
		t.Fatalf("EditOssecConf: %v", err)
	}
	if !changed {
		t.Fatal("changed = false, want true")
	}
	return out
}

func TestReadOssecConfReadsTheClient(t *testing.T) {
	for _, name := range []string{"ossec.conf", "ossec-no-enrollment.conf"} {
		c := mustReadOssec(t, ossecFixture(t, name))
		if !slices.Equal(c.Servers, []string{OssecPlaceholder}) {
			t.Fatalf("%s: Servers = %q, want the package's placeholder", name, c.Servers)
		}
		if c.EnrollmentManager != "" || c.Groups != "" {
			t.Fatalf("%s: EnrollmentManager %q, Groups %q; want both empty", name, c.EnrollmentManager, c.Groups)
		}
	}
}

func TestEditSetsTheAddressAndEnrollment(t *testing.T) {
	in := ossecFixture(t, "ossec.conf")
	out := mustEditOssec(t, in, "198.51.100.5", "")

	c := mustReadOssec(t, out)
	if !slices.Equal(c.Servers, []string{"198.51.100.5"}) || c.EnrollmentManager != "198.51.100.5" || c.Groups != "default" {
		t.Fatalf("read back %+v, want the manager in both places and the default group", c)
	}

	want := strings.Replace(string(in), "<address>MANAGER_IP</address>", "<address>198.51.100.5</address>", 1)
	want = strings.Replace(want, "      <enabled>yes</enabled>\n    </enrollment>",
		"      <enabled>yes</enabled>\n"+
			"      <manager_address>198.51.100.5</manager_address>\n"+
			"      <groups>default</groups>\n"+
			"    </enrollment>", 1)
	if string(out) != want {
		t.Fatalf("out =\n%s\nwant =\n%s", out, want)
	}

	syscheck := func(b []byte) string {
		s := string(b)
		return s[strings.Index(s, "<syscheck>") : strings.Index(s, "</syscheck>")+len("</syscheck>")]
	}
	if syscheck(out) != syscheck(in) {
		t.Fatal("the <syscheck> section changed")
	}
	beforeClient := func(b []byte) string { return string(b[:bytes.Index(b, []byte("  <client>"))]) }
	if beforeClient(out) != beforeClient(in) {
		t.Fatal("the lines before <client> changed")
	}
}

func TestEnrollmentIsAddedWhenAbsent(t *testing.T) {
	out := mustEditOssec(t, ossecFixture(t, "ossec-no-enrollment.conf"), "198.51.100.5", "linux")
	want := "    <crypto_method>aes</crypto_method>\n" +
		"    <enrollment>\n" +
		"      <enabled>yes</enabled>\n" +
		"      <manager_address>198.51.100.5</manager_address>\n" +
		"      <groups>linux</groups>\n" +
		"    </enrollment>\n" +
		"  </client>"
	if !strings.Contains(string(out), want) {
		t.Fatalf("out =\n%s\nwant it to end <client> with\n%s", out, want)
	}
}

func TestACommentedOutAddressIsLeftAlone(t *testing.T) {
	const oldServer = "<!-- <server><address>192.0.2.18</address></server> -->"
	const oldAddress = "<!-- <address>192.0.2.19</address> -->"
	in := []byte("<ossec_config>\n  <client>\n    " + oldServer + "\n" +
		"    <server>" + oldAddress + "<address>192.0.2.20</address><port>1514</port></server>\n" +
		"  </client>\n</ossec_config>\n")

	if c := mustReadOssec(t, in); !slices.Equal(c.Servers, []string{"192.0.2.20"}) {
		t.Fatalf("Servers = %q, want only the address outside the comments", c.Servers)
	}
	out := mustEditOssec(t, in, "198.51.100.5", "")
	if !bytes.Contains(out, []byte("    "+oldServer+"\n")) {
		t.Fatalf("the commented-out server changed:\n%s", out)
	}
	if !bytes.Contains(out, []byte("<server>"+oldAddress+"<address>198.51.100.5</address><port>1514</port></server>")) {
		t.Fatalf("want the comment as it was and the real address replaced:\n%s", out)
	}
}

func TestSeveralManagersAreRefused(t *testing.T) {
	in := []byte("<ossec_config>\n  <client>\n" +
		"    <server>\n      <address>192.0.2.10</address>\n    </server>\n" +
		"    <server>\n      <address>192.0.2.11</address>\n    </server>\n" +
		"  </client>\n</ossec_config>\n")
	out, changed, err := EditOssecConf(in, "198.51.100.5", "")
	if !errors.Is(err, ErrOssecManyServers) || out != nil || changed {
		t.Fatalf("out %q, changed %v, err %v; want nil, false, ErrOssecManyServers", out, changed, err)
	}
	if c := mustReadOssec(t, in); !slices.Equal(c.Servers, []string{"192.0.2.10", "192.0.2.11"}) {
		t.Fatalf("Servers = %q, want both managers in order", c.Servers)
	}
}

func TestEditingToTheSameValuesChangesNothing(t *testing.T) {
	for _, name := range []string{"ossec.conf", "ossec-no-enrollment.conf"} {
		for _, groups := range []string{"", "linux,web"} {
			once := mustEditOssec(t, ossecFixture(t, name), "198.51.100.5", groups)
			again, changed, err := EditOssecConf(once, "198.51.100.5", groups)
			if err != nil || changed || !bytes.Equal(again, once) {
				t.Fatalf("%s, groups %q: second edit changed %v, err %v, out equal %v; want nothing changed",
					name, groups, changed, err, bytes.Equal(again, once))
			}
		}
	}

	spaced := []byte("<ossec_config>\n  <client>\n    <server>\n      <address> 198.51.100.5 </address>\n    </server>\n" +
		"    <enrollment>\n      <manager_address>198.51.100.5</manager_address>\n      <groups>default</groups>\n    </enrollment>\n" +
		"  </client>\n</ossec_config>\n")
	if out, changed, err := EditOssecConf(spaced, "198.51.100.5", ""); err != nil || changed || !bytes.Equal(out, spaced) {
		t.Fatalf("changed %v, err %v; want the address with spaces around it taken as already set", changed, err)
	}
}

func TestNotXMLIsRefused(t *testing.T) {
	fixture := ossecFixture(t, "ossec.conf")
	cases := map[string][]byte{
		"cut in half":                    fixture[:len(fixture)/2],
		"cut right after an end tag":     fixture[:bytes.Index(fixture, []byte("</client>"))+len("</client>")],
		"an end tag that does not match": []byte("<ossec_config><client><server></client></server></ossec_config>"),
		"a stray end tag":                []byte("<ossec_config><client></client></ossec_config></ossec_config>"),
		"a bare ampersand":               []byte("<ossec_config><client><server><address>a&b</address></server></client></ossec_config>"),
	}
	for name, in := range cases {
		if _, err := ReadOssecConf(in); !errors.Is(err, ErrOssecUnreadable) {
			t.Errorf("%s: ReadOssecConf err = %v, want ErrOssecUnreadable", name, err)
		}
		if out, changed, err := EditOssecConf(in, "198.51.100.5", ""); !errors.Is(err, ErrOssecUnreadable) || out != nil || changed {
			t.Errorf("%s: EditOssecConf out %q, changed %v, err %v; want nil, false, ErrOssecUnreadable", name, out, changed, err)
		}
	}
}

func TestNoClientIsRefused(t *testing.T) {
	for _, in := range []string{
		"<ossec_config><syscheck/></ossec_config>",
		"<ossec_config><wodle><client><server><address>192.0.2.10</address></server></client></wodle></ossec_config>",
		"<client><server><address>192.0.2.10</address></server></client>",
		"<agent_config><client><server><address>192.0.2.10</address></server></client></agent_config>",
	} {
		if _, err := ReadOssecConf([]byte(in)); !errors.Is(err, ErrOssecNoClient) {
			t.Errorf("%s: ReadOssecConf err = %v, want ErrOssecNoClient", in, err)
		}
		if out, changed, err := EditOssecConf([]byte(in), "198.51.100.5", ""); !errors.Is(err, ErrOssecNoClient) || out != nil || changed {
			t.Errorf("%s: EditOssecConf out %q, changed %v, err %v; want nil, false, ErrOssecNoClient", in, out, changed, err)
		}
	}
}

func TestValuesAreEscaped(t *testing.T) {
	out := mustEditOssec(t, ossecFixture(t, "ossec.conf"), "x<y", "a&b")
	for _, want := range []string{
		"<address>x&lt;y</address>",
		"<manager_address>x&lt;y</manager_address>",
		"<groups>a&amp;b</groups>",
	} {
		if !bytes.Contains(out, []byte(want)) {
			t.Fatalf("out does not contain %q:\n%s", want, out)
		}
	}
	if c := mustReadOssec(t, out); !slices.Equal(c.Servers, []string{"x<y"}) || c.Groups != "a&b" {
		t.Fatalf("read back %+v, want the values as given", c)
	}
}

func TestOnlyTheFirstClientIsUsed(t *testing.T) {
	in := []byte("<ossec_config>\n  <syscheck>\n    <disabled>no</disabled>\n  </syscheck>\n</ossec_config>\n\n" +
		"<ossec_config>\n  <client>\n    <server>\n      <address>192.0.2.10</address>\n    </server>\n  </client>\n</ossec_config>\n\n" +
		"<ossec_config>\n  <client>\n    <server>\n      <address>192.0.2.11</address>\n    </server>\n  </client>\n</ossec_config>\n")
	if c := mustReadOssec(t, in); !slices.Equal(c.Servers, []string{"192.0.2.10"}) {
		t.Fatalf("Servers = %q, want the first <client>'s only", c.Servers)
	}
	out := mustEditOssec(t, in, "198.51.100.5", "")
	want := strings.Replace(string(in), "<address>192.0.2.10</address>\n    </server>\n",
		"<address>198.51.100.5</address>\n    </server>\n"+
			"    <enrollment>\n      <enabled>yes</enabled>\n      <manager_address>198.51.100.5</manager_address>\n"+
			"      <groups>default</groups>\n    </enrollment>\n", 1)
	if string(out) != want {
		t.Fatalf("out =\n%s\nwant =\n%s", out, want)
	}
}

func TestAServerIsAddedWhenAbsent(t *testing.T) {
	in := []byte("<ossec_config>\n  <client>\n    <enrollment>\n      <enabled>yes</enabled>\n    </enrollment>\n  </client>\n</ossec_config>\n")
	c := mustReadOssec(t, in)
	if b, _ := json.Marshal(c); !bytes.Contains(b, []byte(`"servers":[]`)) {
		t.Fatalf("JSON %s, want an empty servers list, not null", b)
	}
	out := mustEditOssec(t, in, "198.51.100.5", "")
	want := "<ossec_config>\n  <client>\n    <enrollment>\n      <enabled>yes</enabled>\n" +
		"      <manager_address>198.51.100.5</manager_address>\n      <groups>default</groups>\n    </enrollment>\n" +
		"    <server>\n      <address>198.51.100.5</address>\n    </server>\n" +
		"  </client>\n</ossec_config>\n"
	if string(out) != want {
		t.Fatalf("out =\n%s\nwant =\n%s", out, want)
	}

	empty := []byte("<ossec_config>\n  <client>\n  </client>\n</ossec_config>\n")
	want = "<ossec_config>\n  <client>\n" +
		"    <server>\n      <address>198.51.100.5</address>\n    </server>\n" +
		"    <enrollment>\n      <enabled>yes</enabled>\n      <manager_address>198.51.100.5</manager_address>\n" +
		"      <groups>default</groups>\n    </enrollment>\n" +
		"  </client>\n</ossec_config>\n"
	if out := mustEditOssec(t, empty, "198.51.100.5", ""); string(out) != want {
		t.Fatalf("out =\n%s\nwant =\n%s", out, want)
	}
}

func TestEditFollowsTheFilesLayout(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{
			name: "tab indents",
			in:   "<ossec_config>\n\t<client>\n\t\t<server>\n\t\t\t<address>MANAGER_IP</address>\n\t\t</server>\n\t</client>\n</ossec_config>\n",
			want: "<ossec_config>\n\t<client>\n\t\t<server>\n\t\t\t<address>198.51.100.5</address>\n\t\t</server>\n" +
				"\t\t<enrollment>\n\t\t\t<enabled>yes</enabled>\n\t\t\t<manager_address>198.51.100.5</manager_address>\n" +
				"\t\t\t<groups>default</groups>\n\t\t</enrollment>\n\t</client>\n</ossec_config>\n",
		},
		{
			name: "CRLF line ends",
			in:   "<ossec_config>\r\n  <client>\r\n    <server>\r\n      <address>MANAGER_IP</address>\r\n    </server>\r\n  </client>\r\n</ossec_config>\r\n",
			want: "<ossec_config>\r\n  <client>\r\n    <server>\r\n      <address>198.51.100.5</address>\r\n    </server>\r\n" +
				"    <enrollment>\r\n      <enabled>yes</enabled>\r\n      <manager_address>198.51.100.5</manager_address>\r\n" +
				"      <groups>default</groups>\r\n    </enrollment>\r\n  </client>\r\n</ossec_config>\r\n",
		},
		{
			name: "one line",
			in:   "<ossec_config><client><server><address>MANAGER_IP</address></server></client></ossec_config>",
			want: "<ossec_config><client><server><address>198.51.100.5</address></server>" +
				"<enrollment><enabled>yes</enabled><manager_address>198.51.100.5</manager_address><groups>default</groups></enrollment>" +
				"</client></ossec_config>",
		},
		{
			name: "empty elements",
			in:   "<ossec_config>\n  <client>\n    <server>\n      <address/>\n    </server>\n    <enrollment />\n  </client>\n</ossec_config>\n",
			want: "<ossec_config>\n  <client>\n    <server>\n      <address>198.51.100.5</address>\n    </server>\n" +
				"    <enrollment><manager_address>198.51.100.5</manager_address><groups>default</groups></enrollment>\n" +
				"  </client>\n</ossec_config>\n",
		},
		{
			name: "comments and white space inside the values",
			in: "<ossec_config><client><server><address>\n  192.0.2<!-- was 192.0.2.19 -->.20\n</address></server>" +
				"<enrollment><manager_address><!-- none yet --></manager_address><groups> </groups></enrollment></client></ossec_config>",
			want: "<ossec_config><client><server><address>\n  198.51.100.5<!-- was 192.0.2.19 -->\n</address></server>" +
				"<enrollment><manager_address>198.51.100.5<!-- none yet --></manager_address><groups>default</groups></enrollment></client></ossec_config>",
		},
		{
			name: "a server without an address",
			in:   "<ossec_config>\n  <client>\n    <server>\n      <port>1514</port>\n    </server>\n  </client>\n</ossec_config>\n",
			want: "<ossec_config>\n  <client>\n    <server>\n      <port>1514</port>\n      <address>198.51.100.5</address>\n    </server>\n" +
				"    <enrollment>\n      <enabled>yes</enabled>\n      <manager_address>198.51.100.5</manager_address>\n" +
				"      <groups>default</groups>\n    </enrollment>\n  </client>\n</ossec_config>\n",
		},
		{
			name: "an enrollment naming another manager, left disabled",
			in: "<ossec_config>\n  <client>\n    <server>\n      <address>192.0.2.19</address>\n    </server>\n" +
				"    <enrollment>\n      <enabled>no</enabled>\n      <manager_address>192.0.2.19</manager_address>\n" +
				"      <groups>web</groups>\n    </enrollment>\n  </client>\n</ossec_config>\n",
			want: "<ossec_config>\n  <client>\n    <server>\n      <address>198.51.100.5</address>\n    </server>\n" +
				"    <enrollment>\n      <enabled>no</enabled>\n      <manager_address>198.51.100.5</manager_address>\n" +
				"      <groups>default</groups>\n    </enrollment>\n  </client>\n</ossec_config>\n",
		},
	}
	for _, tc := range cases {
		out, changed, err := EditOssecConf([]byte(tc.in), "198.51.100.5", "")
		if err != nil || !changed {
			t.Fatalf("%s: changed %v, err %v", tc.name, changed, err)
		}
		if string(out) != tc.want {
			t.Errorf("%s: out =\n%q\nwant =\n%q", tc.name, out, tc.want)
		}
	}
}

func TestEnableOssecEnrollment(t *testing.T) {
	on := ossecFixture(t, "ossec.conf")
	off := bytes.Replace(on, []byte("<enabled>yes</enabled>"), []byte("<enabled> no </enabled>"), 1)
	if bytes.Equal(off, on) {
		t.Fatal("fixture has no <enabled>yes</enabled> to turn off")
	}

	out, was, err := EnableOssecEnrollment(off)
	if err != nil || was != "no" {
		t.Fatalf("EnableOssecEnrollment = was %q, %v; want was no", was, err)
	}
	if want := bytes.Replace(off, []byte("<enabled> no </enabled>"), []byte("<enabled> yes </enabled>"), 1); !bytes.Equal(out, want) {
		t.Errorf("out differs from the file with only <enrollment>'s <enabled> said yes:\n%s", out)
	}

	for name, src := range map[string][]byte{
		"already on":      on,
		"no <enabled>":    bytes.Replace(on, []byte("<enabled>yes</enabled>"), nil, 1),
		"no <enrollment>": []byte("<ossec_config>\n  <client>\n    <server><address>192.0.2.10</address></server>\n  </client>\n</ossec_config>\n"),
	} {
		out, was, err := EnableOssecEnrollment(src)
		if err != nil || was != "" || !bytes.Equal(out, src) {
			t.Errorf("%s: EnableOssecEnrollment = was %q, %v, changed %v; want it left as it is", name, was, err, !bytes.Equal(out, src))
		}
	}
	if _, _, err := EnableOssecEnrollment([]byte("<ossec_config><client>")); !errors.Is(err, ErrOssecUnreadable) {
		t.Errorf("unreadable: err = %v, want ErrOssecUnreadable", err)
	}
}

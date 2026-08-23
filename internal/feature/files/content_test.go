package files

import (
	"strings"
	"testing"
)

func TestKindForName(t *testing.T) {
	cases := []struct {
		name  string
		isDir bool
		want  Kind
	}{
		{"stacks", true, KindDir},
		{"docker-compose.yml", false, KindText},
		{"nginx.conf", false, KindText},
		{"Makefile", false, KindText},
		{"screenshot.PNG", false, KindImage},
		{"photo.jpeg", false, KindImage},
		{"sfpanel.db", false, KindBinary},
		{"backup.tar.gz", false, KindBinary},
		{"app.wasm", false, KindBinary},
		// Script-capable, so it must NOT be rendered inline as an image — it
		// would execute in the panel's own origin. Text is both safe and what
		// an operator wants to do with one.
		{"logo.svg", false, KindText},
	}
	for _, tc := range cases {
		if got := KindForName(tc.name, tc.isDir); got != tc.want {
			t.Errorf("KindForName(%q) = %s, want %s", tc.name, got, tc.want)
		}
	}
}

func TestKindForContent(t *testing.T) {
	cases := []struct {
		desc string
		name string
		data []byte
		want Kind
	}{
		{"plain ascii", "a.txt", []byte("hello world\n"), KindText},
		{"empty file", "a.txt", nil, KindText},
		{"utf-8 korean", "a.txt", []byte("안녕하세요 서버 관리자입니다\n"), KindText},
		{"utf-8 emoji", "a.txt", []byte("deploy 🚀 done\n"), KindText},
		// The case that corrupted files: a NUL inside the first bytes.
		{"elf header", "a.out", []byte{0x7f, 'E', 'L', 'F', 0x02, 0x01, 0x01, 0x00}, KindBinary},
		{"sqlite header", "x.dat", append([]byte("SQLite format 3"), 0x00), KindBinary},
		{"png bytes", "x.dat", []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}, KindBinary},
		// Latin-1: invalid UTF-8, so json.Marshal would replace it with U+FFFD
		// and a save would write that back. Refusing the editor is the point.
		{"latin-1 bytes", "a.txt", []byte{0x48, 0xe9, 0x6c, 0x6c, 0x6f}, KindBinary},
		// Extension wins for images so a preview is offered without a sniff.
		{"image by extension", "a.png", []byte("not really a png"), KindImage},
	}
	for _, tc := range cases {
		if got := KindForContent(tc.name, tc.data); got != tc.want {
			t.Errorf("%s: KindForContent(%q) = %s, want %s", tc.desc, tc.name, got, tc.want)
		}
	}
}

// Truncating the sniff window mid-character makes utf8.Valid fail on a
// perfectly good file. Any Korean, Japanese or emoji content is long enough to
// land a multi-byte rune on the boundary eventually, and misreporting it as
// binary sends the operator to a download prompt instead of the editor.
func TestKindForContentHandlesRuneBoundary(t *testing.T) {
	// Build content whose sniffLen-th byte falls inside a 3-byte rune.
	for offset := 0; offset < 3; offset++ {
		body := strings.Repeat("a", offset) + strings.Repeat("한", sniffLen)
		if got := KindForContent("notes.txt", []byte(body)); got != KindText {
			t.Errorf("offset %d: multi-byte content classified %s, want text", offset, got)
		}
	}
}

// A NUL beyond the sniff window is not found, and that is accepted: the check
// is a cheap guard against the common case, not a full scan of a 5 MB file.
// This documents the limit rather than pretending it does not exist.
func TestKindForContentOnlyInspectsTheHead(t *testing.T) {
	body := append([]byte(strings.Repeat("a", sniffLen+10)), 0x00)
	if got := KindForContent("a.txt", body); got != KindText {
		t.Errorf("got %s; the sniff window is documented as head-only", got)
	}
}

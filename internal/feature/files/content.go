package files

import (
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// Kind classifies what a file is, so the UI can route it to the right viewer
// instead of assuming everything that is not a directory is editable text.
//
// This exists because the file manager opened Monaco for every non-directory.
// A PNG or a SQLite file under the read cap came back as a string full of
// replacement characters, rendered as mojibake — and pressing Save wrote those
// replacement characters over the original. That is a total, silent loss of the
// file, produced by two clicks and no warning.
type Kind string

const (
	KindDir    Kind = "dir"
	KindText   Kind = "text"
	KindImage  Kind = "image"
	KindBinary Kind = "binary"
)

// imageExtensions are the formats a browser can render inline from a data URL
// or a download endpoint. SVG is deliberately absent: it is script-capable, and
// rendering one from an arbitrary path would run its contents in the panel's
// origin. SVGs classify as text and open in the editor, which is both safe and
// what an operator wants anyway.
var imageExtensions = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true,
	".webp": true, ".bmp": true, ".ico": true, ".avif": true,
}

// binaryExtensions are formats no one edits as text. The sniff below would
// catch most of them anyway; naming them means a listing can classify without
// opening every file.
var binaryExtensions = map[string]bool{
	".zip": true, ".gz": true, ".tgz": true, ".bz2": true, ".xz": true, ".zst": true,
	".tar": true, ".7z": true, ".rar": true,
	".db": true, ".sqlite": true, ".sqlite3": true,
	".pdf": true, ".mp3": true, ".mp4": true, ".mkv": true, ".mov": true, ".webm": true,
	".woff": true, ".woff2": true, ".ttf": true, ".otf": true, ".eot": true,
	".so": true, ".dylib": true, ".dll": true, ".exe": true, ".bin": true, ".o": true, ".a": true,
	".class": true, ".jar": true, ".pyc": true, ".wasm": true,
	".img": true, ".iso": true, ".qcow2": true, ".vmdk": true,
}

// KindForName classifies by extension alone.
//
// Used for directory listings, where opening thousands of files to sniff them
// would turn one request into thousands of syscalls. It is a hint: ReadFile
// sniffs the actual bytes and is the authority.
func KindForName(name string, isDir bool) Kind {
	if isDir {
		return KindDir
	}
	ext := strings.ToLower(filepath.Ext(name))
	switch {
	case imageExtensions[ext]:
		return KindImage
	case binaryExtensions[ext]:
		return KindBinary
	default:
		return KindText
	}
}

// sniffLen is how much of a file KindForContent inspects. Enough to catch a
// header and any early NUL, short enough to stay a single read.
const sniffLen = 8192

// KindForContent classifies by looking at the bytes, which is what actually
// decides whether the editor can be trusted with a file.
//
// Two rules, in order of how often they fire:
//
//   - A NUL byte means binary. No text encoding a text editor handles contains
//     one, and every executable, archive and database has one within the first
//     few hundred bytes.
//   - Invalid UTF-8 means binary. Go's json.Marshal replaces invalid bytes with
//     U+FFFD rather than failing, so content that survives to the browser has
//     already been corrupted — the round trip cannot be lossless, and saving it
//     back would write the corruption to disk.
//
// Legacy single-byte encodings (Latin-1, EUC-KR) fail the second rule and are
// classified binary. That is the intended trade: refusing to edit a file the
// panel cannot round-trip is better than silently rewriting it as U+FFFD.
func KindForContent(name string, data []byte) Kind {
	if len(data) > sniffLen {
		data = data[:sniffLen]
		// Back off to a rune boundary. Cutting mid-character makes utf8.Valid
		// fail on a perfectly good file — any Korean, Japanese or emoji content
		// would land on a multi-byte rune sooner or later and be misreported as
		// binary, which sends it to a download prompt instead of the editor.
		for len(data) > 0 && !utf8.Valid(data) {
			if r, size := utf8.DecodeLastRune(data); r == utf8.RuneError && size <= 1 {
				data = data[:len(data)-1]
				continue
			}
			break
		}
	}
	ext := strings.ToLower(filepath.Ext(name))
	if imageExtensions[ext] {
		return KindImage
	}
	for _, b := range data {
		if b == 0 {
			return KindBinary
		}
	}
	if !utf8.Valid(data) {
		return KindBinary
	}
	return KindText
}

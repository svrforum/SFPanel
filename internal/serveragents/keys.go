package serveragents

import (
	"bufio"
	"bytes"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// The vendors' apt signing keys, pinned by fingerprint, and where each vendor
// publishes the file that holds it.
const (
	ZabbixKeyFingerprint = "4C3D6F2CC75F5146754FC374D913219AB5333005"
	WazuhKeyFingerprint  = "0DCFCA5547B19D2A6099506096B3EE5F29111145"
	ZabbixKeyURL         = "https://repo.zabbix.com/zabbix-official-repo.key"
	WazuhKeyURL          = "https://packages.wazuh.com/key/GPG-KEY-WAZUH"
)

var (
	ErrKeyNotFound   = errors.New("no key in the file has the pinned fingerprint")
	ErrKeyUnreadable = errors.New("the file is not an OpenPGP public key")
)

const (
	armorBegin = "-----BEGIN PGP PUBLIC KEY BLOCK-----"
	armorEnd   = "-----END PGP PUBLIC KEY BLOCK-----"

	tagSecretKey    = 5
	tagPublicKey    = 6
	tagSecretSubkey = 7
)

// SelectKey returns, as a binary keyring, the one transferable public key in
// file (armored or binary) whose v4 fingerprint is fingerprint. The key comes
// back as the file's own packets: the primary key and everything up to the
// next primary key — its user IDs, signatures and subkeys. Spaces and case in
// fingerprint do not matter; a key ID or any other part of it matches nothing.
//
// A vendor may publish several keys in one file; trusting only the pinned one
// keeps the others from signing for the repository. A file holding any secret
// key material is refused whole.
func SelectKey(file []byte, fingerprint string) ([]byte, error) {
	keyring, err := dearmor(file)
	if err != nil {
		return nil, err
	}
	keys, err := splitKeys(keyring)
	if err != nil {
		return nil, err
	}
	want := strings.ToUpper(strings.Join(strings.Fields(fingerprint), ""))
	for _, k := range keys {
		if k.fingerprint != "" && k.fingerprint == want {
			return k.packets, nil
		}
	}
	return nil, ErrKeyNotFound
}

// keyFingerprints returns the fingerprints of the primary keys in a binary
// keyring, in order; a key that is not version 4 has an empty one.
func keyFingerprints(keyring []byte) ([]string, error) {
	keys, err := splitKeys(keyring)
	if err != nil {
		return nil, err
	}
	fps := make([]string, len(keys))
	for i, k := range keys {
		fps[i] = k.fingerprint
	}
	return fps, nil
}

// dearmor decodes every public key block in an ASCII-armored file and joins
// them; a file without an armor header is taken to be binary already. The
// armor checksum is not checked: the fingerprint is what pins the key.
func dearmor(file []byte) ([]byte, error) {
	if !bytes.Contains(file, []byte(armorBegin)) {
		return file, nil
	}
	const (
		outside = iota
		headers
		body
		checksum
	)
	var out []byte
	var b64 strings.Builder
	state := outside
	sc := bufio.NewScanner(bytes.NewReader(file))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch state {
		case outside:
			if line == armorBegin {
				state = headers
			}
		case headers:
			if line == "" {
				state = body
			}
		case body, checksum:
			switch {
			case strings.HasPrefix(line, armorEnd):
				dec, err := base64.StdEncoding.DecodeString(b64.String())
				if err != nil {
					return nil, fmt.Errorf("%w: %v", ErrKeyUnreadable, err)
				}
				out = append(out, dec...)
				b64.Reset()
				state = outside
			case state == body && strings.HasPrefix(line, "="):
				state = checksum
			case state == body:
				b64.WriteString(line)
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrKeyUnreadable, err)
	}
	if state != outside {
		return nil, fmt.Errorf("%w: an armored block has no end line", ErrKeyUnreadable)
	}
	return out, nil
}

var errPacketHeaderCut = fmt.Errorf("%w: a packet header is cut short", ErrKeyUnreadable)

// publicKey is one transferable public key in a keyring.
type publicKey struct {
	fingerprint string // v4 only; empty for any other version
	packets     []byte // the primary key packet and every packet up to the next one, headers included
}

// splitKeys cuts a binary keyring into its transferable public keys. Each
// starts at a public-key packet and runs to the next; anything before the
// first, a packet it cannot frame, and any secret key packet make the whole
// keyring unreadable.
func splitKeys(keyring []byte) ([]publicKey, error) {
	var keys []publicKey
	for rest := keyring; len(rest) > 0; {
		tag, header, length, err := packetHeader(rest)
		if err != nil {
			return nil, err
		}
		if uint64(len(rest)-header) < length {
			return nil, fmt.Errorf("%w: a packet runs past the end of the file", ErrKeyUnreadable)
		}
		size := header + int(length)
		switch {
		case tag == tagSecretKey || tag == tagSecretSubkey:
			return nil, fmt.Errorf("%w: it holds secret key material", ErrKeyUnreadable)
		case tag == tagPublicKey:
			keys = append(keys, publicKey{fingerprint: v4Fingerprint(rest[header:size])})
		case len(keys) == 0:
			return nil, fmt.Errorf("%w: it does not start with a public key", ErrKeyUnreadable)
		}
		k := &keys[len(keys)-1]
		k.packets = append(k.packets, rest[:size]...)
		rest = rest[size:]
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("%w: it holds no key", ErrKeyUnreadable)
	}
	return keys, nil
}

// packetHeader reads the header at the start of b: the packet's tag, the
// header's own length and the body's length. Indeterminate and partial
// lengths are refused; neither belongs in a key.
func packetHeader(b []byte) (tag byte, header int, length uint64, err error) {
	h := b[0]
	if h&0x80 == 0 {
		return 0, 0, 0, fmt.Errorf("%w: not a packet", ErrKeyUnreadable)
	}
	if h&0x40 == 0 { // old format
		tag = (h >> 2) & 0x0F
		switch h & 3 {
		case 0:
			header = 2
		case 1:
			header = 3
		case 2:
			header = 5
		default:
			return 0, 0, 0, fmt.Errorf("%w: a packet of indeterminate length", ErrKeyUnreadable)
		}
		if len(b) < header {
			return 0, 0, 0, errPacketHeaderCut
		}
		for _, o := range b[1:header] {
			length = length<<8 | uint64(o)
		}
		return tag, header, length, nil
	}
	tag = h & 0x3F
	if len(b) < 2 {
		return 0, 0, 0, errPacketHeaderCut
	}
	switch o1 := b[1]; {
	case o1 < 192:
		return tag, 2, uint64(o1), nil
	case o1 < 224:
		if len(b) < 3 {
			return 0, 0, 0, errPacketHeaderCut
		}
		return tag, 3, (uint64(o1)-192)<<8 + uint64(b[2]) + 192, nil
	case o1 == 255:
		if len(b) < 6 {
			return 0, 0, 0, errPacketHeaderCut
		}
		return tag, 6, uint64(binary.BigEndian.Uint32(b[2:6])), nil
	default:
		return 0, 0, 0, fmt.Errorf("%w: a packet of partial length", ErrKeyUnreadable)
	}
}

// v4Fingerprint is the fingerprint of a version 4 public-key packet body:
// SHA-1 over 0x99, the body's two-octet length and the body, in upper-case
// hex. Any other version has none here.
func v4Fingerprint(body []byte) string {
	if len(body) == 0 || body[0] != 4 || len(body) > 0xFFFF {
		return ""
	}
	h := sha1.New()
	h.Write([]byte{0x99, byte(len(body) >> 8), byte(len(body))})
	h.Write(body)
	return strings.ToUpper(hex.EncodeToString(h.Sum(nil)))
}

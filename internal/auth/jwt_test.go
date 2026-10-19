package auth

import (
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// golang-jwt accepts an empty HMAC key. Only config validation kept one from
// being installed; the parser itself must refuse, so a future path that
// empties the secret cannot turn every token signed with "" into a login.
func TestJWTRefusesAnEmptyKey(t *testing.T) {
	if _, err := GenerateToken("admin", "", time.Minute); !errors.Is(err, ErrEmptyJWTKey) {
		t.Fatalf("GenerateToken with an empty key: %v", err)
	}
	forged, err := GenerateToken("admin", "x", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseToken(forged, ""); !errors.Is(err, ErrEmptyJWTKey) {
		t.Fatalf("ParseToken with an empty key: %v", err)
	}
	// The token the guard is for: GenerateToken no longer makes one, but
	// anyone can sign with "" through the library, and without the guard it
	// parses as a valid login.
	empty, err := jwt.NewWithClaims(jwt.SigningMethodHS256, Claims{
		Username:         "admin",
		RegisteredClaims: jwt.RegisteredClaims{Issuer: "sfpanel"},
	}).SignedString([]byte(""))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseToken(empty, ""); !errors.Is(err, ErrEmptyJWTKey) {
		t.Fatalf("ParseToken of a token signed with an empty key: %v", err)
	}
}

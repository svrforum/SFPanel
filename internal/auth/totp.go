package auth

import (
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

func GenerateSecret(username string) (*otp.Key, error) {
	return totp.Generate(totp.GenerateOpts{
		Issuer:      "SFPanel",
		AccountName: username,
	})
}

func ValidateCode(secret string, code string) bool {
	return totp.Validate(code, secret)
}

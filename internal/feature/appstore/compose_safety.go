package appstore

import "github.com/svrforum/SFPanel/internal/composex"

// validateAdvancedCompose delegates to the shared validator. Kept as a
// package-private alias so existing callers don't need to switch import
// paths in the same change.
func validateAdvancedCompose(content string) error {
	return composex.ValidateAdvancedCompose(content)
}

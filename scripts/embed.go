// Package scripts carries the installer in the binary, so a leader that
// installs an agent over SSH hands the target the install.sh of its own
// release — the version it pins, signed with the binary — instead of
// fetching one from the repository's main branch, and does so offline too.
package scripts

import _ "embed"

// InstallScript is scripts/install.sh as this binary was built with it.
//
//go:embed install.sh
var InstallScript []byte

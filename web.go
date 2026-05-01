package sfpanel

import (
	"embed"
	"fmt"
)

//go:embed all:web/dist
var WebDistFS embed.FS

// webDistIndex is embedded as a separate directive so the build fails loudly
// when the frontend hasn't been built. With only the `all:web/dist` line
// above, an empty dist/ directory still compiles successfully and the binary
// ships a blank UI. Requiring index.html turns "frontend forgot to build"
// into a clear compile error.
//
//go:embed web/dist/index.html
var webDistIndex []byte

func init() {
	if len(webDistIndex) == 0 {
		panic(fmt.Sprintf("web/dist/index.html is empty — run `cd web && npm ci && npm run build` before `go build`"))
	}
}

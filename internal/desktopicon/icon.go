// Package desktopicon provides native-window branding without depending on the
// generated frontend bundle. The icon generator keeps its PNG in sync with web.
package desktopicon

import _ "embed"

//go:embed icon-256.png
var desktopIcon []byte

func PNG() []byte {
	return append([]byte(nil), desktopIcon...)
}

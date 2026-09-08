package web

import _ "embed"

// Keep native-window branding tied to the same generated icon as the web UI.
// The standalone helper does not reference DistFS, so the linker discards the
// unrelated web bundle from that binary.
//
//go:embed public/icons/icon-256.png
var desktopIcon []byte

func DesktopIcon() []byte {
	return append([]byte(nil), desktopIcon...)
}

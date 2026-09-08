package web

import (
	"bytes"
	"image/png"
	"testing"
)

func TestDesktopIcon(t *testing.T) {
	icon := DesktopIcon()
	decoded, err := png.Decode(bytes.NewReader(icon))
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Bounds().Dx() != 256 || decoded.Bounds().Dy() != 256 {
		t.Fatalf("desktop icon must be the canonical 256px PNG, got %v", decoded.Bounds())
	}
	icon[0] = 0
	if _, err := png.Decode(bytes.NewReader(DesktopIcon())); err != nil {
		t.Fatalf("callers must not mutate the embedded icon: %v", err)
	}
}

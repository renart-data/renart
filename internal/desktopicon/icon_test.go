package desktopicon

import (
	"bytes"
	"image/png"
	"os"
	"testing"
)

func TestDesktopIcon(t *testing.T) {
	icon := PNG()
	canonical, err := os.ReadFile("../../web/public/icons/icon-256.png")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(icon, canonical) {
		t.Fatal("desktop icon drifted from the web icon; run web generate:icons")
	}
	decoded, err := png.Decode(bytes.NewReader(icon))
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Bounds().Dx() != 256 || decoded.Bounds().Dy() != 256 {
		t.Fatalf("desktop icon must be the canonical 256px PNG, got %v", decoded.Bounds())
	}
	icon[0] = 0
	if _, err := png.Decode(bytes.NewReader(PNG())); err != nil {
		t.Fatalf("callers must not mutate the embedded icon: %v", err)
	}
}

package desktopicon

import (
	"os/exec"
	"strings"
	"testing"
)

func TestStandaloneDoesNotDependOnWebBundle(t *testing.T) {
	// Listing dependencies needs no native WebKit libraries. The helper must
	// also build in a fresh checkout, before the frontend has generated dist.
	command := exec.Command("go", "list", "-deps", "-tags=standalone,desktop,production", "./cmd/renart-gui")
	command.Dir = "../.."
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("list standalone dependencies: %v\n%s", err, output)
	}
	for dependency := range strings.Lines(string(output)) {
		if strings.TrimSpace(dependency) == "renart/web" {
			t.Fatal("standalone helper imports the web bundle; use the independent desktopicon package")
		}
	}
}

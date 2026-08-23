package workspacefs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPathIDRoundTrip(t *testing.T) {
	path := "pipelines/orders/assets/orders.sql"
	decoded, err := DecodePathID(EncodePathID(path))
	if err != nil {
		t.Fatal(err)
	}
	if decoded != path {
		t.Fatalf("decoded path = %q, want %q", decoded, path)
	}
}

func TestJoinRejectsEscapesAndAbsolutePaths(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"../outside", "../../outside", filepath.Join(root, "outside")} {
		if joined, err := Join(root, path); err == nil {
			t.Fatalf("Join(%q) = %q without an error", path, joined)
		}
	}

	joined, err := Join(root, "pipelines/orders")
	if err != nil {
		t.Fatal(err)
	}
	if joined != filepath.Join(root, "pipelines", "orders") {
		t.Fatalf("joined path = %q", joined)
	}
}

func TestWriteFileAtomicCreatesParentAndLeavesFinalMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dashboards", "sales.dashboard.yml")
	if err := WriteFileAtomic(path, []byte("title: Sales\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "title: Sales\n" {
		t.Fatalf("content = %q", content)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %o, want 640", info.Mode().Perm())
	}
}

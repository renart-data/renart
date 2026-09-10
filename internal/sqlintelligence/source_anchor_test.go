package sqlintelligence

import "testing"

func TestSourceAnchorFingerprint(t *testing.T) {
	if got := SourceAnchorFingerprint("hello"); got != "fnv1a64:a430d84680aabd0b" {
		t.Fatal(got)
	}
	if SourceAnchorFingerprint("a\r\n😀") != SourceAnchorFingerprint("a\n😀") {
		t.Fatal("CRLF normalization differs")
	}
	if SourceAnchorFingerprint("SELECT 1") == SourceAnchorFingerprint("SELECT 2") {
		t.Fatal("different content matched")
	}
}

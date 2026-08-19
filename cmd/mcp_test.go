package cmd

import (
	"errors"
	"fmt"
	"io"
	"testing"
)

func TestNormalMCPClientClose(t *testing.T) {
	for _, err := range []error{
		nil,
		io.EOF,
		fmt.Errorf("server is closing: %v", io.EOF),
	} {
		if !normalMCPClientClose(err) {
			t.Errorf("normal close %v was reported as a failure", err)
		}
	}
	if normalMCPClientClose(errors.New("protocol decode failed")) {
		t.Fatal("protocol failure was treated as a normal close")
	}
}

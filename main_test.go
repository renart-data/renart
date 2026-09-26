package main

import (
	"os"
	"reflect"
	"testing"

	bruintelemetry "github.com/bruin-data/bruin/pkg/telemetry"
)

func TestArgsWithDefaultCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want []string
	}{
		{name: "bare binary opens standalone", args: []string{"renart"}, want: []string{"renart", "standalone"}},
		{name: "help stays at root", args: []string{"renart", "--help"}, want: []string{"renart", "--help"}},
		{name: "explicit command is unchanged", args: []string{"renart", "web", "--no-open"}, want: []string{"renart", "web", "--no-open"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := argsWithDefaultCommand(tt.args); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("argsWithDefaultCommand(%v) = %v, want %v", tt.args, got, tt.want)
			}
		})
	}
}

func TestConfigureManagedRuntimeEnvironment(t *testing.T) {
	t.Setenv("UV_NO_MODIFY_PATH", "0")

	previous := bruintelemetry.OptOut
	t.Cleanup(func() { bruintelemetry.OptOut = previous })
	configureManagedRuntimeEnvironment()
	if !bruintelemetry.OptOut {
		t.Fatal("embedded Bruin telemetry must stay disabled")
	}

	if got := os.Getenv("UV_NO_MODIFY_PATH"); got != "1" {
		t.Fatalf("UV_NO_MODIFY_PATH = %q, want 1", got)
	}
}

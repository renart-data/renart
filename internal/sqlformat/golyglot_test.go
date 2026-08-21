package sqlformat

import (
	"context"
	"strings"
	"sync"
	"testing"
)

func TestFormatUsesNativeGolyglot(t *testing.T) {
	formatted, err := Format(context.Background(), "select 1 as id", DialectGeneric)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToUpper(formatted), "SELECT") || !strings.Contains(strings.ToLower(formatted), "id") {
		t.Fatalf("formatted SQL = %q", formatted)
	}
}

func TestFormatHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Format(ctx, "select 1", DialectGeneric); err != context.Canceled {
		t.Fatalf("Format error = %v, want context.Canceled", err)
	}
}

func TestFormatIsSafeAcrossConcurrentCalls(t *testing.T) {
	const calls = 32
	var wait sync.WaitGroup
	errors := make(chan error, calls)
	for index := 0; index < calls; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := Format(context.Background(), "select customer_id from analytics.customers", "duckdb")
			errors <- err
		}()
	}
	wait.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
}

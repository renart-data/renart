package sqlintelligence

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStrictDataTypesEquivalentPreservesLogicalStructure(t *testing.T) {
	tests := []struct {
		name       string
		left       string
		right      string
		equivalent bool
	}{
		{name: "aliases", left: "DECIMAL(18, 4)", right: "NUMERIC(18, 4)", equivalent: true},
		{name: "scale", left: "DECIMAL(18, 4)", right: "NUMERIC(18, 2)", equivalent: false},
		{name: "length", left: "VARCHAR(255)", right: "VARCHAR(128)", equivalent: false},
		{name: "nested element", left: "ARRAY<INTEGER>", right: "ARRAY<BIGINT>", equivalent: false},
		{name: "timezone", left: "TIMESTAMP WITH TIME ZONE", right: "TIMESTAMP WITHOUT TIME ZONE", equivalent: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			equivalent, comparable, err := StrictDataTypesEquivalent(context.Background(), tt.left, tt.right, "generic")
			require.NoError(t, err)
			assert.True(t, comparable)
			assert.Equal(t, tt.equivalent, equivalent)
		})
	}
}

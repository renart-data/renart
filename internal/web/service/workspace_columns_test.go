package service

import (
	"testing"

	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceColumnConversionPreservesBruinColumnContract(t *testing.T) {
	nullable := false
	precision := 18
	scale := 4
	length := 255
	input := []pipeline.Column{{
		Name:         "user_id",
		SourceColumn: "source_user_id",
		Type:         "DECIMAL",
		Mask:         "hash",
		Nullable:     pipeline.DefaultTrueBool{Value: &nullable},
		PrimaryKey:   true,
		Default:      "0",
		Precision:    &precision,
		Scale:        &scale,
		Length:       &length,
		Collation:    "en_US",
		ForeignKey:   &pipeline.ColumnReference{Table: "analytics.users", Column: "id"},
		Checks:       []pipeline.ColumnCheck{},
	}}

	modelColumns := PipelineColumnsToModelColumns(input)
	require.Len(t, modelColumns, 1)
	require.NotNil(t, modelColumns[0].ForeignKey)
	assert.Equal(t, "analytics.users", modelColumns[0].ForeignKey.Table)
	assert.Equal(t, "id", modelColumns[0].ForeignKey.Column)

	roundTrip := ModelColumnsToPipelineColumns(modelColumns)
	require.Len(t, roundTrip, 1)
	require.NotNil(t, roundTrip[0].ForeignKey)
	assert.Equal(t, input, roundTrip)
	require.NotNil(t, roundTrip[0].Nullable.Value)
	assert.False(t, *roundTrip[0].Nullable.Value)
}

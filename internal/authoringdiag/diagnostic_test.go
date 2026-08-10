package authoringdiag

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTypeCheckDiagnosticRegistryHasEditorDelivery(t *testing.T) {
	required := []string{
		CodeSQLSyntax,
		CodeSQLValidationFailed,
		CodeUnresolvedRelation,
		CodeUnresolvedAlias,
		CodeUnresolvedColumn,
		CodeSQLTypeMismatch,
		CodeDeclaredColumnTypeDrift,
		CodeDeclaredColumnNullabilityDrift,
		CodeUnmaterializedColumn,
		CodeCrossConnectionReference,
		CodeExternalRelation,
		CodeCrossPipelineDependencyMissing,
		CodeCrossPipelineRelationAmbiguous,
		CodeDependencyValidationFailed,
		CodeMissingDependency,
		CodeInvalidMaterialization,
		CodeInactiveMaterialization,
		CodeMissingDeclaredColumns,
		CodePythonUndeclaredQueryDependency,
		CodeTemplateRenderFailed,
		CodeAssetDefinitionParseFailed,
	}
	for _, code := range required {
		delivery, ok := TypeCheckDelivery(code)
		require.True(t, ok, "diagnostic code %q is not registered", code)
		require.NotEmpty(t, delivery, "diagnostic code %q has no editor delivery", code)
	}
}

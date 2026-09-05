package navigationtarget

import (
	"testing"

	"github.com/stretchr/testify/require"
	"renart/internal/authoringdiag"
)

func TestColumnTargetUsesFactsNotMessage(t *testing.T) {
	d := authoringdiag.Diagnostic{Code: authoringdiag.CodeDeclaredColumnTypeDrift,
		Message: "A completely different, localized message",
		Subject: &authoringdiag.Subject{Column: "Total ä", Field: "type"}}
	require.Equal(t, &Target{Kind: "asset-column", AssetID: "asset", Column: "Total ä", Field: "type"}, ForDiagnostic("asset", d))
	d.Subject = nil
	require.Nil(t, ForDiagnostic("asset", d), "never extract an identity from prose")
	d.Subject = &authoringdiag.Subject{Column: "Total ä", Field: "type"}
	require.Nil(t, ForDiagnostic("", d), "no invented asset identity")
	d.Code = "unknown-plugin-code"
	require.Nil(t, ForDiagnostic("asset", d), "unknown codes have no exact UI guarantee")
}

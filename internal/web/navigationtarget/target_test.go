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
	require.Equal(t, &Target{Kind: "asset-section", AssetID: "asset", Section: "columns"}, ForDiagnostic("asset", d), "missing facts fall back to the verified owner, never prose")
	d.Subject = &authoringdiag.Subject{Column: "Total ä", Field: "type"}
	require.Nil(t, ForDiagnostic("", d), "no invented asset identity")
	d.Code = "unknown-plugin-code"
	require.Nil(t, ForDiagnostic("asset", d), "unknown codes have no exact UI guarantee")
}

func TestEveryFirstPartyDiagnosticHasExplicitNavigationPolicy(t *testing.T) {
	for _, code := range authoringdiag.RegisteredTypeCheckCodes() {
		policy, ok := DiagnosticPolicy(code)
		require.True(t, ok, code)
		require.NotEmpty(t, policy.Section, code)
		require.NotNil(t, ForDiagnostic("owner", authoringdiag.Diagnostic{Code: code}), code)
	}
	_, ok := DiagnosticPolicy("new-unclassified-code")
	require.False(t, ok)
}

// Package navigationtarget owns resource addresses, not HTTP routes or commands.
package navigationtarget

import "renart/internal/authoringdiag"

// Target is a current editable resource. It never authorizes an edit. Unsupported
// diagnostics remain plain messages until their destination has a tested surface.
// renart:web-name ResourceTarget
type Target struct {
	Kind    string `json:"kind"`
	AssetID string `json:"asset_id"`
	Column  string `json:"column"`
	Field   string `json:"field"`
}

func ForDiagnostic(assetID string, d authoringdiag.Diagnostic) *Target {
	if assetID == "" || d.Code != authoringdiag.CodeDeclaredColumnTypeDrift ||
		d.Subject == nil || d.Subject.Column == "" || d.Subject.Field != "type" {
		return nil
	}
	return &Target{Kind: "asset-column", AssetID: assetID, Column: d.Subject.Column, Field: d.Subject.Field}
}

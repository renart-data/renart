package model

// PreviewMetadata describes a bounded sample, not a stable warehouse snapshot.
// ResultID only invalidates UI selection; it is not a continuation credential.
// renart:web
type PreviewMetadata struct {
	ReturnedRows int    `json:"returned_rows"`
	HasMore      bool   `json:"has_more"`
	Limit        int    `json:"limit"`
	NextLimit    int    `json:"next_limit,omitempty"`
	Continuation string `json:"continuation"`
	Reason       string `json:"reason,omitempty"`
	ResultID     string `json:"result_id"`
}

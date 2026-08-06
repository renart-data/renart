package service

type directErrorResponse struct {
	Error string `json:"error"`
}

type directImportWarning struct {
	Table   string `json:"table"`
	Warning string `json:"warning"`
}

type directImportDatabaseResponse struct {
	Status         string                `json:"status"`
	Preview        bool                  `json:"preview,omitempty"`
	ImportedTables int                   `json:"imported_tables"`
	MergedTables   int                   `json:"merged_tables"`
	Database       string                `json:"database"`
	PipelinePath   string                `json:"pipeline_path"`
	Assets         []directImportAsset   `json:"assets"`
	Warnings       []directImportWarning `json:"warnings"`
}

type directImportAsset struct {
	Name    string      `json:"name"`
	Path    string      `json:"path"`
	Type    string      `json:"type"`
	Columns []SQLColumn `json:"columns"`
}

type directFillAssetDependenciesResponse struct {
	Status           string `json:"status"`
	Message          string `json:"message,omitempty"`
	ProcessedAssets  int    `json:"processed_assets,omitempty"`
	SuccessfulAssets int    `json:"successful_assets,omitempty"`
	FailedAssets     int    `json:"failed_assets,omitempty"`
}

type directFillColumnsAssetResponse struct {
	Status string `json:"status"`
	Asset  string `json:"asset"`
}

type directFillColumnsPipelineResponse struct {
	Status            string   `json:"status"`
	UpdatedAssetNames []string `json:"updated_asset_names"`
	SkippedAssetNames []string `json:"skipped_asset_names"`
	FailedAssetNames  []string `json:"failed_asset_names"`
	ProcessedAssets   int      `json:"processed_assets"`
}

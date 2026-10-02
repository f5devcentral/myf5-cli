package cases

// CaseMetadataResponse represents valid products, versions, severities, etc.
type CaseMetadataResponse struct {
	Data map[string]interface{} `json:"data"`
}

// CaseSummary represents a brief overview of an F5 support case.
//
// Field tags follow GetCaseApiResponse in the F5 OpenAPI specification
// (docs/openapi.json). Two earlier tags matched nothing in the payload and so
// always decoded as empty: `severity` (the API returns `priority`, e.g.
// "2 - Site At Risk") and `createdDate` (the API returns `dateOpened`).
type CaseSummary struct {
	CaseNumber string `json:"caseNumber"`
	Subject    string `json:"subject"`
	Status     string `json:"status"`
	Priority   string `json:"priority"`
	DateOpened string `json:"dateOpened,omitempty"`
	Product    string `json:"product,omitempty"`
	Version    string `json:"version,omitempty"`
}

// ListCasesResponse is the response from GET /case-management/v1/cases.
type ListCasesResponse struct {
	Data  []CaseSummary `json:"data"`
	Links []struct {
		Rel  string `json:"rel"`
		Href string `json:"href"`
	} `json:"links,omitempty"`
}

// CreateCaseResponse is the response from POST /case-management/v1/cases.
type CreateCaseResponse struct {
	Data struct {
		CaseNumber string `json:"caseNumber"`
	} `json:"data"`
	Links []struct {
		Rel  string `json:"rel"`
		Href string `json:"href"`
	} `json:"links,omitempty"`
}

// AddCommentResponse is the response from POST /case-management/v1/cases/{caseNumber}/notes.
type AddCommentResponse struct {
	Data struct {
		ID string `json:"id"`
	} `json:"data"`
}

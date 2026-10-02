package cases

// CaseMetadataResponse represents valid products, versions, severities, etc.
type CaseMetadataResponse struct {
	Data map[string]interface{} `json:"data"`
}

// Support case priority levels, per F5 article K2633 (docs/K2633.txt).
//
// These are PRIORITY values, not "severity" — F5 retired that terminology and
// the API returns this field as `priority`. The live API returns them in the
// "<n> - <Name>" form shown here.
//
// Initial response targets, for choosing a level: P1 30 minutes by phone or
// one hour via the portal, P2 two hours, P3 four hours, P4 24 hours.
const (
	PrioritySiteDown            = "1 - Site Down"
	PrioritySiteAtRisk          = "2 - Site At Risk"
	PriorityPerformanceDegraded = "3 - Performance Degraded"
	PriorityGeneralAssistance   = "4 - General Assistance"
)

// ValidPriorities lists the accepted case priority values.
//
// Note that MyF5 may also display a "Low" option; K2633 states this is shown
// in error and is not a valid case priority, so it is deliberately absent.
var ValidPriorities = []string{
	PrioritySiteDown,
	PrioritySiteAtRisk,
	PriorityPerformanceDegraded,
	PriorityGeneralAssistance,
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

// AddCommentResponse is the response from
// PATCH /case-management/v1/cases/{caseNumber}.
//
// The previous model read data.id, which no documented response returns, so
// it decoded empty even on success. The API returns the case number and an
// update timestamp, alongside the status envelope the other case endpoints
// use.
type AddCommentResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	IsError bool   `json:"isError"`
	Code    int    `json:"code"`
	Data    struct {
		CaseNumber  string `json:"caseNumber"`
		UpdatedDate string `json:"updatedDate"`
	} `json:"data"`
	Links []struct {
		Rel  string `json:"rel"`
		Href string `json:"href"`
	} `json:"links,omitempty"`
}

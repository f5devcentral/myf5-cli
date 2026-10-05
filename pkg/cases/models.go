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
// (docs/openapi-outdated-5Oct2026.json). Two earlier tags matched nothing in
// the payload and so
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

// CaseContact is a person attached to a case.
//
// The two contact objects on a case detail are NOT shaped alike: the case
// owner carries `name` and `preferredContactMethod`, while the alternate
// contact splits the name into `firstName`/`lastName` and adds a phone
// number. Both are modelled here, so Name() can present either.
type CaseContact struct {
	Name                   string `json:"name,omitempty"`
	FirstName              string `json:"firstName,omitempty"`
	LastName               string `json:"lastName,omitempty"`
	Email                  string `json:"email,omitempty"`
	PhoneNumber            string `json:"phoneNumber,omitempty"`
	TimeZone               string `json:"timeZone,omitempty"`
	PreferredContactMethod string `json:"preferredContactMethod,omitempty"`
}

// DisplayName returns the contact's name from whichever fields are populated.
func (c CaseContact) DisplayName() string {
	if c.Name != "" {
		return c.Name
	}
	switch {
	case c.FirstName != "" && c.LastName != "":
		return c.FirstName + " " + c.LastName
	case c.FirstName != "":
		return c.FirstName
	default:
		return c.LastName
	}
}

// CaseEmailActivity is one message in a case's email thread.
type CaseEmailActivity struct {
	Subject      string `json:"subject"`
	FromAddress  string `json:"fromAddress"`
	ToAddress    string `json:"toAddress"`
	Body         string `json:"body"`
	ActivityDate string `json:"activityDate"`
	Contact      struct {
		Name  string `json:"name"`
		Email string `json:"email"`
	} `json:"activityContact"`
}

// CaseDetail is a single case from GET /case-management/v1/cases/{caseNumber}.
//
// WARNING: `dateOpened` is unreliable on this endpoint. The live API returns
// the PRIORITY string in that field on case detail (observed 2026-10-05:
// "4 - General Assistance" where the list endpoint correctly returns
// "09/29/2026 17:59:42" for the same case). The fault is server-side. Prefer
// the list endpoint's value when a real timestamp is needed.
type CaseDetail struct {
	CaseNumber       string `json:"caseNumber"`
	Subject          string `json:"subject"`
	Status           string `json:"status"`
	Priority         string `json:"priority"`
	Product          string `json:"product"`
	Version          string `json:"version"`
	Description      string `json:"description"`
	ReasonForContact string `json:"reasonForContact"`
	SerialNumber     string `json:"serialNumber"`
	SubscriptionID   string `json:"subscriptionId"`
	HostName         string `json:"hostName"`
	CloudProvider    string `json:"cloudProvider"`
	DateOpened       string `json:"dateOpened,omitempty"`

	Account struct {
		AccountName string `json:"accountName"`
		AccountID   string `json:"accountId"`
	} `json:"account"`

	CaseOwnerContact CaseContact `json:"caseOwnercontact"`
	AlternateContact CaseContact `json:"alternateContact"`

	Activity struct {
		EmailActivities   []CaseEmailActivity `json:"emailActivities"`
		CommentActivities []struct {
			Body         string `json:"body"`
			ActivityDate string `json:"activityDate"`
		} `json:"commentActivities"`
	} `json:"activity"`

	Links []struct {
		Rel  string `json:"rel"`
		Href string `json:"href"`
	} `json:"links,omitempty"`
}

// GetCaseResponse is the response from GET /cases/{caseNumber}.
//
// `data` is an array even though exactly one case is requested.
type GetCaseResponse struct {
	Status  string       `json:"status"`
	Message string       `json:"message"`
	IsError bool         `json:"isError"`
	Data    []CaseDetail `json:"data"`
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
//
// The status envelope is not decoration: this API reports failure in the body
// as well as by status code, so a 2xx alone does not mean a case was opened.
// Callers must check IsError before trusting Data.
type CreateCaseResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	IsError bool   `json:"isError"`
	Code    int    `json:"code,omitempty"`
	Data    struct {
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

package ihealth

// QKViewSummary represents a QKView record uploaded to iHealth.
type QKViewSummary struct {
	ID          string `json:"id"`
	FileName    string `json:"filename,omitempty"`
	Description string `json:"description,omitempty"`
	CaseNumber  string `json:"case_number,omitempty"`
	UploadDate  string `json:"upload_date,omitempty"`
	Status      string `json:"status,omitempty"`
	GUIURL      string `json:"gui_url,omitempty"`
}

// UploadQKViewResponse is the response returned upon uploading a QKView to iHealth.
type UploadQKViewResponse struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

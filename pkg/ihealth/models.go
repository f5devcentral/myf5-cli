package ihealth

import (
	"fmt"
	"time"
)

// QKViewSummary represents a QKView record uploaded to iHealth, assembled from
// the API's detail resource.
type QKViewSummary struct {
	ID          string `json:"id"`
	FileName    string `json:"filename,omitempty"`
	Description string `json:"description,omitempty"`
	CaseNumber  string `json:"case_number,omitempty"`
	UploadDate  string `json:"upload_date,omitempty"`
	Status      string `json:"status,omitempty"`
	GUIURL      string `json:"gui_url,omitempty"`
	Hostname    string `json:"hostname,omitempty"`
	FileSize    int64  `json:"file_size,omitempty"`
}

// qkviewDetail mirrors the JSON payload returned by GET /qkview-analyzer/api/qkviews/{id}.
type qkviewDetail struct {
	FileName          string `json:"file_name"`
	Description       string `json:"description"`
	Hostname          string `json:"hostname"`
	GUIURI            string `json:"gui_uri"`
	F5SupportCase     string `json:"f5_support_case"`
	ProcessingStatus  string `json:"processing_status"`
	ProcessingMessage string `json:"processing_messages"`
	FileSize          int64  `json:"file_size"`
	// Timestamps are epoch MILLISECONDS, not seconds and not RFC 3339.
	GenerationDate int64 `json:"generation_date"`
	ExpirationDate int64 `json:"expiration_date"`
	Upload         struct {
		Date        int64 `json:"date"`
		PerformedBy struct {
			Name  string `json:"name"`
			Email string `json:"email"`
		} `json:"performed_by"`
	} `json:"upload"`
}

// toSummary projects the API's detail representation onto the CLI's view.
func (d qkviewDetail) toSummary(id string) *QKViewSummary {
	guiURL := d.GUIURI
	if guiURL == "" && id != "" {
		guiURL = fmt.Sprintf("https://ihealth.f5.com/qkview-analyzer/qv/%s", id)
	}
	s := &QKViewSummary{
		ID:          id,
		FileName:    d.FileName,
		Description: d.Description,
		CaseNumber:  d.F5SupportCase,
		Status:      d.ProcessingStatus,
		GUIURL:      guiURL,
		Hostname:    d.Hostname,
		FileSize:    d.FileSize,
	}
	if d.Upload.Date > 0 {
		s.UploadDate = time.UnixMilli(d.Upload.Date).UTC().Format("2006-01-02 15:04:05 UTC")
	}
	return s
}

// UploadQKViewResponse is the response returned upon uploading a QKView to iHealth.
type UploadQKViewResponse struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

package ihealth

import (
	"testing"
)

func FuzzParseUploadResponse(f *testing.F) {
	f.Add([]byte(`<qkview><id>12345</id><status>processing</status></qkview>`), "https://ihealth2-api.f5.com/qkview-analyzer/api/v1/qkviews/12345")
	f.Add([]byte(`{"id":"12345","status":"completed"}`), "")
	f.Add([]byte(`{"qkview":{"id":12345,"status":"processing"}}`), "")
	f.Add([]byte(`{"errors":[{"message":"forbidden"}]}`), "")
	f.Add([]byte(``), "https://ihealth2-api.f5.com/qkview-analyzer/api/v1/qkviews/99999")
	f.Add([]byte(`not valid json or xml at all`), "")
	f.Add([]byte(`<xml>malformed`), "")

	f.Fuzz(func(t *testing.T, body []byte, locHeader string) {
		// parseUploadResponse must never panic on arbitrary responses
		_, _ = parseUploadResponse(body, locHeader)
	})
}

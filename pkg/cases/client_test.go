package cases

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestListCases(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/case-management/v1/cases" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("k") != DefaultKParam {
			t.Errorf("expected k param %s, got %s", DefaultKParam, r.URL.Query().Get("k"))
		}
		if r.Header.Get("Authorization") != "Bearer case-tok" {
			t.Errorf("unexpected auth header: %s", r.Header.Get("Authorization"))
		}

		// `type` is required by the Case Management API. Omitting it returns
		// 400 "Missing type in the query params" from the service.
		if got := r.URL.Query().Get("type"); got != TypeAllOpenCases {
			t.Errorf("type = %q, want %q", got, TypeAllOpenCases)
		}

		resp := ListCasesResponse{
			Data: []CaseSummary{
				{CaseNumber: "C10001", Subject: "SSL handshake failure", Status: "Open", Priority: "2 - Site At Risk"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	c := NewClient("case-tok", server.URL)
	c.HTTPClient = server.Client()

	list, err := c.ListCases(context.Background(), false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(list.Data) != 1 || list.Data[0].CaseNumber != "C10001" {
		t.Errorf("unexpected cases returned: %+v", list)
	}
}

// TestAddCommentRequestShape verifies that AddComment sends PATCH /case-management/v1/cases/{n}
// with a {"comments": ...} JSON payload and decodes the response envelope.
func TestAddCommentRequestShape(t *testing.T) {
	var gotMethod, gotPath, gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(`{"status":"Success","message":"Comments added to Case.","isError":false,"code":200,"data":{"caseNumber":"01234567","updatedDate":"05/28/2024 20:14:15"}}`)); err != nil {
			t.Fatalf("write: %v", err)
		}
	}))
	defer server.Close()

	c := NewClient("tok", server.URL)
	c.HTTPClient = server.Client()

	resp, err := c.AddComment(context.Background(), "01234567", "a note")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotMethod != http.MethodPatch {
		t.Errorf("method = %s, want PATCH", gotMethod)
	}
	if want := "/case-management/v1/cases/01234567"; gotPath != want {
		t.Errorf("path = %s, want %s", gotPath, want)
	}
	if !strings.Contains(gotBody, `"comments"`) {
		t.Errorf("body = %s, want the plural field %q", gotBody, "comments")
	}
	if strings.Contains(gotBody, `"comment"`) && !strings.Contains(gotBody, `"comments"`) {
		t.Errorf("body uses singular %q; the API expects %q", "comment", "comments")
	}

	if resp.Data.CaseNumber != "01234567" {
		t.Errorf("CaseNumber = %q, want 01234567", resp.Data.CaseNumber)
	}
	if resp.IsError {
		t.Errorf("IsError = true for a success payload")
	}
}

func TestListCasesDecodesLiveFieldNames(t *testing.T) {
	payload := `{"data":[{"caseNumber":"01234567","subject":"Example Case Subject","status":"Resolution Provided","priority":"3 - Site Not At Risk","dateOpened":"2025-09-12"}]}`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(payload)); err != nil {
			t.Fatalf("write: %v", err)
		}
	}))
	defer server.Close()

	c := NewClient("tok", server.URL)
	c.HTTPClient = server.Client()

	list, err := c.ListCases(context.Background(), false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(list.Data) != 1 {
		t.Fatalf("expected 1 case, got %d", len(list.Data))
	}
	got := list.Data[0]
	if got.Priority != "3 - Site Not At Risk" {
		t.Errorf("Priority = %q; a tag mismatch decodes as empty", got.Priority)
	}
	if got.DateOpened != "2025-09-12" {
		t.Errorf("DateOpened = %q; a tag mismatch decodes as empty", got.DateOpened)
	}
	if got.Status != "Resolution Provided" {
		t.Errorf("Status = %q", got.Status)
	}
}

// TestBodyReportedErrorsBecomeErrors verifies that a 2xx response carrying
// isError:true in the JSON envelope is surfaced as an error rather than
// decoded as an empty result.
func TestBodyReportedErrorsBecomeErrors(t *testing.T) {
	const payload = `{"status":"Error","message":"Something went wrong upstream","isError":true,"data":null}`

	t.Run("ListCases", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK) // 2xx despite the failure
			io.WriteString(w, payload)
		}))
		defer server.Close()

		c := NewClientWithKey("tok", server.URL, "key")
		c.HTTPClient = server.Client()

		_, err := c.ListCases(context.Background(), false)
		if err == nil {
			t.Fatal("a 2xx with isError:true must be an error, not an empty case list")
		}
		if !strings.Contains(err.Error(), "Something went wrong upstream") {
			t.Errorf("error should carry the API's message, got: %v", err)
		}
	})

	t.Run("GetMetadata", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			io.WriteString(w, payload)
		}))
		defer server.Close()

		c := NewClientWithKey("tok", server.URL, "key")
		c.HTTPClient = server.Client()

		_, err := c.GetMetadata(context.Background())
		if err == nil {
			t.Fatal("a 2xx with isError:true must be an error, not metadata with 0 keys")
		}
	})

	t.Run("GetCase", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			io.WriteString(w, payload)
		}))
		defer server.Close()

		c := NewClientWithKey("tok", server.URL, "key")
		c.HTTPClient = server.Client()

		_, err := c.GetCase(context.Background(), "01234567")
		if err == nil {
			t.Fatal("a 2xx with isError:true must be an error")
		}
	})

	// A healthy response must still succeed: the check must not reject
	// everything.
	t.Run("success is unaffected", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"status":"Success","isError":false,"data":[]}`)
		}))
		defer server.Close()

		c := NewClientWithKey("tok", server.URL, "key")
		c.HTTPClient = server.Client()

		if _, err := c.ListCases(context.Background(), false); err != nil {
			t.Errorf("a genuinely empty list is not an error: %v", err)
		}
	})
}

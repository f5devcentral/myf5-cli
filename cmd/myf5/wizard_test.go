package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestDetectSystemTimezone(t *testing.T) {
	tz := detectSystemTimezone()
	if !strings.HasPrefix(tz, "(GMT ") {
		t.Errorf("expected timezone starting with '(GMT ', got %q", tz)
	}
}

func TestRunCaseWizard_DryRunFlow(t *testing.T) {
	// Simulated inputs for the wizard steps:
	// Step 1: Option 3 (skip auto-detect)
	// Step 2: Product Family (accept default BIG-IP)
	//         Product (accept default BIG-IP LTM)
	//         Version (accept default 17.5.1)
	//         Serial Number ("test-serial-12345")
	// Step 3: Priority (4)
	//         Reason (1 - General Problem)
	//         Subject ("BGP peer session flapping")
	//         Description ("Detailed logs and core dump captured")
	// Step 4: Email ("user@example.com")
	//         Contact Method (Email)
	//         Timezone (default)
	//         Hostname ("bigip-edge.corp.net")
	//         Ticket Ref ("INC-9999")
	// Step 5: Action (d - dry run)
	//         Submit after dry run? (n - no)
	inputLines := []string{
		"3",                                    // Step 1: skip auto-detect
		"",                                     // Step 2: Product family (default)
		"",                                     // Product (default)
		"",                                     // Version (default)
		"test-serial-12345",                    // Serial number
		"4",                                    // Step 3: Priority 4
		"1",                                    // Reason 1
		"BGP peer session flapping",            // Subject
		"Detailed logs and core dump captured", // Description
		"user@example.com",                     // Step 4: Email
		"",                                     // Contact Method (default)
		"",                                     // Timezone (default)
		"bigip-edge.corp.net",                  // Hostname
		"INC-9999",                             // Ticket Ref
		"d",                                    // Step 5: Dry Run
		"n",                                    // Submit now? No
	}

	input := strings.Join(inputLines, "\n") + "\n"
	in := strings.NewReader(input)
	var out bytes.Buffer

	err := RunCaseWizard(context.Background(), in, &out, nil)
	if err != nil {
		t.Fatalf("wizard returned unexpected error: %v", err)
	}

	outStr := out.String()
	if !strings.Contains(outStr, "MyF5 Support Case Wizard") {
		t.Errorf("expected wizard header in output")
	}
	if !strings.Contains(outStr, "Executing Dry Run validation...") {
		t.Errorf("expected dry run execution message in output")
	}
	if !strings.Contains(outStr, "All required fields present and valid.") {
		t.Errorf("expected required fields validation confirmation")
	}
	if !strings.Contains(outStr, "Dry run complete. No case was created.") {
		t.Errorf("expected safe exit message")
	}
}

func TestRunCaseWizard_CancelFlow(t *testing.T) {
	inputLines := []string{
		"3",                           // Step 1: skip auto-detect
		"",                            // Step 2: Product family (default)
		"",                            // Product (default)
		"",                            // Version (default)
		"sn-999999",                   // Serial number
		"4",                           // Step 3: Priority 4
		"1",                           // Reason 1
		"Config sync failed",          // Subject
		"Error during tmsh mcpd sync", // Description
		"admin@example.com",           // Step 4: Email
		"",                            // Contact Method (default)
		"",                            // Timezone (default)
		"",                            // Hostname
		"",                            // Ticket Ref
		"c",                           // Step 5: Cancel
	}

	input := strings.Join(inputLines, "\n") + "\n"
	in := strings.NewReader(input)
	var out bytes.Buffer

	err := RunCaseWizard(context.Background(), in, &out, nil)
	if err != nil {
		t.Fatalf("wizard returned unexpected error: %v", err)
	}

	outStr := out.String()
	if !strings.Contains(outStr, "Case creation cancelled. No case was created.") {
		t.Errorf("expected cancellation message, got:\n%s", outStr)
	}
}

func TestRunCaseWizard_NGINXSubscriptionFlow(t *testing.T) {
	inputLines := []string{
		"3",                       // Step 1: skip auto-detect
		"NGINX",                   // Step 2: Product family
		"NGINX Plus",              // Product
		"R32",                     // Version
		"A-S00038458",             // Subscription ID
		"2",                       // Step 3: Priority 2 (Site At Risk)
		"3",                       // Reason 3 (Software Bug)
		"HTTP/3 panic on reload",  // Subject
		"",                        // Description (reuse subject)
		"nginx-admin@example.com", // Step 4: Email
		"",                        // Contact Method (default)
		"",                        // Timezone (default)
		"ingress-1.prod",          // Hostname
		"",                        // Ticket Ref
		"d",                       // Step 5: Dry Run
		"n",                       // Submit now? No
	}

	input := strings.Join(inputLines, "\n") + "\n"
	in := strings.NewReader(input)
	var out bytes.Buffer

	err := RunCaseWizard(context.Background(), in, &out, nil)
	if err != nil {
		t.Fatalf("wizard returned unexpected error: %v", err)
	}

	outStr := out.String()
	if !strings.Contains(outStr, "\"subscriptionId\": \"A-S00038458\"") {
		t.Errorf("expected subscriptionId in dry run payload, got:\n%s", outStr)
	}
	if !strings.Contains(outStr, "\"productFamily\": \"NGINX\"") {
		t.Errorf("expected productFamily NGINX in dry run payload, got:\n%s", outStr)
	}
	if !strings.Contains(outStr, "All required fields present and valid.") {
		t.Errorf("expected payload validation confirmation")
	}
}

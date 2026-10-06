package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
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

func TestRunCaseWizard_ReviewEditFieldFlow(t *testing.T) {
	inputLines := []string{
		"3",                                    // Step 1: skip auto-detect
		"",                                     // Step 2: Product family (default)
		"",                                     // Product (default)
		"",                                     // Version (default)
		"test-serial-12345",                    // Serial number
		"4",                                    // Step 3: Priority 4
		"1",                                    // Reason 1
		"Initial typo subject",                 // Subject (accidental typo)
		"Detailed logs and core dump captured", // Description
		"user@example.com",                     // Step 4: Email
		"",                                     // Contact Method (default)
		"",                                     // Timezone (default)
		"bigip-edge.corp.net",                  // Hostname
		"INC-9999",                             // Ticket Ref
		"p",                                    // Step 5: Preview description
		"",                                     // Enter to return from preview
		"7",                                    // Step 5: Direct edit field 7 (Subject)
		"Corrected BGP flapping subject",       // New Subject value
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
	if !strings.Contains(outStr, "Corrected BGP flapping subject") {
		t.Errorf("expected updated subject in output, got:\n%s", outStr)
	}
	if strings.Contains(outStr, "\"subject\": \"Initial typo subject\"") {
		t.Errorf("found old typo subject in dry run payload, should have been replaced")
	}
}

func TestRunCaseWizard_MultilineDescriptionFlow(t *testing.T) {
	inputLines := []string{
		"3",                                 // Step 1: skip auto-detect
		"",                                  // Step 2: Product family (default)
		"",                                  // Product (default)
		"",                                  // Version (default)
		"test-serial-12345",                 // Serial number
		"4",                                 // Step 3: Priority 4
		"1",                                 // Reason 1
		"HA Failover issue",                 // Subject
		":m",                                // Description: enter multiline mode
		"Line 1: High CPU observed on TMM0", // Multiline line 1
		"Line 2: Cluster state degraded to Standby", // Multiline line 2
		".",                // Multiline terminator
		"user@example.com", // Step 4: Email
		"",                 // Contact Method (default)
		"",                 // Timezone (default)
		"",                 // Hostname
		"",                 // Ticket Ref
		"d",                // Step 5: Dry Run
		"n",                // Submit now? No
	}

	input := strings.Join(inputLines, "\n") + "\n"
	in := strings.NewReader(input)
	var out bytes.Buffer

	err := RunCaseWizard(context.Background(), in, &out, nil)
	if err != nil {
		t.Fatalf("wizard returned unexpected error: %v", err)
	}

	outStr := out.String()
	if !strings.Contains(outStr, "Line 1: High CPU observed on TMM0") || !strings.Contains(outStr, "Line 2: Cluster state degraded to Standby") {
		t.Errorf("expected multiline description in payload, got:\n%s", outStr)
	}
}

func TestPromptMultilineBuffer_Commands(t *testing.T) {
	inputLines := []string{
		"First line",
		"Mistake line",
		":p",
		":u",
		"Replaced second line",
		":done",
	}
	input := strings.Join(inputLines, "\n") + "\n"
	in := bufio.NewReader(strings.NewReader(input))
	var out bytes.Buffer

	result, err := promptMultilineBuffer(in, &out, "Test Notes", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := "First line\nReplaced second line"
	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
	outStr := out.String()
	if !strings.Contains(outStr, "[undo] Removed line 2") {
		t.Errorf("expected undo output in %s", outStr)
	}
	if !strings.Contains(outStr, "Current Buffer (2 line(s))") {
		t.Errorf("expected show/preview output in %s", outStr)
	}
}

func TestPromptMultilineBuffer_ClearAndCancel(t *testing.T) {
	// Test :c (clear)
	inputLines := []string{
		"Line to be cleared",
		":c",
		"Fresh line after clear",
		".",
	}
	in := bufio.NewReader(strings.NewReader(strings.Join(inputLines, "\n") + "\n"))
	var out bytes.Buffer
	result, err := promptMultilineBuffer(in, &out, "Test Notes", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "Fresh line after clear" {
		t.Errorf("got %q, want 'Fresh line after clear'", result)
	}

	// Test :b (back / cancel)
	inBack := bufio.NewReader(strings.NewReader(":b\n"))
	var outBack bytes.Buffer
	_, err = promptMultilineBuffer(inBack, &outBack, "Test Notes", "")
	if !errors.Is(err, ErrWizardBack) {
		t.Errorf("expected ErrWizardBack, got %v", err)
	}
}

func TestRunCaseWizard_StepBackNavigation(t *testing.T) {
	// Tests bi-directional field and step navigation:
	// Step 1: Skip (3)
	// Step 2: Accept defaults, enter serial
	// Step 3: Enter Priority (4), Reason (1), Subject ("Initial subject"),
	//         at Description prompt type ":b" to backtrack to Subject!
	//         at Subject prompt enter "Corrected subject",
	//         at Description prompt enter "Valid description"
	// Step 4: at Email prompt type ":b" to backtrack across steps back to Step 3 Description!
	// Step 3: at Description prompt enter "Final revised description"
	// Step 4: enter Email ("testuser@example.com"), accept defaults for rest
	// Step 5: at review prompt type "b" to backtrack to Step 4!
	// Step 4: accept defaults
	// Step 5: Dry Run ("d"), then exit without submitting ("n")
	inputLines := []string{
		"3",                         // Step 1: skip auto-detect
		"",                          // Step 2: Product family (default)
		"",                          // Product (default)
		"",                          // Version (default)
		"test-serial-nav",           // Serial number
		"4",                         // Step 3: Priority 4
		"1",                         // Reason 1
		"Initial subject",           // Subject
		":b",                        // Description: backtrack to Subject!
		"Corrected subject",         // Subject (corrected)
		"Valid description",         // Description
		":b",                        // Step 4: Email prompt: backtrack to Step 3 Description!
		"Final revised description", // Description (final revised)
		"testuser@example.com",      // Step 4: Email
		"",                          // Contact Method (default)
		"",                          // Timezone (default)
		"",                          // Hostname (default)
		"",                          // Ticket Ref (default)
		"b",                         // Step 5: backtrack to Step 4!
		"",                          // Step 4: Email (keep default testuser@example.com)
		"",                          // Contact Method (default)
		"",                          // Timezone (default)
		"",                          // Hostname (default)
		"",                          // Ticket Ref (default)
		"d",                         // Step 5: Dry Run
		"n",                         // Submit now? No
	}

	input := strings.Join(inputLines, "\n") + "\n"
	in := strings.NewReader(input)
	var out bytes.Buffer

	err := RunCaseWizard(context.Background(), in, &out, nil)
	if err != nil {
		t.Fatalf("wizard returned unexpected error: %v", err)
	}

	outStr := out.String()
	if !strings.Contains(outStr, "\"subject\": \"Corrected subject\"") {
		t.Errorf("expected corrected subject in dry run payload, got:\n%s", outStr)
	}
	if !strings.Contains(outStr, "\"description\": \"Final revised description\"") {
		t.Errorf("expected final revised description in dry run payload, got:\n%s", outStr)
	}
	if !strings.Contains(outStr, "\"caseOwnerEmail\": \"testuser@example.com\"") {
		t.Errorf("expected email in dry run payload, got:\n%s", outStr)
	}
	if strings.Contains(outStr, "\"subject\": \"Initial subject\"") {
		t.Errorf("found discarded initial subject in dry run payload")
	}
}

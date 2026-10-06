package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"myf5-cli/pkg/auth"
	"myf5-cli/pkg/bigip"
	"myf5-cli/pkg/cases"
)

// CaseWizardOptions holds pre-populated defaults for the support case wizard.
type CaseWizardOptions struct {
	ProductFamily        string
	Product              string
	Version              string
	SerialNumber         string
	SubscriptionID       string
	Hostname             string
	Subject              string
	Description          string
	Priority             string
	ReasonForContact     string
	ContactEmail         string
	ContactMethod        string
	Timezone             string
	CustomerTicketNumber string
}

// detectSystemTimezone returns an F5-compatible timezone string, e.g. "(GMT -05:00) EST".
func detectSystemTimezone() string {
	zoneName, offsetSecs := time.Now().Zone()
	hours := offsetSecs / 3600
	mins := (offsetSecs % 3600) / 60
	sign := "+"
	if hours < 0 {
		sign = "-"
		hours = -hours
	}
	return fmt.Sprintf("(GMT %s%02d:%02d) %s", sign, hours, mins, zoneName)
}

// readWizardLine reads a trimmed line from reader.
func readWizardLine(reader *bufio.Reader) (string, error) {
	line, err := reader.ReadString('\n')
	trimmed := strings.TrimSpace(line)
	if err != nil {
		if errors.Is(err, io.EOF) && trimmed != "" {
			return trimmed, nil
		}
		return trimmed, err
	}
	return trimmed, nil
}

// promptWithDefault prompts the user with an optional default value.
func promptWithDefault(reader *bufio.Reader, out io.Writer, prompt, def string) (string, error) {
	if def != "" {
		fmt.Fprintf(out, "%s [%s]: ", prompt, def)
	} else {
		fmt.Fprintf(out, "%s: ", prompt)
	}
	val, err := readWizardLine(reader)
	if err != nil {
		if errors.Is(err, io.EOF) && def != "" {
			return def, nil
		}
		return "", err
	}
	if val == "" {
		return def, nil
	}
	return val, nil
}

// promptRequired prompts until a non-empty string is provided or EOF occurs.
func promptRequired(reader *bufio.Reader, out io.Writer, prompt, def string) (string, error) {
	for {
		val, err := promptWithDefault(reader, out, prompt, def)
		if err != nil {
			return "", err
		}
		if val != "" {
			return val, nil
		}
		fmt.Fprintln(out, "  [!] This field is required by the MyF5 API. Please enter a value.")
	}
}

// RunCaseWizard guides the user interactively through creating a support case.
func RunCaseWizard(ctx context.Context, in io.Reader, out io.Writer, client *cases.Client) error {
	reader := bufio.NewReader(in)

	fmt.Fprintln(out, "========================================================================")
	fmt.Fprintln(out, "                     MyF5 Support Case Wizard")
	fmt.Fprintln(out, "========================================================================")
	fmt.Fprintln(out, "This wizard guides you through collecting details and opening a support")
	fmt.Fprintln(out, "case with F5 Support. You will have a chance to dry-run and review")
	fmt.Fprintln(out, "the payload before any live ticket is submitted.")
	fmt.Fprintln(out)

	opts := CaseWizardOptions{
		ProductFamily:    "BIG-IP",
		Product:          "BIG-IP LTM",
		Version:          "17.5.1",
		Priority:         cases.PriorityGeneralAssistance,
		ReasonForContact: "General Problem",
		ContactMethod:    "Email",
		Timezone:         detectSystemTimezone(),
	}

	// Try reading email from cached credentials or profile
	if creds, err := auth.ResolveCredentials("default"); err == nil && creds.ClientID != "" {
		// ClientID is often a UUID or service account, but if user set an email env:
		if envEmail := os.Getenv("MYF5_EMAIL"); envEmail != "" {
			opts.ContactEmail = envEmail
		}
	}

	// ------------------------------------------------------------------------
	// Step 1: Hardware & Diagnostic Auto-Detection
	// ------------------------------------------------------------------------
	fmt.Fprintln(out, "[Step 1/5] Appliance Metadata & Diagnostic Auto-Detection")
	fmt.Fprintln(out, "Auto-detection inspects an appliance or QKView archive to populate:")
	fmt.Fprintln(out, "  - Product Family, Product Name, and TMOS Version")
	fmt.Fprintln(out, "  - Serial Number or License Registration Key")
	fmt.Fprintln(out, "  - Hostname and provisioned modules")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "How would you like to proceed?")
	fmt.Fprintln(out, "  1) Connect to a live BIG-IP / BIG-IQ host (via REST/SSH)")
	fmt.Fprintln(out, "  2) Inspect a local QKView archive (.qkview file)")
	fmt.Fprintln(out, "  3) Skip auto-detection (enter details manually)")

	detectChoice, err := promptWithDefault(reader, out, "Select option [1-3]", "3")
	if err != nil {
		return err
	}

	switch detectChoice {
	case "1":
		host, err := promptWithDefault(reader, out, "Enter appliance Host or IP", "")
		if err == nil && host != "" {
			user, _ := promptWithDefault(reader, out, "Appliance username", "admin")
			fmt.Fprintf(out, "Connecting to %s to extract system metadata...\n", host)
			info := getSystemInfoTarget(ctx, host, "BIG-IP", user, 0, "", "auto", "", "")
			if info != nil {
				info.PopulateCaseAttributes()
				if sn := info.PreferredSerialNumber(); sn != "" {
					opts.SerialNumber = sn
				}
				if info.Version != "" {
					opts.Version = info.Version
				}
				if info.Hostname != "" {
					opts.Hostname = info.Hostname
				}
				if prod, ok := info.CaseAttributes["product"]; ok && prod != "" {
					opts.Product = prod
				}
				fmt.Fprintln(out, "\n✓ Successfully extracted appliance details:")
				fmt.Fprintf(out, "  Product Family : %s\n", opts.ProductFamily)
				fmt.Fprintf(out, "  Product        : %s\n", opts.Product)
				fmt.Fprintf(out, "  TMOS Version   : %s\n", opts.Version)
				fmt.Fprintf(out, "  Serial Number  : %s\n", opts.SerialNumber)
				if opts.Hostname != "" {
					fmt.Fprintf(out, "  Hostname       : %s\n", opts.Hostname)
				}
				fmt.Fprintln(out)
			}
		}

	case "2":
		path, err := promptWithDefault(reader, out, "Path to .qkview file", "")
		if err == nil && path != "" {
			fmt.Fprintf(out, "Inspecting QKView archive %s...\n", path)
			info, err := bigip.InspectQKView(path)
			if err != nil {
				fmt.Fprintf(out, "[!] Failed to inspect QKView: %v\nProceeding with manual entry.\n\n", err)
			} else {
				info.PopulateCaseAttributes()
				if sn := info.PreferredSerialNumber(); sn != "" {
					opts.SerialNumber = sn
				}
				if info.Version != "" {
					opts.Version = info.Version
				}
				if info.Hostname != "" {
					opts.Hostname = info.Hostname
				}
				if prod, ok := info.CaseAttributes["product"]; ok && prod != "" {
					opts.Product = prod
				}
				fmt.Fprintln(out, "\n✓ Successfully extracted QKView diagnostic details:")
				fmt.Fprintf(out, "  Product Family : %s\n", opts.ProductFamily)
				fmt.Fprintf(out, "  Product        : %s\n", opts.Product)
				fmt.Fprintf(out, "  TMOS Version   : %s\n", opts.Version)
				fmt.Fprintf(out, "  Serial Number  : %s\n", opts.SerialNumber)
				if opts.Hostname != "" {
					fmt.Fprintf(out, "  Hostname       : %s\n", opts.Hostname)
				}
				fmt.Fprintln(out)
			}
		}
	}

	// ------------------------------------------------------------------------
	// Step 2: Product & Entitlement Details
	// ------------------------------------------------------------------------
	fmt.Fprintln(out, "[Step 2/5] Product & Entitlement Details")
	opts.ProductFamily, err = promptRequired(reader, out, "Product Family (e.g. BIG-IP, BIG-IQ, NGINX)", opts.ProductFamily)
	if err != nil {
		return err
	}
	opts.Product, err = promptRequired(reader, out, "Product Name (e.g. BIG-IP LTM, BIG-IP DNS)", opts.Product)
	if err != nil {
		return err
	}
	opts.Version, err = promptRequired(reader, out, "Product Version (e.g. 17.5.1)", opts.Version)
	if err != nil {
		return err
	}

	isSubscription := strings.EqualFold(opts.ProductFamily, "NGINX") || strings.EqualFold(opts.ProductFamily, "Aspen Mesh")
	if isSubscription {
		opts.SubscriptionID, err = promptRequired(reader, out, "Subscription ID (e.g. A-S00038458)", opts.SubscriptionID)
		if err != nil {
			return err
		}
	} else {
		opts.SerialNumber, err = promptRequired(reader, out, "Serial Number or RegKey (last 7 chars)", opts.SerialNumber)
		if err != nil {
			return err
		}
	}
	fmt.Fprintln(out)

	// ------------------------------------------------------------------------
	// Step 3: Issue Severity & Problem Description
	// ------------------------------------------------------------------------
	fmt.Fprintln(out, "[Step 3/5] Issue Priority & Problem Description")
	fmt.Fprintln(out, "Select Case Priority:")
	fmt.Fprintln(out, "  1) 1 - Site Down            (Critical: production network down / business halt)")
	fmt.Fprintln(out, "  2) 2 - Site At Risk         (High: redundant pair failed / imminent outage)")
	fmt.Fprintln(out, "  3) 3 - Performance Degraded (Medium: degraded throughput / viable workaround)")
	fmt.Fprintln(out, "  4) 4 - General Assistance   (Low: questions / non-urgent config help)")

	prioChoice, err := promptWithDefault(reader, out, "Select Priority [1-4]", "4")
	if err != nil {
		return err
	}
	switch prioChoice {
	case "1":
		opts.Priority = cases.PrioritySiteDown
	case "2":
		opts.Priority = cases.PrioritySiteAtRisk
	case "3":
		opts.Priority = cases.PriorityPerformanceDegraded
	case "4":
		opts.Priority = cases.PriorityGeneralAssistance
	default:
		if strings.TrimSpace(prioChoice) != "" {
			opts.Priority = prioChoice
		}
	}

	fmt.Fprintln(out, "\nSelect Reason for Contact:")
	fmt.Fprintln(out, "  1) General Problem")
	fmt.Fprintln(out, "  2) Configuration Assistance")
	fmt.Fprintln(out, "  3) Software Bug / Issue")
	fmt.Fprintln(out, "  4) Hardware Issue")
	fmt.Fprintln(out, "  5) Upgrade Assistance")

	reasonChoice, err := promptWithDefault(reader, out, "Select Reason [1-5]", "1")
	if err != nil {
		return err
	}
	switch reasonChoice {
	case "1":
		opts.ReasonForContact = "General Problem"
	case "2":
		opts.ReasonForContact = "Configuration Assistance"
	case "3":
		opts.ReasonForContact = "Software Bug"
	case "4":
		opts.ReasonForContact = "Hardware Issue"
	case "5":
		opts.ReasonForContact = "Upgrade Assistance"
	default:
		if strings.TrimSpace(reasonChoice) != "" {
			opts.ReasonForContact = reasonChoice
		}
	}

	opts.Subject, err = promptRequired(reader, out, "One-Line Problem Summary (Subject)", opts.Subject)
	if err != nil {
		return err
	}

	opts.Description, err = promptWithDefault(reader, out, "Detailed Problem Description (press Enter to reuse Subject)", opts.Subject)
	if err != nil {
		return err
	}
	if opts.Description == "" {
		opts.Description = opts.Subject
	}
	fmt.Fprintln(out)

	// ------------------------------------------------------------------------
	// Step 4: Contact & Environmental Information
	// ------------------------------------------------------------------------
	fmt.Fprintln(out, "[Step 4/5] Contact & Environmental Information")
	opts.ContactEmail, err = promptRequired(reader, out, "Case Owner Email", opts.ContactEmail)
	if err != nil {
		return err
	}
	opts.ContactMethod, err = promptWithDefault(reader, out, "Preferred Contact Method (Email, Phone, Web)", opts.ContactMethod)
	if err != nil {
		return err
	}
	opts.Timezone, err = promptWithDefault(reader, out, "Timezone", opts.Timezone)
	if err != nil {
		return err
	}
	opts.Hostname, _ = promptWithDefault(reader, out, "Affected Hostname (optional)", opts.Hostname)
	opts.CustomerTicketNumber, _ = promptWithDefault(reader, out, "Internal Ticket Reference (optional)", opts.CustomerTicketNumber)
	fmt.Fprintln(out)

	// Construct final API payload
	payload := map[string]interface{}{
		"productFamily":          opts.ProductFamily,
		"product":                opts.Product,
		"version":                opts.Version,
		"subject":                opts.Subject,
		"description":            opts.Description,
		"priority":               opts.Priority,
		"reasonForContact":       opts.ReasonForContact,
		"caseOwnerEmail":         opts.ContactEmail,
		"preferredContactMethod": opts.ContactMethod,
		"timezone":               opts.Timezone,
	}

	if isSubscription {
		payload["subscriptionId"] = opts.SubscriptionID
	} else {
		payload["serialNumber"] = opts.SerialNumber
	}
	if opts.Hostname != "" {
		payload["hostName"] = opts.Hostname
	}
	if opts.CustomerTicketNumber != "" {
		payload["customerTicketNumber"] = opts.CustomerTicketNumber
	}

	// ------------------------------------------------------------------------
	// Step 5: Summary, Review & Safety Gate
	// ------------------------------------------------------------------------
	fmt.Fprintln(out, "========================================================================")
	fmt.Fprintln(out, "                      Case Review & Confirmation")
	fmt.Fprintln(out, "========================================================================")
	fmt.Fprintf(out, "  Product Family  : %s\n", opts.ProductFamily)
	fmt.Fprintf(out, "  Product         : %s\n", opts.Product)
	fmt.Fprintf(out, "  Version         : %s\n", opts.Version)
	if isSubscription {
		fmt.Fprintf(out, "  Subscription ID : %s\n", opts.SubscriptionID)
	} else {
		fmt.Fprintf(out, "  Serial Number   : %s\n", opts.SerialNumber)
	}
	fmt.Fprintf(out, "  Priority        : %s\n", opts.Priority)
	fmt.Fprintf(out, "  Reason          : %s\n", opts.ReasonForContact)
	fmt.Fprintf(out, "  Subject         : %s\n", opts.Subject)
	fmt.Fprintf(out, "  Owner Email     : %s\n", opts.ContactEmail)
	fmt.Fprintf(out, "  Contact Method  : %s\n", opts.ContactMethod)
	fmt.Fprintf(out, "  Timezone        : %s\n", opts.Timezone)
	if opts.Hostname != "" {
		fmt.Fprintf(out, "  Hostname        : %s\n", opts.Hostname)
	}
	if opts.CustomerTicketNumber != "" {
		fmt.Fprintf(out, "  Ticket Ref      : %s\n", opts.CustomerTicketNumber)
	}
	fmt.Fprintln(out, "========================================================================")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "SAFETY NOTICE:")
	fmt.Fprintln(out, "  Submitting this case will immediately create a real, visible support")
	fmt.Fprintln(out, "  ticket with F5 Support under your corporate company account.")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Available actions:")
	fmt.Fprintln(out, "  [d] Dry Run   - Validate payload against MyF5 requirements (Safe, Recommended)")
	fmt.Fprintln(out, "  [s] Submit    - Submit live case to F5 Support")
	fmt.Fprintln(out, "  [c] Cancel    - Abort without submitting (Default)")

	action, err := promptWithDefault(reader, out, "Action [d/s/C]", "c")
	if err != nil {
		return err
	}

	action = strings.ToLower(strings.TrimSpace(action))
	if action == "d" || action == "dry" || action == "dry-run" {
		fmt.Fprintln(out, "\nExecuting Dry Run validation...")
		jsonBytes, _ := json.MarshalIndent(payload, "", "  ")
		fmt.Fprintln(out, string(jsonBytes))

		// Check required fields locally
		required := cases.RequiredCreateFields()
		required = append(required, cases.EntitlementFieldFor(opts.ProductFamily))
		var missing []string
		for _, f := range required {
			if v, ok := payload[f].(string); !ok || strings.TrimSpace(v) == "" {
				missing = append(missing, f)
			}
		}

		if len(missing) > 0 {
			fmt.Fprintf(out, "\n[ERROR] Incomplete payload: missing required fields: %s\n", strings.Join(missing, ", "))
			return fmt.Errorf("missing required fields: %s", strings.Join(missing, ", "))
		}

		fmt.Fprintln(out, "\n✓ All required fields present and valid.")
		confirm, _ := promptWithDefault(reader, out, "Would you like to submit this case now to F5 Support? [y/N]", "n")
		if !strings.EqualFold(confirm, "y") && !strings.EqualFold(confirm, "yes") {
			fmt.Fprintln(out, "Dry run complete. No case was created.")
			return nil
		}
		action = "s"
	}

	if action == "s" || action == "submit" {
		if client == nil {
			fmt.Fprintln(out, "[DRY RUN] Wizard finished in offline test mode; skipping remote API call.")
			return nil
		}
		fmt.Fprintln(out, "\nSubmitting case to MyF5 Support...")
		resp, err := client.CreateCase(ctx, payload)
		if err != nil {
			var missingErr *cases.ErrMissingCreateFields
			if errors.As(err, &missingErr) {
				return fmt.Errorf("missing required fields: %w", err)
			}
			return fmt.Errorf("failed to create case: %w", err)
		}
		if resp.IsError {
			msg := resp.Message
			if msg == "" {
				msg = "API reported an error without a message"
			}
			return fmt.Errorf("F5 Support API error: %s", msg)
		}

		fmt.Fprintln(out, "\n========================================================================")
		if resp.Data.CaseNumber != "" {
			fmt.Fprintf(out, "✓ Support Case Created Successfully: %s\n", resp.Data.CaseNumber)
		} else {
			fmt.Fprintln(out, "✓ Support Case Created (no case number returned by API)")
		}
		fmt.Fprintln(out, "========================================================================")
		if resp.Data.CaseNumber != "" {
			fmt.Fprintf(out, "View status  : myf5 cases get %s\n", resp.Data.CaseNumber)
			fmt.Fprintf(out, "Add comment  : myf5 cases comment %s \"notes\"\n", resp.Data.CaseNumber)
		}
		return nil
	}

	fmt.Fprintln(out, "Case creation cancelled. No case was created.")
	return nil
}

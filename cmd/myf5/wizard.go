package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
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

// ErrWizardBack signals that the user requested to navigate back to the previous step or prompt.
var ErrWizardBack = errors.New("wizard: back to previous step")

// isBackCommand reports whether an input string indicates a back request.
func isBackCommand(s string) bool {
	s = strings.TrimSpace(strings.ToLower(s))
	return s == ":b" || s == ":back" || s == "back" || s == "b"
}

// promptNavField prompts with an optional default value, returning ErrWizardBack if user inputs a back command.
func promptNavField(reader *bufio.Reader, out io.Writer, prompt, def string) (string, error) {
	val, err := promptWithDefault(reader, out, prompt, def)
	if err != nil {
		return "", err
	}
	if isBackCommand(val) {
		return "", ErrWizardBack
	}
	return val, nil
}

// promptRequiredNavField prompts until a non-empty value is provided, returning ErrWizardBack on back commands.
func promptRequiredNavField(reader *bufio.Reader, out io.Writer, prompt, def string) (string, error) {
	for {
		val, err := promptNavField(reader, out, prompt, def)
		if err != nil {
			return "", err
		}
		if val != "" {
			return val, nil
		}
		fmt.Fprintln(out, "  [!] This field is required by the MyF5 API. Please enter a value.")
	}
}

// editInExternalEditor opens the user's preferred text editor to compose or edit multiline text.
func editInExternalEditor(initialContent string) (string, error) {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = os.Getenv("VISUAL")
	}
	if editor == "" {
		if runtime.GOOS == "windows" {
			editor = "notepad.exe"
		} else {
			for _, ed := range []string{"nano", "vim", "vi"} {
				if p, err := exec.LookPath(ed); err == nil && p != "" {
					editor = ed
					break
				}
			}
		}
	}
	if editor == "" {
		return "", errors.New("no text editor found in environment ($EDITOR or nano/vim/notepad)")
	}

	tmpFile, err := os.CreateTemp("", "myf5-case-*.txt")
	if err != nil {
		return "", fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if initialContent != "" {
		if _, err := tmpFile.WriteString(initialContent); err != nil {
			tmpFile.Close()
			return "", err
		}
	}
	tmpFile.Close()

	cmd := exec.Command(editor, tmpPath)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("editor failed: %w", err)
	}

	data, err := os.ReadFile(tmpPath)
	if err != nil {
		return "", fmt.Errorf("failed to read temp file: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

// promptMultilineBuffer provides an interactive multiline buffer editor.
// It prevents accidental loss from premature Enter presses and supports:
//   - Line numbers [1], [2], ...
//   - ':undo' or ':u' to remove the last line
//   - ':show' or ':p' to display the buffer contents
//   - ':edit' or ':e' to open the buffer in an external editor ($EDITOR / nano / vim)
//   - ':clear' or ':c' to wipe the buffer
//   - ':back' or ':b' to cancel buffer entry and return ErrWizardBack
//   - '.' on a blank line, or ':done' to finish input
func promptMultilineBuffer(reader *bufio.Reader, out io.Writer, title string, initialText string) (string, error) {
	fmt.Fprintf(out, "\n--- %s ---\n", title)
	fmt.Fprintln(out, "Enter notes/description line by line.")
	fmt.Fprintln(out, "Finish input: type '.' on an empty line or ':done'")
	fmt.Fprintln(out, "Commands:")
	fmt.Fprintln(out, "  :u / :undo   - Undo / remove the last line entered")
	fmt.Fprintln(out, "  :p / :show   - Preview current buffer with line numbers")
	fmt.Fprintln(out, "  :e / :edit   - Open buffer in external editor ($EDITOR / nano / vim / notepad)")
	fmt.Fprintln(out, "  :c / :clear  - Clear all entered lines")
	fmt.Fprintln(out, "  :b / :back   - Cancel buffer entry and return to previous field")
	fmt.Fprintln(out, "--------------------------------------------------------")

	var lines []string
	if strings.TrimSpace(initialText) != "" {
		for _, l := range strings.Split(initialText, "\n") {
			lines = append(lines, l)
		}
		fmt.Fprintf(out, "Pre-populated buffer (%d line(s)):\n", len(lines))
		for i, l := range lines {
			fmt.Fprintf(out, "  [%d] %s\n", i+1, l)
		}
		fmt.Fprintln(out, "(Type '.' to keep current lines, or continue adding lines below:)")
	}

	for {
		fmt.Fprintf(out, "[%d] > ", len(lines)+1)
		line, err := reader.ReadString('\n')
		lineTrimmed := strings.TrimRight(line, "\r\n")

		if err != nil {
			if errors.Is(err, io.EOF) {
				if lineTrimmed != "" {
					cmd := strings.TrimSpace(lineTrimmed)
					if cmd == "." || cmd == ":done" {
						break
					}
					lines = append(lines, lineTrimmed)
				}
				break
			}
			return "", err
		}

		trimmedCmd := strings.TrimSpace(lineTrimmed)
		switch {
		case trimmedCmd == "." || trimmedCmd == ":done":
			goto finish
		case trimmedCmd == ":u" || trimmedCmd == ":undo":
			if len(lines) == 0 {
				fmt.Fprintln(out, "  [!] Buffer is empty; nothing to undo.")
			} else {
				last := lines[len(lines)-1]
				lines = lines[:len(lines)-1]
				fmt.Fprintf(out, "  [undo] Removed line %d: %q\n", len(lines)+1, last)
			}
		case trimmedCmd == ":p" || trimmedCmd == ":show" || trimmedCmd == ":list":
			if len(lines) == 0 {
				fmt.Fprintln(out, "  [buffer is currently empty]")
			} else {
				fmt.Fprintf(out, "--- Current Buffer (%d line(s)) ---\n", len(lines))
				for i, l := range lines {
					fmt.Fprintf(out, "  [%d] %s\n", i+1, l)
				}
				fmt.Fprintln(out, "-----------------------------------")
			}
		case trimmedCmd == ":e" || trimmedCmd == ":edit":
			curText := strings.Join(lines, "\n")
			edited, edErr := editInExternalEditor(curText)
			if edErr != nil {
				fmt.Fprintf(out, "  [!] Could not launch editor: %v\n", edErr)
			} else {
				lines = nil
				if strings.TrimSpace(edited) != "" {
					for _, l := range strings.Split(edited, "\n") {
						lines = append(lines, l)
					}
				}
				fmt.Fprintf(out, "  [editor] Buffer updated (%d line(s) loaded).\n", len(lines))
			}
		case trimmedCmd == ":c" || trimmedCmd == ":clear":
			lines = nil
			fmt.Fprintln(out, "  [buffer cleared]")
		case trimmedCmd == ":b" || trimmedCmd == ":back" || trimmedCmd == ":cancel":
			return "", ErrWizardBack
		default:
			lines = append(lines, lineTrimmed)
		}
	}

finish:
	content := strings.TrimSpace(strings.Join(lines, "\n"))
	return content, nil
}

// promptMultilineDescription provides robust description input:
// - Direct single-line input
// - Pressing Enter reuses default (e.g. Subject)
// - Entering ':m' launches the interactive multiline buffer editor (with undo, preview, clear)
// - Entering ':e' launches the user's system text editor ($EDITOR / notepad)
// - Entering ':b' or ':back' returns ErrWizardBack to return to previous field
func promptMultilineDescription(reader *bufio.Reader, out io.Writer, def string) (string, error) {
	fmt.Fprintln(out, "Detailed Problem Description:")
	fmt.Fprintln(out, "  (Type description, ':m' for multiline buffer, ':e' for editor, ':b' for back, or Enter to reuse default)")
	if def != "" {
		displayDef := def
		if len(displayDef) > 50 {
			displayDef = displayDef[:47] + "..."
		}
		fmt.Fprintf(out, "  Default: %q\n", displayDef)
	}
	fmt.Fprint(out, "Description: ")

	line, err := reader.ReadString('\n')
	trimmed := strings.TrimRight(line, "\r\n")

	if err != nil && errors.Is(err, io.EOF) && strings.TrimSpace(trimmed) == "" {
		if def != "" {
			return def, nil
		}
		return "", err
	}

	trimmedSpace := strings.TrimSpace(trimmed)
	if trimmedSpace == "" {
		if def != "" {
			return def, nil
		}
		return promptMultilineDescription(reader, out, def)
	}

	if isBackCommand(trimmedSpace) {
		return "", ErrWizardBack
	}

	if trimmedSpace == ":e" || trimmedSpace == ":edit" {
		edited, edErr := editInExternalEditor(def)
		if edErr != nil {
			fmt.Fprintf(out, "  [!] Could not launch editor: %v\n", edErr)
			return promptMultilineDescription(reader, out, def)
		}
		if edited == "" {
			edited = def
		}
		return edited, nil
	}

	if trimmedSpace == ":m" || trimmedSpace == ":multiline" {
		bufContent, bufErr := promptMultilineBuffer(reader, out, "Case Description Buffer", "")
		if bufErr != nil {
			if errors.Is(bufErr, ErrWizardBack) {
				return promptMultilineDescription(reader, out, def)
			}
			return "", bufErr
		}
		if bufContent == "" {
			bufContent = def
		}
		return bufContent, nil
	}

	return trimmed, nil
}

// editWizardField allows re-editing any individual field without restarting the wizard.
func editWizardField(reader *bufio.Reader, out io.Writer, fieldNum int, opts *CaseWizardOptions) error {
	var err error
	switch fieldNum {
	case 1:
		opts.ProductFamily, err = promptRequiredNavField(reader, out, "Product Family (e.g. BIG-IP, BIG-IQ, NGINX)", opts.ProductFamily)
	case 2:
		opts.Product, err = promptRequiredNavField(reader, out, "Product Name (e.g. BIG-IP LTM, BIG-IP DNS)", opts.Product)
	case 3:
		opts.Version, err = promptRequiredNavField(reader, out, "Product Version (e.g. 17.5.1)", opts.Version)
	case 4:
		isSub := strings.EqualFold(opts.ProductFamily, "NGINX") || strings.EqualFold(opts.ProductFamily, "Aspen Mesh")
		if isSub {
			opts.SubscriptionID, err = promptRequiredNavField(reader, out, "Subscription ID (e.g. A-S00038458)", opts.SubscriptionID)
		} else {
			opts.SerialNumber, err = promptRequiredNavField(reader, out, "Serial Number or RegKey (last 7 chars)", opts.SerialNumber)
		}
	case 5:
		fmt.Fprintln(out, "Select Case Priority:")
		fmt.Fprintln(out, "  1) 1 - Site Down            (Critical: production network down)")
		fmt.Fprintln(out, "  2) 2 - Site At Risk         (High: redundant pair failed)")
		fmt.Fprintln(out, "  3) 3 - Performance Degraded (Medium: degraded throughput)")
		fmt.Fprintln(out, "  4) 4 - General Assistance   (Low: non-urgent questions/config)")
		prioChoice, pErr := promptNavField(reader, out, "Select Priority [1-4]", "4")
		if pErr != nil {
			if errors.Is(pErr, ErrWizardBack) {
				return nil
			}
			return pErr
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
	case 6:
		fmt.Fprintln(out, "Select Reason for Contact:")
		fmt.Fprintln(out, "  1) General Problem")
		fmt.Fprintln(out, "  2) Configuration Assistance")
		fmt.Fprintln(out, "  3) Software Bug")
		fmt.Fprintln(out, "  4) Hardware Issue")
		fmt.Fprintln(out, "  5) Upgrade Assistance")
		reasonChoice, rErr := promptNavField(reader, out, "Select Reason [1-5]", "1")
		if rErr != nil {
			if errors.Is(rErr, ErrWizardBack) {
				return nil
			}
			return rErr
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
	case 7:
		opts.Subject, err = promptRequiredNavField(reader, out, "One-Line Problem Summary (Subject)", opts.Subject)
	case 8:
		opts.Description, err = promptMultilineDescription(reader, out, opts.Description)
	case 9:
		opts.ContactEmail, err = promptRequiredNavField(reader, out, "Case Owner Email", opts.ContactEmail)
	case 10:
		opts.ContactMethod, err = promptNavField(reader, out, "Preferred Contact Method (Email, Phone, Web)", opts.ContactMethod)
	case 11:
		opts.Timezone, err = promptNavField(reader, out, "Timezone", opts.Timezone)
	case 12:
		opts.Hostname, err = promptNavField(reader, out, "Affected Hostname (optional)", opts.Hostname)
	case 13:
		opts.CustomerTicketNumber, err = promptNavField(reader, out, "Internal Ticket Reference (optional)", opts.CustomerTicketNumber)
	default:
		fmt.Fprintf(out, "Invalid field number %d (must be 1-13)\n", fieldNum)
	}
	if errors.Is(err, ErrWizardBack) {
		return nil
	}
	return err
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
		if envEmail := os.Getenv("MYF5_EMAIL"); envEmail != "" {
			opts.ContactEmail = envEmail
		}
	}

	step := 1
	startField := 1
	for step >= 1 && step <= 5 {
		switch step {
		case 1:
			next, err := runStep1AutoDetection(ctx, reader, out, &opts)
			if err != nil {
				return err
			}
			step = next
			startField = 1
		case 2:
			next, nextField, err := runStep2Entitlement(reader, out, &opts, startField)
			if err != nil {
				return err
			}
			step = next
			startField = nextField
		case 3:
			next, nextField, err := runStep3Details(reader, out, &opts, startField)
			if err != nil {
				return err
			}
			step = next
			startField = nextField
		case 4:
			next, nextField, err := runStep4Contact(reader, out, &opts, startField)
			if err != nil {
				return err
			}
			step = next
			startField = nextField
		case 5:
			next, err := runStep5Review(ctx, reader, out, client, &opts)
			if err != nil {
				return err
			}
			if next > 0 && next <= 4 {
				step = next
				startField = 1
				continue
			}
			return nil
		}
	}
	return nil
}

// runStep1AutoDetection handles appliance metadata and QKView diagnostic auto-detection.
func runStep1AutoDetection(ctx context.Context, reader *bufio.Reader, out io.Writer, opts *CaseWizardOptions) (int, error) {
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

	detectChoice, err := promptNavField(reader, out, "Select option [1-3]", "3")
	if err != nil {
		if errors.Is(err, ErrWizardBack) {
			return 1, nil
		}
		return 0, err
	}

	switch detectChoice {
	case "1":
		host, err := promptNavField(reader, out, "Enter appliance Host or IP", "")
		if err == nil && host != "" {
			user, _ := promptNavField(reader, out, "Appliance username", "admin")
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
				if info.Platform != "" {
					opts.Product = fmt.Sprintf("BIG-IP %s", info.Platform)
				}
				fmt.Fprintln(out, "\n✓ Successfully extracted appliance details:")
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
		qkviewPath, err := promptNavField(reader, out, "Enter path to .qkview file", "")
		if err == nil && qkviewPath != "" {
			info, qkErr := bigip.InspectQKView(qkviewPath)
			if qkErr != nil {
				fmt.Fprintf(out, "[!] Failed to inspect QKView: %v\n", qkErr)
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
				if info.Platform != "" {
					opts.Product = fmt.Sprintf("BIG-IP %s", info.Platform)
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
	return 2, nil
}

// runStep2Entitlement prompts for product identity and entitlement identifiers.
func runStep2Entitlement(reader *bufio.Reader, out io.Writer, opts *CaseWizardOptions, startField int) (int, int, error) {
	fmt.Fprintln(out, "[Step 2/5] Product & Entitlement Details")
	fmt.Fprintln(out, "  (Type ':b' at any prompt to go back to previous field)")

	field := startField
	if field < 1 || field > 4 {
		field = 1
	}
	for field <= 4 {
		var err error
		switch field {
		case 1:
			opts.ProductFamily, err = promptRequiredNavField(reader, out, "Product Family (e.g. BIG-IP, BIG-IQ, NGINX)", opts.ProductFamily)
			if errors.Is(err, ErrWizardBack) {
				return 1, 1, nil // go back to Step 1
			}
			if err != nil {
				return 0, 0, err
			}
			field = 2
		case 2:
			opts.Product, err = promptRequiredNavField(reader, out, "Product Name (e.g. BIG-IP LTM, BIG-IP DNS)", opts.Product)
			if errors.Is(err, ErrWizardBack) {
				field = 1 // go back to Product Family
				continue
			}
			if err != nil {
				return 0, 0, err
			}
			field = 3
		case 3:
			opts.Version, err = promptRequiredNavField(reader, out, "Product Version (e.g. 17.5.1)", opts.Version)
			if errors.Is(err, ErrWizardBack) {
				field = 2 // go back to Product Name
				continue
			}
			if err != nil {
				return 0, 0, err
			}
			field = 4
		case 4:
			isSubscription := strings.EqualFold(opts.ProductFamily, "NGINX") || strings.EqualFold(opts.ProductFamily, "Aspen Mesh")
			if isSubscription {
				opts.SubscriptionID, err = promptRequiredNavField(reader, out, "Subscription ID (e.g. A-S00038458)", opts.SubscriptionID)
			} else {
				opts.SerialNumber, err = promptRequiredNavField(reader, out, "Serial Number or RegKey (last 7 chars)", opts.SerialNumber)
			}
			if errors.Is(err, ErrWizardBack) {
				field = 3 // go back to Version
				continue
			}
			if err != nil {
				return 0, 0, err
			}
			field = 5 // complete Step 2
		}
	}
	fmt.Fprintln(out)
	return 3, 1, nil
}

// runStep3Details prompts for priority, reason for contact, subject, and detailed description.
func runStep3Details(reader *bufio.Reader, out io.Writer, opts *CaseWizardOptions, startField int) (int, int, error) {
	fmt.Fprintln(out, "[Step 3/5] Issue Priority & Problem Description")
	fmt.Fprintln(out, "  (Type ':b' at any prompt to go back to previous field)")

	field := startField
	if field < 1 || field > 4 {
		field = 1
	}
	for field <= 4 {
		switch field {
		case 1:
			fmt.Fprintln(out, "Select Case Priority:")
			fmt.Fprintln(out, "  1) 1 - Site Down            (Critical: production network down / business halt)")
			fmt.Fprintln(out, "  2) 2 - Site At Risk         (High: redundant pair failed / imminent outage)")
			fmt.Fprintln(out, "  3) 3 - Performance Degraded (Medium: degraded throughput / viable workaround)")
			fmt.Fprintln(out, "  4) 4 - General Assistance   (Low: questions / non-urgent config help)")

			defPrio := "4"
			switch opts.Priority {
			case cases.PrioritySiteDown:
				defPrio = "1"
			case cases.PrioritySiteAtRisk:
				defPrio = "2"
			case cases.PriorityPerformanceDegraded:
				defPrio = "3"
			case cases.PriorityGeneralAssistance:
				defPrio = "4"
			}

			prioChoice, err := promptNavField(reader, out, "Select Priority [1-4]", defPrio)
			if errors.Is(err, ErrWizardBack) {
				return 2, 4, nil // go back to Step 2, field 4 (Entitlement)
			}
			if err != nil {
				return 0, 0, err
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
			field = 2
		case 2:
			fmt.Fprintln(out, "\nSelect Reason for Contact:")
			fmt.Fprintln(out, "  1) General Problem")
			fmt.Fprintln(out, "  2) Configuration Assistance")
			fmt.Fprintln(out, "  3) Software Bug / Issue")
			fmt.Fprintln(out, "  4) Hardware Issue")
			fmt.Fprintln(out, "  5) Upgrade Assistance")

			defReason := "1"
			switch opts.ReasonForContact {
			case "General Problem":
				defReason = "1"
			case "Configuration Assistance":
				defReason = "2"
			case "Software Bug":
				defReason = "3"
			case "Hardware Issue":
				defReason = "4"
			case "Upgrade Assistance":
				defReason = "5"
			}

			reasonChoice, err := promptNavField(reader, out, "Select Reason [1-5]", defReason)
			if errors.Is(err, ErrWizardBack) {
				field = 1 // go back to Priority
				continue
			}
			if err != nil {
				return 0, 0, err
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
			field = 3
		case 3:
			var err error
			opts.Subject, err = promptRequiredNavField(reader, out, "One-Line Problem Summary (Subject)", opts.Subject)
			if errors.Is(err, ErrWizardBack) {
				field = 2 // go back to Reason
				continue
			}
			if err != nil {
				return 0, 0, err
			}
			field = 4
		case 4:
			descDef := opts.Description
			if descDef == "" {
				descDef = opts.Subject
			}
			var err error
			opts.Description, err = promptMultilineDescription(reader, out, descDef)
			if errors.Is(err, ErrWizardBack) {
				field = 3 // go back to Subject
				continue
			}
			if err != nil {
				return 0, 0, err
			}
			if opts.Description == "" {
				opts.Description = opts.Subject
			}
			field = 5 // complete Step 3
		}
	}
	fmt.Fprintln(out)
	return 4, 1, nil
}

// runStep4Contact prompts for contact details and environment metadata.
func runStep4Contact(reader *bufio.Reader, out io.Writer, opts *CaseWizardOptions, startField int) (int, int, error) {
	fmt.Fprintln(out, "[Step 4/5] Contact & Environmental Information")
	fmt.Fprintln(out, "  (Type ':b' at any prompt to go back to previous field)")

	field := startField
	if field < 1 || field > 5 {
		field = 1
	}
	for field <= 5 {
		var err error
		switch field {
		case 1:
			opts.ContactEmail, err = promptRequiredNavField(reader, out, "Case Owner Email", opts.ContactEmail)
			if errors.Is(err, ErrWizardBack) {
				return 3, 4, nil // go back to Step 3 Description!
			}
			if err != nil {
				return 0, 0, err
			}
			field = 2
		case 2:
			opts.ContactMethod, err = promptNavField(reader, out, "Preferred Contact Method (Email, Phone, Web)", opts.ContactMethod)
			if errors.Is(err, ErrWizardBack) {
				field = 1 // go back to Email
				continue
			}
			if err != nil {
				return 0, 0, err
			}
			field = 3
		case 3:
			opts.Timezone, err = promptNavField(reader, out, "Timezone", opts.Timezone)
			if errors.Is(err, ErrWizardBack) {
				field = 2 // go back to Contact Method
				continue
			}
			if err != nil {
				return 0, 0, err
			}
			field = 4
		case 4:
			opts.Hostname, err = promptNavField(reader, out, "Affected Hostname (optional)", opts.Hostname)
			if errors.Is(err, ErrWizardBack) {
				field = 3 // go back to Timezone
				continue
			}
			if err != nil {
				return 0, 0, err
			}
			field = 5
		case 5:
			opts.CustomerTicketNumber, err = promptNavField(reader, out, "Internal Ticket Reference (optional)", opts.CustomerTicketNumber)
			if errors.Is(err, ErrWizardBack) {
				field = 4 // go back to Hostname
				continue
			}
			if err != nil {
				return 0, 0, err
			}
			field = 6 // complete Step 4
		}
	}
	fmt.Fprintln(out)
	return 5, 1, nil
}

// runStep5Review presents payload review, dry-run safety gates, field re-editing, and submission.
func runStep5Review(ctx context.Context, reader *bufio.Reader, out io.Writer, client *cases.Client, opts *CaseWizardOptions) (int, error) {
	for {
		isSubscription := strings.EqualFold(opts.ProductFamily, "NGINX") || strings.EqualFold(opts.ProductFamily, "Aspen Mesh")

		// Construct API payload
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

		fmt.Fprintln(out, "========================================================================")
		fmt.Fprintln(out, "                      Case Review & Confirmation")
		fmt.Fprintln(out, "========================================================================")
		fmt.Fprintf(out, "  [1]  Product Family  : %s\n", opts.ProductFamily)
		fmt.Fprintf(out, "  [2]  Product         : %s\n", opts.Product)
		fmt.Fprintf(out, "  [3]  Version         : %s\n", opts.Version)
		if isSubscription {
			fmt.Fprintf(out, "  [4]  Subscription ID : %s\n", opts.SubscriptionID)
		} else {
			fmt.Fprintf(out, "  [4]  Serial Number   : %s\n", opts.SerialNumber)
		}
		fmt.Fprintf(out, "  [5]  Priority        : %s\n", opts.Priority)
		fmt.Fprintf(out, "  [6]  Reason          : %s\n", opts.ReasonForContact)
		fmt.Fprintf(out, "  [7]  Subject         : %s\n", opts.Subject)

		descLines := strings.Split(opts.Description, "\n")
		descPreview := descLines[0]
		if len(descPreview) > 50 {
			descPreview = descPreview[:47] + "..."
		}
		if len(descLines) > 1 {
			fmt.Fprintf(out, "  [8]  Description     : %s (%d lines, type 'p' to preview all)\n", descPreview, len(descLines))
		} else {
			fmt.Fprintf(out, "  [8]  Description     : %s\n", descPreview)
		}

		fmt.Fprintf(out, "  [9]  Owner Email     : %s\n", opts.ContactEmail)
		fmt.Fprintf(out, "  [10] Contact Method  : %s\n", opts.ContactMethod)
		fmt.Fprintf(out, "  [11] Timezone        : %s\n", opts.Timezone)
		if opts.Hostname != "" {
			fmt.Fprintf(out, "  [12] Hostname        : %s\n", opts.Hostname)
		} else {
			fmt.Fprintf(out, "  [12] Hostname        : (none)\n")
		}
		if opts.CustomerTicketNumber != "" {
			fmt.Fprintf(out, "  [13] Ticket Ref      : %s\n", opts.CustomerTicketNumber)
		} else {
			fmt.Fprintf(out, "  [13] Ticket Ref      : (none)\n")
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
		fmt.Fprintln(out, "  [e] Edit      - Edit a field (or type field number 1-13)")
		fmt.Fprintln(out, "  [p] Preview   - Preview full Description text")
		fmt.Fprintln(out, "  [c] Cancel    - Abort without submitting (Default)")

		action, err := promptWithDefault(reader, out, "Action [d/s/e/p/C]", "c")
		if err != nil {
			return 0, err
		}

		action = strings.ToLower(strings.TrimSpace(action))

		if action == "b" || action == ":b" || action == "back" || action == ":back" {
			return 4, nil // return to Step 4
		}

		// If user typed a direct field number 1-13
		if num, aErr := strconv.Atoi(action); aErr == nil && num >= 1 && num <= 13 {
			if editErr := editWizardField(reader, out, num, opts); editErr != nil {
				return 0, editErr
			}
			continue
		}

		if action == "p" || action == "preview" {
			fmt.Fprintln(out, "\n------------------- Full Description -------------------")
			fmt.Fprintln(out, opts.Description)
			fmt.Fprintln(out, "--------------------------------------------------------")
			promptWithDefault(reader, out, "Press Enter to return to review", "")
			continue
		}

		if action == "e" || action == "edit" {
			fieldStr, pErr := promptWithDefault(reader, out, "Select field to edit [1-13, or 'b' for back]", "b")
			if pErr != nil {
				return 0, pErr
			}
			if num, aErr := strconv.Atoi(strings.TrimSpace(fieldStr)); aErr == nil && num >= 1 && num <= 13 {
				if editErr := editWizardField(reader, out, num, opts); editErr != nil {
					return 0, editErr
				}
			}
			continue
		}

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
				return 0, fmt.Errorf("missing required fields: %s", strings.Join(missing, ", "))
			}

			fmt.Fprintln(out, "\n✓ All required fields present and valid.")
			confirm, _ := promptWithDefault(reader, out, "Would you like to submit this case now to F5 Support? [y/N]", "n")
			if !strings.EqualFold(confirm, "y") && !strings.EqualFold(confirm, "yes") {
				fmt.Fprintln(out, "Dry run complete. No case was created.")
				return 0, nil
			}
			action = "s"
		}

		if action == "s" || action == "submit" {
			if client == nil {
				fmt.Fprintln(out, "[DRY RUN] Wizard finished in offline test mode; skipping remote API call.")
				return 0, nil
			}
			fmt.Fprintln(out, "\nSubmitting case to MyF5 Support...")
			resp, err := client.CreateCase(ctx, payload)
			if err != nil {
				var missingErr *cases.ErrMissingCreateFields
				if errors.As(err, &missingErr) {
					return 0, fmt.Errorf("missing required fields: %w", err)
				}
				return 0, fmt.Errorf("failed to create case: %w", err)
			}
			if resp.IsError {
				msg := resp.Message
				if msg == "" {
					msg = "API reported an error without a message"
				}
				return 0, fmt.Errorf("F5 Support API error: %s", msg)
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
			return 0, nil
		}

		fmt.Fprintln(out, "Case creation cancelled. No case was created.")
		return 0, nil
	}
}

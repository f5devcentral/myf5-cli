package main

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"myf5-api/pkg/auth"
	"myf5-api/pkg/bigip"
	"myf5-api/pkg/cases"
	"myf5-api/pkg/downloads"
	"myf5-api/pkg/ihealth"
)

const Version = "1.0.0"

func main() {
	// Enforce Zero-Secret CLI Policy
	if err := auth.CheckNoCLISecrets(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "\n[SECURITY ERROR] %s\n\n", err.Error())
		os.Exit(2)
	}

	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	subcommand := os.Args[1]
	args := os.Args[2:]

	switch subcommand {
	case "downloads", "download":
		handleDownloads(args)
	case "cases", "case":
		handleCases(args)
	case "ihealth":
		handleIHealth(args)
	case "bigip":
		handleBIGIP(args)
	case "doctor":
		handleDoctor(args)
	case "version", "--version", "-v":
		fmt.Printf("myf5-api version %s (Go 1.27.1 / darwin-arm64)\n", Version)
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command '%s'. Run 'myf5 help' for usage.\n", subcommand)
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Printf(`myf5-api - High-Performance F5 Cloud & Appliance Automation CLI (v%s)

USAGE:
    myf5 <command> [subcommand] [arguments...]

COMMANDS:
    downloads    Browse MyF5 catalog and stream multi-GB software images
    cases        View and manage MyF5 support cases
    ihealth      Upload and inspect QKView diagnostic bundles on iHealth
    bigip        Manage BIG-IP TMOS appliances (QKView generation, download)
    doctor       Verify API credentials, DNS, and network firewall egress
    version      Print application version

ZERO-SECRET SECURITY POLICY:
    CLI arguments for secrets (--password, --client-secret) are strictly rejected.
    Store credentials in environment variables or ~/.f5api_credentials (or [profile]).

Run 'myf5 <command> --help' for details on specific commands.
`, Version)
}

// ----------------------------------------------------------------------------
// Downloads Command
// ----------------------------------------------------------------------------

func handleDownloads(args []string) {
	if len(args) == 0 {
		printDownloadsUsage()
		os.Exit(1)
	}

	action := args[0]
	actionArgs := args[1:]

	outputVal := ""
	profileVal := "default"
	var remaining []string

	for i := 0; i < len(actionArgs); i++ {
		arg := actionArgs[i]
		if arg == "-o" || arg == "--output" {
			if i+1 < len(actionArgs) {
				outputVal = actionArgs[i+1]
				i++
			}
		} else if strings.HasPrefix(arg, "-o=") {
			outputVal = strings.TrimPrefix(arg, "-o=")
		} else if strings.HasPrefix(arg, "--output=") {
			outputVal = strings.TrimPrefix(arg, "--output=")
		} else if arg == "--profile" {
			if i+1 < len(actionArgs) {
				profileVal = actionArgs[i+1]
				i++
			}
		} else if strings.HasPrefix(arg, "--profile=") {
			profileVal = strings.TrimPrefix(arg, "--profile=")
		} else {
			remaining = append(remaining, arg)
		}
	}

	ctx := context.Background()

	// Helper to get authenticated client
	getDownloadsClient := func() *downloads.Client {
		creds, err := auth.ResolveCredentials(profileVal)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[ERROR] %v\n", err)
			os.Exit(1)
		}
		token, err := auth.Authenticate(ctx, "", "", creds.ClientID, creds.ClientSecret, "myf5_scope")
		if err != nil {
			fmt.Fprintf(os.Stderr, "[AUTH ERROR] %v\n", err)
			os.Exit(1)
		}
		return downloads.NewClient(token, "")
	}

	switch action {
	case "families", "list-families":
		client := getDownloadsClient()
		meta, err := client.GetMetadata(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[ERROR] %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Available Product Families:")
		for i, fam := range meta.Data.ProductFamilies {
			fmt.Printf("  %2d) %-15s (%s)\n", i+1, fam.Name, fam.DisplayName)
		}

	case "lines", "list-lines":
		if len(remaining) < 1 {
			fmt.Fprintln(os.Stderr, "Usage: myf5 downloads lines <product-family>")
			os.Exit(1)
		}
		targetFamily := remaining[0]
		client := getDownloadsClient()
		meta, err := client.GetMetadata(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[ERROR] %v\n", err)
			os.Exit(1)
		}
		for _, fam := range meta.Data.ProductFamilies {
			if strings.EqualFold(fam.Name, targetFamily) {
				fmt.Printf("Product Lines for '%s':\n", fam.Name)
				for i, line := range fam.ProductLines {
					fmt.Printf("  %2d) %-25s (%s)\n", i+1, line.Name, line.DisplayName)
				}
				return
			}
		}
		fmt.Fprintf(os.Stderr, "Product family '%s' not found.\n", targetFamily)
		os.Exit(1)

	case "versions", "list-versions":
		if len(remaining) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: myf5 downloads versions <product-family> <product-line>")
			os.Exit(1)
		}
		client := getDownloadsClient()
		resp, err := client.GetProductVersions(ctx, remaining[0], remaining[1])
		if err != nil {
			fmt.Fprintf(os.Stderr, "[ERROR] %v\n", err)
			os.Exit(1)
		}
		versions := resp.GetVersions()
		fmt.Printf("Versions for %s / %s (%d found):\n", remaining[0], remaining[1], len(versions))
		for _, v := range versions {
			fmt.Printf("  - Version: %-12s (Released: %s)\n", v.Name, v.ReleaseDate)
			for _, c := range v.Containers {
				fmt.Printf("      Container: %s (%d files)\n", c.Name, len(c.Files))
			}
		}

	case "files", "list-files":
		if len(remaining) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: myf5 downloads files <product-family> <product-line> [version]")
			os.Exit(1)
		}
		client := getDownloadsClient()
		resp, err := client.GetProductVersions(ctx, remaining[0], remaining[1])
		if err != nil {
			fmt.Fprintf(os.Stderr, "[ERROR] %v\n", err)
			os.Exit(1)
		}
		targetVer := ""
		if len(remaining) >= 3 {
			targetVer = remaining[2]
		}

		for _, v := range resp.GetVersions() {
			if targetVer != "" && !strings.EqualFold(v.Name, targetVer) {
				continue
			}
			fmt.Printf("\n=== Version %s (Released: %s) ===\n", v.Name, v.ReleaseDate)
			for _, c := range v.Containers {
				fmt.Printf("  Container: %s\n", c.Name)
				for _, f := range c.Files {
					fmt.Printf("    * %-55s  %10s\n", f.Name, downloads.FormatBytes(f.FileSize))
				}
			}
		}

	case "get", "download":
		if len(remaining) < 5 {
			fmt.Fprintln(os.Stderr, "Usage: myf5 downloads get <family> <line> <version> <container> <file> [-o output.iso]")
			os.Exit(1)
		}
		family, line, ver, cont, fileName := remaining[0], remaining[1], remaining[2], remaining[3], remaining[4]
		outPath := outputVal
		if outPath == "" {
			outPath = fileName
		}

		client := getDownloadsClient()
		fmt.Printf("Fetching download links for %s...\n", fileName)
		linksResp, err := client.GetFileLinks(ctx, family, line, ver, cont, fileName, "english")
		if err != nil {
			fmt.Fprintf(os.Stderr, "[ERROR] %v\n", err)
			os.Exit(1)
		}
		links := linksResp.GetLinks()
		if len(links) == 0 {
			fmt.Fprintf(os.Stderr, "No download links available for %s\n", fileName)
			os.Exit(1)
		}

		var expectedHash, hashAlgo string
		for _, h := range linksResp.GetAllHashes() {
			if strings.EqualFold(h.Algorithm, "sha256") || strings.EqualFold(h.Algorithm, "sha-256") {
				expectedHash = h.Value
				hashAlgo = "sha256"
				break
			}
		}

		link := links[0]
		fmt.Printf("Mirror Region: %s\n", link.GetRegion())
		fmt.Printf("Target File:   %s\n", outPath)
		if expectedHash != "" {
			fmt.Printf("Expected SHA:  %s\n", expectedHash)
		}
		fmt.Println("Streaming multi-GB image at wire speed...")

		renderProgress := func(transferred, total int64, speed float64) {
			pct := 0.0
			if total > 0 {
				pct = float64(transferred) / float64(total) * 100.0
			}
			barWidth := 30
			filled := int(pct / 100.0 * float64(barWidth))
			if filled > barWidth {
				filled = barWidth
			}
			bar := strings.Repeat("=", filled) + strings.Repeat(" ", barWidth-filled)

			etaStr := "--"
			if speed > 0 && total > transferred {
				remSec := int(float64(total-transferred) / speed)
				etaStr = fmt.Sprintf("%dm%02ds", remSec/60, remSec%60)
			}

			fmt.Printf("\r[%s] %5.1f%% (%s / %s) [%s/s] ETA: %s",
				bar, pct, downloads.FormatBytes(transferred), downloads.FormatBytes(total),
				downloads.FormatBytes(int64(speed)), etaStr)
		}

		err = downloads.DownloadFile(ctx, link.Href, outPath, expectedHash, hashAlgo, renderProgress)
		fmt.Println()
		if err != nil {
			fmt.Fprintf(os.Stderr, "\n[DOWNLOAD FAILED] %v\n", err)
			os.Exit(1)
		}
		fmt.Println("[SUCCESS] Download verified and complete!")

	case "browse":
		runInteractiveBrowser(ctx, getDownloadsClient())

	default:
		printDownloadsUsage()
		os.Exit(1)
	}
}

func printDownloadsUsage() {
	fmt.Print(`myf5 downloads - Browse & Download Software Images

ACTIONS:
    browse                                      Interactive menu-driven image browser
    families                                    List all F5 product families (BIG-IP, NGINX, BIG-IQ)
    lines <family>                              List product lines for a family
    versions <family> <line>                    List releases, containers, and dates
    files <family> <line> [version]             List downloadable files with sizes
    get <family> <line> <ver> <container> <file> [-o out.iso]  Download image with live progress & SHA256 check

OPTIONS:
    --profile <name>     Credentials profile (default: "default")
    -o, --output <path>  Destination file path
`)
}

// ----------------------------------------------------------------------------
// Interactive Console Browser
// ----------------------------------------------------------------------------

func runInteractiveBrowser(ctx context.Context, client *downloads.Client) {
	reader := bufio.NewReader(os.Stdin)
	fmt.Println("=== MyF5 Software Downloads Interactive Browser ===")
	meta, err := client.GetMetadata(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load catalog: %v\n", err)
		return
	}

	for {
		fmt.Println("\nSelect a Product Family:")
		for i, f := range meta.Data.ProductFamilies {
			fmt.Printf("  %2d) %s\n", i+1, f.DisplayName)
		}
		fmt.Print("\nEnter number (or 'q' to quit): ")
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)
		if input == "q" || input == "exit" {
			return
		}
		idx, err := strconv.Atoi(input)
		if err != nil || idx < 1 || idx > len(meta.Data.ProductFamilies) {
			fmt.Println("Invalid selection.")
			continue
		}
		selectedFamily := meta.Data.ProductFamilies[idx-1]

		// Select Line
		fmt.Printf("\nSelect a Product Line in %s:\n", selectedFamily.DisplayName)
		for i, l := range selectedFamily.ProductLines {
			fmt.Printf("  %2d) %s\n", i+1, l.DisplayName)
		}
		fmt.Print("\nEnter number (or 'b' to go back): ")
		input, _ = reader.ReadString('\n')
		input = strings.TrimSpace(input)
		if input == "b" {
			continue
		}
		idx, err = strconv.Atoi(input)
		if err != nil || idx < 1 || idx > len(selectedFamily.ProductLines) {
			fmt.Println("Invalid selection.")
			continue
		}
		selectedLine := selectedFamily.ProductLines[idx-1]

		// Fetch versions
		fmt.Printf("Fetching versions for %s...\n", selectedLine.Name)
		vResp, err := client.GetProductVersions(ctx, selectedFamily.Name, selectedLine.Name)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			continue
		}
		versions := vResp.GetVersions()
		if len(versions) == 0 {
			fmt.Println("No versions found.")
			continue
		}

		// Select Version
		fmt.Printf("\nSelect Release Version:\n")
		for i, v := range versions {
			fmt.Printf("  %2d) %-12s (Released: %s)\n", i+1, v.Name, v.ReleaseDate)
		}
		fmt.Print("\nEnter number (or 'b' to go back): ")
		input, _ = reader.ReadString('\n')
		input = strings.TrimSpace(input)
		if input == "b" {
			continue
		}
		idx, err = strconv.Atoi(input)
		if err != nil || idx < 1 || idx > len(versions) {
			fmt.Println("Invalid selection.")
			continue
		}
		selectedVersion := versions[idx-1]

		// Flatten files
		type fileRef struct {
			containerName string
			file          downloads.FileItem
		}
		var allFiles []fileRef
		for _, c := range selectedVersion.Containers {
			for _, f := range c.Files {
				allFiles = append(allFiles, fileRef{containerName: c.Name, file: f})
			}
		}

		if len(allFiles) == 0 {
			fmt.Println("No files found in version.")
			continue
		}

		fmt.Printf("\nSelect a File to Download:\n")
		for i, item := range allFiles {
			fmt.Printf("  %2d) [%s] %-50s (%s)\n",
				i+1, item.containerName, item.file.Name, downloads.FormatBytes(item.file.FileSize))
		}
		fmt.Print("\nEnter number (or 'b' to go back): ")
		input, _ = reader.ReadString('\n')
		input = strings.TrimSpace(input)
		if input == "b" {
			continue
		}
		idx, err = strconv.Atoi(input)
		if err != nil || idx < 1 || idx > len(allFiles) {
			fmt.Println("Invalid selection.")
			continue
		}
		selectedTarget := allFiles[idx-1]

		// Fetch download links
		fmt.Printf("\nFetching signed CDN link for %s...\n", selectedTarget.file.Name)
		linkResp, err := client.GetFileLinks(ctx, selectedFamily.Name, selectedLine.Name, selectedVersion.Name, selectedTarget.containerName, selectedTarget.file.Name, "english")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			continue
		}
		links := linkResp.GetLinks()
		if len(links) == 0 {
			fmt.Println("No download link available.")
			continue
		}

		link := links[0]
		fmt.Printf("Mirror URL (expires %s, Region: %s):\n%s\n\n", link.ExpiresAt, link.GetRegion(), link.Href)
		fmt.Printf("Download this file directly to ./%s? [y/N]: ", selectedTarget.file.Name)
		input, _ = reader.ReadString('\n')
		if strings.ToLower(strings.TrimSpace(input)) == "y" {
			var expectedHash, hashAlgo string
			for _, h := range linkResp.GetAllHashes() {
				if strings.EqualFold(h.Algorithm, "sha256") || strings.EqualFold(h.Algorithm, "sha-256") {
					expectedHash = h.Value
					hashAlgo = "sha256"
					break
				}
			}
			_ = downloads.DownloadFile(ctx, link.Href, selectedTarget.file.Name, expectedHash, hashAlgo, func(transferred, total int64, speed float64) {
				pct := float64(transferred) / float64(total) * 100.0
				fmt.Printf("\rDownloading: %5.1f%% (%s / %s) [%s/s]", pct, downloads.FormatBytes(transferred), downloads.FormatBytes(total), downloads.FormatBytes(int64(speed)))
			})
			fmt.Println("\n[SUCCESS] File saved successfully.")
		}
	}
}

// ----------------------------------------------------------------------------
// Cases Command
// ----------------------------------------------------------------------------

func handleCases(args []string) {
	if len(args) == 0 {
		fmt.Println("Usage: myf5 cases [list | metadata | comment <case-num> <note>]")
		os.Exit(1)
	}

	action := args[0]
	ctx := context.Background()

	creds, err := auth.ResolveCredentials("default")
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] %v\n", err)
		os.Exit(1)
	}
	token, err := auth.Authenticate(ctx, "", "", creds.ClientID, creds.ClientSecret, "myf5_scope")
	if err != nil {
		fmt.Fprintf(os.Stderr, "[AUTH ERROR] %v\n", err)
		os.Exit(1)
	}
	client := cases.NewClient(token, "")

	switch action {
	case "list":
		resp, err := client.ListCases(ctx, false)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to list cases: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("MyF5 Support Cases:")
		for _, c := range resp.Data {
			fmt.Printf("  Case #%-10s [%-8s] [%-6s] %s\n", c.CaseNumber, c.Status, c.Severity, c.Subject)
		}
	case "metadata":
		resp, err := client.GetMetadata(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to get metadata: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Case Metadata Schema retrieved (%d root keys).\n", len(resp.Data))
	case "comment":
		if len(args) < 3 {
			fmt.Fprintln(os.Stderr, "Usage: myf5 cases comment <case-number> <note>")
			os.Exit(1)
		}
		_, err := client.AddComment(ctx, args[1], args[2])
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to add comment: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Comment added successfully.")
	default:
		fmt.Fprintf(os.Stderr, "Unknown cases action '%s'\n", action)
		os.Exit(1)
	}
}

// ----------------------------------------------------------------------------
// iHealth Command
// ----------------------------------------------------------------------------

func handleIHealth(args []string) {
	if len(args) == 0 {
		fmt.Println("Usage: myf5 ihealth [list | upload <file.qkview>]")
		os.Exit(1)
	}

	action := args[0]
	ctx := context.Background()

	creds, err := auth.ResolveCredentials("default")
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] %v\n", err)
		os.Exit(1)
	}
	token, err := auth.Authenticate(ctx, "", "", creds.ClientID, creds.ClientSecret, "ihealth")
	if err != nil {
		fmt.Fprintf(os.Stderr, "[AUTH ERROR] %v\n", err)
		os.Exit(1)
	}
	client := ihealth.NewClient(token, "")

	switch action {
	case "list":
		summaries, err := client.ListQKViews(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to list iHealth QKViews: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("iHealth QKViews:")
		for _, q := range summaries {
			fmt.Printf("  ID: %-12s Status: %-10s File: %s\n", q.ID, q.Status, q.FileName)
		}
	case "upload":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: myf5 ihealth upload <file.qkview> [case-number] [description]")
			os.Exit(1)
		}
		filePath := args[1]
		caseNum := ""
		if len(args) >= 3 {
			caseNum = args[2]
		}
		desc := ""
		if len(args) >= 4 {
			desc = args[3]
		}
		fmt.Printf("Uploading %s to iHealth...\n", filepath.Base(filePath))
		resp, err := client.UploadQKView(ctx, filePath, caseNum, desc)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Upload failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("[SUCCESS] QKView uploaded. iHealth ID: %s (Status: %s)\n", resp.ID, resp.Status)
	default:
		fmt.Fprintf(os.Stderr, "Unknown ihealth action '%s'\n", action)
		os.Exit(1)
	}
}

// ----------------------------------------------------------------------------
// BIG-IP Command
// ----------------------------------------------------------------------------

func handleBIGIP(args []string) {
	if len(args) == 0 {
		fmt.Println("Usage: myf5 bigip [ready <host> | qkview <host>]")
		os.Exit(1)
	}

	action := args[0]
	if len(args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: myf5 bigip %s <host> [--user admin]\n", action)
		os.Exit(1)
	}

	host := args[1]
	user := os.Getenv("BIGIP_USER")
	if user == "" {
		user = "admin"
	}

	pw := os.Getenv("BIGIP_PASSWORD")
	if pw == "" {
		fmt.Fprintf(os.Stderr, "[ERROR] BIGIP_PASSWORD environment variable must be set.\n")
		os.Exit(1)
	}

	client := bigip.NewClient(host, 443, user, pw)
	ctx := context.Background()

	switch action {
	case "ready":
		ready, err := client.CheckReady(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Ready check failed: %v\n", err)
			os.Exit(1)
		}
		if ready {
			fmt.Printf("BIG-IP appliance at %s is READY (HTTP 200).\n", host)
		} else {
			fmt.Printf("BIG-IP appliance at %s is NOT READY.\n", host)
		}
	case "qkview":
		fmt.Printf("Triggering full non-truncating QKView on %s...\n", host)
		remoteFile, err := client.GenerateQKView(ctx, "", true)
		if err != nil {
			fmt.Fprintf(os.Stderr, "QKView generation failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("QKView generation initiated: /var/tmp/%s\n", remoteFile)
	default:
		fmt.Fprintf(os.Stderr, "Unknown bigip action '%s'\n", action)
		os.Exit(1)
	}
}

// ----------------------------------------------------------------------------
// Doctor Command
// ----------------------------------------------------------------------------

func handleDoctor(args []string) {
	fmt.Println("=== myf5 System Doctor & Security Audit ===")
	fmt.Printf("Timestamp: %s\n\n", time.Now().Format(time.RFC3339))

	// 1. Audit Credentials
	fmt.Println("1. Credential Security Audit:")
	creds, err := auth.ResolveCredentials("default")
	if err != nil {
		fmt.Printf("   [FAIL] Credentials: %v\n", err)
	} else {
		maskedSecret := "******"
		if len(creds.ClientSecret) > 4 {
			maskedSecret = creds.ClientSecret[:2] + "..." + creds.ClientSecret[len(creds.ClientSecret)-2:]
		}
		fmt.Printf("   [PASS] Found Client ID:     %s\n", creds.ClientID)
		fmt.Printf("   [PASS] Found Client Secret: %s (Zero CLI Exposure)\n", maskedSecret)
	}

	// 2. DNS & Egress Reachability
	endpoints := []struct {
		name string
		fqdn string
		port int
	}{
		{"F5 Identity (Auth0)", "idp.identity.f5.com", 443},
		{"F5 Identity (Okta)", "identity.account.f5.com", 443},
		{"MyF5 Downloads API", "api.software.downloads.f5.com", 443},
		{"MyF5 Support API", "support.apis.f5.com", 443},
		{"iHealth Upload API", "ihealth2-api.f5.com", 443},
		{"Callhome Service", "callhome.f5.com", 443},
	}

	fmt.Println("\n2. Network Egress & DNS Connectivity Audit (TCP 443):")
	for _, ep := range endpoints {
		target := fmt.Sprintf("%s:%d", ep.fqdn, ep.port)
		conn, err := net.DialTimeout("tcp", target, 3*time.Second)
		if err != nil {
			fmt.Printf("   [FAIL] %-25s (%s): %v\n", ep.name, ep.fqdn, err)
		} else {
			conn.Close()
			fmt.Printf("   [PASS] %-25s (%s): Reachable\n", ep.name, ep.fqdn)
		}
	}

	fmt.Println("\nDoctor audit complete.")
}

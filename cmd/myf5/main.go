package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"myf5-cli/pkg/auth"
	"myf5-cli/pkg/bigip"
	"myf5-cli/pkg/cases"
	"myf5-cli/pkg/client"
	"myf5-cli/pkg/downloads"
	"myf5-cli/pkg/ihealth"
	"myf5-cli/pkg/logger"
	"myf5-cli/pkg/progress"

	"golang.org/x/term"
)

const Version = "1.1.0"

func main() {
	// Set up graceful SIGINT / SIGTERM context
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Handle cancellation: first signal cancels context gracefully, allowing deferred file
	// flushing and network connection teardown; second signal forces immediate exit.
	go func() {
		<-ctx.Done()
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
		defer signal.Stop(sigChan)

		select {
		case <-sigChan:
			fmt.Fprintln(os.Stderr, "\n[FORCED] Aborted immediately by user.")
			os.Exit(130)
		case <-time.After(10 * time.Second):
			// Safety net: if cleanup takes longer than 10s, force exit
			fmt.Fprintln(os.Stderr, "\n[TIMEOUT] Graceful shutdown timed out; exiting.")
			os.Exit(130)
		}
	}()

	// Enforce Zero-Secret CLI Policy
	if err := auth.CheckNoCLISecrets(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "\n[SECURITY ERROR] %s\n\n", err.Error())
		exitFunc(2)
	}

	// Extract global --proxy flag if present
	cleanArgs, proxyFlag := extractProxyFlag(os.Args[1:])
	if proxyFlag != "" {
		if err := client.SetGlobalProxy(proxyFlag); err != nil {
			fmt.Fprintf(os.Stderr, "\n[ERROR] Invalid proxy URL: %v\n\n", err)
			exitFunc(1)
		}
	}

	if len(cleanArgs) < 1 {
		if term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd())) {
			startInteractive(ctx)
			return
		}
		printUsage()
		exitFunc(1)
		return
	}

	subcommand := cleanArgs[0]
	args := cleanArgs[1:]

	logger.Get().Info("Invoking command %s", subcommand)
	executeSubcommand(ctx, subcommand, args)
}

// extractProxyFlag extracts --proxy <url> or --proxy=<url> from an argument slice,
// returning the cleaned arguments and the extracted proxy URL (if any).
func extractProxyFlag(args []string) ([]string, string) {
	var proxyURL string
	var filtered []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--proxy" {
			if i+1 < len(args) {
				proxyURL = args[i+1]
				i++
			}
		} else if strings.HasPrefix(arg, "--proxy=") {
			proxyURL = strings.TrimPrefix(arg, "--proxy=")
		} else {
			filtered = append(filtered, arg)
		}
	}
	return filtered, proxyURL
}

// executeSubcommand dispatches a subcommand and its arguments.
func executeSubcommand(ctx context.Context, subcommand string, args []string) {
	// If help is requested for a subcommand (e.g. `myf5 downloads --help`), print usage and exit 0
	if isSubcommandHelp(args) {
		printCommandHelp(subcommand)
		exitFunc(0)
		return
	}

	switch subcommand {
	case "downloads", "download":
		handleDownloads(ctx, args)
	case "cases", "case":
		handleCases(ctx, args)
	case "wizard":
		handleCases(ctx, append([]string{"wizard"}, args...))
	case "ihealth":
		handleIHealth(ctx, args)
	case "bigip":
		handleBIGIP(ctx, args)
	case "bigiq":
		handleBIGIQ(ctx, args)
	case "geoip":
		handleBIGIP(ctx, append([]string{"geoip"}, args...))
	case "push":
		handlePush(ctx, args)
	case "pull", "fetch":
		handlePull(ctx, args)
	case "proxy":
		handleProxyCommand(args)
	case "doctor":
		handleDoctor(ctx, args)
	case "interactive", "shell", "repl", "i":
		startInteractive(ctx)
	case "version", "--version", "-v":
		fmt.Printf("myf5-cli version %s (Go 1.27.1 / darwin-arm64)\n", Version)
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command '%s'. Run 'myf5 help' for usage.\n", subcommand)
		exitFunc(1)
	}
}

// isHelpFlag reports whether an argument asks for usage.
func isHelpFlag(a string) bool {
	return a == "--help" || a == "-h" || a == "help"
}

// isSubcommandHelp reports whether args consists of a single help request flag.
func isSubcommandHelp(args []string) bool {
	return len(args) == 1 && isHelpFlag(args[0])
}

// normalizeHelpArgs converts a trailing help flag into an empty argument list
// for commands that print usage when called with no arguments.
func normalizeHelpArgs(subcommand string, args []string) []string {
	if len(args) != 1 || !isHelpFlag(args[0]) {
		return args
	}
	switch subcommand {
	case "doctor", "interactive", "shell", "repl", "i", "wizard", "proxy":
		return args
	default:
		return nil
	}
}

// printCommandHelp dispatches to the corresponding command's usage documentation.
func printCommandHelp(subcommand string) {
	switch subcommand {
	case "downloads", "download":
		printDownloadsUsage()
	case "cases", "case", "wizard":
		printCasesUsage()
	case "ihealth":
		printIHealthUsage()
	case "bigip", "geoip":
		printApplianceUsage("BIG-IP")
	case "bigiq":
		printApplianceUsage("BIG-IQ")
	case "push":
		printPushUsage()
	case "pull", "fetch":
		printPullUsage()
	case "proxy":
		printProxyUsage()
	case "doctor":
		printDoctorUsage()
	case "interactive", "shell", "repl", "i":
		printInteractiveUsage()
	default:
		printUsage()
	}
}

func printInteractiveUsage() {
	fmt.Println(`myf5 interactive - Launch Interactive REPL Shell

USAGE:
    myf5 interactive
    myf5 shell
    myf5 repl
    myf5 i

FEATURES:
    - Tab autocompletion for commands, subcommands, and flags
    - Command history and line navigation
    - Safe execution without exiting your shell on errors`)
}

func printCasesUsage() {
	fmt.Println(`myf5 cases - View & Manage Support Cases

USAGE:
    myf5 cases list [--all] [--json]
    myf5 cases get <case-number> [--json]
    myf5 cases comment <case-number> [note] (interactive buffer if note omitted)
    myf5 cases create [flags]
    myf5 cases wizard                       (guided interactive case creator)
    myf5 cases metadata

Run 'myf5 cases create --help' for details on creating support cases.`)
}

func printIHealthUsage() {
	fmt.Println(`myf5 ihealth - Diagnostics & QKView Management

USAGE:
    myf5 ihealth list [--json]
    myf5 ihealth upload <file.qkview> [case-number] [description]`)
}

func printApplianceUsage(targetKind string) {
	cmdName := "bigip"
	if strings.EqualFold(targetKind, "BIG-IQ") {
		cmdName = "bigiq"
		fmt.Printf(`myf5 %s - Manage BIG-IQ Appliance

USAGE:
    myf5 %s ready <host>
    myf5 %s info <host | file.qkview> [--json]
    myf5 %s serial <host | file.qkview> [--json]
    myf5 %s push <host> <file> [--dest <path>] [--method auto|icontrol|scp]
    myf5 %s download <host> <remote-file> [-o <dest>] [--method auto|icontrol|scp]

OPTIONS:
    --user <username>       Appliance username (default: admin)
    --port <port>           Port override (default: 443 for REST, 22 for SCP)
    -i <key-path>           SSH private key file
    --cert <cert-path>      TLS client certificate for mTLS
    --cert-key <key-path>   TLS client private key
    --method <method>       Transfer method: auto, icontrol, or scp
    --json                  Output machine-readable JSON
`, cmdName, cmdName, cmdName, cmdName, cmdName, cmdName)
	} else {
		fmt.Printf(`myf5 %s - Manage BIG-IP TMOS Appliance

USAGE:
    myf5 %s ready <host>
    myf5 %s info <host | file.qkview> [--json]
    myf5 %s serial <host | file.qkview> [--json]
    myf5 %s qkview <host> [--dest <path>] [--keep-remote] [--method auto|icontrol|scp]
    myf5 %s push <host> <file> [--dest <path>] [--apply-geoip] [--method auto|icontrol|scp]
    myf5 %s download <host> <remote-file> [-o <dest>] [--method auto|icontrol|scp]
    myf5 %s geoip <host> <zip-path> [--keep-extracted] [--method auto|icontrol|scp] [--json]

OPTIONS:
    --user <username>       Appliance username (default: admin)
    --port <port>           Port override (default: 443 for REST, 22 for SCP)
    -i <key-path>           SSH private key file
    --cert <cert-path>      TLS client certificate for mTLS
    --cert-key <key-path>   TLS client private key
    --method <method>       Transfer method: auto, icontrol, or scp
    --apply-geoip           Post-upload: automatically extract and apply GeoIP RPMs to TMM
    --keep-extracted        Retain extracted GeoIP files on appliance (default: cleaned up)
    --keep-remote           Keep generated QKView file on appliance after download
    --json                  Output machine-readable JSON
`, cmdName, cmdName, cmdName, cmdName, cmdName, cmdName, cmdName, cmdName)
	}
}

func printPushUsage() {
	fmt.Println(`myf5 push - Transfer Files to BIG-IP / BIG-IQ with Automatic Failover

USAGE:
    myf5 push <host> <file> [options]

OPTIONS:
    --target <type>         Target appliance type: bigip or bigiq (default: bigip)
    --dest <path>           Remote destination directory (default: /shared/images)
    --apply-geoip           Post-upload: automatically extract and apply GeoIP RPMs to TMM (BIG-IP only)
    --keep-extracted        Retain extracted GeoIP files on appliance (default: cleaned up)
    --method <method>       auto, icontrol, or scp (default: auto)
    --user <username>       Appliance user (default: admin)
    --port <port>           Port override
    -i <key-path>           SSH private key path
    --cert <cert-path>      TLS client certificate
    --cert-key <key-path>   TLS client private key`)
}

func printPullUsage() {
	fmt.Println(`myf5 pull - Retrieve Files from BIG-IP / BIG-IQ with Automatic Failover

USAGE:
    myf5 pull <host> <remote-file> [options]

OPTIONS:
    -o <path>               Local output destination (default: OS Downloads directory)
    --target <type>         Target appliance type: bigip or bigiq (default: bigip)
    --method <method>       auto, icontrol, or scp (default: auto)
    --user <username>       Appliance user (default: admin)
    --port <port>           Port override
    -i <key-path>           SSH private key path
    --cert <cert-path>      TLS client certificate
    --cert-key <key-path>   TLS client private key`)
}

func printProxyUsage() {
	fmt.Println(`myf5 proxy - Manage HTTP/HTTPS/SOCKS5 Egress Proxy

USAGE:
    myf5 proxy show            Display active egress proxy
    myf5 proxy set <url>       Set global proxy (http://, https://, socks5://)
    myf5 proxy clear           Clear active global proxy
    myf5 --proxy <url> <cmd>   Use proxy for a single command invocation

SUPPORTED SCHEMES:
    http://user:pass@host:port     HTTP CONNECT proxy
    https://user:pass@host:port    HTTPS CONNECT proxy
    socks5://user:pass@host:port   SOCKS5 proxy (RFC 1928 / RFC 1929)
    socks5h://...                  SOCKS5 proxy with remote DNS resolution

ENVIRONMENT VARIABLES:
    MYF5_PROXY, F5_PROXY, HTTPS_PROXY, HTTP_PROXY, ALL_PROXY, NO_PROXY`)
}

func handleProxyCommand(args []string) {
	if len(args) == 0 || args[0] == "show" || args[0] == "status" {
		p := client.GetGlobalProxy()
		if p == nil {
			p = client.ResolveEnvProxy()
		}
		if p == nil {
			fmt.Println("No proxy configured. Outbound connections connect directly.")
		} else {
			fmt.Printf("Active proxy: %s\n", client.RedactProxyURL(p.String()))
		}
		return
	}
	if isHelpFlag(args[0]) {
		printProxyUsage()
		return
	}
	if args[0] == "clear" || args[0] == "unset" || args[0] == "off" {
		client.ClearGlobalProxy()
		fmt.Println("Global proxy cleared. Outbound connections will connect directly (unless environment proxy is active).")
		return
	}
	if args[0] == "set" && len(args) > 1 {
		if err := client.SetGlobalProxy(args[1]); err != nil {
			fmt.Fprintf(os.Stderr, "Error setting proxy: %v\n", err)
			exitFunc(1)
			return
		}
		fmt.Printf("Global proxy set to %s\n", client.RedactProxyURL(args[1]))
		return
	}
	if err := client.SetGlobalProxy(args[0]); err != nil {
		fmt.Fprintf(os.Stderr, "Error setting proxy: %v\n", err)
		exitFunc(1)
		return
	}
	fmt.Printf("Global proxy set to %s\n", client.RedactProxyURL(args[0]))
}

func printDoctorUsage() {
	fmt.Print(`myf5 doctor - Verify credentials, proxy egress, DNS, and firewall egress

USAGE:
    myf5 doctor [--proxy <url>]

Runs a read-only diagnostic: audits ~/.f5api_credentials permissions and
environment variables, audits configured proxy egress, resolves the MyF5
API hostnames, and checks outbound HTTPS reachability (directly or via proxy).
Takes no arguments and changes nothing.
`)
}

func printUsage() {
	fmt.Printf(`myf5-cli - High-Performance F5 Cloud & Appliance Automation CLI (v%s)

IMPORTANT NOTICE:
    This tool is NOT an officially supported F5 product or service.
    For issues, bugs, or questions, please email Greg Robinson (g.robinson@f5.com)
    or file an issue at https://github.com/SalesAmerSP/myf5-cli/issues.

USAGE:
    myf5 [options] <command> [subcommand] [arguments...]

COMMANDS:
    downloads    Browse MyF5 catalog and stream multi-GB software images
    cases        View and manage MyF5 support cases (wizard available)
    wizard       Launch interactive guided support case creation wizard
    ihealth      Upload and inspect QKView diagnostic bundles on iHealth
    bigip        Manage BIG-IP TMOS appliances (ready check, info, serial, QKView generation, push, download)
    bigiq        Manage BIG-IQ appliances (ready check, info, serial, push, download)
    push         Push software downloads or files to BIG-IP / BIG-IQ (auto failover)
    pull         Download files from BIG-IP / BIG-IQ (auto failover)
    geoip        Apply GeoIP database package to BIG-IP appliance (alias: bigip geoip)
    proxy        View or configure HTTP/HTTPS/SOCKS5 egress proxy
    doctor       Verify API credentials, DNS, and network firewall egress
    interactive  Interactive REPL shell with tab completion (alias: shell, repl)
    version      Print application version

GLOBAL OPTIONS:
    --proxy <url>        Route outbound traffic via HTTP, HTTPS, or SOCKS5 proxy
                         (e.g., http://proxy:8080, socks5://127.0.0.1:1080)
    --profile <name>     Credentials profile (default: "default")

ZERO-SECRET SECURITY POLICY:
    CLI arguments for secrets (--password, --client-secret) are strictly rejected.
    Store credentials in environment variables or ~/.f5api_credentials (or [profile]).
    For appliance commands without BIGIP_PASSWORD / BIGIQ_PASSWORD, interactive masked prompt is used.

Run 'myf5 <command> --help' for details on specific commands.
`, Version)
}

// checkErr terminates the process with 130 if the context was cancelled (Ctrl-C), or 1 on normal errors with remediation.
func checkErr(ctx context.Context, err error, prefix string) {
	if err == nil {
		return
	}
	if errors.Is(err, context.Canceled) || ctx.Err() != nil {
		fmt.Fprintln(os.Stderr, "\n[CANCELLED] Operation aborted by user.")
		exitFunc(130)
	}
	printDetailedError(prefix, err)
	exitFunc(1)
}

func printDetailedError(prefix string, err error) {
	if prefix != "" {
		fmt.Fprintf(os.Stderr, "\n[ERROR] %s: %v\n", prefix, err)
		logger.Get().Error("%s: %v", prefix, err)
	} else {
		fmt.Fprintf(os.Stderr, "\n[ERROR] %v\n", err)
		logger.Get().Error("%v", err)
	}
	if rem := suggestRemediation(err); rem != "" {
		fmt.Fprintf(os.Stderr, "\nREMEDIATION:\n%s\n", rem)
	}
}

func suggestRemediation(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()

	if strings.Contains(msg, "unencrypted HTTP is forbidden") {
		return "  • Appliance management strictly requires HTTPS.\n  • Use https://<host> or pass the bare hostname/IP (e.g. 'myf5 bigip ready 10.1.1.4')."
	}
	if strings.Contains(msg, "Appliance Mode") || strings.Contains(msg, "/mgmt/tm/util/bash") {
		return "  • The TMOS bash utility is restricted or disabled on this appliance.\n  • Use native SCP ('--method scp') to transfer files directly without invoking bash."
	}
	if strings.Contains(msg, "401") || strings.Contains(msg, "Unauthorized") || strings.Contains(msg, "invalid_client") || strings.Contains(msg, "credentials") {
		return "  • Run 'myf5 doctor' to verify your cloud credentials and network connectivity.\n  • Check ~/.f5api_credentials permissions (chmod 600 ~/.f5api_credentials).\n  • For BIG-IP/BIG-IQ, verify username/password or set BIGIP_PASSWORD / BIGIP_SSH_KEY."
	}
	if strings.Contains(msg, "429") || strings.Contains(msg, "Too Many Requests") {
		return "  • MyF5 APIs enforce a rate limit of approximately 10 requests per minute.\n  • Wait 60 seconds before retrying your command."
	}
	if strings.Contains(msg, "connection refused") || strings.Contains(msg, "timeout") || strings.Contains(msg, "no such host") || strings.Contains(msg, "i/o timeout") {
		return "  • Verify hostname DNS resolution and that port 443 (HTTPS) or 22 (SSH) is reachable.\n  • Check security group and firewall egress rules.\n  • Run 'myf5 doctor' to test network egress."
	}
	if strings.Contains(msg, "traversal") || strings.Contains(msg, "outside destination") {
		return "  • The specified target path contains illegal directory traversal characters.\n  • Provide a valid path located within the target directory."
	}
	return ""
}

// ----------------------------------------------------------------------------
// Downloads Command
// ----------------------------------------------------------------------------

func handleDownloads(ctx context.Context, args []string) {
	if len(args) == 0 {
		printDownloadsUsage()
		exitFunc(1)
	}

	action := args[0]
	actionArgs := args[1:]

	outputVal := ""
	profileVal := "default"
	pushHost := ""
	pushMethod := "auto"
	pushDest := ""
	pushUser := ""
	pushPort := 0
	pushKey := ""
	pushTarget := "BIG-IP"
	pushCert := ""
	pushCertKey := ""
	pushApplyGeoIP := false
	pushKeepExtracted := false
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
		} else if arg == "--push" {
			if i+1 < len(actionArgs) {
				pushHost = actionArgs[i+1]
				i++
			}
		} else if strings.HasPrefix(arg, "--push=") {
			pushHost = strings.TrimPrefix(arg, "--push=")
		} else if arg == "--push-method" {
			if i+1 < len(actionArgs) {
				pushMethod = actionArgs[i+1]
				i++
			}
		} else if strings.HasPrefix(arg, "--push-method=") {
			pushMethod = strings.TrimPrefix(arg, "--push-method=")
		} else if arg == "--push-dest" {
			if i+1 < len(actionArgs) {
				pushDest = actionArgs[i+1]
				i++
			}
		} else if strings.HasPrefix(arg, "--push-dest=") {
			pushDest = strings.TrimPrefix(arg, "--push-dest=")
		} else if arg == "--push-user" {
			if i+1 < len(actionArgs) {
				pushUser = actionArgs[i+1]
				i++
			}
		} else if strings.HasPrefix(arg, "--push-user=") {
			pushUser = strings.TrimPrefix(arg, "--push-user=")
		} else if arg == "--push-port" {
			if i+1 < len(actionArgs) {
				if p, err := strconv.Atoi(actionArgs[i+1]); err == nil {
					pushPort = p
				}
				i++
			}
		} else if strings.HasPrefix(arg, "--push-port=") {
			if p, err := strconv.Atoi(strings.TrimPrefix(arg, "--push-port=")); err == nil {
				pushPort = p
			}
		} else if arg == "--push-key" {
			if i+1 < len(actionArgs) {
				pushKey = actionArgs[i+1]
				i++
			}
		} else if strings.HasPrefix(arg, "--push-key=") {
			pushKey = strings.TrimPrefix(arg, "--push-key=")
		} else if arg == "--push-target" {
			if i+1 < len(actionArgs) {
				pushTarget = actionArgs[i+1]
				i++
			}
		} else if strings.HasPrefix(arg, "--push-target=") {
			pushTarget = strings.TrimPrefix(arg, "--push-target=")
		} else if arg == "--push-cert" {
			if i+1 < len(actionArgs) {
				pushCert = actionArgs[i+1]
				i++
			}
		} else if strings.HasPrefix(arg, "--push-cert=") {
			pushCert = strings.TrimPrefix(arg, "--push-cert=")
		} else if arg == "--push-cert-key" || arg == "--push-key-file" {
			if i+1 < len(actionArgs) {
				pushCertKey = actionArgs[i+1]
				i++
			}
		} else if strings.HasPrefix(arg, "--push-cert-key=") {
			pushCertKey = strings.TrimPrefix(arg, "--push-cert-key=")
		} else if strings.HasPrefix(arg, "--push-key-file=") {
			pushCertKey = strings.TrimPrefix(arg, "--push-key-file=")
		} else if arg == "--apply-geoip" || arg == "--push-apply-geoip" {
			pushApplyGeoIP = true
		} else if arg == "--keep-extracted" || arg == "--push-keep-extracted" {
			pushKeepExtracted = true
		} else {
			remaining = append(remaining, arg)
		}
	}

	// Helper to get authenticated client
	getDownloadsClient := func() *downloads.Client {
		creds, err := auth.ResolveCredentials(profileVal)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[ERROR] %v\n", err)
			exitFunc(1)
		}
		token, err := auth.Authenticate(ctx, "", "", creds.ClientID, creds.ClientSecret, "myf5_scope")
		checkErr(ctx, err, "Authentication failed")
		return downloads.NewClientWithKey(token, "", creds.APIKey)
	}

	switch action {
	case "families", "products", "list-products", "list-families":
		client := getDownloadsClient()
		meta, err := client.GetMetadata(ctx)
		checkErr(ctx, err, "Failed to load product families")
		fmt.Println("Available Product Families:")
		for i, fam := range meta.Data.ProductFamilies {
			if fam.DisplayName != "" {
				fmt.Printf("  %2d) %-25s (%s)\n", i+1, fam.Name, fam.DisplayName)
			} else {
				fmt.Printf("  %2d) %s\n", i+1, fam.Name)
			}
		}

	case "lines", "list-lines":
		if len(remaining) < 1 {
			fmt.Fprintln(os.Stderr, "Usage: myf5 downloads lines <product-family>")
			exitFunc(1)
		}
		targetFamily := remaining[0]
		client := getDownloadsClient()
		meta, err := client.GetMetadata(ctx)
		checkErr(ctx, err, "Failed to load product lines")
		for _, fam := range meta.Data.ProductFamilies {
			if strings.EqualFold(fam.Name, targetFamily) {
				fmt.Printf("Product Lines for '%s':\n", fam.GetTitle())
				for i, line := range fam.ProductLines {
					if line.DisplayName != "" {
						fmt.Printf("  %2d) %-25s (%s)\n", i+1, line.Name, line.DisplayName)
					} else {
						fmt.Printf("  %2d) %s\n", i+1, line.Name)
					}
				}
				return
			}
		}
		fmt.Fprintf(os.Stderr, "Product family '%s' not found.\n", targetFamily)
		exitFunc(1)

	case "versions", "list-versions":
		if len(remaining) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: myf5 downloads versions <product-family> <product-line>")
			exitFunc(1)
		}
		client := getDownloadsClient()
		resp, err := client.GetProductVersions(ctx, remaining[0], remaining[1])
		checkErr(ctx, err, "Failed to load versions")
		versions := resp.GetVersions()
		fmt.Printf("Versions for %s / %s (%d found):\n", remaining[0], remaining[1], len(versions))
		for _, v := range versions {
			fmt.Printf("  - Version: %-12s (Released: %s)\n", v.Name, v.ReleaseDate)
			for _, c := range v.Containers {
				fmt.Printf("      Container: %s (%d files)\n", c.Name, len(c.Files))
			}
		}

	case "containers", "list-containers":
		if len(remaining) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: myf5 downloads containers <product-family> <product-line> [version]")
			exitFunc(1)
		}
		client := getDownloadsClient()
		resp, err := client.GetProductVersions(ctx, remaining[0], remaining[1])
		checkErr(ctx, err, "Failed to load containers")
		targetVer := ""
		if len(remaining) >= 3 {
			targetVer = remaining[2]
		}

		for _, v := range resp.GetVersions() {
			if targetVer != "" && !strings.EqualFold(v.Name, targetVer) {
				continue
			}
			fmt.Printf("\n=== Version %s (Released: %s, %d containers) ===\n", v.Name, v.ReleaseDate, len(v.Containers))
			for _, c := range v.Containers {
				typeTag := ""
				if c.Type != "" {
					typeTag = fmt.Sprintf("[%s] ", c.Type)
				}
				desc := ""
				if c.Description != "" && c.Description != c.Name {
					desc = fmt.Sprintf(" - %s", c.Description)
				}
				fmt.Printf("  - %-36s %s[%s] (%d files)%s\n", c.Name, typeTag, c.GetCategory(), len(c.Files), desc)
			}
		}

	case "files", "list-files":
		if len(remaining) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: myf5 downloads files <product-family> <product-line> [version] [container]")
			exitFunc(1)
		}
		client := getDownloadsClient()
		resp, err := client.GetProductVersions(ctx, remaining[0], remaining[1])
		checkErr(ctx, err, "Failed to load files")
		targetVer := ""
		if len(remaining) >= 3 {
			targetVer = remaining[2]
		}
		targetCont := ""
		if len(remaining) >= 4 {
			targetCont = remaining[3]
		}

		for _, v := range resp.GetVersions() {
			if targetVer != "" && !strings.EqualFold(v.Name, targetVer) {
				continue
			}
			fmt.Printf("\n=== Version %s (Released: %s) ===\n", v.Name, v.ReleaseDate)
			for _, c := range v.Containers {
				if targetCont != "" && !strings.EqualFold(c.Name, targetCont) && !strings.Contains(strings.ToLower(c.Name), strings.ToLower(targetCont)) {
					continue
				}
				typeTag := ""
				if c.Type != "" {
					typeTag = fmt.Sprintf("[%s] ", c.Type)
				}
				fmt.Printf("  Container: %s %s[%s] (%d files)\n", c.Name, typeTag, c.GetCategory(), len(c.Files))
				for _, f := range c.Files {
					desc := ""
					if f.Description != "" {
						desc = fmt.Sprintf(" - %s", f.Description)
					}
					fmt.Printf("    * %-55s  %10s%s\n", f.Name, downloads.FormatBytes(f.FileSize), desc)
				}
			}
		}

	case "get", "download":
		if len(remaining) < 5 {
			fmt.Fprintln(os.Stderr, "Usage: myf5 downloads get <family> <line> <version> <container> <file> [-o output.iso]")
			exitFunc(1)
		}
		family, line, ver, cont, fileName := remaining[0], remaining[1], remaining[2], remaining[3], remaining[4]
		outPath := outputVal
		if outPath == "" {
			outPath = filepath.Join(getDefaultDownloadsDir(), filepath.Base(fileName))
		} else {
			if strings.HasSuffix(outPath, "/") || strings.HasSuffix(outPath, "\\") {
				outPath = filepath.Join(outPath, filepath.Base(fileName))
			} else if fi, err := os.Stat(outPath); err == nil && fi.IsDir() {
				outPath = filepath.Join(outPath, filepath.Base(fileName))
			}
		}

		client := getDownloadsClient()
		fmt.Printf("Fetching download links for %s...\n", fileName)
		linksResp, err := client.GetFileLinks(ctx, family, line, ver, cont, fileName, "english")
		checkErr(ctx, err, "Failed to retrieve download link")
		links := linksResp.GetLinks()
		if len(links) == 0 {
			fmt.Fprintf(os.Stderr, "No download links available for %s\n", fileName)
			exitFunc(1)
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

		bar := progress.NewBar(0, "Downloading")
		renderProgress := func(transferred, total int64, speed float64) {
			bar.Total = total
			bar.Update(transferred)
		}

		err = downloads.DownloadFile(ctx, link.Href, outPath, expectedHash, hashAlgo, renderProgress)
		if err != nil {
			if errors.Is(err, context.Canceled) || ctx.Err() != nil {
				bar.Abort("Download cancelled by user.")
				fmt.Fprintln(os.Stderr, "\n[CANCELLED] Operation aborted by user.")
				exitFunc(130)
			}
			fmt.Fprintf(os.Stderr, "\n[DOWNLOAD FAILED] %v\n", err)
			exitFunc(1)
		}
		bar.Finish(fmt.Sprintf("Download verified and complete: %s", outPath))
		if pushHost != "" {
			fmt.Printf("\n--- Automatic Appliance Push Requested ---\n")
			executePushToAppliance(ctx, pushHost, outPath, pushMethod, pushDest, pushUser, pushPort, pushKey, pushTarget, pushCert, pushCertKey, pushApplyGeoIP, pushKeepExtracted, false)
		}

	case "browse":
		runInteractiveBrowser(ctx, getDownloadsClient())

	default:
		printDownloadsUsage()
		exitFunc(1)
	}
}

func printDownloadsUsage() {
	fmt.Print(`myf5 downloads - Browse & Download Software Images

ACTIONS:
    browse                                      Interactive menu-driven image browser (categories & search)
    families                                    List all F5 product families (BIG-IP, NGINX, BIG-IQ)
    lines <family>                              List product lines for a family
    versions <family> <line>                    List releases, containers, and dates
    containers <family> <line> [version]        List categories/containers with type tags & categories
    files <family> <line> [version] [container] List downloadable files with sizes
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
		if errors.Is(err, context.Canceled) || ctx.Err() != nil {
			fmt.Fprintln(os.Stderr, "\n[CANCELLED] Operation aborted by user.")
			exitFunc(130)
		}
		fmt.Fprintf(os.Stderr, "Failed to load catalog: %v\n", err)
		exitFunc(1)
	}

familyLoop:
	for {
		fmt.Println("\nSelect a Product Family:")
		for i, f := range meta.Data.ProductFamilies {
			fmt.Printf("  %2d) %s\n", i+1, f.GetTitle())
		}
		fmt.Print("\nEnter number (or 'q' to quit): ")
		input, err := reader.ReadString('\n')
		if err != nil {
			if ctx.Err() != nil {
				fmt.Fprintln(os.Stderr, "\n[CANCELLED] Operation aborted by user.")
				exitFunc(130)
			}
			return
		}
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

	lineLoop:
		for {
			// Select Line
			fmt.Printf("\nSelect a Product Line in %s:\n", selectedFamily.GetTitle())
			for i, l := range selectedFamily.ProductLines {
				fmt.Printf("  %2d) %s\n", i+1, l.GetTitle())
			}
			fmt.Print("\nEnter number (or 'b' to go back, 'q' to quit): ")
			input, err = reader.ReadString('\n')
			if err != nil {
				if ctx.Err() != nil {
					fmt.Fprintln(os.Stderr, "\n[CANCELLED] Operation aborted by user.")
					exitFunc(130)
				}
				return
			}
			input = strings.TrimSpace(input)
			if input == "q" || input == "exit" {
				return
			}
			if input == "b" {
				continue familyLoop
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
				if errors.Is(err, context.Canceled) || ctx.Err() != nil {
					fmt.Fprintln(os.Stderr, "\n[CANCELLED] Operation aborted by user.")
					exitFunc(130)
				}
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				continue
			}
			versions := vResp.GetVersions()
			if len(versions) == 0 {
				fmt.Println("No versions found.")
				continue
			}

		versionLoop:
			for {
				// Select Version
				fmt.Printf("\nSelect Release Version in %s:\n", selectedLine.GetTitle())
				for i, v := range versions {
					fmt.Printf("  %2d) %-12s (Released: %s, %d containers)\n", i+1, v.Name, v.ReleaseDate, len(v.Containers))
				}
				fmt.Print("\nEnter number (or 'b' to go back, 'q' to quit): ")
				input, err = reader.ReadString('\n')
				if err != nil {
					if ctx.Err() != nil {
						fmt.Fprintln(os.Stderr, "\n[CANCELLED] Operation aborted by user.")
						exitFunc(130)
					}
					return
				}
				input = strings.TrimSpace(input)
				if input == "q" || input == "exit" {
					return
				}
				if input == "b" {
					continue lineLoop
				}
				idx, err = strconv.Atoi(input)
				if err != nil || idx < 1 || idx > len(versions) {
					fmt.Println("Invalid selection.")
					continue
				}
				selectedVersion := versions[idx-1]

			categoryLoop:
				for {
					// Group containers by category
					categorySections := []string{
						"Base Software & ISO Releases",
						"Virtual Edition (VE)",
						"F5OS Tenant Images",
						"Public Cloud Images",
						"Hotfixes & Patches",
						"Security Signatures & Attack Updates",
						"Tools, Telemetry & Diagnostics",
						"Historical Archives",
					}
					grouped := make(map[string][]downloads.Container)
					for _, c := range selectedVersion.Containers {
						cat := c.GetCategory()
						grouped[cat] = append(grouped[cat], c)
					}

					type catGroup struct {
						name       string
						containers []downloads.Container
						totalFiles int
					}
					var activeCats []catGroup
					for _, sec := range categorySections {
						conts := grouped[sec]
						if len(conts) == 0 {
							continue
						}
						totalFiles := 0
						for _, c := range conts {
							totalFiles += len(c.Files)
						}
						activeCats = append(activeCats, catGroup{
							name:       sec,
							containers: conts,
							totalFiles: totalFiles,
						})
					}

					// Also catch any unmapped categories if any
					var otherConts []downloads.Container
					for _, c := range selectedVersion.Containers {
						cat := c.GetCategory()
						matched := false
						for _, sec := range categorySections {
							if sec == cat {
								matched = true
								break
							}
						}
						if !matched {
							otherConts = append(otherConts, c)
						}
					}
					if len(otherConts) > 0 {
						totalFiles := 0
						for _, c := range otherConts {
							totalFiles += len(c.Files)
						}
						activeCats = append(activeCats, catGroup{
							name:       "Other",
							containers: otherConts,
							totalFiles: totalFiles,
						})
					}

					totalVersionFiles := 0
					for _, c := range selectedVersion.Containers {
						totalVersionFiles += len(c.Files)
					}

					fmt.Printf("\n=== %s / %s (Version %s) ===\n", selectedFamily.Name, selectedLine.GetTitle(), selectedVersion.Name)
					fmt.Printf("Select a Category:\n")
					for i, cat := range activeCats {
						containerPlural := "containers"
						if len(cat.containers) == 1 {
							containerPlural = "container"
						}
						fmt.Printf("  %2d) %-38s (%d %s, %d files)\n",
							i+1, cat.name, len(cat.containers), containerPlural, cat.totalFiles)
					}
					fmt.Printf("\n  [Tip: Type 'all' to browse all %d files]\n", totalVersionFiles)
					fmt.Print("Enter category number (or 'b' to go back, 'q' to quit): ")
					input, err = reader.ReadString('\n')
					if err != nil {
						if ctx.Err() != nil {
							fmt.Fprintln(os.Stderr, "\n[CANCELLED] Operation aborted by user.")
							exitFunc(130)
						}
						return
					}
					input = strings.TrimSpace(input)
					if input == "q" || input == "exit" {
						return
					}
					if input == "b" {
						continue versionLoop
					}

					var targetContainers []downloads.Container
					var categoryTitle string

					if strings.EqualFold(input, "all") {
						targetContainers = selectedVersion.Containers
						categoryTitle = fmt.Sprintf("All Containers (%d files)", totalVersionFiles)
					} else {
						catIdx, err := strconv.Atoi(input)
						if err != nil || catIdx < 1 || catIdx > len(activeCats) {
							fmt.Println("Invalid category selection.")
							continue
						}
						chosenCat := activeCats[catIdx-1]
						targetContainers = make([]downloads.Container, len(chosenCat.containers))
						copy(targetContainers, chosenCat.containers)
						categoryTitle = chosenCat.name

						// Sort containers within the category: base release first, then others descending
						sort.SliceStable(targetContainers, func(i, j int) bool {
							nameI := strings.ToLower(targetContainers[i].Name)
							nameJ := strings.ToLower(targetContainers[j].Name)
							baseName := strings.ToLower(selectedVersion.Name)
							isBaseI := nameI == baseName || nameI == baseName+"_virtual-edition" || nameI == baseName+"_tenant_f5os"
							isBaseJ := nameJ == baseName || nameJ == baseName+"_virtual-edition" || nameJ == baseName+"_tenant_f5os"
							if isBaseI && !isBaseJ {
								return true
							}
							if !isBaseI && isBaseJ {
								return false
							}
							return nameI > nameJ
						})
					}

				containerLoop:
					for {
						type fileRef struct {
							containerName string
							file          downloads.FileItem
						}
						var activeFiles []fileRef
						var containerHeader string

						// If there's only 1 container in the category, show files directly
						if len(targetContainers) == 1 {
							chosen := targetContainers[0]
							for _, f := range chosen.Files {
								activeFiles = append(activeFiles, fileRef{containerName: chosen.Name, file: f})
							}
							containerHeader = fmt.Sprintf("%s (%d files)", chosen.Name, len(chosen.Files))
						} else if strings.EqualFold(input, "all") {
							for _, c := range targetContainers {
								for _, f := range c.Files {
									activeFiles = append(activeFiles, fileRef{containerName: c.Name, file: f})
								}
							}
							containerHeader = categoryTitle
						} else {
							fmt.Printf("\n=== %s / %s ===\n", selectedVersion.Name, categoryTitle)
							fmt.Printf("Select a Container:\n")
							for i, c := range targetContainers {
								typeTag := ""
								if c.Type != "" {
									typeTag = fmt.Sprintf("[%s] ", c.Type)
								}
								desc := ""
								if c.Description != "" && c.Description != c.Name {
									desc = fmt.Sprintf(" - %s", c.Description)
								}
								fmt.Printf("  %2d) %-35s %s(%d files)%s\n", i+1, c.Name, typeTag, len(c.Files), desc)
							}
							fmt.Printf("\n  [Tip: Type 'all' to show all files in this category]\n")
							fmt.Print("Enter container number (or 'b' to go back, 'q' to quit): ")
							contInput, err := reader.ReadString('\n')
							if err != nil {
								if ctx.Err() != nil {
									fmt.Fprintln(os.Stderr, "\n[CANCELLED] Operation aborted by user.")
									exitFunc(130)
								}
								return
							}
							contInput = strings.TrimSpace(contInput)
							if contInput == "q" || contInput == "exit" {
								return
							}
							if contInput == "b" {
								continue categoryLoop
							}

							if strings.EqualFold(contInput, "all") {
								for _, c := range targetContainers {
									for _, f := range c.Files {
										activeFiles = append(activeFiles, fileRef{containerName: c.Name, file: f})
									}
								}
								containerHeader = fmt.Sprintf("%s - All (%d files)", categoryTitle, len(activeFiles))
							} else {
								cIdx, err := strconv.Atoi(contInput)
								if err != nil || cIdx < 1 || cIdx > len(targetContainers) {
									fmt.Println("Invalid container selection.")
									continue
								}
								chosen := targetContainers[cIdx-1]
								for _, f := range chosen.Files {
									activeFiles = append(activeFiles, fileRef{containerName: chosen.Name, file: f})
								}
								containerHeader = fmt.Sprintf("%s (%d files)", chosen.Name, len(chosen.Files))
							}
						}

						if len(activeFiles) == 0 {
							fmt.Println("No files found in selection.")
							if len(targetContainers) == 1 {
								continue categoryLoop
							}
							continue
						}

						searchTerm := ""
						for {
							var displayFiles []fileRef
							if searchTerm != "" {
								for _, item := range activeFiles {
									if strings.Contains(strings.ToLower(item.file.Name), strings.ToLower(searchTerm)) ||
										strings.Contains(strings.ToLower(item.file.Description), strings.ToLower(searchTerm)) {
										displayFiles = append(displayFiles, item)
									}
								}
							} else {
								displayFiles = activeFiles
							}

							fmt.Printf("\n=== %s ===\n", containerHeader)
							if searchTerm != "" {
								fmt.Printf("[Filter: '%s' - %d matching files (type '/clear' to view all)]\n", searchTerm, len(displayFiles))
							}
							fmt.Printf("Select a File to Download:\n")
							for i, item := range displayFiles {
								desc := ""
								if item.file.Description != "" {
									desc = fmt.Sprintf(" - %s", item.file.Description)
								}
								contLabel := ""
								if len(targetContainers) > 1 && strings.Contains(containerHeader, "All") {
									contLabel = fmt.Sprintf("[%s] ", item.containerName)
								}
								fmt.Printf("  %2d) %s%-52s (%s)%s\n",
									i+1, contLabel, item.file.Name, downloads.FormatBytes(item.file.FileSize), desc)
							}

							fmt.Print("\nEnter file number, or '/<keyword>' to filter (or 'b' to go back, 'q' to quit): ")
							fileInput, err := reader.ReadString('\n')
							if err != nil {
								if ctx.Err() != nil {
									fmt.Fprintln(os.Stderr, "\n[CANCELLED] Operation aborted by user.")
									exitFunc(130)
								}
								return
							}
							fileInput = strings.TrimSpace(fileInput)
							if fileInput == "q" || fileInput == "exit" {
								return
							}
							if fileInput == "b" {
								if searchTerm != "" {
									searchTerm = ""
									continue
								}
								if len(targetContainers) == 1 {
									continue categoryLoop
								}
								continue containerLoop
							}
							if fileInput == "/clear" || fileInput == "/all" {
								searchTerm = ""
								continue
							}
							if strings.HasPrefix(fileInput, "/") {
								searchTerm = strings.TrimPrefix(fileInput, "/")
								continue
							}

							fIdx, err := strconv.Atoi(fileInput)
							if err != nil || fIdx < 1 || fIdx > len(displayFiles) {
								if !strings.ContainsAny(fileInput, "0123456789") && len(fileInput) >= 2 {
									searchTerm = fileInput
									continue
								}
								fmt.Println("Invalid file selection.")
								continue
							}

							selectedTarget := displayFiles[fIdx-1]

							// Fetch download links
							fmt.Printf("\nFetching signed CDN link for %s...\n", selectedTarget.file.Name)
							linkResp, err := client.GetFileLinks(ctx, selectedFamily.Name, selectedLine.Name, selectedVersion.Name, selectedTarget.containerName, selectedTarget.file.Name, "english")
							if err != nil {
								if errors.Is(err, context.Canceled) || ctx.Err() != nil {
									fmt.Fprintln(os.Stderr, "\n[CANCELLED] Operation aborted by user.")
									exitFunc(130)
								}
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
							targetPath := filepath.Join(getDefaultDownloadsDir(), filepath.Base(selectedTarget.file.Name))
							fmt.Printf("Download this file directly to %s? [y/N]: ", targetPath)
							confirmInput, err := reader.ReadString('\n')
							if err != nil {
								if ctx.Err() != nil {
									fmt.Fprintln(os.Stderr, "\n[CANCELLED] Operation aborted by user.")
									exitFunc(130)
								}
								return
							}
							if strings.ToLower(strings.TrimSpace(confirmInput)) == "y" {
								var expectedHash, hashAlgo string
								for _, h := range linkResp.GetAllHashes() {
									if strings.EqualFold(h.Algorithm, "sha256") || strings.EqualFold(h.Algorithm, "sha-256") {
										expectedHash = h.Value
										hashAlgo = "sha256"
										break
									}
								}
								bar := progress.NewBar(0, "Downloading")
								err := downloads.DownloadFile(ctx, link.Href, targetPath, expectedHash, hashAlgo, func(transferred, total int64, speed float64) {
									bar.Total = total
									bar.Update(transferred)
								})
								if err != nil {
									if errors.Is(err, context.Canceled) || ctx.Err() != nil {
										bar.Abort("Download cancelled by user.")
										fmt.Fprintln(os.Stderr, "\n[CANCELLED] Operation aborted by user.")
										exitFunc(130)
									}
									fmt.Fprintf(os.Stderr, "\n[ERROR] Download failed: %v\n", err)
								} else {
									bar.Finish(fmt.Sprintf("File saved successfully to %s", targetPath))
								}
							}
						}
					}
				}
			}
		}
	}
}

// getDefaultDownloadsDir returns the canonical OS downloads directory using platform standards.
func getDefaultDownloadsDir() string {
	return downloads.DefaultDownloadsDir()
}

// ----------------------------------------------------------------------------
// Cases Command
// ----------------------------------------------------------------------------

func handleCases(ctx context.Context, args []string) {
	if len(args) == 0 {
		printCasesUsage()
		exitFunc(1)
	}

	action := args[0]

	var client *cases.Client
	if action == "wizard" || action == "new" || (action == "create" && len(args) == 1) {
		creds, err := auth.ResolveCredentials("default")
		if err == nil {
			token, aErr := auth.Authenticate(ctx, "", "", creds.ClientID, creds.ClientSecret, "myf5_scope")
			if aErr == nil {
				client = cases.NewClientWithKey(token, "", creds.APIKey)
			}
		}
	} else {
		creds, err := auth.ResolveCredentials("default")
		if err != nil {
			fmt.Fprintf(os.Stderr, "[ERROR] %v\n", err)
			exitFunc(1)
		}
		token, err := auth.Authenticate(ctx, "", "", creds.ClientID, creds.ClientSecret, "myf5_scope")
		checkErr(ctx, err, "Authentication failed")
		client = cases.NewClientWithKey(token, "", creds.APIKey)
	}

	switch action {
	case "wizard", "new":
		if err := RunCaseWizard(ctx, os.Stdin, os.Stdout, client); err != nil {
			fmt.Fprintf(os.Stderr, "[ERROR] %v\n", err)
			exitFunc(1)
		}
	case "list":
		// Closed cases are hidden by default because the open ones are what a
		// person normally wants. Without --all there was no way to see a closed
		// case at all, which looked identical to the tool failing to find it.
		showClosed := false
		for _, a := range args[1:] {
			switch a {
			case "--all", "-a":
				showClosed = true
			default:
				fmt.Fprintf(os.Stderr, "Unknown flag %q\n\nUsage: myf5 cases list [--all]\n", a)
				exitFunc(1)
			}
		}
		resp, err := client.ListCases(ctx, showClosed)
		checkErr(ctx, err, "Failed to list cases")
		if showClosed {
			fmt.Println("MyF5 Support Cases (open and closed):")
		} else {
			fmt.Println("MyF5 Support Cases (open only — use --all to include closed):")
		}
		for _, c := range resp.Data {
			// Priority values are long ("2 - Site At Risk"), so the column is
			// wider than the old 6-wide severity field it replaced.
			fmt.Printf("  Case #%-10s [%-20s] [%-18s] %s\n", c.CaseNumber, c.Status, c.Priority, c.Subject)
		}
		if len(resp.Data) == 0 {
			if showClosed {
				fmt.Println("  (no cases)")
			} else {
				fmt.Println("  (no open cases — closed ones are hidden; use --all)")
			}
		}
	case "metadata":
		resp, err := client.GetMetadata(ctx)
		checkErr(ctx, err, "Failed to get metadata")
		fmt.Printf("Case Metadata Schema retrieved (%d root keys).\n", len(resp.Data))
	case "comment":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: myf5 cases comment <case-number> [note]")
			exitFunc(1)
		}
		caseNum := args[1]
		var note string
		if len(args) >= 3 {
			note = strings.Join(args[2:], " ")
		} else {
			fmt.Printf("Enter comment notes for case %s:\n", caseNum)
			reader := bufio.NewReader(os.Stdin)
			var err error
			note, err = promptMultilineBuffer(reader, os.Stdout, fmt.Sprintf("Case %s Comment Notes", caseNum), "")
			if err != nil {
				if errors.Is(err, ErrWizardBack) {
					fmt.Println("Comment entry cancelled.")
					return
				}
				if errors.Is(err, context.Canceled) || ctx.Err() != nil {
					fmt.Fprintln(os.Stderr, "\n[CANCELLED] Operation aborted by user.")
					exitFunc(130)
				}
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				exitFunc(1)
			}
			note = strings.TrimSpace(note)
			if note == "" {
				fmt.Println("Empty comment. No comment was submitted.")
				return
			}

			// Safety confirmation
			fmt.Fprintln(os.Stdout, "\n--- Comment Preview ---")
			for _, l := range strings.Split(note, "\n") {
				fmt.Fprintf(os.Stdout, "  %s\n", l)
			}
			fmt.Fprintln(os.Stdout, "-----------------------")
			confirm, _ := promptWithDefault(reader, os.Stdout, fmt.Sprintf("Submit this comment to case %s? [y/N]", caseNum), "n")
			if !strings.EqualFold(confirm, "y") && !strings.EqualFold(confirm, "yes") {
				fmt.Println("Submission cancelled. No comment was submitted.")
				return
			}
		}
		resp, err := client.AddComment(ctx, caseNum, note)
		checkErr(ctx, err, "Failed to add comment")
		// The API carries an isError flag in the body, so a 2xx does not by
		// itself mean the comment landed. Discarding the response here would
		// report success for a server-side failure.
		if resp.IsError {
			msg := resp.Message
			if msg == "" {
				msg = "the API reported an error without a message"
			}
			fmt.Fprintf(os.Stderr, "[ERROR] Failed to add comment: %s\n", msg)
			exitFunc(1)
		}
		if resp.Data.CaseNumber != "" {
			fmt.Printf("Comment added to case %s (updated %s).\n", resp.Data.CaseNumber, resp.Data.UpdatedDate)
		} else {
			fmt.Println("Comment added successfully.")
		}
	case "get", "show", "detail":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: myf5 cases get <case-number>")
			exitFunc(1)
		}
		detail, err := client.GetCase(ctx, args[1])
		checkErr(ctx, err, "Failed to retrieve case")
		printCaseDetail(detail)
	case "create":
		handleCaseCreate(ctx, client, args[1:])
	default:
		fmt.Fprintf(os.Stderr, "Unknown cases action '%s'\n", action)
		exitFunc(1)
	}
}

func printCaseDetail(d *cases.CaseDetail) {
	fmt.Printf("Case %s - %s\n", d.CaseNumber, d.Subject)
	fmt.Printf("  Status     : %s\n", d.Status)
	fmt.Printf("  Priority   : %s\n", d.Priority)
	fmt.Printf("  Product    : %s %s\n", d.Product, d.Version)
	if d.ReasonForContact != "" {
		fmt.Printf("  Reason     : %s\n", d.ReasonForContact)
	}
	if d.SerialNumber != "" {
		fmt.Printf("  Serial     : %s\n", d.SerialNumber)
	}
	if d.HostName != "" {
		fmt.Printf("  Hostname   : %s\n", d.HostName)
	}
	if d.Account.AccountName != "" {
		fmt.Printf("  Account    : %s (%s)\n", d.Account.AccountName, d.Account.AccountID)
	}
	if n := d.CaseOwnerContact.DisplayName(); n != "" {
		fmt.Printf("  Owner      : %s <%s>\n", n, d.CaseOwnerContact.Email)
	}
	if n := d.AlternateContact.DisplayName(); n != "" {
		fmt.Printf("  Alternate  : %s <%s>\n", n, d.AlternateContact.Email)
	}
	// dateOpened is deliberately not printed: the detail endpoint returns the
	// priority string in that field. See CaseDetail.
	for _, l := range d.Links {
		if l.Rel == "case-detail" {
			fmt.Printf("  Web        : %s\n", l.Href)
		}
	}
	if d.Description != "" {
		fmt.Println("\nDescription:")
		for _, line := range strings.Split(strings.TrimRight(d.Description, "\n"), "\n") {
			fmt.Printf("  %s\n", line)
		}
	}
	if n := len(d.Activity.EmailActivities); n > 0 {
		fmt.Printf("\nEmail thread (%d message(s), newest first):\n", n)
		for _, e := range d.Activity.EmailActivities {
			who := e.Contact.Name
			if who == "" {
				who = e.FromAddress
			}
			fmt.Printf("  [%s] %s\n", e.ActivityDate, who)
		}
	}
}

const caseCreateUsage = `Usage: myf5 cases create [flags]

Opens a MyF5 support case. To launch the guided step-by-step wizard instead,
run 'myf5 cases wizard' or 'myf5 cases create --wizard'.

All flags below are required by the API when not using the wizard:

  --family <s>        Product family, e.g. "BIG-IP"
  --product <s>       Product within the family, e.g. "BIG-IP LTM"
  --version <s>       Product version, e.g. "17.5.1"

  One entitlement identifier is required, and which one depends on the family:
  --serial <s>        Serial number, or the last 7 of a registration key.
                      Required for BIG-IP and other hardware families.
  --subscription <s>  Subscription number, e.g. "A-S00038458".
                      Required for NGINX and Aspen Mesh instead of --serial.
                      Either way the API verifies it and rejects anything it
                      does not recognise or that lacks active support.
  --subject <s>       One-line summary
  --description <s>   Full problem description
  --priority <s>      e.g. "4 - General Assistance", "1 - Site Down"
  --reason <s>        Reason for contact, e.g. "General Problem".
                      "Pre Sales" is available to F5 Systems Engineers; a
                      customer account cannot open a presales case.
  --email <s>         Case owner email
  --contact-method <s>  Preferred contact method (default "Email")
  --timezone <s>      e.g. "(GMT -5:00) US/Eastern - Eastern Standard Time"

Optional:
  --wizard                  Launch the interactive step-by-step wizard
  --hostname <s>            Affected hostname
  --customer-ticket <s>     Your own ticket reference
  --dry-run                 Print the payload and exit without creating

Valid values for --family, --product, --priority, --reason and --timezone
come from 'myf5 cases metadata'. NOTE: that endpoint is returning HTTP 500
server-side as of 2026-10-05, so values cannot currently be enumerated.
Copy them from an existing case ('myf5 cases get <num>') until it returns.`

func handleCaseCreate(ctx context.Context, client *cases.Client, args []string) {
	if len(args) == 0 {
		if term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd())) {
			fmt.Print("No case parameters specified. Launch interactive Support Case Wizard? [Y/n]: ")
			reader := bufio.NewReader(os.Stdin)
			ans, err := reader.ReadString('\n')
			if err == nil {
				ans = strings.TrimSpace(ans)
				if ans == "" || strings.EqualFold(ans, "y") || strings.EqualFold(ans, "yes") {
					if err := RunCaseWizard(ctx, os.Stdin, os.Stdout, client); err != nil {
						fmt.Fprintf(os.Stderr, "[ERROR] %v\n", err)
						exitFunc(1)
					}
					return
				}
			}
		}
		fmt.Println(caseCreateUsage)
		exitFunc(1)
	}

	payload := map[string]interface{}{}
	dryRun := false

	flagToField := map[string]string{
		"--family":          "productFamily",
		"--product":         "product",
		"--version":         "version",
		"--serial":          "serialNumber",
		"--subscription":    "subscriptionId",
		"--subject":         "subject",
		"--description":     "description",
		"--priority":        "priority",
		"--reason":          "reasonForContact",
		"--email":           "caseOwnerEmail",
		"--contact-method":  "preferredContactMethod",
		"--timezone":        "timezone",
		"--hostname":        "hostName",
		"--customer-ticket": "customerTicketNumber",
	}

	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--wizard" || a == "-w" {
			if err := RunCaseWizard(ctx, os.Stdin, os.Stdout, client); err != nil {
				fmt.Fprintf(os.Stderr, "[ERROR] %v\n", err)
				exitFunc(1)
			}
			return
		}
		if a == "--dry-run" {
			dryRun = true
			continue
		}
		if a == "-h" || a == "--help" {
			fmt.Println(caseCreateUsage)
			return
		}
		field, known := flagToField[a]
		if !known {
			fmt.Fprintf(os.Stderr, "Unknown flag %q\n\n%s\n", a, caseCreateUsage)
			exitFunc(1)
		}
		if i+1 >= len(args) {
			fmt.Fprintf(os.Stderr, "Flag %s requires a value\n", a)
			exitFunc(1)
		}
		payload[field] = args[i+1]
		i++
	}

	// The API rejects a missing contact method, but it has one sane default.
	if _, ok := payload["preferredContactMethod"]; !ok {
		payload["preferredContactMethod"] = "Email"
	}

	if dryRun {
		out, err := json.MarshalIndent(payload, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "[ERROR] failed to render payload: %v\n", err)
			exitFunc(1)
		}
		fmt.Println("Dry run — the following payload would be POSTed:")
		fmt.Println(string(out))
		required := cases.RequiredCreateFields()
		// The entitlement identifier depends on the product family: BIG-IP is
		// verified by serial number, NGINX and Aspen Mesh by subscription.
		family, _ := payload["productFamily"].(string)
		required = append(required, cases.EntitlementFieldFor(family))

		var missing []string
		for _, f := range required {
			if v, ok := payload[f].(string); !ok || strings.TrimSpace(v) == "" {
				missing = append(missing, f)
			}
		}
		if len(missing) > 0 {
			// Reuse the real error so a dry run explains a missing entitlement
			// identifier exactly as a live attempt would. Printing a bare field
			// list here sent people to look for a serial number without saying
			// that the web UI opens such cases without one.
			fmt.Printf("\nIncomplete: %v\n", &cases.ErrMissingCreateFields{Missing: missing})
			exitFunc(1)
		}
		fmt.Println("\nAll required fields present.")
		return
	}

	resp, err := client.CreateCase(ctx, payload)
	if err != nil {
		var missingErr *cases.ErrMissingCreateFields
		if errors.As(err, &missingErr) {
			fmt.Fprintf(os.Stderr, "[ERROR] %v\n\n%s\n", err, caseCreateUsage)
			exitFunc(1)
		}
		checkErr(ctx, err, "Failed to create case")
	}
	if resp.IsError {
		msg := resp.Message
		if msg == "" {
			msg = "the API reported an error without a message"
		}
		fmt.Fprintf(os.Stderr, "[ERROR] Failed to create case: %s\n", msg)
		exitFunc(1)
	}
	if resp.Data.CaseNumber != "" {
		fmt.Printf("Case %s created.\n", resp.Data.CaseNumber)
	} else {
		fmt.Println("Case created, but the API returned no case number.")
	}
}

// ----------------------------------------------------------------------------
// iHealth Command
// ----------------------------------------------------------------------------

func handleIHealth(ctx context.Context, args []string) {
	if len(args) == 0 {
		printIHealthUsage()
		exitFunc(1)
	}

	action := args[0]

	// iHealth needs an OAuth client granted the "ihealth" scope, which is
	// usually a different client from the general MyF5 one.
	creds, err := auth.ResolveCredentialsForService("default", "ihealth")
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] %v\n", err)
		exitFunc(1)
	}
	token, err := auth.Authenticate(ctx, "", "", creds.ClientID, creds.ClientSecret, "ihealth")
	checkErr(ctx, err, "Authentication failed")
	client := ihealth.NewClient(token, "")

	switch action {
	case "list":
		summaries, err := client.ListQKViews(ctx)
		checkErr(ctx, err, "Failed to list iHealth QKViews")
		if len(summaries) == 0 {
			fmt.Println("No QKViews found.")
			break
		}
		fmt.Println("iHealth QKViews:")
		for _, q := range summaries {
			fmt.Printf("  ID: %-12s Status: %-10s File: %s\n", q.ID, q.Status, q.FileName)
			if q.Hostname != "" {
				fmt.Printf("    Host: %s\n", q.Hostname)
			}
			if q.UploadDate != "" {
				fmt.Printf("    Uploaded: %s", q.UploadDate)
				if q.FileSize > 0 {
					fmt.Printf("  (%.1f MB)", float64(q.FileSize)/(1024*1024))
				}
				fmt.Println()
			}
			if q.CaseNumber != "" {
				fmt.Printf("    Case: %s\n", q.CaseNumber)
			}
			if q.GUIURL != "" {
				fmt.Printf("    %s\n", q.GUIURL)
			}
		}
	case "upload":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: myf5 ihealth upload <file.qkview> [case-number] [description]")
			exitFunc(1)
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
		fileStat, err := os.Stat(filePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Cannot access file %s: %v\n", filePath, err)
			exitFunc(1)
		}
		var totalSize int64
		if fileStat != nil {
			totalSize = fileStat.Size()
		}
		bar := progress.NewBar(totalSize, "Uploading")
		resp, err := client.UploadQKViewWithProgress(ctx, filePath, caseNum, desc, func(transferred, total int64) {
			bar.Update(transferred)
		})
		if err != nil {
			if errors.Is(err, context.Canceled) || ctx.Err() != nil {
				bar.Abort("Upload cancelled by user.")
				fmt.Fprintln(os.Stderr, "\n[CANCELLED] Operation aborted by user.")
				exitFunc(130)
			}
			fmt.Fprintf(os.Stderr, "\nUpload failed: %v\n", err)
			exitFunc(1)
		}
		bar.Finish(fmt.Sprintf("QKView uploaded. iHealth ID: %s (Status: %s)", resp.ID, resp.Status))
		if resp.ID != "" {
			fmt.Printf("    https://ihealth.f5.com/qkview-analyzer/qv/%s\n", resp.ID)
		}
	default:
		fmt.Fprintf(os.Stderr, "Unknown ihealth action '%s'\n", action)
		exitFunc(1)
	}
}

// ----------------------------------------------------------------------------
// Appliance & Push Commands (BIG-IP, BIG-IQ)
// ----------------------------------------------------------------------------

func handleBIGIP(ctx context.Context, args []string) {
	handleAppliance(ctx, "BIG-IP", args)
}

func handleBIGIQ(ctx context.Context, args []string) {
	handleAppliance(ctx, "BIG-IQ", args)
}

func handlePush(ctx context.Context, args []string) {
	if len(args) < 2 {
		printPushUsage()
		exitFunc(1)
	}

	var positional []string
	user := ""
	port := 0
	method := "auto"
	destPath := ""
	keyPath := ""
	targetKind := "BIG-IP"
	certPath := ""
	certKeyPath := ""
	applyGeoIP := false
	keepExtracted := false
	jsonOutput := false

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--user" || arg == "-u" {
			if i+1 < len(args) {
				user = args[i+1]
				i++
			}
		} else if strings.HasPrefix(arg, "--user=") {
			user = strings.TrimPrefix(arg, "--user=")
		} else if arg == "--port" || arg == "-p" {
			if i+1 < len(args) {
				if p, err := strconv.Atoi(args[i+1]); err == nil {
					port = p
				}
				i++
			}
		} else if strings.HasPrefix(arg, "--port=") {
			if p, err := strconv.Atoi(strings.TrimPrefix(arg, "--port=")); err == nil {
				port = p
			}
		} else if arg == "--method" || arg == "-m" {
			if i+1 < len(args) {
				method = args[i+1]
				i++
			}
		} else if strings.HasPrefix(arg, "--method=") {
			method = strings.TrimPrefix(arg, "--method=")
		} else if arg == "--dest" || arg == "-d" {
			if i+1 < len(args) {
				destPath = args[i+1]
				i++
			}
		} else if strings.HasPrefix(arg, "--dest=") {
			destPath = strings.TrimPrefix(arg, "--dest=")
		} else if arg == "--identity" || arg == "-i" {
			if i+1 < len(args) {
				keyPath = args[i+1]
				i++
			}
		} else if strings.HasPrefix(arg, "--identity=") {
			keyPath = strings.TrimPrefix(arg, "--identity=")
		} else if arg == "--target" || arg == "-t" {
			if i+1 < len(args) {
				targ := args[i+1]
				if strings.EqualFold(targ, "bigiq") || strings.EqualFold(targ, "big-iq") {
					targetKind = "BIG-IQ"
				}
				i++
			}
		} else if strings.HasPrefix(arg, "--target=") {
			targ := strings.TrimPrefix(arg, "--target=")
			if strings.EqualFold(targ, "bigiq") || strings.EqualFold(targ, "big-iq") {
				targetKind = "BIG-IQ"
			}
		} else if arg == "--cert" {
			if i+1 < len(args) {
				certPath = args[i+1]
				i++
			}
		} else if strings.HasPrefix(arg, "--cert=") {
			certPath = strings.TrimPrefix(arg, "--cert=")
		} else if arg == "--cert-key" || arg == "--key-file" {
			if i+1 < len(args) {
				certKeyPath = args[i+1]
				i++
			}
		} else if strings.HasPrefix(arg, "--cert-key=") {
			certKeyPath = strings.TrimPrefix(arg, "--cert-key=")
		} else if strings.HasPrefix(arg, "--key-file=") {
			certKeyPath = strings.TrimPrefix(arg, "--key-file=")
		} else if arg == "--apply-geoip" || arg == "--geoip" {
			applyGeoIP = true
		} else if arg == "--keep-extracted" || arg == "--no-cleanup" {
			keepExtracted = true
		} else if arg == "--json" {
			jsonOutput = true
		} else if !strings.HasPrefix(arg, "-") {
			positional = append(positional, arg)
		}
	}

	if certPath == "" {
		if strings.EqualFold(targetKind, "BIG-IQ") {
			certPath = os.Getenv("BIGIQ_CERT")
		}
		if certPath == "" {
			certPath = os.Getenv("BIGIP_CERT")
		}
	}
	if certKeyPath == "" {
		if strings.EqualFold(targetKind, "BIG-IQ") {
			certKeyPath = os.Getenv("BIGIQ_KEY")
		}
		if certKeyPath == "" {
			certKeyPath = os.Getenv("BIGIP_KEY")
		}
	}

	if len(positional) < 2 {
		printPushUsage()
		exitFunc(1)
	}

	host := positional[0]
	localFile := positional[1]
	executePushToAppliance(ctx, host, localFile, method, destPath, user, port, keyPath, targetKind, certPath, certKeyPath, applyGeoIP, keepExtracted, jsonOutput)
}

func handlePull(ctx context.Context, args []string) {
	if len(args) < 2 {
		printPullUsage()
		exitFunc(1)
	}

	var positional []string
	user := ""
	port := 0
	method := "auto"
	localDest := ""
	keyPath := ""
	targetKind := "BIG-IP"
	certPath := ""
	certKeyPath := ""

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--user" || arg == "-u" {
			if i+1 < len(args) {
				user = args[i+1]
				i++
			}
		} else if strings.HasPrefix(arg, "--user=") {
			user = strings.TrimPrefix(arg, "--user=")
		} else if arg == "--port" || arg == "-p" {
			if i+1 < len(args) {
				if p, err := strconv.Atoi(args[i+1]); err == nil {
					port = p
				}
				i++
			}
		} else if strings.HasPrefix(arg, "--port=") {
			if p, err := strconv.Atoi(strings.TrimPrefix(arg, "--port=")); err == nil {
				port = p
			}
		} else if arg == "--method" || arg == "-m" {
			if i+1 < len(args) {
				method = args[i+1]
				i++
			}
		} else if strings.HasPrefix(arg, "--method=") {
			method = strings.TrimPrefix(arg, "--method=")
		} else if arg == "--output" || arg == "-o" || arg == "--dest" || arg == "-d" {
			if i+1 < len(args) {
				localDest = args[i+1]
				i++
			}
		} else if strings.HasPrefix(arg, "--output=") {
			localDest = strings.TrimPrefix(arg, "--output=")
		} else if strings.HasPrefix(arg, "--dest=") {
			localDest = strings.TrimPrefix(arg, "--dest=")
		} else if arg == "--identity" || arg == "-i" {
			if i+1 < len(args) {
				keyPath = args[i+1]
				i++
			}
		} else if strings.HasPrefix(arg, "--identity=") {
			keyPath = strings.TrimPrefix(arg, "--identity=")
		} else if arg == "--target" || arg == "-t" {
			if i+1 < len(args) {
				targ := args[i+1]
				if strings.EqualFold(targ, "bigiq") || strings.EqualFold(targ, "big-iq") {
					targetKind = "BIG-IQ"
				}
				i++
			}
		} else if strings.HasPrefix(arg, "--target=") {
			targ := strings.TrimPrefix(arg, "--target=")
			if strings.EqualFold(targ, "bigiq") || strings.EqualFold(targ, "big-iq") {
				targetKind = "BIG-IQ"
			}
		} else if arg == "--cert" {
			if i+1 < len(args) {
				certPath = args[i+1]
				i++
			}
		} else if strings.HasPrefix(arg, "--cert=") {
			certPath = strings.TrimPrefix(arg, "--cert=")
		} else if arg == "--cert-key" || arg == "--key-file" {
			if i+1 < len(args) {
				certKeyPath = args[i+1]
				i++
			}
		} else if strings.HasPrefix(arg, "--cert-key=") {
			certKeyPath = strings.TrimPrefix(arg, "--cert-key=")
		} else if strings.HasPrefix(arg, "--key-file=") {
			certKeyPath = strings.TrimPrefix(arg, "--key-file=")
		} else if !strings.HasPrefix(arg, "-") {
			positional = append(positional, arg)
		}
	}

	if certPath == "" {
		if strings.EqualFold(targetKind, "BIG-IQ") {
			certPath = os.Getenv("BIGIQ_CERT")
		}
		if certPath == "" {
			certPath = os.Getenv("BIGIP_CERT")
		}
	}
	if certKeyPath == "" {
		if strings.EqualFold(targetKind, "BIG-IQ") {
			certKeyPath = os.Getenv("BIGIQ_KEY")
		}
		if certKeyPath == "" {
			certKeyPath = os.Getenv("BIGIP_KEY")
		}
	}

	if len(positional) < 2 {
		printPullUsage()
		exitFunc(1)
	}

	host := positional[0]
	remoteFile := positional[1]
	executeDownloadFromAppliance(ctx, host, remoteFile, localDest, method, user, port, keyPath, targetKind, certPath, certKeyPath)
}

func handleAppliance(ctx context.Context, targetKind string, args []string) {
	cmdName := "bigip"
	if strings.EqualFold(targetKind, "BIG-IQ") {
		cmdName = "bigiq"
	}

	if len(args) == 0 {
		printApplianceUsage(targetKind)
		exitFunc(1)
	}

	action := strings.ToLower(args[0])
	actionArgs := args[1:]

	var positional []string
	user := ""
	port := 0
	method := "auto"
	destPath := ""
	keyPath := ""
	certPath := ""
	certKeyPath := ""
	keepRemote := false
	applyGeoIP := false
	keepExtracted := false
	jsonOutput := false

	for i := 0; i < len(actionArgs); i++ {
		arg := actionArgs[i]
		if arg == "--user" || arg == "-u" {
			if i+1 < len(actionArgs) {
				user = actionArgs[i+1]
				i++
			}
		} else if strings.HasPrefix(arg, "--user=") {
			user = strings.TrimPrefix(arg, "--user=")
		} else if arg == "--port" || arg == "-p" {
			if i+1 < len(actionArgs) {
				if p, err := strconv.Atoi(actionArgs[i+1]); err == nil {
					port = p
				}
				i++
			}
		} else if strings.HasPrefix(arg, "--port=") {
			if p, err := strconv.Atoi(strings.TrimPrefix(arg, "--port=")); err == nil {
				port = p
			}
		} else if arg == "--method" || arg == "-m" {
			if i+1 < len(actionArgs) {
				method = actionArgs[i+1]
				i++
			}
		} else if strings.HasPrefix(arg, "--method=") {
			method = strings.TrimPrefix(arg, "--method=")
		} else if arg == "--output" || arg == "-o" || arg == "--dest" || arg == "-d" {
			if i+1 < len(actionArgs) {
				destPath = actionArgs[i+1]
				i++
			}
		} else if strings.HasPrefix(arg, "--output=") {
			destPath = strings.TrimPrefix(arg, "--output=")
		} else if strings.HasPrefix(arg, "--dest=") {
			destPath = strings.TrimPrefix(arg, "--dest=")
		} else if arg == "--identity" || arg == "-i" {
			if i+1 < len(actionArgs) {
				keyPath = actionArgs[i+1]
				i++
			}
		} else if strings.HasPrefix(arg, "--identity=") {
			keyPath = strings.TrimPrefix(arg, "--identity=")
		} else if arg == "--cert" {
			if i+1 < len(actionArgs) {
				certPath = actionArgs[i+1]
				i++
			}
		} else if strings.HasPrefix(arg, "--cert=") {
			certPath = strings.TrimPrefix(arg, "--cert=")
		} else if arg == "--cert-key" || arg == "--key-file" {
			if i+1 < len(actionArgs) {
				certKeyPath = actionArgs[i+1]
				i++
			}
		} else if strings.HasPrefix(arg, "--cert-key=") {
			certKeyPath = strings.TrimPrefix(arg, "--cert-key=")
		} else if strings.HasPrefix(arg, "--key-file=") {
			certKeyPath = strings.TrimPrefix(arg, "--key-file=")
		} else if arg == "--keep-remote" || arg == "--no-delete" || arg == "--no-cleanup" {
			keepRemote = true
		} else if arg == "--apply-geoip" || arg == "--geoip" {
			applyGeoIP = true
		} else if arg == "--keep-extracted" {
			keepExtracted = true
		} else if arg == "--json" {
			jsonOutput = true
		} else if !strings.HasPrefix(arg, "-") {
			positional = append(positional, arg)
		}
	}

	if certPath == "" {
		if strings.EqualFold(targetKind, "BIG-IQ") {
			certPath = os.Getenv("BIGIQ_CERT")
		}
		if certPath == "" {
			certPath = os.Getenv("BIGIP_CERT")
		}
	}
	if certKeyPath == "" {
		if strings.EqualFold(targetKind, "BIG-IQ") {
			certKeyPath = os.Getenv("BIGIQ_KEY")
		}
		if certKeyPath == "" {
			certKeyPath = os.Getenv("BIGIP_KEY")
		}
	}

	method = strings.ToLower(strings.TrimSpace(method))
	if method != "auto" && method != "icontrol" && method != "scp" && method != "ssh" {
		fmt.Fprintf(os.Stderr, "[ERROR] Invalid method %q. Valid methods: 'auto', 'icontrol', 'scp'\n", method)
		exitFunc(1)
	}
	if method == "ssh" {
		method = "scp"
	}

	switch action {
	case "ready":
		if len(positional) < 1 {
			fmt.Fprintf(os.Stderr, "Usage: myf5 %s ready <host> [--user <user>] [--port <port>] [--cert <cert>] [--cert-key <key>]\n", cmdName)
			exitFunc(1)
		}
		host := positional[0]
		if user == "" {
			if strings.EqualFold(targetKind, "BIG-IQ") {
				user = os.Getenv("BIGIQ_USER")
			}
			if user == "" {
				user = os.Getenv("BIGIP_USER")
			}
			if user == "" {
				user = "admin"
			}
		}

		pw := ""
		if strings.EqualFold(targetKind, "BIG-IQ") {
			pw = os.Getenv("BIGIQ_PASSWORD")
		}
		if pw == "" {
			pw = os.Getenv("BIGIP_PASSWORD")
		}
		if pw == "" && certPath == "" && term.IsTerminal(int(os.Stdin.Fd())) {
			fmt.Printf("Enter %s password for %s@%s: ", targetKind, user, host)
			bytePw, err := term.ReadPassword(int(os.Stdin.Fd()))
			fmt.Println()
			if err == nil {
				pw = strings.TrimSpace(string(bytePw))
				// Zero raw password byte buffer immediately to scrub cleartext secret from memory
				auth.ZeroBytes(bytePw)
			}
		}
		if pw == "" && certPath == "" {
			fmt.Fprintf(os.Stderr, "[ERROR] Password or client certificate required. Set BIGIP_PASSWORD / BIGIQ_PASSWORD / BIGIP_CERT or enter at prompt.\n")
			exitFunc(1)
		}

		client, err := bigip.NewClientWithOptions(bigip.ClientOptions{
			Host:           host,
			Port:           port,
			Username:       user,
			Password:       pw,
			SkipTLSVerify:  true,
			ClientCertPath: certPath,
			ClientKeyPath:  certKeyPath,
		})
		checkErr(ctx, err, "Failed to initialize appliance client")
		ready, err := client.CheckReady(ctx)
		checkErr(ctx, err, "Ready check failed")
		if ready {
			fmt.Printf("%s appliance at %s is READY (HTTP 200).\n", targetKind, host)
		} else {
			fmt.Printf("%s appliance at %s is NOT READY.\n", targetKind, host)
		}

	case "info", "system-info", "sysinfo":
		if len(positional) < 1 {
			fmt.Fprintf(os.Stderr, "Usage: myf5 %s info <host | file.qkview> [--json] [--method auto|icontrol|ssh] [--user <user>] [--port <port>] [-i <key>] [--cert <cert>] [--cert-key <key>]\n", cmdName)
			exitFunc(1)
		}
		target := positional[0]
		info := getSystemInfoTarget(ctx, target, targetKind, user, port, keyPath, method, certPath, certKeyPath)
		if jsonOutput {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			if err := enc.Encode(info); err != nil {
				checkErr(ctx, err, "Failed to encode JSON")
			}
		} else {
			fmt.Print(info.Summary())
		}

	case "serial", "sn", "serialnumber":
		if len(positional) < 1 {
			fmt.Fprintf(os.Stderr, "Usage: myf5 %s serial <host | file.qkview> [--json] [--method auto|icontrol|ssh] [--user <user>] [--port <port>] [-i <key>] [--cert <cert>] [--cert-key <key>]\n", cmdName)
			exitFunc(1)
		}
		target := positional[0]
		info := getSystemInfoTarget(ctx, target, targetKind, user, port, keyPath, method, certPath, certKeyPath)
		sn := info.PreferredSerialNumber()
		if jsonOutput {
			res := map[string]string{
				"serialNumber":        sn,
				"chassisSerialNumber": info.ChassisSerialNumber,
				"registrationKey":     info.RegistrationKey,
				"source":              info.Source,
			}
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			if err := enc.Encode(res); err != nil {
				checkErr(ctx, err, "Failed to encode JSON")
			}
		} else {
			if sn != "" {
				fmt.Println(sn)
			} else {
				fmt.Fprintln(os.Stderr, "[WARNING] No serial number or registration key found.")
				exitFunc(1)
			}
		}
	case "push", "upload":
		if len(positional) < 2 {
			fmt.Fprintf(os.Stderr, "Usage: myf5 %s push <host> <file> [--method auto|icontrol|scp] [--dest <dest>] [--apply-geoip] [--keep-extracted] [--user <user>] [--port <port>] [-i <key>] [--cert <cert>] [--cert-key <key>] [--json]\n", cmdName)
			exitFunc(1)
		}
		host := positional[0]
		localFile := positional[1]
		executePushToAppliance(ctx, host, localFile, method, destPath, user, port, keyPath, targetKind, certPath, certKeyPath, applyGeoIP, keepExtracted, jsonOutput)

	case "geoip":
		if strings.EqualFold(targetKind, "BIG-IQ") {
			fmt.Fprintf(os.Stderr, "[ERROR] GeoIP database updates are only supported on BIG-IP appliances.\n")
			exitFunc(1)
		}
		if len(positional) < 2 {
			fmt.Fprintf(os.Stderr, "Usage: myf5 %s geoip <host> <zip-path> [--method auto|icontrol|scp] [--dest <path>] [--keep-extracted] [--user <user>] [--port <port>] [-i <key>] [--cert <cert>] [--cert-key <key>] [--json]\n", cmdName)
			exitFunc(1)
		}
		host := positional[0]
		zipTarget := positional[1]

		if fi, err := os.Stat(zipTarget); err == nil && !fi.IsDir() {
			// Local file: push it to appliance and apply
			executePushToAppliance(ctx, host, zipTarget, method, destPath, user, port, keyPath, targetKind, certPath, certKeyPath, true, keepExtracted, jsonOutput)
		} else {
			// Remote file on appliance: apply directly
			if err := bigip.ValidateRemoteAppliancePath(zipTarget); err != nil {
				fmt.Fprintf(os.Stderr, "[ERROR] Invalid remote zip path: %v\n", err)
				exitFunc(1)
			}

			if user == "" {
				user = os.Getenv("BIGIP_USER")
				if user == "" {
					user = "admin"
				}
			}
			pw := os.Getenv("BIGIP_PASSWORD")
			if keyPath == "" {
				if k := os.Getenv("BIGIP_SSH_KEY"); k != "" {
					keyPath = k
				} else if k := os.Getenv("SSH_KEY_PATH"); k != "" {
					keyPath = k
				}
			}

			if pw == "" && keyPath == "" && certPath == "" {
				if !hasDefaultSSHAuth() && term.IsTerminal(int(os.Stdin.Fd())) {
					fmt.Printf("Enter %s password for %s@%s: ", targetKind, user, host)
					bytePw, err := term.ReadPassword(int(os.Stdin.Fd()))
					fmt.Println()
					if err == nil {
						pw = strings.TrimSpace(string(bytePw))
						auth.ZeroBytes(bytePw)
					}
				}
			}

			var spinner *progress.Spinner
			if !jsonOutput {
				spinner = progress.NewSpinner("Applying GeoIP updates on BIG-IP (extracting RPMs, updating data, loading tmsh)")
				spinner.Start()
			}

			geoOpts := bigip.GeoIPOptions{
				Port:           port,
				Username:       user,
				Password:       pw,
				IdentityFile:   keyPath,
				ClientCertPath: certPath,
				ClientKeyPath:  certKeyPath,
				Method:         bigip.TransferMethod(method),
				KeepExtracted:  keepExtracted,
			}

			statusCb := func(status string) {
				if spinner != nil {
					spinner.SetMessage(status)
				}
			}

			res, err := bigip.ApplyGeoIP(ctx, host, zipTarget, geoOpts, statusCb)
			if err != nil {
				if errors.Is(err, context.Canceled) || ctx.Err() != nil {
					if spinner != nil {
						spinner.Abort("GeoIP application cancelled by user.")
					}
					fmt.Fprintln(os.Stderr, "\n[CANCELLED] Operation aborted by user.")
					exitFunc(130)
				}
				if spinner != nil {
					spinner.Fail(fmt.Sprintf("GeoIP application failed: %v", err))
				} else {
					fmt.Fprintf(os.Stderr, "[ERROR] GeoIP application failed: %v\n", err)
				}
				exitFunc(1)
			}

			if spinner != nil {
				spinner.Success(fmt.Sprintf("GeoIP database applied successfully via %s (time: %s)", strings.ToUpper(string(res.MethodUsed)), res.Duration.Round(100*time.Millisecond)))
			}

			if jsonOutput {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				if err := enc.Encode(res); err != nil {
					checkErr(ctx, err, "Failed to encode JSON")
				}
			} else {
				fmt.Print(res.Summary())
			}
		}

	case "download", "pull", "fetch":
		if len(positional) < 2 {
			fmt.Fprintf(os.Stderr, "Usage: myf5 %s download <host> <remote-file> [-o <dest>] [--method auto|icontrol|scp] [--user <user>] [--port <port>] [-i <key>] [--cert <cert>] [--cert-key <key>]\n", cmdName)
			exitFunc(1)
		}
		host := positional[0]
		remoteFile := positional[1]
		executeDownloadFromAppliance(ctx, host, remoteFile, destPath, method, user, port, keyPath, targetKind, certPath, certKeyPath)

	case "qkview":
		if len(positional) < 1 {
			fmt.Fprintf(os.Stderr, "Usage: myf5 %s qkview <host> [--method auto|icontrol|scp] [--dest <path>] [--user <user>] [--port <port>] [-i <key>] [--cert <cert>] [--cert-key <key>] [--keep-remote]\n", cmdName)
			exitFunc(1)
		}
		host := positional[0]
		if user == "" {
			user = os.Getenv("BIGIP_USER")
			if user == "" {
				user = "admin"
			}
		}
		pw := os.Getenv("BIGIP_PASSWORD")
		if keyPath == "" {
			if k := os.Getenv("BIGIP_SSH_KEY"); k != "" {
				keyPath = k
			} else if k := os.Getenv("SSH_KEY_PATH"); k != "" {
				keyPath = k
			}
		}

		if pw == "" && keyPath == "" && certPath == "" {
			if !hasDefaultSSHAuth() && term.IsTerminal(int(os.Stdin.Fd())) {
				fmt.Printf("Enter %s password for %s@%s: ", targetKind, user, host)
				bytePw, err := term.ReadPassword(int(os.Stdin.Fd()))
				fmt.Println()
				if err == nil {
					pw = strings.TrimSpace(string(bytePw))
					// Zero raw password byte buffer immediately to scrub cleartext secret from memory
					auth.ZeroBytes(bytePw)
				}
			}
		}

		var requestedFilename string
		if destPath != "" && strings.HasSuffix(destPath, ".qkview") {
			candidate := filepath.Base(destPath)
			if err := bigip.ValidateQKViewFilename(candidate); err == nil {
				requestedFilename = candidate
			}
		}

		var transferMethod bigip.TransferMethod
		switch method {
		case "icontrol":
			transferMethod = bigip.MethodIControl
		case "scp", "ssh":
			transferMethod = bigip.MethodSCP
		default:
			transferMethod = bigip.MethodAuto
		}

		qkOpts := bigip.TransferOptions{
			TargetType:     targetKind,
			Method:         transferMethod,
			Username:       user,
			Password:       pw,
			Port:           port,
			IdentityFile:   keyPath,
			ClientCertPath: certPath,
			ClientKeyPath:  certKeyPath,
			Timeout:        30 * time.Second,
		}

		spinner := progress.NewSpinner(fmt.Sprintf("Generating diagnostic QKView on %s", host))
		spinner.Start()

		genLogFn := func(format string, args ...any) {
			spinner.SetMessage(fmt.Sprintf(format, args...))
		}

		remoteFile, genMethod, err := bigip.GenerateQKViewWithFailover(ctx, host, requestedFilename, true, qkOpts, genLogFn, func(status string) {
			spinner.SetMessage(status)
		})
		if err != nil {
			if errors.Is(err, context.Canceled) || ctx.Err() != nil {
				spinner.Abort("QKView generation cancelled by user.")
				fmt.Fprintln(os.Stderr, "\n[CANCELLED] Operation aborted by user.")
				exitFunc(130)
			}
			spinner.Fail(fmt.Sprintf("QKView generation failed: %v", err))
			exitFunc(1)
		}
		spinner.Success(fmt.Sprintf("QKView generated on appliance (%s): /var/tmp/qkviews/%s", strings.ToUpper(string(genMethod)), remoteFile))

		destFile := filepath.Join(getDefaultDownloadsDir(), remoteFile)
		if destPath != "" {
			if fi, err := os.Stat(destPath); err == nil && fi.IsDir() {
				destFile = filepath.Join(destPath, remoteFile)
			} else {
				destFile = destPath
			}
		}

		bar := progress.NewBar(0, "Downloading diagnostic QKView")
		dlLogFn := func(format string, args ...any) {
			bar.Abort("Primary download failed")
			fmt.Printf("\n[FAILOVER] "+format+"\n", args...)
			bar = progress.NewBar(0, "Downloading via fallback")
		}
		savedFile, methodUsed, err := bigip.DownloadFileWithFailover(ctx, host, "/var/tmp/qkviews/"+remoteFile, destFile, qkOpts, dlLogFn, func(transferred, total int64) {
			bar.Total = total
			bar.Update(transferred)
		})
		if err != nil {
			if errors.Is(err, context.Canceled) || ctx.Err() != nil {
				bar.Abort("QKView download cancelled by user.")
				fmt.Fprintln(os.Stderr, "\n[CANCELLED] Operation aborted by user.")
				exitFunc(130)
			}
			bar.Abort(fmt.Sprintf("Download failed: %v", err))
			exitFunc(1)
		}
		bar.Finish(fmt.Sprintf("Diagnostic QKView saved (%s) to %s", strings.ToUpper(string(methodUsed)), savedFile))

		if !keepRemote {
			if cleanupErr := bigip.DeleteQKViewWithFailover(ctx, host, remoteFile, qkOpts); cleanupErr != nil {
				fmt.Fprintf(os.Stderr, "[WARNING] Failed to clean up appliance temporary file /var/tmp/qkviews/%s: %v\n", remoteFile, cleanupErr)
			} else {
				fmt.Printf("Appliance temporary file /var/tmp/qkviews/%s cleaned up.\n", remoteFile)
			}
		} else {
			fmt.Printf("Appliance temporary file preserved at /var/tmp/qkviews/%s (--keep-remote).\n", remoteFile)
		}

	default:
		fmt.Fprintf(os.Stderr, "Unknown %s action '%s'\n", cmdName, action)
		exitFunc(1)
	}
}

func hasDefaultSSHAuth() bool {
	if os.Getenv("SSH_AUTH_SOCK") != "" {
		return true
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return false
	}
	for _, k := range []string{
		filepath.Join(home, ".ssh", "id_ed25519"),
		filepath.Join(home, ".ssh", "id_rsa"),
		filepath.Join(home, ".ssh", "id_ecdsa"),
	} {
		if _, err := os.Stat(k); err == nil {
			return true
		}
	}
	return false
}

func getSystemInfoTarget(ctx context.Context, target, targetKind, user string, port int, keyPath, method, certPath, certKeyPath string) *bigip.SystemInfo {
	if fi, err := os.Stat(target); err == nil && !fi.IsDir() {
		info, err := bigip.InspectQKView(target)
		checkErr(ctx, err, fmt.Sprintf("Failed to inspect QKView %s", target))
		return info
	}
	if strings.HasSuffix(strings.ToLower(target), ".qkview") {
		fmt.Fprintf(os.Stderr, "[ERROR] QKView file not found: %s\n", target)
		exitFunc(1)
	}

	host := target
	if user == "" {
		if strings.EqualFold(targetKind, "BIG-IQ") {
			user = os.Getenv("BIGIQ_USER")
		}
		if user == "" {
			user = os.Getenv("BIGIP_USER")
		}
		if user == "" {
			user = "admin"
		}
	}

	if certPath == "" {
		if strings.EqualFold(targetKind, "BIG-IQ") {
			certPath = os.Getenv("BIGIQ_CERT")
		}
		if certPath == "" {
			certPath = os.Getenv("BIGIP_CERT")
		}
	}
	if certKeyPath == "" {
		if strings.EqualFold(targetKind, "BIG-IQ") {
			certKeyPath = os.Getenv("BIGIQ_KEY")
		}
		if certKeyPath == "" {
			certKeyPath = os.Getenv("BIGIP_KEY")
		}
	}

	pw := ""
	if strings.EqualFold(targetKind, "BIG-IQ") {
		pw = os.Getenv("BIGIQ_PASSWORD")
	}
	if pw == "" {
		pw = os.Getenv("BIGIP_PASSWORD")
	}
	if pw == "" && keyPath == "" && certPath == "" {
		if !hasDefaultSSHAuth() && term.IsTerminal(int(os.Stdin.Fd())) {
			fmt.Printf("Enter %s password for %s@%s: ", targetKind, user, host)
			bytePw, err := term.ReadPassword(int(os.Stdin.Fd()))
			fmt.Println()
			if err == nil {
				pw = strings.TrimSpace(string(bytePw))
				auth.ZeroBytes(bytePw)
			}
		}
	}

	info, err := resolveApplianceSystemInfo(ctx, host, port, user, pw, keyPath, method, targetKind, certPath, certKeyPath)
	checkErr(ctx, err, fmt.Sprintf("Failed to retrieve system info from %s", host))
	return info
}

func resolveApplianceSystemInfo(ctx context.Context, host string, port int, user, pw, keyPath, method, targetKind, certPath, certKeyPath string) (*bigip.SystemInfo, error) {
	if method == "scp" || method == "ssh" {
		opts := bigip.SCPOptions{
			Host:     host,
			Port:     port,
			Username: user,
			Password: pw,
			KeyPath:  keyPath,
			Timeout:  30 * time.Second,
		}
		return bigip.GetSystemInfoSSH(ctx, opts)
	}

	client, err := bigip.NewClientWithOptions(bigip.ClientOptions{
		Host:           host,
		Port:           port,
		Username:       user,
		Password:       pw,
		SkipTLSVerify:  true,
		ClientCertPath: certPath,
		ClientKeyPath:  certKeyPath,
	})
	if err == nil {
		info, err := client.GetSystemInfo(ctx)
		if err == nil {
			return info, nil
		}
		if method != "auto" {
			return nil, err
		}
	} else if method != "auto" {
		return nil, err
	}

	if method == "auto" {
		opts := bigip.SCPOptions{
			Host:     host,
			Port:     port,
			Username: user,
			Password: pw,
			KeyPath:  keyPath,
			Timeout:  30 * time.Second,
		}
		sshInfo, sshErr := bigip.GetSystemInfoSSH(ctx, opts)
		if sshErr == nil {
			return sshInfo, nil
		}
		return nil, fmt.Errorf("iControl REST error: %w; SSH error: %v", err, sshErr)
	}

	return nil, err
}

// executePushToAppliance handles streaming upload of a local file to a BIG-IP or BIG-IQ appliance
// via either iControl REST chunked transfer or SCP over SSH, with automatic seamless failover.
func executePushToAppliance(
	ctx context.Context,
	host, localPath, method, destPath, user string,
	port int,
	keyPath, targetKind, certPath, certKeyPath string,
	applyGeoIP bool,
	keepExtracted bool,
	jsonOutput bool,
) {
	if targetKind == "" {
		targetKind = "BIG-IP"
	}
	if method == "" {
		method = "auto"
	}
	method = strings.ToLower(strings.TrimSpace(method))
	if method != "auto" && method != "icontrol" && method != "scp" && method != "ssh" {
		fmt.Fprintf(os.Stderr, "[ERROR] Invalid push method %q. Valid methods: 'auto', 'icontrol', 'scp'\n", method)
		exitFunc(1)
	}
	if method == "ssh" {
		method = "scp"
	}

	if applyGeoIP {
		if strings.EqualFold(targetKind, "BIG-IQ") {
			fmt.Fprintf(os.Stderr, "[ERROR] GeoIP database updates are only supported on BIG-IP appliances.\n")
			exitFunc(1)
		}
		if destPath == "" {
			destPath = "/var/tmp"
		}
	}

	stat, err := os.Stat(localPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] Local file %q does not exist or cannot be read: %v\n", localPath, err)
		exitFunc(1)
	}
	fileSize := stat.Size()
	baseName := filepath.Base(localPath)

	if user == "" {
		if strings.EqualFold(targetKind, "BIG-IQ") {
			user = os.Getenv("BIGIQ_USER")
		}
		if user == "" {
			user = os.Getenv("BIGIP_USER")
		}
		if user == "" {
			if method == "scp" {
				user = "root"
			} else {
				user = "admin"
			}
		}
	}

	if certPath == "" {
		if strings.EqualFold(targetKind, "BIG-IQ") {
			certPath = os.Getenv("BIGIQ_CERT")
		}
		if certPath == "" {
			certPath = os.Getenv("BIGIP_CERT")
		}
	}
	if certKeyPath == "" {
		if strings.EqualFold(targetKind, "BIG-IQ") {
			certKeyPath = os.Getenv("BIGIQ_KEY")
		}
		if certKeyPath == "" {
			certKeyPath = os.Getenv("BIGIP_KEY")
		}
	}

	pw := ""
	if strings.EqualFold(targetKind, "BIG-IQ") {
		pw = os.Getenv("BIGIQ_PASSWORD")
	}
	if pw == "" {
		pw = os.Getenv("BIGIP_PASSWORD")
	}

	if keyPath == "" {
		if k := os.Getenv("BIGIP_SSH_KEY"); k != "" {
			keyPath = k
		} else if k := os.Getenv("SSH_KEY_PATH"); k != "" {
			keyPath = k
		}
	}

	if pw == "" && keyPath == "" && certPath == "" {
		if !hasDefaultSSHAuth() && term.IsTerminal(int(os.Stdin.Fd())) {
			fmt.Printf("Enter %s password for %s@%s: ", targetKind, user, host)
			bytePw, err := term.ReadPassword(int(os.Stdin.Fd()))
			fmt.Println()
			if err == nil {
				pw = strings.TrimSpace(string(bytePw))
				// Zero raw password byte buffer immediately to scrub cleartext secret from memory
				auth.ZeroBytes(bytePw)
			}
		}
	}

	opts := bigip.TransferOptions{
		TargetType:     targetKind,
		Method:         bigip.TransferMethod(method),
		Username:       user,
		Password:       pw,
		Port:           port,
		IdentityFile:   keyPath,
		ClientCertPath: certPath,
		ClientKeyPath:  certKeyPath,
		DestDir:        destPath,
		Timeout:        30 * time.Second,
	}

	var bar *progress.Bar
	var renderProgress func(transferred, total int64)
	var logFn bigip.FailoverLogger

	if !jsonOutput {
		fmt.Printf("Pushing %s (%s) to %s (%s) via %s...\n", baseName, downloads.FormatBytes(fileSize), targetKind, host, strings.ToUpper(method))

		renderProgress = func(transferred, total int64) {
			if bar == nil {
				bar = progress.NewBar(0, fmt.Sprintf("Uploading to %s", targetKind))
			}
			bar.Total = total
			bar.Update(transferred)
		}

		logFn = func(format string, args ...any) {
			if bar != nil {
				bar.Abort("Primary upload method failed")
				bar = nil
			}
			fmt.Printf("\n[FAILOVER] "+format+"\n", args...)
		}
	} else {
		logFn = func(format string, args ...any) {
			logger.Get().Info("[FAILOVER] "+format, args...)
		}
	}

	finalPath, methodUsed, err := bigip.PushFileWithFailover(ctx, host, localPath, opts, logFn, renderProgress)
	if err != nil {
		if errors.Is(err, context.Canceled) || ctx.Err() != nil {
			if bar != nil {
				bar.Abort("Upload cancelled by user.")
			}
			fmt.Fprintln(os.Stderr, "\n[CANCELLED] Operation aborted by user.")
			exitFunc(130)
		}
		if bar != nil {
			bar.Abort(fmt.Sprintf("Upload failed: %v", err))
		} else {
			fmt.Fprintf(os.Stderr, "[ERROR] Upload failed: %v\n", err)
		}
		exitFunc(1)
	}

	if !applyGeoIP {
		if bar != nil {
			bar.Finish(fmt.Sprintf("File successfully pushed to %s (%s) at %s", targetKind, strings.ToUpper(string(methodUsed)), finalPath))
		} else if !jsonOutput {
			fmt.Printf("File successfully pushed to %s (%s) at %s\n", targetKind, strings.ToUpper(string(methodUsed)), finalPath)
		} else {
			res := map[string]any{
				"host":       host,
				"localPath":  localPath,
				"remotePath": finalPath,
				"method":     string(methodUsed),
				"size":       fileSize,
			}
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			if err := enc.Encode(res); err != nil {
				checkErr(ctx, err, "Failed to encode JSON")
			}
		}
		return
	}

	if bar != nil {
		bar.Finish(fmt.Sprintf("File successfully pushed to %s (%s) at %s", targetKind, strings.ToUpper(string(methodUsed)), finalPath))
	} else if !jsonOutput {
		fmt.Printf("File successfully pushed to %s (%s) at %s\n", targetKind, strings.ToUpper(string(methodUsed)), finalPath)
	}

	var spinner *progress.Spinner
	if !jsonOutput {
		fmt.Println()
		spinner = progress.NewSpinner("Applying GeoIP updates on BIG-IP (extracting RPMs, updating data, loading tmsh)")
		spinner.Start()
	}

	geoOpts := bigip.GeoIPOptions{
		Port:           port,
		Username:       user,
		Password:       pw,
		IdentityFile:   keyPath,
		ClientCertPath: certPath,
		ClientKeyPath:  certKeyPath,
		Method:         bigip.TransferMethod(method),
		KeepExtracted:  keepExtracted,
	}

	statusCb := func(status string) {
		if spinner != nil {
			spinner.SetMessage(status)
		}
	}

	res, err := bigip.ApplyGeoIP(ctx, host, finalPath, geoOpts, statusCb)
	if err != nil {
		if errors.Is(err, context.Canceled) || ctx.Err() != nil {
			if spinner != nil {
				spinner.Abort("GeoIP application cancelled by user.")
			}
			fmt.Fprintln(os.Stderr, "\n[CANCELLED] Operation aborted by user.")
			exitFunc(130)
		}
		if spinner != nil {
			spinner.Fail(fmt.Sprintf("GeoIP application failed: %v", err))
		} else {
			fmt.Fprintf(os.Stderr, "[ERROR] GeoIP application failed: %v\n", err)
		}
		exitFunc(1)
	}

	if spinner != nil {
		spinner.Success(fmt.Sprintf("GeoIP database applied successfully via %s (time: %s)", strings.ToUpper(string(res.MethodUsed)), res.Duration.Round(100*time.Millisecond)))
	}

	if jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(res); err != nil {
			checkErr(ctx, err, "Failed to encode JSON")
		}
	} else {
		fmt.Print(res.Summary())
	}
}

// executeDownloadFromAppliance handles streaming download of a remote file from a BIG-IP or BIG-IQ appliance
// via either iControl REST or SCP over SSH, with automatic seamless failover.
func executeDownloadFromAppliance(
	ctx context.Context,
	host, remotePath, localDest, method, user string,
	port int,
	keyPath, targetKind, certPath, certKeyPath string,
) {
	if targetKind == "" {
		targetKind = "BIG-IP"
	}
	if method == "" {
		method = "auto"
	}
	method = strings.ToLower(strings.TrimSpace(method))
	if method != "auto" && method != "icontrol" && method != "scp" && method != "ssh" {
		fmt.Fprintf(os.Stderr, "[ERROR] Invalid download method %q. Valid methods: 'auto', 'icontrol', 'scp'\n", method)
		exitFunc(1)
	}
	if method == "ssh" {
		method = "scp"
	}

	if err := bigip.ValidateApplianceDownloadPath(remotePath); err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] %v\n", err)
		exitFunc(1)
	}

	if localDest == "" {
		base := path.Base(path.Clean(remotePath))
		if base == "" || base == "." || base == "/" {
			base = "appliance_download"
		}
		localDest = filepath.Join(getDefaultDownloadsDir(), base)
	}

	if user == "" {
		if strings.EqualFold(targetKind, "BIG-IQ") {
			user = os.Getenv("BIGIQ_USER")
		}
		if user == "" {
			user = os.Getenv("BIGIP_USER")
		}
		if user == "" {
			if method == "scp" {
				user = "root"
			} else {
				user = "admin"
			}
		}
	}

	if certPath == "" {
		if strings.EqualFold(targetKind, "BIG-IQ") {
			certPath = os.Getenv("BIGIQ_CERT")
		}
		if certPath == "" {
			certPath = os.Getenv("BIGIP_CERT")
		}
	}
	if certKeyPath == "" {
		if strings.EqualFold(targetKind, "BIG-IQ") {
			certKeyPath = os.Getenv("BIGIQ_KEY")
		}
		if certKeyPath == "" {
			certKeyPath = os.Getenv("BIGIP_KEY")
		}
	}

	pw := ""
	if strings.EqualFold(targetKind, "BIG-IQ") {
		pw = os.Getenv("BIGIQ_PASSWORD")
	}
	if pw == "" {
		pw = os.Getenv("BIGIP_PASSWORD")
	}

	if keyPath == "" {
		if k := os.Getenv("BIGIP_SSH_KEY"); k != "" {
			keyPath = k
		} else if k := os.Getenv("SSH_KEY_PATH"); k != "" {
			keyPath = k
		}
	}

	if pw == "" && keyPath == "" && certPath == "" {
		if !hasDefaultSSHAuth() && term.IsTerminal(int(os.Stdin.Fd())) {
			fmt.Printf("Enter %s password for %s@%s: ", targetKind, user, host)
			bytePw, err := term.ReadPassword(int(os.Stdin.Fd()))
			fmt.Println()
			if err == nil {
				pw = strings.TrimSpace(string(bytePw))
				// Zero raw password byte buffer immediately to scrub cleartext secret from memory
				auth.ZeroBytes(bytePw)
			}
		}
	}

	opts := bigip.TransferOptions{
		TargetType:     targetKind,
		Method:         bigip.TransferMethod(method),
		Username:       user,
		Password:       pw,
		Port:           port,
		IdentityFile:   keyPath,
		ClientCertPath: certPath,
		ClientKeyPath:  certKeyPath,
		Timeout:        30 * time.Second,
	}

	fmt.Printf("Downloading %s from %s (%s) via %s...\n", remotePath, targetKind, host, strings.ToUpper(method))

	var bar *progress.Bar
	renderProgress := func(transferred, total int64) {
		if bar == nil {
			bar = progress.NewBar(0, fmt.Sprintf("Downloading from %s", targetKind))
		}
		bar.Total = total
		bar.Update(transferred)
	}

	logFn := func(format string, args ...any) {
		if bar != nil {
			bar.Abort("Primary download method failed")
			bar = nil
		}
		fmt.Printf("\n[FAILOVER] "+format+"\n", args...)
	}

	savedPath, methodUsed, err := bigip.DownloadFileWithFailover(ctx, host, remotePath, localDest, opts, logFn, renderProgress)
	if err != nil {
		if errors.Is(err, context.Canceled) || ctx.Err() != nil {
			if bar != nil {
				bar.Abort("Download cancelled by user.")
			}
			fmt.Fprintln(os.Stderr, "\n[CANCELLED] Operation aborted by user.")
			exitFunc(130)
		}
		if bar != nil {
			bar.Abort(fmt.Sprintf("Download failed: %v", err))
		} else {
			fmt.Fprintf(os.Stderr, "[ERROR] Download failed: %v\n", err)
		}
		exitFunc(1)
	}
	if bar != nil {
		bar.Finish(fmt.Sprintf("File successfully downloaded from %s (%s) to %s", targetKind, strings.ToUpper(string(methodUsed)), savedPath))
	} else {
		fmt.Printf("File successfully downloaded from %s (%s) to %s\n", targetKind, strings.ToUpper(string(methodUsed)), savedPath)
	}
}

// ----------------------------------------------------------------------------
// Doctor Command
// ----------------------------------------------------------------------------

func handleDoctor(ctx context.Context, args []string) {
	// doctor takes no arguments and runs on a bare invocation, so a help
	// request cannot be inferred from an empty argument list the way it is for
	// the other commands. Without this, `doctor --help` performed a live
	// credential and network audit instead of describing itself.
	for _, a := range args {
		if isHelpFlag(a) {
			printDoctorUsage()
			return
		}
	}

	fmt.Println("=== myf5 System Doctor & Security Audit ===")
	fmt.Printf("Timestamp: %s\n\n", time.Now().Format(time.RFC3339))

	hasError := false

	// 1. Audit Credentials
	fmt.Println("1. Credential Security Audit:")
	creds, err := auth.ResolveCredentials("default")
	if err != nil {
		fmt.Printf("   [FAIL] Credentials: %v\n", err)
		hasError = true
	} else {
		fmt.Printf("   [PASS] Found Client ID:     %s\n", creds.ClientID)
		fmt.Println("   [PASS] Found Client Secret: [REDACTED] (Zero CLI Exposure)")
	}

	// 2. Outbound Proxy Configuration
	fmt.Println("\n2. Outbound Proxy Configuration:")
	activeProxy := client.GetGlobalProxy()
	if activeProxy == nil {
		activeProxy = client.ResolveEnvProxy()
	}
	if activeProxy != nil {
		fmt.Printf("   [INFO] Active Egress Proxy:  %s\n", client.RedactProxyURL(activeProxy.String()))
		noProxy := os.Getenv("NO_PROXY")
		if noProxy == "" {
			noProxy = os.Getenv("no_proxy")
		}
		if noProxy != "" {
			fmt.Printf("   [INFO] Active NO_PROXY:      %s\n", noProxy)
		}
	} else {
		fmt.Println("   [INFO] Active Egress Proxy:  Direct (none configured)")
	}

	// 3. DNS & Egress Reachability
	// We audit connectivity to all production cloud services across authentication,
	// software downloads, case management, diagnostic analysis, and telemetry.
	endpoints := []struct {
		name string
		fqdn string
		port int
	}{
		{"F5 Identity (Auth0)", "idp.identity.f5.com", 443},
		{"F5 Identity (Okta)", "identity.account.f5.com", 443},
		{"MyF5 Downloads API", "api.software.downloads.f5.com", 443},
		{"MyF5 Support API", "support.apis.f5.com", 443},
		{"iHealth Primary API", "ihealth-api.f5.com", 443},
		{"iHealth Upload API", "ihealth2-api.f5.com", 443},
		{"Callhome Service", "callhome.f5.com", 443},
		{"F5 Cloud Gateway", "api.f5.com", 443},
	}

	fmt.Println("\n3. Network Egress & DNS Connectivity Audit (TCP 443):")
	for _, ep := range endpoints {
		target := net.JoinHostPort(ep.fqdn, strconv.Itoa(ep.port))
		dialCtx, dialCancel := context.WithTimeout(ctx, 4*time.Second)
		conn, err := client.DialProxy(dialCtx, "tcp", target, nil)
		dialCancel()

		viaMsg := ""
		if activeProxy != nil && !client.ShouldBypassProxy(target, os.Getenv("NO_PROXY")) && !client.ShouldBypassProxy(target, os.Getenv("no_proxy")) {
			viaMsg = " (via proxy)"
		}

		if err != nil {
			fmt.Printf("   [FAIL] %-25s (%s%s): %v\n", ep.name, ep.fqdn, viaMsg, err)
			hasError = true
		} else {
			conn.Close()
			fmt.Printf("   [PASS] %-25s (%s%s): Reachable\n", ep.name, ep.fqdn, viaMsg)
		}
	}

	fmt.Println("\nDoctor audit complete.")
	if hasError {
		exitFunc(1)
	}
}

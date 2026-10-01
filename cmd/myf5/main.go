package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"myf5-api/pkg/auth"
	"myf5-api/pkg/bigip"
	"myf5-api/pkg/cases"
	"myf5-api/pkg/downloads"
	"myf5-api/pkg/ihealth"
	"myf5-api/pkg/progress"

	"golang.org/x/term"
)

const Version = "1.0.0"

func main() {
	// Set up graceful SIGINT / SIGTERM context
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Handle cancellation: first signal cancels context gracefully, second signal forces immediate exit
	go func() {
		<-ctx.Done()
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
		defer signal.Stop(sigChan)

		select {
		case <-sigChan:
			fmt.Fprintln(os.Stderr, "\n[FORCED] Aborted immediately by user.")
			os.Exit(130)
		case <-time.After(500 * time.Millisecond):
			// If blocked on interactive stdin or inactive network, exit cleanly
			fmt.Fprintln(os.Stderr, "\n[CANCELLED] Operation aborted by user.")
			os.Exit(130)
		}
	}()

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
		handleDownloads(ctx, args)
	case "cases", "case":
		handleCases(ctx, args)
	case "ihealth":
		handleIHealth(ctx, args)
	case "bigip":
		handleBIGIP(ctx, args)
	case "doctor":
		handleDoctor(ctx, args)
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

IMPORTANT NOTICE:
    This tool is NOT an officially supported F5 product or service.
    For issues, bugs, or questions, please email Greg Robinson (g.robinson@f5.com)
    or file an issue at https://github.com/SalesAmerSP/myf5-api/issues.

USAGE:
    myf5 <command> [subcommand] [arguments...]

COMMANDS:
    downloads    Browse MyF5 catalog and stream multi-GB software images
    cases        View and manage MyF5 support cases
    ihealth      Upload and inspect QKView diagnostic bundles on iHealth
    bigip        Manage BIG-IP TMOS appliances (ready check, QKView generation)
    doctor       Verify API credentials, DNS, and network firewall egress
    version      Print application version

ZERO-SECRET SECURITY POLICY:
    CLI arguments for secrets (--password, --client-secret) are strictly rejected.
    Store credentials in environment variables or ~/.f5api_credentials (or [profile]).
    For BIG-IP commands without BIGIP_PASSWORD set, interactive masked entry will prompt.

Run 'myf5 <command> --help' for details on specific commands.
`, Version)
}

// checkErr terminates the process with 130 if the context was cancelled (Ctrl-C), or 1 on normal errors.
func checkErr(ctx context.Context, err error, prefix string) {
	if err == nil {
		return
	}
	if errors.Is(err, context.Canceled) || ctx.Err() != nil {
		fmt.Fprintln(os.Stderr, "\n[CANCELLED] Operation aborted by user.")
		os.Exit(130)
	}
	if prefix != "" {
		fmt.Fprintf(os.Stderr, "[ERROR] %s: %v\n", prefix, err)
	} else {
		fmt.Fprintf(os.Stderr, "[ERROR] %v\n", err)
	}
	os.Exit(1)
}

// ----------------------------------------------------------------------------
// Downloads Command
// ----------------------------------------------------------------------------

func handleDownloads(ctx context.Context, args []string) {
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

	// Helper to get authenticated client
	getDownloadsClient := func() *downloads.Client {
		creds, err := auth.ResolveCredentials(profileVal)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[ERROR] %v\n", err)
			os.Exit(1)
		}
		token, err := auth.Authenticate(ctx, "", "", creds.ClientID, creds.ClientSecret, "myf5_scope")
		checkErr(ctx, err, "Authentication failed")
		return downloads.NewClient(token, "")
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
			os.Exit(1)
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
		os.Exit(1)

	case "versions", "list-versions":
		if len(remaining) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: myf5 downloads versions <product-family> <product-line>")
			os.Exit(1)
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
			os.Exit(1)
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
			os.Exit(1)
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
			os.Exit(1)
		}
		family, line, ver, cont, fileName := remaining[0], remaining[1], remaining[2], remaining[3], remaining[4]
		outPath := outputVal
		if outPath == "" {
			outPath = filepath.Join(getDefaultDownloadsDir(), filepath.Base(fileName))
		}

		client := getDownloadsClient()
		fmt.Printf("Fetching download links for %s...\n", fileName)
		linksResp, err := client.GetFileLinks(ctx, family, line, ver, cont, fileName, "english")
		checkErr(ctx, err, "Failed to retrieve download link")
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
				os.Exit(130)
			}
			fmt.Fprintf(os.Stderr, "\n[DOWNLOAD FAILED] %v\n", err)
			os.Exit(1)
		}
		bar.Finish(fmt.Sprintf("Download verified and complete: %s", outPath))

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
			os.Exit(130)
		}
		fmt.Fprintf(os.Stderr, "Failed to load catalog: %v\n", err)
		os.Exit(1)
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
				os.Exit(130)
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
					os.Exit(130)
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
					os.Exit(130)
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
						os.Exit(130)
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
							os.Exit(130)
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
									os.Exit(130)
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
									os.Exit(130)
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
									os.Exit(130)
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
									os.Exit(130)
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
										os.Exit(130)
									}
									fmt.Fprintf(os.Stderr, "\n[ERROR] Download failed: %v\n", err)
								} else {
									bar.Finish(fmt.Sprintf("File saved successfully to %s", targetPath))
								}
							}
						}
						if len(targetContainers) == 1 {
							continue categoryLoop
						}
					}
				}
			}
		}
	}
}

// getDefaultDownloadsDir returns ~/Downloads if it exists, falling back to home dir or current dir.
func getDefaultDownloadsDir() string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		dl := filepath.Join(home, "Downloads")
		if info, err := os.Stat(dl); err == nil && info.IsDir() {
			return dl
		}
		return home
	}
	return "."
}

// ----------------------------------------------------------------------------
// Cases Command
// ----------------------------------------------------------------------------

func handleCases(ctx context.Context, args []string) {
	if len(args) == 0 {
		fmt.Println("Usage: myf5 cases [list | metadata | comment <case-num> <note>]")
		os.Exit(1)
	}

	action := args[0]

	creds, err := auth.ResolveCredentials("default")
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] %v\n", err)
		os.Exit(1)
	}
	token, err := auth.Authenticate(ctx, "", "", creds.ClientID, creds.ClientSecret, "myf5_scope")
	checkErr(ctx, err, "Authentication failed")
	client := cases.NewClient(token, "")

	switch action {
	case "list":
		resp, err := client.ListCases(ctx, false)
		checkErr(ctx, err, "Failed to list cases")
		fmt.Println("MyF5 Support Cases:")
		for _, c := range resp.Data {
			fmt.Printf("  Case #%-10s [%-8s] [%-6s] %s\n", c.CaseNumber, c.Status, c.Severity, c.Subject)
		}
	case "metadata":
		resp, err := client.GetMetadata(ctx)
		checkErr(ctx, err, "Failed to get metadata")
		fmt.Printf("Case Metadata Schema retrieved (%d root keys).\n", len(resp.Data))
	case "comment":
		if len(args) < 3 {
			fmt.Fprintln(os.Stderr, "Usage: myf5 cases comment <case-number> <note>")
			os.Exit(1)
		}
		_, err := client.AddComment(ctx, args[1], args[2])
		checkErr(ctx, err, "Failed to add comment")
		fmt.Println("Comment added successfully.")
	default:
		fmt.Fprintf(os.Stderr, "Unknown cases action '%s'\n", action)
		os.Exit(1)
	}
}

// ----------------------------------------------------------------------------
// iHealth Command
// ----------------------------------------------------------------------------

func handleIHealth(ctx context.Context, args []string) {
	if len(args) == 0 {
		fmt.Println("Usage: myf5 ihealth [list | upload <file.qkview>]")
		os.Exit(1)
	}

	action := args[0]

	creds, err := auth.ResolveCredentials("default")
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] %v\n", err)
		os.Exit(1)
	}
	token, err := auth.Authenticate(ctx, "", "", creds.ClientID, creds.ClientSecret, "ihealth")
	checkErr(ctx, err, "Authentication failed")
	client := ihealth.NewClient(token, "")

	switch action {
	case "list":
		summaries, err := client.ListQKViews(ctx)
		checkErr(ctx, err, "Failed to list iHealth QKViews")
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
		fileStat, err := os.Stat(filePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Cannot access file %s: %v\n", filePath, err)
			os.Exit(1)
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
				os.Exit(130)
			}
			fmt.Fprintf(os.Stderr, "\nUpload failed: %v\n", err)
			os.Exit(1)
		}
		bar.Finish(fmt.Sprintf("QKView uploaded. iHealth ID: %s (Status: %s)", resp.ID, resp.Status))
	default:
		fmt.Fprintf(os.Stderr, "Unknown ihealth action '%s'\n", action)
		os.Exit(1)
	}
}

// ----------------------------------------------------------------------------
// BIG-IP Command
// ----------------------------------------------------------------------------

func handleBIGIP(ctx context.Context, args []string) {
	if len(args) == 0 {
		fmt.Println("Usage: myf5 bigip [ready <host> | qkview <host>] [--user <user>] [--port <port>]")
		os.Exit(1)
	}

	action := args[0]
	actionArgs := args[1:]
	if len(actionArgs) < 1 {
		fmt.Fprintf(os.Stderr, "Usage: myf5 bigip %s <host> [--user <user>] [--port <port>]\n", action)
		os.Exit(1)
	}

	host := actionArgs[0]
	user := os.Getenv("BIGIP_USER")
	if user == "" {
		user = "admin"
	}
	port := 0

	for i := 1; i < len(actionArgs); i++ {
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
		}
	}

	pw := os.Getenv("BIGIP_PASSWORD")
	if pw == "" {
		// Prompt securely from terminal if interactive TTY is available
		if term.IsTerminal(int(os.Stdin.Fd())) {
			fmt.Printf("Enter BIG-IP password for %s@%s: ", user, host)
			bytePw, err := term.ReadPassword(int(os.Stdin.Fd()))
			fmt.Println()
			if err == nil {
				pw = strings.TrimSpace(string(bytePw))
			}
		}
	}

	if pw == "" {
		fmt.Fprintf(os.Stderr, "[ERROR] BIGIP_PASSWORD environment variable must be set, or entered at prompt.\n")
		os.Exit(1)
	}

	client := bigip.NewClient(host, port, user, pw)

	switch action {
	case "ready":
		ready, err := client.CheckReady(ctx)
		checkErr(ctx, err, "Ready check failed")
		if ready {
			fmt.Printf("BIG-IP appliance at %s is READY (HTTP 200).\n", host)
		} else {
			fmt.Printf("BIG-IP appliance at %s is NOT READY.\n", host)
		}
	case "qkview":
		spinner := progress.NewSpinner(fmt.Sprintf("Generating diagnostic QKView on %s", host))
		spinner.Start()
		remoteFile, err := client.GenerateQKViewWithStatus(ctx, "", true, func(status string) {
			spinner.SetMessage(status)
		})
		if err != nil {
			if errors.Is(err, context.Canceled) || ctx.Err() != nil {
				spinner.Abort("QKView generation cancelled by user.")
				fmt.Fprintln(os.Stderr, "\n[CANCELLED] Operation aborted by user.")
				os.Exit(130)
			}
			spinner.Fail(fmt.Sprintf("QKView generation failed: %v", err))
			os.Exit(1)
		}
		spinner.Success(fmt.Sprintf("QKView generated on appliance: /var/tmp/qkviews/%s", remoteFile))

		destPath := filepath.Join(getDefaultDownloadsDir(), remoteFile)
		bar := progress.NewBar(0, "Downloading")
		err = client.DownloadQKView(ctx, remoteFile, destPath, func(transferred, total int64) {
			bar.Total = total
			bar.Update(transferred)
		})
		if err != nil {
			if errors.Is(err, context.Canceled) || ctx.Err() != nil {
				bar.Abort("QKView download cancelled by user.")
				fmt.Fprintln(os.Stderr, "\n[CANCELLED] Operation aborted by user.")
				os.Exit(130)
			}
			fmt.Fprintf(os.Stderr, "\n[ERROR] Failed to download QKView: %v\n", err)
			os.Exit(1)
		}
		bar.Finish(fmt.Sprintf("QKView saved to %s", destPath))

	case "download":
		if len(actionArgs) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: myf5 bigip download <url> <remote-filename>")
			os.Exit(1)
		}
		remoteFile := actionArgs[1]
		destPath := filepath.Join(getDefaultDownloadsDir(), remoteFile)
		bar := progress.NewBar(0, "Downloading")
		err := client.DownloadQKView(ctx, remoteFile, destPath, func(transferred, total int64) {
			bar.Total = total
			bar.Update(transferred)
		})
		if err != nil {
			if errors.Is(err, context.Canceled) || ctx.Err() != nil {
				bar.Abort("Download cancelled by user.")
				fmt.Fprintln(os.Stderr, "\n[CANCELLED] Operation aborted by user.")
				os.Exit(130)
			}
			fmt.Fprintf(os.Stderr, "\n[ERROR] Download failed: %v\n", err)
			os.Exit(1)
		}
		bar.Finish(fmt.Sprintf("File saved to %s", destPath))
	default:
		fmt.Fprintf(os.Stderr, "Unknown bigip action '%s'\n", action)
		os.Exit(1)
	}
}

// ----------------------------------------------------------------------------
// Doctor Command
// ----------------------------------------------------------------------------

func handleDoctor(ctx context.Context, args []string) {
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
		target := net.JoinHostPort(ep.fqdn, strconv.Itoa(ep.port))
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

# Command-Line Interface (CLI) Reference

`myf5` is a high-performance, zero-dependency command-line utility built in pure Go for orchestrating F5 cloud services, BIG-IP TMOS appliances, and BIG-IQ Centralized Management.

---

## Global Syntax & Policy

```bash
myf5 <command> [subcommand] [arguments...] [options]
```

### Zero-Secret Security Enforcement
In accordance with enterprise DevSecOps standards, **passing credentials or tokens via command-line flags is strictly prohibited**. 
- Flags like `--password`, `-pw`, `--client-secret`, or `--token` are trapped prior to command dispatch. (Note that `-p` is *not* trapped — on appliance commands it is the port flag.)
- When detected, `myf5` immediately terminates with exit code `2` to prevent credentials from leaking into process tables (`/proc/$PID/cmdline`, `ps aux`), system audit logs (auditd, Falco), and shell histories (`.zsh_history`, `.bash_history`).
- Store cloud credentials in `~/.f5api_credentials` or set `F5_CLIENT_ID` / `F5_CLIENT_SECRET` in the environment.
- Appliance passwords are provided via environment variables (`BIGIP_PASSWORD`, `BIGIQ_PASSWORD`), SSH keys/agents, or entered interactively at the masked terminal prompt.

---

## Summary of Commands

| Command | Description |
| :--- | :--- |
| [`downloads`](#1-myf5-downloads) | Browse the MyF5 software catalog and stream multi-GB images |
| [`cases`](#2-myf5-cases) | Inspect, query, and comment on MyF5 support cases |
| [`wizard`](#11-myf5-wizard) | Interactive guided support case creation wizard with live appliance auto-fill |
| [`ihealth`](#3-myf5-ihealth) | Upload and audit QKView diagnostic bundles on F5 iHealth |
| [`bigip`](#4-myf5-bigip) | Orchestrate BIG-IP TMOS appliances (readiness, QKView generation, push, GeoIP) |
| [`bigiq`](#5-myf5-bigiq) | Orchestrate BIG-IQ Centralized Management appliances (readiness, push) |
| [`push`](#6-myf5-push) | Push downloads or files to BIG-IP / BIG-IQ via iControl REST or SCP |
| [`pull`](#7-myf5-pull) | Pull files or diagnostics from BIG-IP / BIG-IQ via iControl REST or SCP |
| [`geoip`](#12-myf5-geoip) | Apply GeoIP updates directly to BIG-IP appliances (alias: `bigip geoip`) |
| [`proxy`](#13-myf5-proxy) | View or configure HTTP/HTTPS/SOCKS5 outbound egress proxy |
| [`doctor`](#8-myf5-doctor) | Run connectivity, DNS, egress, and credential diagnostics |
| [`interactive`](#14-myf5-interactive) | Launch interactive REPL shell with tab completion (`shell`, `repl`) |
| [`completion`](#9-myf5-completion) | Generate native shell autocompletion scripts (`bash`, `zsh`, `fish`) |
| [`version`](#10-myf5-version) | Display application version and toolchain info |

---

## 1. `myf5 downloads`

Browse the MyF5 software image catalog, query releases, and download images with live streaming progress, cryptographic checksum verification, and optional post-download appliance push.

> **Endpoint:** `https://api.software.downloads.f5.com` (gateway prefix `/v1/downloads-management/v1/downloads/...`). Requests authenticate with an OAuth2 Bearer token and do not use a `k` query parameter.

### Actions

#### `browse`
Launches an interactive, menu-driven terminal browser for navigating product families, lines, releases, containers, and files.
```bash
myf5 downloads browse
```

#### `families`
Lists all available F5 product families (e.g., `BIG-IP`, `NGINX`, `BIG-IQ`).
```bash
myf5 downloads families
```

#### `lines <family>`
Lists all product lines associated with a product family.
```bash
myf5 downloads lines BIG-IP
```

#### `versions <family> <line>`
Lists release versions, release dates, and container counts for a given product line.
```bash
myf5 downloads versions BIG-IP big-ip_v17.x
```

#### `containers <family> <line> [version]`
Lists container categories (e.g., `Virtual Edition`, `GeoLocationUpdates`, `Hotfix`) for a given version.
```bash
myf5 downloads containers BIG-IP big-ip_v17.x 17.1.1
```

#### `files <family> <line> [version] [container]`
Lists downloadable files, formatted sizes, and containers.
```bash
myf5 downloads files BIG-IP big-ip_v17.x 17.1.1 "Virtual Edition"
```

#### `get <family> <line> <version> <container> <file> [-o <path>] [push-options...]`
Streams the specified software image directly to disk. Uses constant RAM (<10 MB) via `io.TeeReader` and validates SHA-256 integrity upon completion. If post-download push options are provided, pushes the verified image directly to the target appliance.

```bash
# Standard local download
myf5 downloads get BIG-IP big-ip_v17.x 17.1.1 "Virtual Edition" BIGIP-17.1.1-0.0.6.ALL.qcow2.tar.gz -o /var/tmp/bigip.qcow2.tar.gz

# Download and push directly to BIG-IP appliance
myf5 downloads get BIG-IP big-ip_v17.x 17.1.1 "Base Software & ISO Releases" BIGIP-17.1.1-0.0.6.iso \
    -o /var/tmp/BIGIP-17.1.1.iso \
    --push 10.1.1.4 \
    --push-method icontrol \
    --push-dest /shared/images
```

### Options
- `-o, --output <path>`: Destination file path for downloaded image (default: `~/Downloads/<filename>`).
- `--profile <name>`: Select credentials profile from `~/.f5api_credentials` (default: `"default"`).
- `--push <host>`: Target appliance host IP, FQDN, or URL to push to immediately following download verification.
- `--push-method <icontrol|scp>`: Appliance transfer method (`icontrol` or `scp`, default: `icontrol`).
- `--push-dest <path>`: Destination directory on appliance (e.g., `/shared/images` or `/shared/GeoIP`).
- `--push-target <bigip|bigiq>`: Appliance operating system type (`BIG-IP` or `BIG-IQ`, default: `BIG-IP`).
- `--push-user <user>`: Appliance administrative user (default: `$BIGIP_USER` / `$BIGIQ_USER` or `"admin"`).
- `--push-port <port>`: Appliance HTTPS or SSH port (default: 443 for iControl REST, 22 for SCP).
- `--push-key <key>`: Path to SSH private key for SCP transfer authentication.

---

## 2. `myf5 cases`

Manage and inspect MyF5 technical support cases via the Support Case API
(`https://support.apis.f5.com`, endpoint prefix `/case-management/v1/cases`).

> **Case Number Format:** Case numbers are 8 digits with no letter prefix (for example, `01234567`), matching the format displayed in the MyF5 portal.

### Actions

#### `list [--all]`
Lists support cases for the authenticated account, displaying case number,
status, priority, and subject. Open cases only by default; `--all` includes
closed ones.
```bash
myf5 cases list
myf5 cases list --all
```

#### `metadata`
Retrieves the authoritative schema metadata and valid taxonomy values for case creation from the live MyF5 API.
```bash
myf5 cases metadata
```

Returns the full taxonomy schema containing 10 categories: `prodFamilies` (with nested `products` and supported `versions`), `priorities`, `reasonsForContact`, `preferredContactMethods`, `timeZones`, `accounts`, `cloudProviders`, `linuxDistributions`, `nginxPlusVersions`, and `status`.

#### `comment <case-number> [note]`
Appends a status comment or investigation update to an open support case. If `[note]` is omitted, launches an interactive multiline text buffer with editing, undo/redo (`Ctrl+Z`), and word wrapping.
```bash
# Pass comment directly
myf5 cases comment 01234567 "Uploaded new diagnostic packet captures."

# Launch interactive multiline editor
myf5 cases comment 01234567
```

> The API reports failure in the response body (`isError`) as well as by HTTP status code; `myf5` validates both before reporting success.

#### `get <case-number>`
Shows one case in full: status, priority, product, version, opened timestamp (`dateOpened`),
account, contacts, the description, and a list of the messages on the email thread.
```bash
myf5 cases get 01234567
```

#### `create [flags]`
Opens a support case. Run with no flags for the full list (or to launch the interactive wizard when connected to a terminal).
```bash
myf5 cases create \
  --family BIG-IP --product "BIG-IP LTM" --version 17.5.1 \
  --serial ABC1234 \
  --subject "SSL handshake failures after upgrade" \
  --description "Full description of the problem." \
  --priority "4 - General Assistance" \
  --reason "General Problem" \
  --email you@example.com \
  --timezone "(GMT -5:00) US/Eastern - Eastern Standard Time"
```

Use `--dry-run` to print the payload and validate required fields locally without contacting the API.

> **Entitlement Identifier Requirements:**
> - **BIG-IP, F5OS, and hardware families** require `--serial` (chassis serial number or the last 7 characters of a registration key).
> - **NGINX and Aspen Mesh** require `--subscription` (for example, `A-S00038458`).
> - `myf5` validates the family-specific identifier locally before sending the request. If the identifier is unrecognized or not covered by an active support contract, the API returns `403 Forbidden` (a data validation error rather than a credentials error).
> - **Opening a case without an entitlement identifier:** The public Support Case API requires an entitlement identifier on creation, whereas the [MyF5 web portal](https://my.f5.com) allows opening a case without one. If you do not have a serial number or subscription ID available, open the case in the MyF5 web portal first and then use `myf5 cases comment` or `myf5 ihealth upload` with the assigned case number.
> - **Pre-Sales Cases:** `--reason "Pre Sales"` is restricted to F5 Systems Engineers; customer accounts should use `"General Problem"` or another reason from `myf5 cases metadata`.

#### `wizard` (or top-level `myf5 wizard`)
Launches the interactive, step-by-step Support Case Creation Wizard with appliance auto-detection and dry-run safety gates.
```bash
myf5 cases wizard
# or simply:
myf5 wizard
# or via flag:
myf5 cases create --wizard
```

The wizard guides you through:
1. **Appliance & Diagnostic Auto-Detection**: Optionally connects to a live BIG-IP appliance or inspects a local `.qkview` diagnostic tarball to automatically extract product family, product name, TMOS version, serial number / registration key, and hostname.
2. **Product & Entitlement Selection**: Prompts for required fields with sane defaults, enforcing serial numbers for hardware appliances or subscriptions for NGINX / Aspen Mesh.
3. **Priority & Problem Description**: Guided selection of case priority (`1 - Site Down` to `4 - General Assistance`) and reason for contact.
4. **Contact & Environment**: Automatically detects local system timezone in F5 format `(GMT -04:00) EDT`.
5. **Review & Dry-Run Safety Gate**: Displays a formatted review box and prompts with `[d] Dry Run`, `[s] Submit`, or `[c] Cancel` (default). In dry-run mode, validates all required fields without submitting any data to F5 Support.

---

## 3. `myf5 ihealth`

Interact with the F5 iHealth API (`ihealth-api.f5.com` / `ihealth2-api.f5.com`) for diagnostic QKView bundle processing.

### Actions

#### `list`
Lists recent QKView uploads and processing statuses associated with the account.
```bash
myf5 ihealth list
```

#### `upload <file.qkview> [case-number] [description]`
Uploads a local diagnostic QKView bundle to iHealth with a real-time progress bar.
```bash
myf5 ihealth upload /path/to/appliance.qkview "01234567" "Initial triage bundle"
```

---

## 4. `myf5 bigip`

Automate BIG-IP TMOS appliances over iControl REST or native Go SCP over SSH. Supports direct IP addressing, custom ports, and cloud/UDF external access proxies.

### Actions

#### `ready <host> [--user <user>] [--port <port>]`
Verifies management plane availability against `/mgmt/tm/sys/ready` (with fallback to `/mgmt/shared/echo`).
```bash
# Direct appliance IP
myf5 bigip ready 10.1.1.4

# Via UDF or Cloud External Access Proxy
myf5 bigip ready https://aa68f6ba-a751-4740-9382-85f7cd7940f2.access.udf.f5.com/
```

#### `info <host | file.qkview> [--json]`
Inspects a live BIG-IP appliance or local `.qkview` archive to extract TMOS version, build, chassis/platform model, base registration key, and chassis serial number.
```bash
# Query live appliance
myf5 bigip info 10.1.1.4 --user admin

# Query offline QKView diagnostic archive
myf5 bigip info /path/to/diagnostic.qkview
```

#### `serial <host | file.qkview> [--json]`
Quickly extracts and prints the hardware chassis serial number from a live appliance or offline `.qkview`.
```bash
myf5 bigip serial 10.1.1.4
```

#### `qkview <host> [--user <user>] [--port <port>]`
Generates a complete, non-truncating (`-s0`) diagnostic QKView archive on the BIG-IP appliance via `/mgmt/tm/util/qkview` or `/mgmt/cm/autodeploy/qkview` with live spinner status updates, then downloads it directly to local disk (falling back to native Go SCP if autodeploy download is unavailable).
```bash
myf5 bigip qkview 10.1.1.4 --user admin
```

#### `push <host> <file> [--method auto|icontrol|scp] [--dest <path>] [--apply-geoip] [--user <user>] [--port <port>] [-i <key>]`
Pushes a local software image, engineering hotfix, or GeoDB archive to the BIG-IP appliance with automatic seamless failover between iControl REST and SCP.
```bash
# Push ISO to /shared/images with automatic failover (default)
myf5 bigip push 10.1.1.4 /var/tmp/BIGIP-17.1.1.iso --dest /shared/images

# Push hotfix explicitly via native Go SCP
myf5 bigip push 10.1.1.4 /var/tmp/Hotfix-BIGIP-17.1.1.1.0.0.2-ENG.iso --method scp -i ~/.ssh/f5_key

# Push GeoDB update archive and immediately extract & activate in TMM
myf5 bigip push 10.1.1.4 /var/tmp/ip-geolocation-default.zip --apply-geoip
```

#### `geoip <host> <zip-path> [--keep-extracted] [--method auto|icontrol|scp] [--json]`
Uploads a GeoIP update ZIP archive, extracts the RPM packages on the appliance, installs them via `geoip_update_data`, and signals TMM to reload the geolocation database.
```bash
myf5 bigip geoip 10.1.1.4 /var/tmp/ip-geolocation-default.zip
```

#### `download <host> <remote-file> [-o <dest>] [--method auto|icontrol|scp] [--user <user>] [--port <port>] [-i <key>]`
Downloads a remote file or diagnostic bundle from the BIG-IP appliance with automatic failover between iControl REST and SCP.
```bash
# Download software image from /shared/images/ (auto-attempts REST, falls back to SCP)
myf5 bigip download 10.1.1.4 /shared/images/BIGIP-17.1.1.iso -o ./BIGIP-17.1.1.iso

# Download configuration file via SCP
myf5 bigip download 10.1.1.4 /config/bigip.conf -o ./bigip.conf --method scp -u root -i ~/.ssh/id_ed25519
```

### Options
- `-u, --user <username>`: Administrative user (default: `$BIGIP_USER` or `"admin"` for REST, `"root"`/`"admin"` for SCP). Note: on TMOS, accounts restricted to `tmsh` shell can only SCP to whitelisted paths (`/var/tmp`); direct SCP to `/shared/images` requires Advanced Shell (`bash`) access or `root`.
- `-p, --port <port>`: HTTPS port for iControl (default: 443) or SSH port for SCP (default: 22).
- `-m, --method <auto|icontrol|scp>`: Transfer protocol with automatic failover (default: `auto`).
- `-d, --dest <path>`: Destination directory on appliance for uploads (e.g., `/shared/images` or `/var/tmp`).
- `-o, --output <path>`: Local destination path for downloads (default: current directory or default downloads dir).
- `-i, --identity <path>`: Private key path for SSH authentication during SCP transfers.
- **Authentication**: Set via `BIGIP_PASSWORD` environment variable, SSH key / agent, or enter interactively at the masked terminal prompt.

---

## 5. `myf5 bigiq`

Manage and orchestrate BIG-IQ Centralized Management appliances.

### Actions

#### `ready <host> [--user <user>] [--port <port>]`
Checks BIG-IQ management plane availability against `/mgmt/tm/sys/ready` or `/mgmt/shared/echo`.
```bash
myf5 bigiq ready 192.0.2.50
```

#### `push <host> <file> [--method auto|icontrol|scp] [--dest <path>] [--user <user>] [--port <port>] [-i <key>]`
Uploads software images, hotfixes, or files to BIG-IQ central software repositories with automatic failover.
```bash
# Push to BIG-IQ central image repository via auto failover
myf5 bigiq push 192.0.2.50 /var/tmp/BIGIP-17.1.1.iso --dest /shared/images

# Push via native SCP with identity key
myf5 bigiq push 192.0.2.50 /var/tmp/BIGIP-17.1.1.iso --method scp -i ~/.ssh/id_rsa
```

#### `download <host> <remote-file> [-o <dest>] [--method auto|icontrol|scp] [--user <user>] [--port <port>] [-i <key>]`
Downloads software images or files from BIG-IQ with automatic failover.
```bash
myf5 bigiq download 192.0.2.50 /shared/images/BIGIP-17.1.1.iso -o ./BIGIP-17.1.1.iso
```

### Options
- `-u, --user <username>`: Administrative user (default: `$BIGIQ_USER`, `$BIGIP_USER`, or `"admin"`).
- `-p, --port <port>`: Management HTTPS / SSH port.
- `-m, --method <auto|icontrol|scp>`: Transfer protocol with automatic failover (default: `auto`).
- `-d, --dest <path>`: Remote directory on appliance.
- `-o, --output <path>`: Local destination file path.
- `-i, --identity <path>`: SSH private key for SCP transfer.
- **Authentication**: Set via `BIGIQ_PASSWORD` (or `BIGIP_PASSWORD`), SSH key / agent, or enter interactively at the masked terminal prompt.

---

## 6. `myf5 push`

Unified command to push software downloads, hotfixes, or packages to any BIG-IP or BIG-IQ instance with automatic failover.

### Syntax
```bash
myf5 push <host> <file> [--method auto|icontrol|scp] [--dest <path>] [--target bigip|bigiq] [--user <user>] [--port <port>] [-i <key>]
```

### Examples
```bash
# Push software image to BIG-IP with automatic failover
myf5 push 10.1.1.4 /var/tmp/BIGIP-17.1.1.iso --target bigip --dest /shared/images

# Push software image to BIG-IQ repository via SCP
myf5 push 192.0.2.50 /var/tmp/BIGIP-17.1.1.iso --target bigiq --method scp --dest /shared/images

# Push using specific SSH key
myf5 push 10.1.1.4 /var/tmp/ip-geolocation-default.zip --dest /shared/GeoIP -i ~/.ssh/f5_deploy
```

---

## 7. `myf5 pull`

Unified command to download files, diagnostics, or software images from any BIG-IP or BIG-IQ appliance with automatic failover.

### Syntax
```bash
myf5 pull <host> <remote-file> [-o <dest>] [--method auto|icontrol|scp] [--target bigip|bigiq] [--user <user>] [--port <port>] [-i <key>]
```

### Examples
```bash
# Pull software image from BIG-IP (attempts REST, falls back to SCP)
myf5 pull 10.1.1.4 /shared/images/BIGIP-17.1.1.iso -o ./BIGIP-17.1.1.iso

# Pull system log file from BIG-IP via SCP
myf5 pull 10.1.1.4 /var/log/ltm -o ./ltm.log --method scp -u root -i ~/.ssh/id_ed25519

# Pull image from BIG-IQ central repository
myf5 pull 192.0.2.50 /shared/images/BIGIP-17.1.1.iso --target bigiq
```

---

## 8. `myf5 doctor`

Pre-flight diagnostic audit that verifies local credentials, DNS resolution, and TCP port 443 egress to all authoritative F5 cloud endpoints.

```bash
myf5 doctor
```

Output audits:
1. `~/.f5api_credentials` or environment credentials (redacted output).
2. Outbound TCP 443 connectivity to Auth0, Okta, Downloads API, Support Case API, iHealth, and Callhome.

---

## 9. `myf5 completion`

Generates tab autocompletion scripts for the specified shell.

```bash
myf5 completion [bash | zsh | fish]
```

### Examples

```bash
# Zsh (current session)
source <(myf5 completion zsh)

# Zsh (persistent configuration)
myf5 completion zsh > "${fpath[1]}/_myf5"

# Bash (current session)
source <(myf5 completion bash)

# Bash (persistent on Linux)
myf5 completion bash | sudo tee /etc/bash_completion.d/myf5 > /dev/null

# Fish (persistent)
myf5 completion fish > ~/.config/fish/completions/myf5.fish
```

---

## 10. `myf5 version`

Prints the compiled application version and target architecture.
```bash
myf5 version
```

---

## 11. `myf5 wizard`

Top-level alias for `myf5 cases wizard`. Launches an interactive, multi-step terminal wizard to guide you through creating an F5 Support Case with zero memorization of product schema strings:

```bash
myf5 wizard
```

### Features:
- **Automatic Environment Detection**: Reads local machine timezone and maps it to valid F5 Salesforce timezone strings automatically.
- **Live Device Autofill**: Optionally connects to a live BIG-IP appliance (`--user admin`) or parses a local `.qkview` file to extract the chassis serial number, TMOS product version, and software line automatically.
- **Multiline Text Buffer with Undo**: The description step launches an interactive multiline text buffer with editing, word wrapping, undo/redo (`Ctrl+Z`), and line navigation.
- **Bidirectional Step Navigation**: Type `back` or `b` at any prompt to re-visit previous steps and adjust earlier selections.
- **Safety Dry-Run Gate**: Displays the full JSON payload before making any network write. Requests explicit final confirmation before sending to the live API.

---

## 12. `myf5 geoip`

Top-level alias for `myf5 bigip geoip`. Uploads, unpacks, installs, and reloads F5 GeoIP database packages directly on a BIG-IP appliance in a single automated command:

```bash
myf5 geoip <host> <zip-path> [--keep-extracted] [--method auto|icontrol|scp] [--json]
```

### Actions:
1. Validates the local ZIP package and inspects contained GeoIP RPMs (`F5GeoIPData`, `F5GeoIPDataV6`, etc.).
2. Transfers the package to `/var/tmp` on the appliance via iControl REST or SCP.
3. Automatically unzips packages and applies updates using TMOS `geoip_update_data -f`.
4. Executes `tmsh load sys geoip-data` to activate updated geolocation routing in TMM memory without disruption.
5. Cleans up temporary files on the appliance (unless `--keep-extracted` is specified).

---

## 13. `myf5 proxy`

Manage outbound HTTP, HTTPS, and SOCKS5 proxy settings for routing API traffic through corporate forward proxies, bastion hosts, or air-gapped jump boxes.

```bash
myf5 proxy show            # Display active egress proxy configuration
myf5 proxy set <url>       # Persistently configure default proxy (http://, https://, socks5://)
myf5 proxy clear           # Remove persistent proxy configuration
myf5 --proxy <url> <cmd>   # Specify proxy for a single command invocation
```

### Supported Schemes:
- `http://[user:pass@]host:port`: Standard HTTP CONNECT proxy.
- `https://[user:pass@]host:port`: TLS CONNECT proxy.
- `socks5://[user:pass@]host:port`: RFC 1928 SOCKS5 proxy (local DNS resolution).
- `socks5h://[user:pass@]host:port`: SOCKS5 proxy with remote DNS resolution on the proxy server.

### Environment Variable Fallback:
If no `--proxy` flag or persistent configuration is found, `myf5` respects `MYF5_PROXY`, `F5_PROXY`, `ALL_PROXY`, `HTTPS_PROXY`, and `HTTP_PROXY`. Proxy bypass rules can be specified via `NO_PROXY`.

---

## 14. `myf5 interactive` (alias: `shell`, `repl`)

Launches an interactive Read-Eval-Print-Loop (REPL) shell environment with built-in tab autocompletion, command history, and session state:

```bash
myf5 interactive
# Or shortcuts:
myf5 shell
myf5 repl
```

Within the interactive shell, execute commands directly without the `myf5` prefix:
```
myf5> doctor
myf5> downloads families
myf5> cases list
myf5> exit
```

---

## 15. Signal Handling & Interruption Safety (CTRL+C)

All long-running network connections, streaming file transfers, and appliance operations in `myf5-cli` implement robust interrupt and cancellation handling:
- **Clean Signal Traps**: Intercepts `SIGINT` (Ctrl+C) and `SIGTERM` via isolated command contexts (`signal.NotifyContext`).
- **Safe Connection Teardown**: Immediately aborts in-flight HTTP requests, closes SOCKS5 proxy tunnels, and gracefully closes SSH/SCP channels without socket leaks or hanging child processes.
- **Atomic Partial File Cleanup**: Streaming file downloads automatically scrub and remove temporary `.part` files on disk upon interrupt, preventing corrupted or incomplete downloads from being mistaken for valid files.
- **Exit Code 130**: Returns standard POSIX exit code `130` (`128 + SIGINT`) on cancellation.

---

## 16. Process Exit Codes

`myf5` returns standard POSIX exit codes to support reliable shell script branching and CI/CD pipelines:

| Exit Code | Classification | Description |
| :--- | :--- | :--- |
| `0` | **Success** | Operation completed successfully and verified. |
| `1` | **General Error** | Command syntax error, invalid arguments, network failure, missing credentials in non-interactive environment, or appliance error. |
| `2` | **Zero-Secret Violation** | Security error triggered when prohibited credential flags (`--password`, `--client-secret`) are supplied on the command line. |
| `130` | **Cancelled / Interrupted** | Execution aborted by user via `SIGINT` (Ctrl+C) or `SIGTERM`. |


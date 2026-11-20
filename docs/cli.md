# Command-Line Interface (CLI) Reference

`myf5` is a high-performance, zero-dependency command-line utility built in pure Go for orchestrating F5 cloud services, BIG-IP TMOS appliances, and BIG-IQ Centralized Management.

---

## Global Syntax & Policy

```bash
myf5 <command> [subcommand] [arguments...] [options]
```

### Two-Tier Zero-Secret Security Enforcement
In accordance with enterprise DevSecOps standards, **passing credentials or tokens via command-line flags is strictly prohibited**:
- **Tier 1 — Global Pre-Dispatch Rejection (`pkg/auth.CheckNoCLISecrets` on `os.Args[1:]`, Exit Code `2`)**: Trapped before command dispatch across all commands. Rejects all 22 global secret flags: `--password`, `-password`, `--pw`, `-pw`, `--client-secret`, `-client-secret`, `--clientsecret`, `-clientsecret`, `--secret`, `-secret`, `--token`, `-token`, `--access-token`, `-access-token`, `--api-key`, `-api-key`, `--apikey`, `-apikey`, `--auth-token`, `-auth-token`, `--key`, `-key`. (Note that `-p` is *not* trapped — on appliance commands it is the port flag, and `--cert-key` / `--key-file` specifies a TLS client private key file path.)
- **Tier 2 — Appliance Subcommand Flag Rejection (`cmd/myf5.rejectSecretFlag`, Exit Code `1`)**: Appliance subcommand flag parsers additionally block `--pass`, `-P`, and `--ssh-passphrase` with exit code `1`.
- This two-tier defense prevents credentials from leaking into process tables (`/proc/$PID/cmdline`, `ps aux`), system audit logs (auditd, Falco), and shell histories (`.zsh_history`, `.bash_history`).
- Store cloud credentials in `~/.f5api_credentials` or set `F5_CLIENT_ID` / `F5_CLIENT_SECRET` (and dedicated `IHEALTH_CLIENT_ID` / `IHEALTH_CLIENT_SECRET`) in the environment.
- Appliance passwords are provided via environment variables (`BIGIP_PASSWORD`, `BIGIQ_PASSWORD`), SSH keys (`-i <path>`) / `ssh-agent` (`SSH_AUTH_SOCK`), or entered interactively at the masked terminal prompt. Encrypted SSH private keys with `-i` are supported via `SSH_AUTH_SOCK` (`ssh-agent`), as the CLI never prompts for an SSH key passphrase on the command line.

### Machine-Readable Output (`--json`) & Terminal Color (`NO_COLOR`)
- **`--json` Output**: Supported across `cases` (`list`, `get`, `comment`, `metadata`), `ihealth` (`list`, `upload`), `info` (`myf5 info`, `myf5 bigip info`, `myf5 bigiq info`), `serial` (`myf5 serial`, `myf5 bigip serial`, `myf5 bigiq serial`), `push` (`myf5 push`, `myf5 bigip push`, `myf5 bigiq push`), and `geoip` (`myf5 geoip`, `myf5 bigip geoip`).
- **`NO_COLOR`**: Setting the standard `NO_COLOR` environment variable (e.g., `export NO_COLOR=1`) disables ANSI color escape sequences in interactive selectors (`myf5 downloads browse`, `myf5 wizard`) and terminal output.

---

## Summary of Commands

| Command | Description |
| :--- | :--- |
| [`downloads`](#1-myf5-downloads) | Browse the MyF5 software catalog and stream multi-GB images |
| [`cases`](#2-myf5-cases) | Inspect, query, create, and comment on MyF5 support cases |
| [`wizard`](#10-myf5-wizard) | Interactive guided support case creation wizard with live BIG-IP / BIG-IQ & `.qkview` auto-fill |
| [`ihealth`](#3-myf5-ihealth) | Upload and audit QKView diagnostic bundles on F5 iHealth |
| [`bigip`](#4-myf5-bigip) | Orchestrate BIG-IP TMOS appliances (`ready`, `info`, `serial`, `qkview`, `push`, `pull`, `download`, `geoip`) |
| [`bigiq`](#5-myf5-bigiq) | Orchestrate BIG-IQ Centralized Management appliances (`ready`, `info`, `serial`, `qkview`, `push`, `pull`, `download`) |
| [`push`](#6-myf5-push) | Push downloads or files to BIG-IP / BIG-IQ via iControl REST or SCP (`--target auto\|bigip\|bigiq`) |
| [`pull`](#7-myf5-pull) | Pull `.iso` or `.qkview` artifacts from BIG-IP / BIG-IQ via iControl REST or SCP (`--target auto\|bigip\|bigiq`) |
| [`geoip`](#11-myf5-geoip) | Apply GeoIP updates directly to BIG-IP appliances (BIG-IP only; alias: `bigip geoip`) |
| [`proxy`](#12-myf5-proxy) | View or configure HTTP/HTTPS/SOCKS5 outbound egress proxy |
| [`doctor`](#8-myf5-doctor) | Run connectivity, DNS, egress, and credential diagnostics |
| [`interactive`](#13-myf5-interactive-alias-shell-repl-i) | Launch interactive REPL shell with tab completion (`shell`, `repl`, `i`) |
| [`version`](#9-myf5-version) | Display application version and toolchain info |

> **Top-Level Appliance Shortcuts:** `myf5 ready`, `myf5 info`, `myf5 serial`, `myf5 qkview`, `myf5 push`, `myf5 pull`, and `myf5 geoip` can be invoked directly as top-level commands with automatic target detection.

---

## 1. `myf5 downloads`

Browse the MyF5 software image catalog, query releases, and download images (via signed AWS CloudFront `*.cloudfront.net` CDN URLs) with live streaming progress, cryptographic SHA-256 checksum verification, and optional post-download appliance push.

> **Endpoint:** `https://api.software.downloads.f5.com` (gateway prefix `/v1/downloads-management/v1/downloads/...`). Authenticates with an OAuth2 Bearer token (`audience=api://ciam-api-myf5`, `scope=myf5_scope`) with automatic `F5_API_KEY` gateway fallback if required.

### Actions

#### `browse`
Launches an interactive, menu-driven terminal browser for navigating product families, lines, releases, containers, and files (respects `NO_COLOR`).
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

# Download and push directly to BIG-IP or BIG-IQ appliance
myf5 downloads get BIG-IP big-ip_v17.x 17.1.1 "Base Software & ISO Releases" BIGIP-17.1.1-0.0.6.iso \
    -o /var/tmp/BIGIP-17.1.1.iso \
    --push 10.1.1.4 \
    --push-target bigip \
    --push-method icontrol \
    --push-dest /shared/images \
    --push-known-hosts ~/.ssh/known_hosts
```

### Options
- `-o, --output <path>`: Destination file path for downloaded image (default: `~/Downloads/<filename>`).
- `--profile <name>`: Select credentials profile from `~/.f5api_credentials` (default: `"default"`).
- `--push <host>`: Target appliance host IP, FQDN, or URL to push to immediately following download verification.
- `--push-method <auto|icontrol|scp>`: Appliance transfer method (`auto`, `icontrol`, or `scp`, default: `auto`).
- `--push-dest <path>`: Destination directory on appliance (e.g., `/shared/images` or `/var/tmp`).
- `--push-target <auto|bigip|bigiq>`: Appliance operating system type (accepts `"BIG-IQ"`, `"big-iq"`, `"bigiq"`, `"BIG-IP"`, `"big-ip"`, `"bigip"`, or `"auto"`, default: `BIG-IP`).
- `--push-user <user>`: Appliance administrative user (default: `$BIGIP_USER` / `$BIGIQ_USER` or `"admin"`).
- `--push-port <port>`: Appliance HTTPS or SSH port (default: 443 for iControl REST, 22 for SCP).
- `--push-key <key>`: Path to SSH private key for SCP transfer authentication (encrypted keys supported via `SSH_AUTH_SOCK`).
- `--push-known-hosts <path>`: Path to OpenSSH `known_hosts` file for host-key verification before sending credentials.
- `--push-cert <path>` / `--push-cert-key <path>`: TLS client certificate and private key for mTLS.
- `--apply-geoip` / `--keep-extracted`: Automatically unpack and apply GeoIP RPMs on BIG-IP after upload.

---

## 2. `myf5 cases`

Manage and inspect MyF5 technical support cases via the Support Case API
(`https://support.apis.f5.com`, endpoint prefix `/case-management/v1/cases`).

> **Case Number Format:** Case numbers are 8 digits with no letter prefix (for example, `01234567`), matching the format displayed in the MyF5 portal.

### Actions

#### `list [--all] [--json]`
Lists support cases for the authenticated account, displaying case number,
status, priority, and subject. Open cases only by default; `--all` includes
closed ones, and `--json` emits the raw JSON response.
```bash
myf5 cases list
myf5 cases list --all
myf5 cases list --all --json
```

#### `metadata [--json]`
Retrieves the authoritative schema metadata and valid taxonomy values for case creation from the live MyF5 API.
```bash
myf5 cases metadata
myf5 cases metadata --json
```

Returns the full taxonomy schema containing 10 categories: `prodFamilies` (with nested `products` and supported `versions`), `priorities`, `reasonsForContact`, `preferredContactMethods`, `timeZones`, `accounts`, `cloudProviders`, `linuxDistributions`, `nginxPlusVersions`, and `status`.

#### `comment <case-number> [note] [--json]`
Appends a status comment or investigation update to an open support case. If `[note]` is omitted, launches an interactive multiline text buffer supporting line-by-line undo (`:u` / `:undo`), buffer preview (`:p` / `:show`), external editor launch (`:e` / `:edit`), clear (`:c` / `:clear`), and cancel/back (`:b` / `:back`).
```bash
# Pass comment directly (with optional --json output)
myf5 cases comment 01234567 "Uploaded new diagnostic packet captures." --json

# Launch interactive multiline editor
myf5 cases comment 01234567
```

> The API reports failure in the response body (`isError`) as well as by HTTP status code; `myf5` validates both before reporting success.

#### `get <case-number> [--json]`
Shows one case in full: status, priority, product, version, opened timestamp (`dateOpened`),
account, contacts, the description, and a list of the messages on the email thread. Pass `--json` for machine-readable JSON output.
```bash
myf5 cases get 01234567
myf5 cases get 01234567 --json
```

#### `create [flags]`
Opens a support case. Run with no flags for the full list (or to launch the interactive wizard when connected to a terminal).
Supports `--from-appliance <host|file.qkview>`, `--from-device <host|file.qkview>`, `--from-bigip <host|file.qkview>`, and `--from-bigiq <host|file.qkview>` to auto-populate `productFamily`, `product`, `version`, `serialNumber`, and `hostName` from a live BIG-IP / BIG-IQ appliance or local `.qkview` archive.
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

# Or auto-populate from a live BIG-IP / BIG-IQ host or offline .qkview:
myf5 cases create --from-bigiq 192.0.2.50 \
  --subject "BIG-IQ device discovery timeout" \
  --description "Full description of the problem." \
  --priority "3 - Performance Degraded" \
  --reason "General Problem" \
  --email you@example.com \
  --timezone "(GMT -5:00) US/Eastern - Eastern Standard Time" --dry-run
```

Use `--dry-run` to print the payload and validate required fields locally without contacting the API.

> **Entitlement Identifier Requirements:**
> - **BIG-IP, BIG-IQ, F5OS, and hardware families** require `--serial` (chassis serial number or the last 7 characters of a registration key).
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
1. **Appliance & Diagnostic Auto-Detection**: Optionally connects to a live **BIG-IP** or **BIG-IQ** appliance or inspects a local `.qkview` diagnostic tarball (`pkg/bigip.InspectQKView`) to automatically extract product family (`"BIG-IP"` or `"BIG-IQ"`), product name (e.g., `"BIG-IP LTM"` or `"BIG-IQ Centralized Management"`), version, serial number / registration key, and hostname.
2. **Product & Entitlement Selection**: Prompts for required fields with sane defaults, enforcing serial numbers for hardware/virtual appliances or subscriptions for NGINX / Aspen Mesh.
3. **Priority & Problem Description**: Guided selection of case priority (`1 - Site Down` to `4 - General Assistance`) and reason for contact.
4. **Contact & Environment**: Automatically detects local system timezone in F5 format `(GMT -04:00) EDT`.
5. **Review & Dry-Run Safety Gate**: Displays a formatted review box and prompts with `[d] Dry Run`, `[s] Submit`, or `[c] Cancel` (default). In dry-run mode, validates all required fields without submitting any data to F5 Support.

---

## 3. `myf5 ihealth`

Interact with the F5 iHealth API (`ihealth-api.f5.com` / `ihealth2-api.f5.com`) for diagnostic QKView bundle processing. Uses dedicated `IHEALTH_CLIENT_ID` / `IHEALTH_CLIENT_SECRET` credentials (`audience=api://ciam-api-ihealth`, empty `scope=""` per K000130498).

### Actions

#### `list [--json]`
Lists recent QKView uploads and processing statuses associated with the account.
```bash
myf5 ihealth list
myf5 ihealth list --json
```

#### `upload <file.qkview> [case-number] [description] [--json]`
Uploads a local diagnostic QKView bundle to iHealth with a real-time progress bar.
```bash
myf5 ihealth upload /path/to/appliance.qkview "01234567" "Initial triage bundle"
myf5 ihealth upload /path/to/appliance.qkview "01234567" "Initial triage bundle" --json
```

---

## 4. `myf5 bigip`

Automate BIG-IP TMOS appliances over iControl REST or native Go SCP over SSH. Supports direct IP addressing, custom ports, and cloud/UDF external access proxies. Top-level commands (`myf5 ready`, `myf5 info`, `myf5 serial`, `myf5 qkview`, `myf5 push`, `myf5 pull`, `myf5 geoip`) provide direct shortcuts.

### Actions

#### `ready <host> [--user <user>] [--port <port>] [--cert <cert>] [--cert-key <key>]`
Verifies management plane availability against `/mgmt/tm/sys/ready` (with fallback to `/mgmt/shared/echo`). Exits `0` when `READY` and `1` when `NOT READY` or on error.
```bash
# Direct appliance IP
myf5 bigip ready 10.1.1.4

# Via UDF or Cloud External Access Proxy
myf5 bigip ready https://00000000-0000-0000-0000-000000000000.access.udf.f5.com/
```

#### `info <host | file.qkview> [--json] [--method auto|icontrol|ssh] [--known-hosts <path>]`
Inspects a live BIG-IP appliance (with automatic REST-to-SSH fallback) or local `.qkview` archive (`pkg/bigip.InspectQKView`) to extract TMOS version, build, chassis/platform model, base registration key, and chassis serial number.
```bash
# Query live appliance
myf5 bigip info 10.1.1.4 --user admin --json

# Query offline QKView diagnostic archive
myf5 bigip info /path/to/diagnostic.qkview --json
```

#### `serial <host | file.qkview> [--json] [--method auto|icontrol|ssh] [--known-hosts <path>]`
Quickly extracts and prints the hardware chassis serial number (or registration key fallback) from a live appliance or offline `.qkview`.
```bash
myf5 bigip serial 10.1.1.4 --json
myf5 bigip serial /path/to/diagnostic.qkview
```

#### `qkview <host> [--dest <path>] [--keep-remote] [--method auto|icontrol|scp] [--user <user>] [--port <port>] [-i <key>] [--known-hosts <path>]`
Generates a complete, non-truncating (`-s0`) diagnostic QKView archive on the BIG-IP appliance via `/mgmt/tm/util/qkview` or `/mgmt/cm/autodeploy/qkview` with live spinner status updates, then downloads it directly to local disk (falling back to verified native Go SCP if autodeploy download is unavailable) and cleans up the remote temporary file unless `--keep-remote` is specified.
```bash
myf5 bigip qkview 10.1.1.4 --user admin --known-hosts ~/.ssh/known_hosts
```

#### `push <host> <file> [--method auto|icontrol|scp] [--dest <path>] [--apply-geoip] [--keep-extracted] [--user <user>] [--port <port>] [-i <key>] [--known-hosts <path>] [--cert <cert>] [--cert-key <key>] [--json]`
Pushes a local software image, engineering hotfix, or GeoDB archive to the BIG-IP appliance with automatic seamless failover between iControl REST (`512 KiB` `DefaultUploadChunkSize` `Content-Range` chunks to `/mgmt/shared/file-transfer/uploads/<filename>` followed by `/mgmt/tm/util/bash` promotion) and verified `64 KiB` SCP.
```bash
# Push ISO to /shared/images with automatic failover (default)
myf5 bigip push 10.1.1.4 /var/tmp/BIGIP-17.1.1.iso --dest /shared/images --json

# Push hotfix explicitly via native Go SCP with known_hosts verification
myf5 bigip push 10.1.1.4 /var/tmp/Hotfix-BIGIP-17.1.1.1.0.0.2-ENG.iso --method scp -i ~/.ssh/f5_key --known-hosts ~/.ssh/known_hosts

# Push GeoDB update archive and immediately extract & activate in TMM
myf5 bigip push 10.1.1.4 /var/tmp/ip-geolocation-default.zip --apply-geoip --json
```

#### `geoip <host> <zip-path> [--keep-extracted] [--method auto|icontrol|scp] [--known-hosts <path>] [--json]`
**BIG-IP only.** Stages the GeoIP `.zip` archive to `/shared/tmp`, unpacks the `.rpm` files into `/shared/GeoIP` via `rpm2cpio | cpio -idmv` (and `geoip_update_data -f`), and executes `tmsh load sys geoip` (per K11176). Rejected on BIG-IQ because BIG-IQ Centralized Management does not host TMM GeoIP tables.
```bash
myf5 bigip geoip 10.1.1.4 /var/tmp/ip-geolocation-default.zip --json
```

#### `download <host> <remote-file> [-o <dest>]` / `pull <host> <remote-file> [-o <dest>]`
Validates that `<remote-file>` is an `.iso` or `.qkview` inside `/shared/images`, `/var/config/rest/downloads`, `/var/tmp`, or `/shared/tmp` (`ValidateApplianceDownloadPath`), downloads it via `1 MiB` HTTP `Range` chunks over iControl REST, and automatically falls back to verified `64 KiB` SCP.
```bash
# Download software image from /shared/images/ (attempts 1 MiB REST Range chunks, falls back to SCP)
myf5 bigip download 10.1.1.4 /shared/images/BIGIP-17.1.1.iso -o ./BIGIP-17.1.1.iso

# Pull diagnostic QKView from /var/tmp/qkviews/ via SCP with known_hosts verification
myf5 bigip pull 10.1.1.4 /var/tmp/qkviews/diagnostic.qkview -o ./diagnostic.qkview --method scp -u root -i ~/.ssh/id_ed25519 --known-hosts ~/.ssh/known_hosts
```

### Options
- `-u, --user <username>`: Administrative user (default: `$BIGIP_USER` or `"admin"` for REST, `"root"`/`"admin"` for SCP). Note: on TMOS, accounts restricted to `tmsh` shell can only SCP to whitelisted paths (`/var/tmp`); direct SCP to `/shared/images` requires Advanced Shell (`bash`) access or `root`.
- `-p, --port <port>`: HTTPS port for iControl (default: 443) or SSH port for SCP (default: 22).
- `-m, --method <auto|icontrol|scp>`: Transfer protocol with automatic failover (default: `auto`).
- `-d, --dest <path>`: Destination directory on appliance for uploads (e.g., `/shared/images` or `/var/tmp`).
- `-o, --output <path>`: Local destination path for downloads (default: `~/Downloads/<filename>`).
- `-i, --identity <path>`: Private key path for SSH authentication during SCP transfers (encrypted keys are supported via `SSH_AUTH_SOCK`).
- `--known-hosts <path>`: OpenSSH `known_hosts` file for host-key verification before sending credentials (also configured via `BIGIP_KNOWN_HOSTS`, `MYF5_SSH_KNOWN_HOSTS`, and `MYF5_STRICT_HOST_KEY_CHECKING=1`).
- `--cert <path>` / `--cert-key <path>` (`--key-file <path>`): Client TLS certificate and private key for mTLS (`BIGIP_CERT` / `BIGIP_KEY`).
- `--json`: Output machine-readable JSON (for `info`, `serial`, `push`, and `geoip`).
- **Authentication & TLS**: Set via `BIGIP_PASSWORD`, SSH key / `ssh-agent`, or interactive masked prompt. Appliance REST clients default to `SkipTLSVerify: true` for self-signed management certificates unless `MYF5_STRICT_APPLIANCE_TLS=1` is set or mTLS (`--cert`/`--cert-key`) is used; custom root CAs can be loaded via `MYF5_CA_BUNDLE` or `SSL_CERT_FILE`.

---

## 5. `myf5 bigiq`

Manage and orchestrate BIG-IQ Centralized Management appliances with full command parity (`ready`, `info`, `serial`, `qkview`, `push`, `pull`, `download`):
- **Token Authentication**: Authenticates via `POST /mgmt/shared/authn/login` with `{"username":"...","password":"..."}` (omitting `loginProviderName` per BIG-IQ REST API schema, with automatic fallback if required), refreshes tokens within 5 minutes of expiry via `POST /mgmt/shared/authn/exchange`, and stops immediately after 1 failed login attempt (`loginFailedErr`) to prevent appliance account lockout.
- **Readiness Probes**: When `/mgmt/tm/sys/ready` returns `403`/`404` on BIG-IQ, `ready` probes `/mgmt/shared/echo`, `/mgmt/cm/system/machineid-resolver`, and `/mgmt/shared/failover-state` (exiting `0` when `READY` and `1` when `NOT READY` or on error).
- **System Info & Serial**: `GetSystemInfo` queries `/mgmt/shared/identified-devices/config/device-info` and `/mgmt/shared/resolver/device-groups/cm-shared-all-big-iqs/devices`, keeping hardware `ChassisSerialNumber` (`serialNumber`) distinct from UUID `machineId`.

### Actions

#### `ready <host> [--user <user>] [--port <port>] [--cert <cert>] [--cert-key <key>]`
Checks BIG-IQ management plane availability and exits `0` when `READY` or `1` when `NOT READY` / on error.
```bash
myf5 bigiq ready 192.0.2.50
```

#### `info <host | file.qkview> [--user <user>] [--port <port>] [--known-hosts <path>] [--json]`
Inspects a live BIG-IQ appliance (with REST-to-SSH fallback) or offline `.qkview` archive (`pkg/bigip.InspectQKView`) to extract system version, edition, build, platform model, and `ChassisSerialNumber` (kept distinct from UUID `machineId`).
```bash
myf5 bigiq info 192.0.2.50 --user admin --json
myf5 bigiq info /path/to/bigiq_support.qkview --json
```

#### `serial <host | file.qkview> [--user <user>] [--port <port>] [--known-hosts <path>] [--json]`
Retrieves and prints the BIG-IQ hardware serial number (or registration key) from a live BIG-IQ host or offline `.qkview`.
```bash
myf5 bigiq serial 192.0.2.50 --user admin --json
```

#### `qkview <host> [--dest <path>] [--keep-remote] [--method auto|icontrol|scp] [--user <user>] [--port <port>] [-i <key>] [--known-hosts <path>]`
Generates and downloads a diagnostic QKView from BIG-IQ. When BIG-IQ REST `/mgmt/tm/util/qkview` or `/mgmt/tm/util/bash` is restricted (`403 Forbidden`), automatically routes directly to SSH/SCP (`/var/tmp` QKView generation and verified SCP retrieval).
```bash
myf5 bigiq qkview 192.0.2.50 --user admin --known-hosts ~/.ssh/known_hosts
```

#### `push <host> <file> [--method auto|icontrol|scp] [--dest <path>] [--user <user>] [--port <port>] [-i <key>] [--known-hosts <path>] [--json]`
Uploads software images, hotfixes, or files to BIG-IQ with automatic failover. Streams `512 KiB` `Content-Range` chunks to `/mgmt/cm/autodeploy/software-image-uploads/<filename>` (for `.iso` images) or `/mgmt/shared/file-transfer/uploads/<filename>`, and automatically fails over (or routes directly when targeting non-ISO paths like `/var/tmp`) to verified `64 KiB` SCP when `webd` blocks `/mgmt/tm/util/bash` (`403 Forbidden`).
```bash
# Push to BIG-IQ central image repository via auto failover
myf5 bigiq push 192.0.2.50 /var/tmp/BIGIP-17.1.1.iso --dest /shared/images --json

# Push via native SCP with identity key and known_hosts verification
myf5 bigiq push 192.0.2.50 /var/tmp/BIGIP-17.1.1.iso --method scp -i ~/.ssh/id_ed25519 --known-hosts ~/.ssh/known_hosts
```

#### `download <host> <remote-file> [-o <dest>]` / `pull <host> <remote-file> [-o <dest>]`
Validates that `<remote-file>` is an `.iso` or `.qkview` inside `/shared/images`, `/var/config/rest/downloads`, `/var/tmp`, or `/shared/tmp` (`ValidateApplianceDownloadPath`), downloads via `1 MiB` HTTP `Range` chunks or single-response `200 OK` on BIG-IQ `/mgmt/shared/file-transfer/downloads/`, and falls back to verified `64 KiB` SCP.
```bash
myf5 bigiq download 192.0.2.50 /shared/images/BIGIP-17.1.1.iso -o ./BIGIP-17.1.1.iso
myf5 bigiq pull 192.0.2.50 /var/tmp/qkviews/bigiq.qkview -o ./bigiq.qkview --known-hosts ~/.ssh/known_hosts
```

### Options
- `-u, --user <username>`: Administrative user (default: `$BIGIQ_USER`, `$BIGIP_USER`, or `"admin"`).
- `-p, --port <port>`: Management HTTPS / SSH port.
- `-m, --method <auto|icontrol|scp>`: Transfer protocol with automatic failover (default: `auto`).
- `-d, --dest <path>`: Remote directory on appliance.
- `-o, --output <path>`: Local destination file path.
- `-i, --identity <path>`: SSH private key for SCP transfer (encrypted keys supported via `SSH_AUTH_SOCK`).
- `--known-hosts <path>`: OpenSSH `known_hosts` file for host-key verification (`BIGIQ_KNOWN_HOSTS`, `BIGIP_KNOWN_HOSTS`, `MYF5_SSH_KNOWN_HOSTS`, `MYF5_STRICT_HOST_KEY_CHECKING=1`).
- `--cert <path>` / `--cert-key <path>` (`--key-file <path>`): Client TLS certificate and private key for mTLS (`BIGIQ_CERT` / `BIGIQ_KEY`).
- `--json`: Output machine-readable JSON (for `info`, `serial`, and `push`).
- **Authentication**: Set via `BIGIQ_PASSWORD` (or `BIGIP_PASSWORD`), SSH key / agent, or enter interactively at the masked terminal prompt.

---

## 6. `myf5 push`

Unified command to push software downloads, hotfixes, or packages to any BIG-IP or BIG-IQ instance with automatic failover.
- `--target auto|bigip|bigiq` accepts `"BIG-IQ"`, `"big-iq"`, `"bigiq"`, `"BIG-IP"`, `"big-ip"`, `"bigip"`, or `"auto"` (default: `auto`).
- Streams `512 KiB` (`DefaultUploadChunkSize`) `Content-Range` chunks to `/mgmt/shared/file-transfer/uploads/<filename>` (or `/mgmt/cm/autodeploy/software-image-uploads/<filename>` for `.iso` images on BIG-IQ), promotes via `/mgmt/tm/util/bash`, and automatically fails over (or routes directly when `--target bigiq` targets non-ISO paths like `/var/tmp`) to verified `64 KiB` SCP when `webd` blocks `/mgmt/tm/util/bash` (`403 Forbidden`).

### Syntax
```bash
myf5 push <host> <file> [--method auto|icontrol|scp] [--dest <path>] [--target auto|bigip|bigiq] [--apply-geoip] [--keep-extracted] [--user <user>] [--port <port>] [-i <key>] [--known-hosts <path>] [--cert <cert>] [--cert-key <key>] [--json]
```

### Examples
```bash
# Push software image to BIG-IP with automatic failover
myf5 push 10.1.1.4 /var/tmp/BIGIP-17.1.1.iso --target bigip --dest /shared/images --json

# Push software image to BIG-IQ repository via SCP with known_hosts verification
myf5 push 192.0.2.50 /var/tmp/BIGIP-17.1.1.iso --target bigiq --method scp --dest /shared/images --known-hosts ~/.ssh/known_hosts

# Push using specific SSH key to /var/tmp
myf5 push 10.1.1.4 /var/tmp/ip-geolocation-default.zip --dest /var/tmp -i ~/.ssh/f5_deploy
```

---

## 7. `myf5 pull`

Unified command to download `.iso` software images or `.qkview` diagnostic archives from any BIG-IP or BIG-IQ appliance with automatic failover.
- Validates that `<remote-file>` is an `.iso` or `.qkview` inside `/shared/images`, `/var/config/rest/downloads`, `/var/tmp`, or `/shared/tmp` (`ValidateApplianceDownloadPath`).
- Downloads via `1 MiB` HTTP `Range` chunks (or single-response `200 OK` on BIG-IQ `/mgmt/shared/file-transfer/downloads/`), and automatically falls back to verified `64 KiB` SCP.
- `--target auto|bigip|bigiq` accepts `"BIG-IQ"`, `"big-iq"`, `"bigiq"`, `"BIG-IP"`, `"big-ip"`, `"bigip"`, or `"auto"` (default: `auto`).

### Syntax
```bash
myf5 pull <host> <remote-file> [-o <dest>] [--method auto|icontrol|scp] [--target auto|bigip|bigiq] [--user <user>] [--port <port>] [-i <key>] [--known-hosts <path>] [--cert <cert>] [--cert-key <key>]
```

### Examples
```bash
# Pull software image from BIG-IP (attempts 1 MiB REST Range chunks, falls back to SCP)
myf5 pull 10.1.1.4 /shared/images/BIGIP-17.1.1.iso -o ./BIGIP-17.1.1.iso

# Pull diagnostic QKView from BIG-IP via SCP with known_hosts verification
myf5 pull 10.1.1.4 /var/tmp/qkviews/support.qkview -o ./support.qkview --method scp -u root -i ~/.ssh/id_ed25519 --known-hosts ~/.ssh/known_hosts

# Pull image from BIG-IQ central repository
myf5 pull 192.0.2.50 /shared/images/BIGIP-17.1.1.iso --target bigiq
```

---

## 8. `myf5 doctor`

Pre-flight diagnostic audit that verifies local credentials, DNS resolution, and TCP port 443 egress to all 8 authoritative F5 cloud endpoints (`idp.identity.f5.com`, `identity.account.f5.com`, `api.software.downloads.f5.com`, `support.apis.f5.com`, `ihealth-api.f5.com`, `ihealth2-api.f5.com`, `callhome.f5.com`, and `api.f5.com`).

```bash
myf5 doctor [--proxy <url>]
```

Output audits:
1. `~/.f5api_credentials` or environment credentials (redacted output).
2. Active outbound proxy and `NO_PROXY` configuration.
3. Outbound TCP 443 connectivity to Auth0, Okta, Downloads API, Support Case API, iHealth (`ihealth-api` & `ihealth2-api`), Callhome, and Cloud Gateway.

---

## 9. `myf5 version`

Prints the compiled application version and target architecture.
```bash
myf5 version
```

---

## 10. `myf5 wizard`

Top-level alias for `myf5 cases wizard`. Launches an interactive, multi-step terminal wizard to guide you through creating an F5 Support Case with zero memorization of product schema strings:

```bash
myf5 wizard
```

### Features:
- **Automatic Environment Detection**: Reads local machine timezone and maps it to valid F5 Salesforce timezone strings automatically.
- **Live BIG-IP & BIG-IQ Device Autofill**: Step 1 optionally connects to a live **BIG-IP** or **BIG-IQ** appliance or parses a local `.qkview` file (`pkg/bigip.InspectQKView`) to extract the product family (`"BIG-IP"` or `"BIG-IQ"`), product (`"BIG-IP LTM"` or `"BIG-IQ Centralized Management"`), software version, chassis serial number, and hostname automatically. Non-interactive `myf5 cases create` also accepts `--from-appliance`, `--from-device`, `--from-bigip`, and `--from-bigiq`.
- **Multiline Text Buffer with Undo & External Editor**: The description step supports entering `:m` for an interactive multiline text buffer with line-by-line undo (`:u` / `:undo`), buffer preview (`:p` / `:show`), external `$EDITOR` launch (`:e` / `:edit`), and clear (`:c` / `:clear`).
- **Bidirectional Step & Field Navigation**: Type `:b`, `:back`, `back`, or `b` at any prompt to re-visit previous fields or steps and adjust earlier selections.
- **Safety Dry-Run Gate**: Displays the full JSON payload before making any network write. Requests explicit final confirmation before sending to the live API.

---

## 11. `myf5 geoip`

Top-level alias for `myf5 bigip geoip`. **BIG-IP only** (rejected on BIG-IQ because BIG-IQ Centralized Management does not host TMM GeoIP tables). Uploads, unpacks, installs, and reloads F5 GeoIP database packages directly on a BIG-IP appliance in a single automated command:

```bash
myf5 geoip <host> <zip-path> [--keep-extracted] [--method auto|icontrol|scp] [--known-hosts <path>] [--json]
```

### Actions (per AskF5 K11176):
1. Rejects BIG-IQ targets immediately before transfer.
2. Stages the `.zip` archive to `/shared/tmp` (or `/var/tmp`) on the BIG-IP appliance via iControl REST or verified SCP.
3. Unpacks the internal `.rpm` packages into `/shared/GeoIP` via `rpm2cpio | cpio -idmv` (and `geoip_update_data -f`).
4. Executes `tmsh load sys geoip` to reload the geolocation database into TMM memory without traffic disruption.
5. Cleans up temporary staging files on the appliance (unless `--keep-extracted` is specified).

---

## 12. `myf5 proxy`

Manage outbound HTTP, HTTPS, and SOCKS5 proxy settings for routing API traffic through corporate forward proxies, bastion hosts, or air-gapped jump boxes.

```bash
myf5 proxy show            # Display active egress proxy configuration
myf5 proxy set <url>       # Proxy for the current process only (e.g. interactive mode); use MYF5_PROXY or --proxy for other commands
myf5 proxy clear           # Clear the proxy for the current process only
myf5 --proxy <url> <cmd>   # Specify proxy for a single command invocation
```

### Supported Schemes:
- `http://[user:pass@]host:port`: Standard HTTP CONNECT proxy.
- `https://[user:pass@]host:port`: TLS CONNECT proxy.
- `socks5://[user:pass@]host:port`: RFC 1928 SOCKS5 proxy (local DNS resolution).
- `socks5h://[user:pass@]host:port`: SOCKS5 proxy with remote DNS resolution on the proxy server.

### Environment Variable Fallback:
If no `--proxy` flag is passed, `myf5` respects `MYF5_PROXY`, `F5_PROXY`, `ALL_PROXY`, `HTTPS_PROXY`, and `HTTP_PROXY`. Proxy bypass rules can be specified via `NO_PROXY`.

---

## 13. `myf5 interactive` (alias: `shell`, `repl`, `i`)

Launches an interactive Read-Eval-Print-Loop (REPL) shell environment with built-in `TAB` autocompletion, command history, and session state (also launched automatically when invoking `myf5` with no arguments in an interactive terminal TTY; respects `NO_COLOR`):

```bash
myf5 interactive
# Or shortcuts:
myf5 shell
myf5 repl
myf5 i
```

### Interactive Features:
- **Built-In `TAB` Autocompletion**: Press `TAB` at the `myf5>` prompt to autocomplete primary commands (`downloads`, `cases`, `wizard`, `ihealth`, `bigip`, `bigiq`, `push`, `pull`, `geoip`, `proxy`, `doctor`, `interactive`, `version`, `help`, `clear`, `exit`, `quit`), subcommands, and command-specific flags.
- **In-Process Command Isolation**: Failed commands or `Ctrl+C` interrupts cancel only the active command and return cleanly to the `myf5>` prompt without terminating your interactive session.
- **Built-In Shell Commands**: Type `clear` / `cls` to clear the terminal screen, or `exit` / `quit` / `q` (or `Ctrl+D`) to exit.

Within the interactive shell, execute commands directly without the `myf5` prefix:
```
myf5> doctor
myf5> downloads families
myf5> cases list
myf5> bigip info 10.1.1.4
myf5> exit
```

---

## 14. Signal Handling & Interruption Safety (CTRL+C)

All long-running network connections, streaming file transfers, and appliance operations in `myf5-cli` implement robust interrupt and cancellation handling:
- **Clean Signal Traps**: Intercepts `SIGINT` (Ctrl+C) and `SIGTERM` via isolated command contexts (`signal.NotifyContext`).
- **Safe Connection Teardown**: Immediately aborts in-flight HTTP requests, closes SOCKS5 proxy tunnels, and gracefully closes SSH/SCP channels without socket leaks or hanging child processes.
- **Atomic Partial File Cleanup**: Streaming file downloads automatically scrub and remove temporary `.part` files on disk upon interrupt, preventing corrupted or incomplete downloads from being mistaken for valid files.
- **Exit Code 130**: Returns standard POSIX exit code `130` (`128 + SIGINT`) on cancellation.

---

## 15. Process Exit Codes

`myf5` returns standard POSIX exit codes to support reliable shell script branching and CI/CD pipelines:

| Exit Code | Classification | Description |
| :--- | :--- | :--- |
| `0` | **Success** | Operation completed successfully and verified (including `ready` when appliance is `READY`). |
| `1` | **General / Tier-2 Error** | Command syntax error, invalid arguments, `ready` when appliance is `NOT READY`, network failure, missing credentials in non-interactive environment, or Tier 2 appliance secret flag rejection (`--pass`, `-P`, `--ssh-passphrase`). |
| `2` | **Tier-1 Zero-Secret Violation** | Security error triggered before command dispatch when any of the 22 global secret flags (`--password`, `--client-secret`, `--token`, `--key`, etc.) are supplied in `os.Args[1:]`. |
| `130` | **Cancelled / Interrupted** | Execution aborted by user via `SIGINT` (Ctrl+C) or `SIGTERM`. |


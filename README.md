# myf5-cli

A high-performance, memory-safe, zero-dependency Go CLI and client library for **MyF5 Cloud APIs**, **Software Downloads**, **iHealth Diagnostics**, **BIG-IP TMOS Appliances**, and **BIG-IQ Centralized Management**.

> [!IMPORTANT]
> **DISCLAIMER:** This project is a community-maintained tool authored and maintained by **Greg Robinson** ([`g.robinson@f5.com`](mailto:g.robinson@f5.com)) and is **NOT** covered by an official F5 Technical Support SLA.
>
> **API ENTITLEMENT PREREQUISITE:** While this CLI source code is open source, **the MyF5 API itself is not publicly open**. Using MyF5 Cloud API commands (`downloads`, `cases`) requires F5 to enable API support on your customer account and issue account-specific OAuth2 credentials (`F5_CLIENT_ID` and `F5_CLIENT_SECRET`). Direct appliance commands (`bigip`, `bigiq`, `push`, `pull`, `geoip`) and `ihealth` operate independently with your appliance or iHealth credentials.

---

## Key Highlights

- **Memory-Safe & Statically Compiled**: Compiles to a single static binary with zero external runtime dependencies (`CGO_ENABLED=0`).
- **Wire-Speed Multi-GB Streaming**: Streams 2.5 GB – 10+ GB BIG-IP ISOs, OVAs, and qcow2 images directly to disk using `io.TeeReader` with simultaneous SHA-256 verification and minimal RAM footprint (< 10 MB).
- **Direct Appliance Push & Pull Engine**: Transfers software ISOs, engineering hotfixes, diagnostic QKViews, and configuration files directly between local systems and BIG-IP / BIG-IQ appliances with **automatic seamless failover** between **iControl REST chunked transfer** (HTTPS) and **native Go SCP streaming** (SSH).
- **End-to-End Download & Deploy**: Downloads an image from MyF5, cryptographically verifies its SHA-256 checksum, and immediately pushes it to target appliances in a single atomic pipeline (`myf5 downloads get ... --push <host>`).
- **Automated GeoIP Deployment Engine**: Downloads, stages, unpacks, installs, and reloads MaxMind/Digital Envoy GeoIP databases into BIG-IP TMOS in a single atomic pipeline (`myf5 geoip` or `myf5 downloads get ... --apply-geoip`).
- **Outbound Corporate & Lab Proxy Support**: Universal support for HTTP, HTTPS, SOCKS5, and SOCKS5h proxies via `--proxy` or standard environment variables (`ALL_PROXY`, `HTTPS_PROXY`, `HTTP_PROXY`, `NO_PROXY`). Includes diagnostic probe command `myf5 proxy`.
- **Interactive Case Creation Wizard**: Step-by-step TUI wizard (`myf5 wizard`) with automatic serial and version discovery from live appliances or `.qkview` files, interactive multiline text buffer with undo (`Ctrl+Z`), and dry-run safety gates.
- **Signal Handling & Interruption Safety**: Comprehensive trap for `SIGINT` (Ctrl+C) and `SIGTERM` across all commands; ensures partial `.part` downloads are pruned, active SSH channels and TCP sockets are closed gracefully, and no orphaned processes linger.
- **MyF5 Downloads API Client**: Targets `api.software.downloads.f5.com` over the `/v1/downloads-management/v1/...` gateway route with automatic fallback and HTTP 429 rate-limit backoff.
- **Strict Zero-Secret CLI Security**: Rejects plaintext secret arguments (`--password`, `--client-secret`) with exit code `2` to protect against process table (`ps aux`) and shell history leaks.
- **Multiple Credential Formats**: Automatically discovers credentials in `F5_CLIENT_ID` / `F5_CLIENT_SECRET` environment variables or `~/.f5api_credentials` (both `KEY=VALUE` and `[profile]` INI formats).
- **Interactive Console & REPL**: Menu-driven terminal TUI for catalog exploration (`myf5 downloads browse`) and interactive CLI shell (`myf5 interactive`).
- **Universal Appliance Connectivity**: Seamlessly supports standard LAN management IPs (`10.1.1.4`), custom management ports (`:8443`), and cloud/UDF access proxies (`https://*.access.udf.f5.com`).

---

## Architecture & Directory Layout

```
myf5-cli/
├── cmd/
│   └── myf5/
│       ├── main.go             # Unified CLI entrypoint, flag parsing, signal handling
│       ├── wizard.go           # Interactive case creation wizard & multiline text editor
│       ├── interactive.go      # Interactive console REPL and catalog browser
│       └── selector.go         # Terminal menu selector and download confirmation TUI
├── pkg/
│   ├── auth/                   # OAuth2 authentication, Zero-Secret validator, credentials resolver
│   ├── client/                 # Secure TLS 1.2+ HTTP transport, SOCKS5/HTTP proxy dialer, timeouts
│   ├── downloads/              # Downloads API catalog client and streaming downloader
│   ├── cases/                  # MyF5 Support Case API client & metadata schema
│   ├── ihealth/                # F5 iHealth QKView multipart upload and status client
│   ├── bigip/                  # BIG-IP & BIG-IQ client (iControl REST chunking, bash promotion, native SCP, failover)
│   │   ├── client.go           # Appliance HTTP client, readiness, QKView automation, REST download
│   │   ├── info.go             # TMOS system, platform, and chassis serial inspection
│   │   ├── upload.go           # iControl REST chunked file transfer & bash promotion
│   │   ├── scp.go              # Native Go SCP streaming (upload & download) over SSH
│   │   ├── geoip.go            # TMOS GeoIP extraction, RPM installation, and reload engine
│   │   ├── lifecycle.go        # TMOS software (K5903) and hardware (K4309 / K13144) lifecycle evaluation
│   │   └── failover.go         # Dual-protocol automatic failover engine
│   ├── logger/                 # Structured audit & debug logging with automatic secret scrubbing
│   └── progress/               # Terminal progress bar and spinner rendering
├── scripts/
│   └── gen-sbom.sh             # CycloneDX 1.5 SBOM generator with SPDX license metadata
├── .github/
│   ├── workflows/              # GitHub Actions CI, Release, and Security workflows
│   ├── ISSUE_TEMPLATE/         # Structured Bug Report & Feature Request templates
│   ├── CODEOWNERS              # Repository ownership mappings
│   └── pull_request_template.md
├── docs/
│   ├── cli.md                  # Comprehensive CLI reference manual
│   ├── appliance_push.md       # Appliance software deployment, hotfix, and GeoDB push guide
│   ├── downloads.md            # Downloads catalog taxonomy, GeoDB automation & direct push
│   ├── credentials.md          # Zero-Secret policy & credential management
│   └── network_and_firewall.md # Egress firewall rules, proxy routing, and cloud endpoint architecture
├── Makefile                    # Developer build, test, and release automation
├── SECURITY.md                 # Security & vulnerability reporting policy
├── CONTRIBUTING.md             # Developer workflow & coding standards
├── CONTRIBUTING_AI.md          # Rules for AI assistants — this tool writes to live F5 Support
├── LICENSE                     # Apache License 2.0
├── go.mod
└── README.md
```

---

## Installation & Building

### Pre-Compiled Binaries
Download pre-built standalone binaries for Linux, macOS, and Windows from the [GitHub Releases page](https://github.com/SalesAmerSP/myf5-cli/releases).

### Developer Build with GNU Make
```bash
# Clone the repository
git clone https://github.com/SalesAmerSP/myf5-cli.git
cd myf5-cli

# Build for host OS & architecture
make build

# Run full quality test suite (race detection, formatting, vet)
make test

# Build cross-platform distribution binaries
make cross-compile
```

---

## Credential Configuration

In accordance with the **Zero-Secret CLI Policy**, secret flags are prohibited on the command line. Provide credentials using either method:

> [!IMPORTANT]
> **Don't have a `client_id` and `client_secret` yet?** They are issued from MyF5, but only after your account is granted API Catalog access. In short: log in at [my.f5.com](https://my.f5.com), request API Catalog access from F5 Support, then create an API key once the grant lands. Full steps are in [Obtaining API Credentials](./docs/credentials.md#obtaining-api-credentials).

### 1. `~/.f5api_credentials` (Recommended for Local Dev)
Create `~/.f5api_credentials` with mode `0600`:

```ini
username=your.email@f5.com
client_id=your_oauth2_client_id
client_secret=your_oauth2_client_secret
```

Or with profiles:
```ini
[default]
client_id = your_oauth2_client_id
client_secret = your_oauth2_client_secret

[production]
client_id = prod_oauth2_client_id
client_secret = prod_oauth2_client_secret
```

### 2. Environment Variables (Recommended for CI/CD)
```bash
# Cloud API credentials
export F5_CLIENT_ID="your_oauth2_client_id"
export F5_CLIENT_SECRET="your_oauth2_client_secret"

# Appliance credentials
export BIGIP_USER="admin"
export BIGIP_PASSWORD="YourAppliancePassword"
export BIGIQ_USER="admin"
export BIGIQ_PASSWORD="YourAppliancePassword"
```

---

## CLI Usage Guide

### 1. Diagnostic Doctor
Verify API credentials, DNS resolution, and TCP port 443 egress to all F5 Cloud endpoints:
```bash
./bin/myf5 doctor
```

### 2. MyF5 Software Downloads & Catalog
```bash
# Interactive menu browser
./bin/myf5 downloads browse

# List available product families
./bin/myf5 downloads families

# List product lines (e.g. big-ip_v17.x)
./bin/myf5 downloads lines BIG-IP

# List release versions and containers
./bin/myf5 downloads versions BIG-IP big-ip_v17.x

# List downloadable files for a version
./bin/myf5 downloads files BIG-IP big-ip_v17.x 17.1.1

# Download software image with wire-speed streaming and live progress bar
./bin/myf5 downloads get BIG-IP big-ip_v17.x 17.1.1 "Virtual Edition" BIGIP-17.1.1-0.0.6.ALL.qcow2.tar.gz -o /var/tmp/bigip.qcow2.tar.gz

# Download and push directly to BIG-IP appliance in one step
./bin/myf5 downloads get BIG-IP big-ip_v17.x 17.1.1 "Base Software & ISO Releases" BIGIP-17.1.1-0.0.6.iso \
    -o /var/tmp/BIGIP-17.1.1.iso \
    --push 10.1.1.4 \
    --push-method icontrol \
    --push-dest /shared/images
```

### 3. MyF5 Support Cases
```bash
# List open cases (add --all to include closed ones)
./bin/myf5 cases list
./bin/myf5 cases list --all

# Retrieve case creation schema metadata (products, versions, priorities, timezones)
./bin/myf5 cases metadata

# Show one case in full, including contacts and the email thread
./bin/myf5 cases get 01234567

# Add a note or comment to a case (supports interactive multiline editor with Ctrl+Z undo)
# Case numbers are 8 digits, no prefix - exactly as MyF5 displays them.
./bin/myf5 cases comment 01234567 "Investigating packet captures."
# Or launch multiline buffer:
./bin/myf5 cases comment 01234567

# Open a case. The API requires an entitlement identifier validated against F5
# support records: BIG-IP and hardware families use --serial; NGINX and Aspen Mesh
# use --subscription. (To open a case without an entitlement identifier, use the
# MyF5 web portal; see docs/cli.md for details.)
# Use --dry-run first to check the payload without calling the API.
./bin/myf5 cases create --family BIG-IP --product "BIG-IP LTM" \
  --version 17.5.1 --serial ABC1234 \
  --subject "SSL handshake failures" --description "Details here." \
  --priority "4 - General Assistance" --reason "General Problem" \
  --email you@example.com \
  --timezone "(GMT -5:00) US/Eastern - Eastern Standard Time"

# Alternatively, launch the interactive step-by-step case creation wizard.
# Supports auto-detecting serials/versions from live BIG-IPs or .qkview files,
# auto-detects your system timezone, multiline buffer with Ctrl+Z undo, and dry-run safety gates:
./bin/myf5 cases wizard
# Or top-level shortcut:
./bin/myf5 wizard
```

### 4. F5 iHealth
```bash
# List previously uploaded QKViews
./bin/myf5 ihealth list

# Upload a local QKView bundle to iHealth, optionally attaching it to a case
./bin/myf5 ihealth upload /path/to/appliance.qkview "01234567" "Initial triage bundle"
```

### 5. BIG-IP Appliance Management & Image Transfer (Auto Failover)
Works with local management IPs, lab appliances, or cloud/UDF external access proxies with automatic failover between iControl REST and SCP.

```bash
# Check TMOS readiness (via local IP)
./bin/myf5 bigip ready 10.1.1.4

# Check TMOS readiness (via UDF or Cloud Access Proxy)
./bin/myf5 bigip ready https://00000000-0000-0000-0000-000000000000.access.udf.f5.com/

# Inspect appliance version, edition, and build details
./bin/myf5 bigip info 10.1.1.4 --user admin

# Retrieve chassis/system serial number (useful for case creation)
./bin/myf5 bigip serial 10.1.1.4 --user admin

# Trigger full, non-truncating QKView generation (-s0) with resilient download
./bin/myf5 bigip qkview 10.1.1.4 --user admin

# Push software ISO to BIG-IP /shared/images with automatic failover (default)
./bin/myf5 bigip push 10.1.1.4 /var/tmp/BIGIP-17.1.1.iso --dest /shared/images

# Push engineering hotfix via native Go SCP over SSH
./bin/myf5 bigip push 10.1.1.4 /var/tmp/Hotfix-BIGIP-17.1.1.1.0.0.2-ENG.iso --method scp -i ~/.ssh/f5_key

# Download software image from BIG-IP appliance
./bin/myf5 bigip download 10.1.1.4 /shared/images/BIGIP-17.1.1.iso -o ./BIGIP-17.1.1.iso

# Push and activate GeoIP database archive in one step
./bin/myf5 bigip push 10.1.1.4 /var/tmp/ip-geolocation-default.zip --apply-geoip
# Or invoke direct GeoIP installer
./bin/myf5 bigip geoip 10.1.1.4 /var/tmp/ip-geolocation-default.zip
```

### 6. BIG-IQ Centralized Management & Image Transfer
```bash
# Check BIG-IQ management plane readiness
./bin/myf5 bigiq ready 192.0.2.50

# Inspect BIG-IQ system version, edition, and build details
./bin/myf5 bigiq info 192.0.2.50 --user admin

# Retrieve BIG-IQ chassis/system serial number
./bin/myf5 bigiq serial 192.0.2.50 --user admin

# Push software ISO to BIG-IQ central repository (auto failover)
./bin/myf5 bigiq push 192.0.2.50 /var/tmp/BIGIP-17.1.1.iso --dest /shared/images

# Download file from BIG-IQ repository
./bin/myf5 bigiq download 192.0.2.50 /shared/images/BIGIP-17.1.1.iso -o ./BIGIP-17.1.1.iso
```

### 7. Unified Appliance Push & Pull (Auto Failover)
```bash
# Push file to any appliance with target selection and auto failover
./bin/myf5 push 10.1.1.4 /var/tmp/ip-geolocation-default.zip --target bigip --dest /var/tmp

# Pull file from any appliance with auto failover
./bin/myf5 pull 10.1.1.4 /var/log/ltm -o ./ltm.log --method scp -u root -i ~/.ssh/id_ed25519
```

### 8. Automated GeoIP Deployment Engine
Automate the end-to-end lifecycle of MaxMind / Digital Envoy GeoIP updates (askF5 K11176 / K78974041):
```bash
# Download latest GeoIP update package and apply to appliance in a single pipeline:
./bin/myf5 downloads get "BIG-IP" "big-ip_v17.x" "17.1.1" "GeoLocationUpdates" \
    "ip-geolocation-default.zip" -o /var/tmp/geoip.zip \
    --push 10.1.1.4 --apply-geoip

# Or apply an already downloaded archive directly to a BIG-IP:
./bin/myf5 geoip 10.1.1.4 /var/tmp/ip-geolocation-default.zip
```

### 9. Outbound Corporate & Lab Proxy Support
`myf5` supports outbound HTTP, HTTPS, SOCKS5, and SOCKS5h proxies for corporate networks and isolated lab environments:
```bash
# Probe and test proxy connectivity:
./bin/myf5 proxy
./bin/myf5 proxy --proxy socks5://proxy.corp.internal:1080

# Run commands through proxy via global flag:
./bin/myf5 --proxy socks5://10.0.0.1:1080 doctor
./bin/myf5 --proxy http://10.0.0.1:8080 downloads families

# Or configure via standard environment variables:
export ALL_PROXY="socks5://proxy.corp.internal:1080"
export HTTPS_PROXY="http://proxy.corp.internal:8080"
export NO_PROXY="localhost,127.0.0.1,10.0.0.0/8,.internal"
```

### 10. Interactive Case Creation Wizard
Launch a guided terminal interface that validates input at every step:
```bash
./bin/myf5 wizard
# Or:
./bin/myf5 cases wizard
```
- Auto-detects serial number and TMOS version from a live appliance IP or local `.qkview` archive.
- System timezone detection automatically maps to F5's expected string representation.
- Multiline description text buffer supports live editing with undo (`Ctrl+Z`) and redo (`Ctrl+Y`).
- Pre-submission dry-run safety review ensures you never open an accidental case against production.

### 11. Interactive Console REPL & Tab Completion
Launch an interactive Read-Eval-Print-Loop (REPL) shell with built-in `TAB` autocompletion (for commands, subcommands, and flags), command history, and isolated per-command `Ctrl+C` handling:
```bash
./bin/myf5 interactive
# Or aliases (or invoke `./bin/myf5` with no arguments in a terminal TTY):
./bin/myf5 shell
./bin/myf5 repl
```
Within the interactive shell, run commands directly without the `myf5` prefix and press `TAB` at any prompt to autocomplete commands, subcommands, or flags:
```text
myf5> doctor
myf5> downloads families
myf5> bigip info 10.1.1.4
myf5> exit
```

---

## 🛡️ Memory Safety & Zero-Secret Security Architecture

### 1. Memory Safety & Constant-RAM Streaming
- **Zero CGO (`CGO_ENABLED=0`):** Compiles without `cgo` or dynamic C runtime links, eliminating use-after-free, double-free, and buffer overflow vulnerabilities common in C extensions.
- **Wire-Speed Multi-GB Streaming Pipeline:**
  ```
  HTTP TLS Stream ---> io.TeeReader ---> SHA-256 Hasher
                              |
                              v
                      os.File (Disk Write)
  ```
  Instead of buffering 2.5 GB – 10+ GB BIG-IP ISOs/OVAs into RAM, the streaming engine pipelines chunked HTTP packets directly into disk writes and SHA-256 hashing in a single pass. Memory footprint remains constant at **< 10 MB RAM** regardless of download file size.
- **Atomic Verification & Promotion:** Downloads are written to a `.part` temporary file and verified against authoritative checksums before atomic promotion to the target filename.

### 2. Zero-Secret Defense-in-Depth Architecture
- **Process Table Isolation:** Any flag attempting to inject raw credentials (`--password`, `--client-secret`) is trapped by `auth.CheckNoCLISecrets` before flag parsing, exiting with code `2`. This prevents credentials from appearing in `/proc/$PID/cmdline` or `ps aux` listings visible to other unprivileged users on multi-user jump boxes.
- **Shell History Protection:** Because secrets cannot be provided via arguments, commands stored in `~/.bash_history`, `~/.zsh_history`, and auditing agents (e.g., Falco, auditd) remain free of secrets.
- **Hierarchical Credential Resolution:**
  ```
  1. OS Environment Variables (F5_CLIENT_ID / F5_CLIENT_SECRET, BIGIP_PASSWORD / BIGIQ_PASSWORD)
       │ (Not Found)
       ▼
  2. File: ~/.f5api_credentials (Permissions 0600, INI profile support)
       │ (Not Found)
       ▼
  3. SSH Keys / Agent (SCP method: ~/.ssh/id_*, $SSH_AUTH_SOCK)
       │ (Not Found)
       ▼
  4. Interactive Masked Terminal Prompt (Echo suppressed via term.ReadPassword)
  ```
- **Masked Diagnostics:** Diagnostic utilities (`myf5 doctor`) mask all secret strings, displaying only non-sensitive metadata (e.g. `Client ID: 9x8*************************`).

### 3. Interruption Safety & Resource Cleanup (CTRL+C / SIGINT / SIGTERM)
- **Top-Level Signal Trapping:** Global context (`signal.NotifyContext`) traps interruption signals across all CLI operations, ensuring clean cancellation rather than abrupt process termination.
- **Atomic File Cleanup:** Streaming file downloads write to temporary `.part` files. If interrupted via `Ctrl+C`, the `.part` files are immediately removed from disk to prevent corrupt or incomplete files from accumulating.
- **Graceful Network & Session Teardown:** In-flight HTTP request bodies, TLS connection pools, native SSH client sessions, and SCP channels are closed cleanly upon signal receipt, preventing connection hangs and orphaned appliance processes.

---

## 📚 Documentation & Technical Guides

| Guide | Description |
| :--- | :--- |
| **[CLI Reference Manual](docs/cli.md)** | Full reference for all commands, subcommands, arguments, and exit codes. |
| **[Appliance Push & Deployment Guide](docs/appliance_push.md)** | Direct streaming of ISOs, hotfixes, and GeoDB updates to BIG-IP and BIG-IQ via iControl REST and SCP. |
| **[MyF5 Downloads & GeoDB Guide](docs/downloads.md)** | Catalog hierarchy, automated GeoDB updates, and end-to-end download & push workflows. |
| **[Zero-Secret Security Architecture](docs/credentials.md)** | Process isolation, credentials file permissions, SSH keys, and environment setup. |
| **[Network, DNS & Firewall Architecture](docs/network_and_firewall.md)** | Egress firewall requirements (TCP 443), proxy routing, and F5 cloud service endpoints. |

### Reference F5 Knowledge Base Articles

| Article | Covers |
| :--- | :--- |
| [K2633](https://my.f5.com/manage/s/article/K2633) | Submitting a support case — priority definitions and response targets |
| [K12878](https://my.f5.com/manage/s/article/K12878) | Generating diagnostic data with the `qkview` utility |
| [K000090853](https://my.f5.com/manage/s/article/K000090853) | Exchanging files with F5 Support |
| [K27404821](https://my.f5.com/manage/s/article/K27404821) | Using F5 iHealth to diagnose vulnerabilities |
| [K15202](https://my.f5.com/manage/s/article/K15202) | IP addresses and FQDNs for F5 hosted cloud services |
| [K000163521](https://my.f5.com/manage/s/article/K000163521) | Network allowlisting for `api.f5.com` and `callhome.f5.com` |
| [K000130498](https://my.f5.com/manage/s/article/K000130498) | MyF5 API authentication — OAuth2 client-credentials flow |

---

## Running Unit Tests

Run the full Go test suite:
```bash
make test
```
Or with code coverage:
```bash
make test-coverage
```

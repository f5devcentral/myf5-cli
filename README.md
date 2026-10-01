# myf5-api

A high-performance, memory-safe, zero-dependency Go CLI and client library for **MyF5 Cloud APIs**, **Software Downloads**, **iHealth Diagnostics**, and **BIG-IP TMOS Appliances**.

> [!IMPORTANT]
> **DISCLAIMER:** This project is **NOT** an officially supported F5 product or service.
> It is an internal and field engineering automation tool maintained by F5 Service Provider Sales Americas.
> For bugs, issues, or feature requests, please email **Greg Robinson** ([`g.robinson@f5.com`](mailto:g.robinson@f5.com)) or [open a GitHub issue](https://github.com/SalesAmerSP/myf5-api/issues).

---

## Key Highlights

- **Memory-Safe & Statically Compiled**: Compiles to a single static binary with zero external runtime dependencies (`CGO_ENABLED=0`).
- **Wire-Speed Multi-GB Streaming**: Streams 2.5 GB – 10+ GB BIG-IP ISOs, OVAs, and qcow2 images directly to disk using `io.TeeReader` with simultaneous SHA-256 verification and minimal RAM footprint (< 10 MB).
- **MyF5 Downloads Migration Ready**: Defaults to the new October 2, 2026 endpoint (`api.software.downloads.f5.com`) without the legacy `k` query parameter, with automatic pre-cutover fallback and HTTP 429 rate-limit backoff.
- **Strict Zero-Secret CLI Security**: Rejects plaintext secret arguments (`--password`, `--client-secret`) with exit code `2` to protect against process table (`ps aux`) and shell history leaks.
- **Multiple Credential Formats**: Automatically discovers credentials in `F5_CLIENT_ID` / `F5_CLIENT_SECRET` environment variables or `~/.f5api_credentials` (both `KEY=VALUE` and `[profile]` INI formats).
- **Interactive Console Browser**: Menu-driven terminal TUI for exploring product families, branches, release versions, and signed CDN download links.
- **Universal BIG-IP Connectivity**: Seamlessly supports standard LAN management IPs (`10.1.1.4`), custom management ports (`:8443`), and cloud/UDF access proxies (`https://*.access.udf.f5.com`).

---

## Architecture & Directory Layout

```
myf5-api/
├── cmd/
│   └── myf5/
│       └── main.go             # Unified CLI entrypoint & Zero-Secret validator
├── pkg/
│   ├── auth/                   # OAuth2 authentication, Zero-Secret validator, credentials resolver
│   ├── client/                 # Secure TLS 1.2+ HTTP transport with AEAD/PFS ciphers
│   ├── downloads/              # Downloads API catalog client and streaming downloader
│   ├── cases/                  # MyF5 Support Case API client
│   ├── ihealth/                # F5 iHealth QKView multipart upload and status client
│   └── bigip/                  # BIG-IP TMOS iControl REST appliance client (with URL & proxy support)
├── .github/
│   ├── workflows/              # GitHub Actions CI, Release, and Security workflows
│   ├── ISSUE_TEMPLATE/         # Structured Bug Report & Feature Request templates
│   ├── CODEOWNERS              # Repository ownership mappings
│   └── pull_request_template.md
├── docs/
│   └── network_and_firewall.md # Egress firewall rules and DNS cutover documentation
├── Makefile                    # Developer build, test, and release automation
├── SECURITY.md                 # Security & vulnerability reporting policy
├── CONTRIBUTING.md             # Developer workflow & coding standards
├── LICENSE                     # Apache 2.0 License
├── go.mod
└── README.md
```

---

## Installation & Building

### Pre-Compiled Binaries
Download pre-built standalone binaries for Linux, macOS, and Windows from the [GitHub Releases page](https://github.com/SalesAmerSP/myf5-api/releases).

### Developer Build with GNU Make
```bash
# Clone the repository
git clone https://github.com/SalesAmerSP/myf5-api.git
cd myf5-api

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
export F5_CLIENT_ID="your_oauth2_client_id"
export F5_CLIENT_SECRET="your_oauth2_client_secret"
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
```

### 3. MyF5 Support Cases
```bash
# List active cases
./bin/myf5 cases list

# Retrieve case creation schema metadata
./bin/myf5 cases metadata

# Add a note or comment to a case
./bin/myf5 cases comment C1000234 "Investigating packet captures."
```

### 4. F5 iHealth
```bash
# List previously uploaded QKViews
./bin/myf5 ihealth list

# Upload a local QKView bundle to iHealth
./bin/myf5 ihealth upload /path/to/appliance.qkview "C1000234" "Initial triage bundle"
```

### 5. BIG-IP Appliance Management
Works with local management IPs, lab appliances, or cloud/UDF external access proxies.

```bash
# Set password via environment variable (optional)
export BIGIP_PASSWORD="YourAppliancePassword"

# Or omit BIGIP_PASSWORD and you will be prompted securely with terminal echo suppressed.

# Check TMOS readiness (via local IP)
./bin/myf5 bigip ready 10.1.1.4

# Check TMOS readiness (via UDF or Cloud Access Proxy)
./bin/myf5 bigip ready https://aa68f6ba-a751-4740-9382-85f7cd7940f2.access.udf.f5.com/

# Check TMOS readiness with custom user or port
./bin/myf5 bigip ready 10.1.1.4 --port 8443 --user admin

# Trigger full, non-truncating QKView generation (-s0)
./bin/myf5 bigip qkview https://aa68f6ba-a751-4740-9382-85f7cd7940f2.access.udf.f5.com/
```

### 6. Shell Tab Autocompletion (`bash`, `zsh`, `fish`)
Enable fast tab autocompletion for commands, subcommands, flags, and product families:

```bash
# Zsh (current session)
source <(myf5 completion zsh)

# Zsh (persistent setup)
myf5 completion zsh > "${fpath[1]}/_myf5"

# Bash (current session)
source <(myf5 completion bash)

# Bash (persistent setup on Linux)
myf5 completion bash | sudo tee /etc/bash_completion.d/myf5 > /dev/null

# Fish (persistent setup)
myf5 completion fish > ~/.config/fish/completions/myf5.fish
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
  1. OS Environment Variables (F5_CLIENT_ID / F5_CLIENT_SECRET)
       │ (Not Found)
       ▼
  2. File: ~/.f5api_credentials (Permissions 0600, INI profile support)
       │ (Not Found)
       ▼
  3. Interactive Masked Terminal Prompt (Echo suppressed via term.ReadPassword)
  ```
- **Masked Diagnostics:** Diagnostic utilities (`myf5 doctor`) mask all secret strings, displaying only non-sensitive metadata (e.g. `Client ID: 9x8*************************`).

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

# myf5-api

A high-performance, memory-safe, zero-dependency Go CLI and client library for **MyF5 Cloud APIs**, **Software Downloads**, **iHealth Diagnostics**, and **BIG-IP TMOS Appliances**.

---

## Key Highlights

- **Memory-Safe & Statically Compiled**: Compiles to a single static binary with zero external runtime dependencies (`CGO_ENABLED=0`).
- **Wire-Speed Multi-GB Streaming**: Streams 2.5 GB – 10+ GB BIG-IP ISOs, OVAs, and qcow2 images directly to disk using `io.TeeReader` with simultaneous SHA-256 verification and minimal RAM footprint.
- **MyF5 Downloads Migration Ready**: Defaults to the new October 2, 2026 endpoint (`api.software.downloads.f5.com`) without the legacy `k` query parameter, with automatic pre-cutover fallback and HTTP 429 rate-limit backoff.
- **Strict Zero-Secret CLI Security**: Rejects plaintext secret arguments (`--password`, `--client-secret`) with exit code `2` to protect against process table (`ps aux`) and shell history leaks.
- **Multiple Credential Formats**: Automatically discovers credentials in `F5_CLIENT_ID` / `F5_CLIENT_SECRET` environment variables or `~/.f5api_credentials` (both `KEY=VALUE` and `[profile]` INI formats).
- **Interactive Console Browser**: Menu-driven terminal TUI for exploring product families, branches, release versions, and signed CDN download links.

---

## Architecture & Directory Layout

```
myf5-api/
├── cmd/
│   └── myf5/
│       └── main.go           # Unified CLI tool
├── pkg/
│   ├── auth/                 # OAuth2 authentication, Zero-Secret validator, and credentials resolver
│   ├── client/               # Secure TLS 1.2+ HTTP transport with AEAD/PFS ciphers
│   ├── downloads/            # Downloads API catalog client and streaming downloader
│   ├── cases/                # MyF5 Support Case API client
│   ├── ihealth/              # F5 iHealth QKView multipart upload and status client
│   └── bigip/                # BIG-IP TMOS iControl REST appliance client
├── .gitignore
├── go.mod
└── README.md
```

---

## Installation & Building

### Prerequisites
- Go 1.21 or newer (built and tested with Go 1.27)

### Build Single Static Binary
```bash
# Clone the repository
git clone https://github.com/f5/myf5-api.git
cd myf5-api

# Compile the standalone binary
go build -o bin/myf5 ./cmd/myf5
```

### Cross-Compilation (Single Binary for Any OS)
```bash
# Linux x86_64 (Enterprise Jump Boxes / Bastions)
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="-s -w" -o bin/myf5-linux-amd64 ./cmd/myf5

# Linux ARM64
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -ldflags="-s -w" -o bin/myf5-linux-arm64 ./cmd/myf5

# macOS Apple Silicon
GOOS=darwin GOARCH=arm64 go build -o bin/myf5-darwin-arm64 ./cmd/myf5

# Windows 64-bit
GOOS=windows GOARCH=amd64 go build -o bin/myf5.exe ./cmd/myf5
```

---

## Credential Configuration

In accordance with the **Zero-Secret CLI Policy**, secret flags are prohibited on the command line. Provide credentials using either method:

### 1. `~/.f5api_credentials` (Recommended)
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

### 2. Environment Variables
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
```bash
export BIGIP_PASSWORD="YourAppliancePassword"

# Check TMOS readiness
./bin/myf5 bigip ready 192.168.1.245

# Trigger full, non-truncating QKView (-s0)
./bin/myf5 bigip qkview 192.168.1.245
```

---

## Running Unit Tests

Run the full Go test suite:
```bash
go test -v ./...
```

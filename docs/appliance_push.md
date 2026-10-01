# Appliance Transfer Guide: BIG-IP & BIG-IQ Dual Push, Pull, and Automatic Failover Engine

The `myf5` appliance transfer engine enables direct, wire-speed bidirectional streaming of software images, engineering hotfixes, diagnostic QKViews, configuration files, and GeoDB packages between local systems and **F5 BIG-IP TMOS** or **BIG-IQ Centralized Management** appliances.

By embedding both **iControl REST chunked transfer** and **native Go SCP over SSH** with an **automatic seamless failover engine**, `myf5` guarantees file transfers succeed even when one transport protocol is blocked, misconfigured, or restricted by firewall policies.

---

## Architectural Overview & Automatic Failover Engine

`myf5` provides two independent transport mechanisms for both pushing (uploading) and pulling (downloading) files:

1. **Automatic Failover Mode** (Default: `--method auto`)
   - **Upload (`push`)**: Attempts iControl REST first when password authentication is available. If the REST API endpoint fails, times out, or returns an HTTP error, `myf5` displays a `[FAILOVER]` alert and immediately re-executes the upload via native Go SCP over SSH. Conversely, if an SSH key is explicitly provided, SCP is attempted first with automatic REST fallback.
   - **Download (`pull` / `download`)**: Evaluates the requested remote path. For files located in `/shared/images/` or `/var/tmp/qkviews/` (such as ISO software images or QKViews), `myf5` attempts chunked iControl REST download first. If the file is not found via REST (HTTP 404) or REST is unavailable, it immediately falls back to native Go SCP. For arbitrary Linux filesystem paths (e.g., `/config/bigip.conf`), it attempts SCP first, falling back to REST bash staging.
   - **Diagnostic QKView Downloads**: `myf5 bigip qkview <host>` leverages automatic failover so that if the appliance's TMOS autodeploy download endpoint is disabled or missing, the generated QKView archive (`/var/tmp/qkviews/<filename>`) is downloaded via SCP without user intervention.

2. **iControl REST Chunked Transfer** (`--method icontrol`)
   - Operates over HTTPS (port 443 or custom management port).
   - **Upload**: Breaks payloads into 512 KB chunks transferred sequentially to `/mgmt/shared/file-transfer/uploads/<filename>`. Stages uploads in `/var/config/rest/downloads/<filename>` on the appliance, and automatically promotes files to the requested destination (e.g., `/shared/images/` or `/shared/GeoIP/`) using `/mgmt/tm/util/bash`.
   - **Download**: Streams files via HTTP range requests from `/mgmt/cm/autodeploy/software-image-downloads/` or `/mgmt/cm/autodeploy/qkview-downloads/` directly to local disk.
   - Ideal when SSH access is restricted by firewall policies, or when working through HTTPS reverse proxies and cloud access environments (e.g., F5 Unified Demonstration Framework / UDF).

3. **Native Go SCP over SSH** (`--method scp`)
   - Operates over SSH (port 22 or custom port).
   - Implements both remote SCP sink (receive/upload) and SCP source (transmit/download) protocols directly in pure Go (`golang.org/x/crypto/ssh`), requiring zero external OpenSSH binaries.
   - Streams payloads directly to and from arbitrary appliance filesystem locations in a single network pass.
   - Supports SSH key authentication, SSH agent forwarding (`SSH_AUTH_SOCK`), and password / keyboard-interactive (PAM / TACACS+ / RADIUS) authentication.
   - Ideal for multi-gigabyte transfers in datacenter LANs and high-speed management networks.

### Architecture Comparison Matrix

| Feature / Property | Automatic Failover (`auto`) | iControl REST (`icontrol`) | Native SCP over SSH (`scp`) |
| :--- | :--- | :--- | :--- |
| **Default Selection** | **Yes (Default)** | Explicit `--method icontrol` | Explicit `--method scp` |
| **Transport Protocol** | HTTPS or SSHv2 (Adaptive) | HTTPS (TLS 1.2 / 1.3) | SSHv2 |
| **Default Ports** | TCP 443 (REST) / TCP 22 (SSH) | TCP 443 | TCP 22 |
| **External Dependencies** | Zero | Zero (Pure Go `net/http`) | Zero (Pure Go `golang.org/x/crypto/ssh`) |
| **Network Path Compatibility** | All environments (Firewall resilient) | Web proxies, Cloud proxies, F5 UDF | Direct L3 / SSH bastions |
| **Chunk / Buffer Size** | 512 KB REST / 64 KB SCP | 512 KB (`Content-Range` chunking) | 64 KB streaming buffer |
| **Upload Destination** | Direct write or `/shared/images` | `/var/config/rest/downloads` (promoted) | Direct destination write via `scp -t` |
| **Download Source** | Any TMOS path (adaptive) | Autodeploy stores (`software-image-downloads`) | Any valid appliance file path via `scp -f` |
| **Authentication Methods** | Password, SSH Key, SSH Agent | HTTP Basic Auth (admin user & password) | SSH Key, SSH Agent, Password, PAM |
| **Appliance Compatibility** | BIG-IP TMOS (v12+), BIG-IQ (v7+) | BIG-IP TMOS (v12+), BIG-IQ (v7+) | Any BIG-IP TMOS or BIG-IQ instance |

---

## Transfer Flow Diagrams

### 1. iControl REST Chunked Upload & Promotion Flow

```mermaid
sequenceDiagram
    autonumber
    participant Client as myf5 CLI
    participant REST as iControl REST (/mgmt/shared)
    participant Disk as /var/config/rest/downloads
    participant Bash as TMOS Bash (/mgmt/tm/util/bash)
    participant Images as /shared/images

    Client->>REST: POST /mgmt/shared/file-transfer/uploads/BIGIP-17.1.1.iso [Chunk 0-524287/Total]
    REST->>Disk: Write chunk bytes
    REST-->>Client: HTTP 200 OK
    Note over Client,REST: Repeat sequential chunks until 100% complete
    Client->>REST: POST /mgmt/shared/file-transfer/uploads/BIGIP-17.1.1.iso [Final Chunk]
    REST->>Disk: Complete file assembly
    REST-->>Client: HTTP 200 OK (Uploaded to /var/config/rest/downloads/BIGIP-17.1.1.iso)
    opt Destination Promotion Requested (--dest /shared/images)
        Client->>Bash: POST /mgmt/tm/util/bash {command: "run", utilCmdArgs: "-c 'cp -f /var/config/rest/downloads/BIGIP-17.1.1.iso /shared/images/'"}
        Bash->>Images: Copy file to /shared/images/BIGIP-17.1.1.iso
        Bash-->>Client: HTTP 200 OK (Promotion Complete)
    end
```

### 2. Native Go SCP over SSH Streaming Flow

```mermaid
sequenceDiagram
    autonumber
    participant Client as myf5 CLI (Native Go SCP)
    participant SSH as Appliance SSH Daemon (sshd)
    participant Sink as Remote scp sink process (scp -t /shared/images)
    participant Target as /shared/images/BIGIP-17.1.1.iso

    Client->>SSH: TCP Connect & SSH Handshake (Keys / Agent / Password)
    SSH-->>Client: Authentication Accepted
    Client->>SSH: Request Exec "scp -t /shared/images"
    SSH->>Sink: Fork & Exec scp sink
    Sink-->>Client: 0x00 (Ready Acknowledgement)
    Client->>Sink: Protocol Header: "C0644 2516582400 BIGIP-17.1.1.iso\n"
    Sink-->>Client: 0x00 (Header Acknowledgement)
    loop 64 KB Streaming Buffer
        Client->>Sink: Stream raw file bytes
        Sink->>Target: Write directly to filesystem
    end
    Client->>Sink: 0x00 (End-of-File Null Byte)
    Sink-->>Client: 0x00 (Transfer Complete Acknowledgement)
    Client->>SSH: Close SSH Session
```

---

## Authentication & Zero-Secret Security Architecture

Security is enforced at the core of `myf5`. Plaintext secrets (`--password`, `--client-secret`) are **never** accepted via command-line flags. Attempting to supply credentials on the command line triggers an immediate termination with exit code `2`.

This policy ensures that appliance credentials never leak into:
- System process tables (`ps aux`, `/proc/$PID/cmdline`) visible to unprivileged users on multi-user jump boxes.
- Persistent shell histories (`~/.bash_history`, `~/.zsh_history`).
- Operating system telemetry and centralized security audit logs (Falco, auditd, Sysmon).

### Credential Resolution Hierarchy

`myf5` resolves appliance credentials through the following hierarchy:

```
1. Environment Variables:
   - BIG-IP: BIGIP_PASSWORD / BIGIP_USER
   - BIG-IQ: BIGIQ_PASSWORD / BIGIQ_USER (falls back to BIGIP_*)
     │ (If not set)
     ▼
2. SSH Key / Agent (SCP method only):
   - Explicit flag: --key <path>
   - SSH Agent: $SSH_AUTH_SOCK
   - Default Identity Files: ~/.ssh/id_ed25519, ~/.ssh/id_rsa, ~/.ssh/id_ecdsa
     │ (If not available)
     ▼
3. Interactive Masked Terminal Prompt:
   - Terminal echo suppressed via term.ReadPassword
   - Enter password securely in interactive TTY sessions
```

### Environment Setup for Automated Pipelines (CI/CD)

For automated scripts and continuous delivery runners, export the appropriate environment variables:

```bash
# For BIG-IP appliances
export BIGIP_USER="admin"
export BIGIP_PASSWORD="YourSecureAppliancePassword"

# For BIG-IQ Centralized Management
export BIGIQ_USER="admin"
export BIGIQ_PASSWORD="YourSecureAppliancePassword"
```

---

## Step-by-Step Practical Examples

### 1. Pushing a Base TMOS ISO Image for Installation

Software ISO images must reside in `/shared/images` on the BIG-IP appliance to be recognized by the TMOS software installation engine (`tmsh`).

#### Option A: Push via iControl REST with Automatic Promotion
```bash
myf5 bigip push 192.0.2.10 /var/tmp/BIGIP-17.1.1-0.0.6.iso \
    --method icontrol \
    --dest /shared/images
```

#### Option B: Push via Native Go SCP (Defaults to `/shared/images` automatically for ISOs)
```bash
myf5 bigip push 192.0.2.10 /var/tmp/BIGIP-17.1.1-0.0.6.iso \
    --method scp
```

#### TMOS Post-Push Software Installation
Once the file transfer completes, access the appliance via TMSH or SSH to trigger the installation:
```bash
# Verify the image is recognized by TMOS
tmsh show sys software

# Install to a new software volume (e.g., HD1.2)
tmsh install sys software image BIGIP-17.1.1-0.0.6.iso volume HD1.2 create-volume

# Monitor installation status
tmsh show sys software status
```

---

### 2. Pushing an Engineering Hotfix

Hotfix ISO files are uploaded directly to `/shared/images` alongside base images.

```bash
# Push an engineering hotfix via native SCP using a specific SSH key
myf5 bigip push 192.0.2.10 /var/tmp/Hotfix-BIGIP-17.1.1.1.0.0.2-ENG.iso \
    --method scp \
    --key ~/.ssh/f5_deploy_ed25519
```

#### TMOS Hotfix Installation Command
```bash
# Install hotfix on top of target volume
tmsh install sys software hotfix Hotfix-BIGIP-17.1.1.1.0.0.2-ENG.iso volume HD1.2
```

---

### 3. Pushing an Updated GeoDB Archive to `/shared/GeoIP/`

BIG-IP Application Security Manager (ASM), Advanced Firewall Manager (AFM), and Access Policy Manager (APM) utilize GeoLocation databases stored in `/shared/GeoIP/`.

```bash
# Push GeoDB update archive to /shared/GeoIP/ via iControl REST
myf5 bigip push 192.0.2.10 /var/tmp/ip-geolocation-default.zip \
    --method icontrol \
    --dest /shared/GeoIP
```

#### TMOS GeoDB Verification & Reload
```bash
# Connect to TMOS bash and update the active GeoIP databases
ssh admin@192.0.2.10 "geoip_update_data -f /shared/GeoIP/ip-geolocation-default.zip"

# Verify active GeoDB version
tmsh show sys geoip info
```

---

### 4. Pushing to BIG-IQ Central Software Repository

BIG-IQ Centralized Management stores managed software images and hotfixes in its centralized software repository (`/shared/images`), allowing administrators to orchestrate upgrades across fleets of BIG-IP devices from a single console.

#### Check BIG-IQ Management Plane Readiness
```bash
myf5 bigiq ready 192.0.2.50 --user admin
```
*Output:*
```
BIG-IQ appliance at 192.0.2.50 is READY (HTTP 200).
```

#### Push Software Image to BIG-IQ
```bash
# Push directly via BIG-IQ specific command
myf5 bigiq push 192.0.2.50 /var/tmp/BIGIP-17.1.1-0.0.6.iso \
    --method scp \
    --dest /shared/images

# Or use the unified push command
myf5 push 192.0.2.50 /var/tmp/BIGIP-17.1.1-0.0.6.iso \
    --target bigiq \
    --method icontrol \
    --dest /shared/images
```

---

### 5. Downloading Files from Appliances (`pull` / `download` with Automatic Failover)

`myf5` supports bidirectional file transfer. Files can be fetched from any BIG-IP or BIG-IQ appliance using `myf5 bigip download`, `myf5 bigiq download`, or the top-level `myf5 pull`:

```bash
# Download a software image from /shared/images/ (attempts REST first, auto-fails over to SCP)
myf5 bigip download 10.1.1.4 /shared/images/BIGIP-17.1.1-0.0.6.iso -o ./BIGIP-17.1.1.iso

# Download a system configuration file via native SCP
myf5 bigip download 10.1.1.4 /config/bigip.conf -o ./bigip.conf --method scp

# Unified pull command with specific SSH identity key
myf5 pull 10.1.1.4 /var/log/ltm -o ./ltm.log -i ~/.ssh/id_ed25519

# Download from BIG-IQ central repository
myf5 bigiq download 192.0.2.50 /shared/images/BIGIP-17.1.1-0.0.6.iso -o ./BIGIP-17.1.1.iso
```

#### Automatic Download Failover Mechanics
When `--method auto` is used:
1. `myf5` determines whether the file is an autodeploy artifact (under `/shared/images/` or `/var/tmp/qkviews/`).
2. If so, it initiates a streaming download via iControl REST.
3. If the REST API endpoint responds with HTTP 404 (file not staged in autodeploy) or returns a connection error, `myf5` alerts the operator with `[FAILOVER]` and immediately establishes a native Go SCP session, downloading the file directly from the appliance filesystem.

---

### 6. Resilient Diagnostic QKView Generation and Download

Generating a complete QKView diagnostic archive is critical for troubleshooting with F5 Technical Support. `myf5` runs a complete non-truncating (`-s0`) QKView generation on the appliance and retrieves it:

```bash
myf5 bigip qkview 10.1.1.4 --user admin
```

1. **Remote Generation**: Calls `/mgmt/tm/cm/device/diagnostic` with live spinner reporting until TMOS finishes compiling `/var/tmp/qkviews/<filename>`.
2. **Resilient Download**: Attempts chunked REST download from `/mgmt/cm/autodeploy/qkview-downloads/<filename>`.
3. **Automatic Failover**: If TMOS autodeploy is disabled or returns 404, `myf5` automatically switches to native Go SCP, transferring `/var/tmp/qkviews/<filename>` without aborting the workflow.

---

## End-to-End "Download & Push in One Step"

The `myf5 downloads get` command features native post-download push flags. When `--push <host>` is supplied, `myf5`:
1. Streams the software image from the authoritative MyF5 CDN mirror to local disk with constant memory footprint (< 10 MB RAM).
2. Computes the SHA-256 digest on the fly via `io.TeeReader`.
3. Verifies the downloaded file's SHA-256 hash against the official catalog digest.
4. **Only upon successful verification**, immediately begins pushing the validated image directly to the target BIG-IP or BIG-IQ appliance.

If the downloaded file fails checksum verification, the pipeline terminates immediately with an error, preventing corrupt images from ever reaching the appliance.

### Examples

#### Download BIG-IP ISO and Push via iControl REST
```bash
myf5 downloads get "BIG-IP" "big-ip_v17.x" "17.1.1" \
    "Base Software & ISO Releases" "BIGIP-17.1.1-0.0.6.iso" \
    -o /var/tmp/BIGIP-17.1.1.iso \
    --push 10.1.1.4 \
    --push-method icontrol \
    --push-dest /shared/images
```

#### Automated GeoDB Weekly Download & Appliance Update
```bash
myf5 downloads get "BIG-IP" "big-ip_v17.x" "17.1.1" \
    "GeoLocationUpdates" "ip-geolocation-default.zip" \
    -o /var/tmp/ip-geolocation-default.zip \
    --push 10.1.1.4 \
    --push-method scp \
    --push-dest /shared/GeoIP
```

#### Download and Push to BIG-IQ Central Repository
```bash
myf5 downloads get "BIG-IP" "big-ip_v17.x" "17.1.1" \
    "Hotfixes & Patches" "Hotfix-BIGIP-17.1.1.1.0.0.2-ENG.iso" \
    --push bigiq.lab.internal \
    --push-target bigiq \
    --push-method scp \
    --push-dest /shared/images
```

---

## Protocol Deep Dive & Technical Specifications

### 1. iControl REST Chunked File Transfer Protocol

F5 iControl REST handles file transfers via a multipart/chunked endpoint managed by the TMOS `reststatemachine` daemon:

- **Target Endpoint**: `POST /mgmt/shared/file-transfer/uploads/<filename>`
- **Chunk Size**: `524,288` bytes (512 KB), defined as `DefaultUploadChunkSize`.
- **Headers**:
  ```http
  POST /mgmt/shared/file-transfer/uploads/BIGIP-17.1.1.iso HTTP/1.1
  Host: 10.1.1.4
  Authorization: Basic YWRtaW46UGFzc3dvcmQ=
  Content-Type: application/octet-stream
  Content-Range: 0-524287/2516582400
  User-Agent: myf5-api-go/1.0.0
  ```

#### Chunk Header Mathematics
For a file of size $S$ bytes and chunk index $k \ge 0$:
$$\text{Start} = k \times 524288$$
$$\text{End} = \min(\text{Start} + N - 1, S - 1)$$
$$\text{Content-Range} = \text{Start}-\text{End}/S$$

#### Remote File Promotion Mechanism
When `--dest` is specified, `myf5` issues a post-upload REST request to the TMOS bash execution utility:
- **Endpoint**: `POST /mgmt/tm/util/bash`
- **Request Body**:
  ```json
  {
    "command": "run",
    "utilCmdArgs": "-c 'cp -f /var/config/rest/downloads/<filename> <destDir>/<filename>'"
  }
  ```
If appliance bash execution is disabled by TMOS role-based access controls (RBAC), the file remains safely staged in `/var/config/rest/downloads/<filename>`.

### 2. Native Go SCP Sink Protocol Mechanics

Rather than spawning an external OpenSSH subprocess (`/usr/bin/scp`), `myf5` drives the SCP sink protocol natively using Go's `golang.org/x/crypto/ssh` library.

#### Protocol Sequence Details:
1. **Execution Request**: The SSH client initiates an interactive channel and executes:
   ```
   scp -t <destDir>
   ```
   The remote appliance starts `scp` in sink (receive) mode targeting `<destDir>`.
2. **Initial Handshake**: The remote process sends an acknowledgement byte `0x00`. `myf5` validates this byte before transmitting payload headers.
3. **File Metadata Header**: `myf5` formats a POSIX file mode control line:
   ```
   C<octal-mode> <file-size> <filename>\n
   ```
   *Example*: `C0644 2516582400 BIGIP-17.1.1.iso\n`
4. **Header Ack**: The appliance responds with `0x00` confirming the filesystem has allocated the file descriptor.
5. **Byte Streaming**: File content is read in 64 KB buffers (`65,536` bytes) and written directly to the SSH session `StdinPipe`. Progress is continuously reported via terminal progress bars.
6. **EOF & Completion Handshake**: Upon reading local `io.EOF`, `myf5` sends a trailing null byte (`0x00`). The appliance flushes its disk buffers, closes the file, and responds with a final `0x00` completion acknowledgement.

#### Appliance Destination Path Heuristics
When `--dest` is omitted in SCP mode, `myf5` applies intelligent heuristics based on filename:
- Files ending in `.iso`, `.iso.zip`, or containing `hotfix` (case-insensitive) automatically target `/shared/images`.
- All other files (e.g. `.zip`, `.tar.gz`, `.txt`) default to `/var/tmp`.

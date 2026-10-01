# Appliance Push Guide: BIG-IP & BIG-IQ Software, Hotfix, and GeoDB Deployment

The `myf5` appliance push engine enables direct, wire-speed streaming of software images, engineering hotfixes, and GeoDB packages to **F5 BIG-IP TMOS** and **BIG-IQ Centralized Management** appliances.

By embedding both **iControl REST chunked transfer** and **native Go SCP over SSH**, `myf5` eliminates external scripting dependencies (such as external `scp`, `sshpass`, or Python paramiko wrappers) while maintaining strict **Zero-Secret CLI Security**.

---

## Architectural Overview

`myf5` provides two independent transport mechanisms for pushing files to appliances:

1. **iControl REST Chunked Transfer** (Default: `--method icontrol`)
   - Operates over HTTPS (port 443 or custom management port).
   - Breaks payloads into 512 KB chunks transferred sequentially to `/mgmt/shared/file-transfer/uploads/<filename>`.
   - Stages uploads in `/var/config/rest/downloads/<filename>` on the appliance.
   - Automatically promotes files to the requested destination (e.g., `/shared/images/` or `/shared/GeoIP/`) using the TMOS bash utility (`/mgmt/tm/util/bash`).
   - Ideal when SSH access is restricted by firewall policies, or when working through HTTPS reverse proxies and cloud access environments (e.g., F5 Unified Demonstration Framework / UDF).

2. **Native Go SCP over SSH** (`--method scp`)
   - Operates over SSH (port 22 or custom port).
   - Implements the standard remote SCP sink protocol directly in pure Go (`golang.org/x/crypto/ssh`), requiring no external OpenSSH binaries.
   - Streams payloads directly into the target filesystem location (such as `/shared/images`) in a single network pass, without intermediate staging in `/var/config/rest/downloads/`.
   - Supports SSH key authentication, SSH agent forwarding (`SSH_AUTH_SOCK`), and password / keyboard-interactive (PAM / TACACS+ / RADIUS) authentication.
   - Ideal for multi-gigabyte ISO transfers in datacenter LANs and high-speed management networks.

### Architecture Comparison Matrix

| Feature / Property | iControl REST (`icontrol`) | Native SCP over SSH (`scp`) |
| :--- | :--- | :--- |
| **Transport Protocol** | HTTPS (TLS 1.2 / 1.3) | SSHv2 |
| **Default Port** | TCP 443 | TCP 22 |
| **External Dependencies** | Zero (Pure Go `net/http`) | Zero (Pure Go `golang.org/x/crypto/ssh`) |
| **Network Path Compatibility** | Web proxies, Cloud proxies, F5 UDF | Direct L3 / SSH bastions |
| **Chunk / Buffer Size** | 512 KB (`Content-Range` chunking) | 64 KB streaming buffer |
| **Initial Staging Path** | `/var/config/rest/downloads/<file>` | Direct write to destination directory |
| **Promotion Mechanism** | TMOS Bash utility (`/mgmt/tm/util/bash`) | Direct destination write via `scp -t` |
| **Authentication Methods** | HTTP Basic Auth (admin user & password) | SSH Key, SSH Agent, Password, Keyboard-Interactive |
| **Default Image Destination** | Staged in rest downloads; promoted via `--dest` | `/shared/images` for ISO/hotfix; `/var/tmp` for others |
| **Appliance Compatibility** | BIG-IP TMOS (v12+), BIG-IQ (v7+) | Any BIG-IP TMOS or BIG-IQ instance with SSH access |

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

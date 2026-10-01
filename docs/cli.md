# Command-Line Interface (CLI) Reference

`myf5` is a high-performance, zero-dependency command-line utility built in pure Go for orchestrating F5 cloud services, BIG-IP TMOS appliances, and BIG-IQ Centralized Management.

---

## Global Syntax & Policy

```bash
myf5 <command> [subcommand] [arguments...] [options]
```

### Zero-Secret Security Enforcement
In accordance with enterprise DevSecOps standards, **passing credentials or tokens via command-line flags is strictly prohibited**. 
- Flags like `--password`, `-p`, `--client-secret`, or `--token` are trapped prior to command dispatch.
- When detected, `myf5` immediately terminates with exit code `2` to prevent credentials from leaking into process tables (`/proc/$PID/cmdline`, `ps aux`), system audit logs (auditd, Falco), and shell histories (`.zsh_history`, `.bash_history`).
- Store cloud credentials in `~/.f5api_credentials` or set `F5_CLIENT_ID` / `F5_CLIENT_SECRET` in the environment.
- Appliance passwords are provided via environment variables (`BIGIP_PASSWORD`, `BIGIQ_PASSWORD`), SSH keys/agents, or entered interactively at the masked terminal prompt.

---

## Summary of Commands

| Command | Description |
| :--- | :--- |
| [`downloads`](#1-myf5-downloads) | Browse the MyF5 software catalog and stream multi-GB images |
| [`cases`](#2-myf5-cases) | Inspect, query, and comment on MyF5 support cases |
| [`ihealth`](#3-myf5-ihealth) | Upload and audit QKView diagnostic bundles on F5 iHealth |
| [`bigip`](#4-myf5-bigip) | Orchestrate BIG-IP TMOS appliances (readiness, QKView generation, push) |
| [`bigiq`](#5-myf5-bigiq) | Orchestrate BIG-IQ Centralized Management appliances (readiness, push) |
| [`push`](#6-myf5-push) | Push downloads or files to BIG-IP / BIG-IQ via iControl REST or SCP |
| [`doctor`](#7-myf5-doctor) | Run connectivity, DNS, egress, and credential diagnostics |
| [`completion`](#8-myf5-completion) | Generate native shell autocompletion scripts (`bash`, `zsh`, `fish`) |
| [`version`](#9-myf5-version) | Display application version and toolchain info |

---

## 1. `myf5 downloads`

Browse the MyF5 software image catalog, query releases, and download images with live streaming progress, cryptographic checksum verification, and optional post-download appliance push.

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

Manage and inspect MyF5 technical support cases via the Support Case API (`support.apis.f5.com`, endpoints: `/case-management/v1/cases` and `/support/case-management/v1/cases`).

### Actions

#### `list`
Lists active support cases for the authenticated account, displaying case number, status, severity, and subject.
```bash
myf5 cases list
```

#### `metadata`
Retrieves the authoritative schema metadata and valid values for case creation.
```bash
myf5 cases metadata
```

#### `comment <case-number> <note>`
Appends a status comment or investigation update to an open support case.
```bash
myf5 cases comment C1000234 "Uploaded new diagnostic packet captures."
```

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
myf5 ihealth upload /path/to/appliance.qkview "C1000234" "Initial triage bundle"
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

#### `qkview <host> [--user <user>] [--port <port>]`
Generates a complete, non-truncating (`-s0`) diagnostic QKView archive on the BIG-IP appliance via `/mgmt/tm/util/qkview` or `/mgmt/cm/autodeploy/qkview` with live spinner status updates, then downloads it directly to local disk (falling back to native Go SCP if autodeploy download is unavailable).
```bash
myf5 bigip qkview 10.1.1.4 --user admin
```

#### `push <host> <file> [--method auto|icontrol|scp] [--dest <path>] [--user <user>] [--port <port>] [-i <key>]`
Pushes a local software image, engineering hotfix, or GeoDB archive to the BIG-IP appliance with automatic seamless failover between iControl REST and SCP.
```bash
# Push ISO to /shared/images with automatic failover (default)
myf5 bigip push 10.1.1.4 /var/tmp/BIGIP-17.1.1.iso --dest /shared/images

# Push hotfix explicitly via native Go SCP
myf5 bigip push 10.1.1.4 /var/tmp/Hotfix-BIGIP-17.1.1.1.0.0.2-ENG.iso --method scp -i ~/.ssh/f5_key

# Push GeoDB update archive to /var/tmp staging directory via iControl REST
myf5 bigip push 10.1.1.4 /var/tmp/ip-geolocation-default.zip --dest /var/tmp --method icontrol
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

## 11. Process Exit Codes

`myf5` returns standard POSIX exit codes to support reliable shell script branching and CI/CD pipelines:

| Exit Code | Classification | Description |
| :--- | :--- | :--- |
| `0` | **Success** | Operation completed successfully and verified. |
| `1` | **General Error** | Command syntax error, invalid arguments, network failure, missing credentials in non-interactive environment, or appliance error. |
| `2` | **Zero-Secret Violation** | Security error triggered when prohibited credential flags (`--password`, `--client-secret`) are supplied on the command line. |
| `130` | **Cancelled / Interrupted** | Execution aborted by user via `SIGINT` (Ctrl+C) or `SIGTERM`. |

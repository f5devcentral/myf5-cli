# Command-Line Interface (CLI) Reference

`myf5` is a high-performance, zero-dependency command-line utility built in pure Go for orchestrating F5 cloud services and BIG-IP TMOS appliances.

---

## Global Syntax & Policy

```bash
myf5 <command> [subcommand] [arguments...] [options]
```

### Zero-Secret Security Enforcement
In accordance with enterprise DevSecOps standards, **passing credentials or tokens via command-line flags is strictly prohibited**. 
- Flags like `--password`, `-p`, `--client-secret`, or `--token` are trapped prior to command dispatch.
- When detected, `myf5` immediately terminates with exit code `2` to prevent credentials from leaking into process tables (`/proc/$PID/cmdline`, `ps aux`), system audit logs (auditd, Falco), and shell histories (`.zsh_history`, `.bash_history`).
- Store credentials in `~/.f5api_credentials` or set `F5_CLIENT_ID` / `F5_CLIENT_SECRET` in the environment.

---

## Summary of Commands

| Command | Description |
| :--- | :--- |
| [`downloads`](#1-myf5-downloads) | Browse the MyF5 software catalog and stream multi-GB images |
| [`cases`](#2-myf5-cases) | Inspect, query, and comment on MyF5 support cases |
| [`ihealth`](#3-myf5-ihealth) | Upload and audit QKView diagnostic bundles on F5 iHealth |
| [`bigip`](#4-myf5-bigip) | Orchestrate BIG-IP TMOS appliances (readiness, QKView generation) |
| [`doctor`](#5-myf5-doctor) | Run connectivity, DNS, egress, and credential diagnostics |
| [`completion`](#6-myf5-completion) | Generate native shell autocompletion scripts (`bash`, `zsh`, `fish`) |
| [`version`](#7-myf5-version) | Display application version and toolchain info |

---

## 1. `myf5 downloads`

Browse the MyF5 software image catalog, query releases, and download images with live streaming progress and cryptographic checksum verification.

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

#### `get <family> <line> <version> <container> <file> [-o <path>]`
Streams the specified software image directly to disk. Uses constant RAM (<10 MB) via `io.TeeReader` and validates SHA-256 integrity upon completion.
```bash
myf5 downloads get BIG-IP big-ip_v17.x 17.1.1 "Virtual Edition" BIGIP-17.1.1-0.0.6.ALL.qcow2.tar.gz -o /var/tmp/bigip.qcow2.tar.gz
```

### Options
- `-o, --output <path>`: Destination file path for downloaded image.
- `--profile <name>`: Select credentials profile from `~/.f5api_credentials` (default: `"default"`).

---

## 2. `myf5 cases`

Manage and inspect MyF5 technical support cases via the Support Case API (`support.apis.f5.com`).

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

Interact with the F5 iHealth API (`ihealth2-api.f5.com`) for diagnostic QKView bundle processing.

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

Automate BIG-IP TMOS appliances over the iControl REST interface. Supports direct IP addressing, custom ports, and cloud/UDF external access proxies.

### Actions

#### `ready <host> [--user <user>] [--port <port>]`
Verifies management plane availability against `/mgmt/tm/sys/ready`.
```bash
# Direct appliance IP
myf5 bigip ready 10.1.1.4

# Via UDF or Cloud External Access Proxy
myf5 bigip ready https://aa68f6ba-a751-4740-9382-85f7cd7940f2.access.udf.f5.com/
```

#### `qkview <host> [--user <user>] [--port <port>]`
Generates a complete, non-truncating (`-s0`) diagnostic QKView archive on the BIG-IP appliance with live spinner status updates, then downloads it directly to local disk.
```bash
myf5 bigip qkview 10.1.1.4 --user admin
```

### Options
- `-u, --user <username>`: Administrative user (default: `$BIGIP_USER` or `"admin"`).
- `-p, --port <port>`: Target HTTPS management port (default: 443).
- **Authentication**: Set via `BIGIP_PASSWORD` environment variable, or enter securely when prompted via masked terminal input.

---

## 5. `myf5 doctor`

Pre-flight diagnostic audit that verifies local credentials, DNS resolution, and TCP port 443 egress to all authoritative F5 cloud endpoints.

```bash
myf5 doctor
```

Output audits:
1. `~/.f5api_credentials` or environment credentials (redacted output).
2. Outbound TCP 443 connectivity to Auth0, Okta, Downloads API, Support Case API, iHealth, and Callhome.

---

## 6. `myf5 completion`

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

## 7. `myf5 version`

Prints the compiled application version and target architecture.
```bash
myf5 version
```

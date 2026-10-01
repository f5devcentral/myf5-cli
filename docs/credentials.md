# Credential Management & Zero-Secret Security Architecture

Security is built into `myf5` at the architectural level. This document details credential storage, profile management, environment variable resolution, and our defense-in-depth zero-secret security guarantees.

---

## The Zero-Secret CLI Policy

**Flags that accept passwords or client secrets (`--password`, `-p`, `--client-secret`) are strictly forbidden in `myf5`.**

### Why Command-Line Secret Flags Are Prohibited
1. **Process Table Inspection**: On multi-user Unix/Linux jump hosts and bastion servers, command-line arguments are globally visible to all unprivileged users via `/proc/$PID/cmdline` and commands like `ps aux` or `top`.
2. **Persistent Shell Histories**: Commands entered on the terminal are written in plaintext to `~/.bash_history` and `~/.zsh_history`.
3. **Host Auditing & EDR Logs**: System telemetry daemons (such as `auditd`, Sysmon for Linux, and Falco) log command-line invocations, creating plaintext secret exposures in centralized logging pipelines.

In `myf5`, any command containing `--password` or `--client-secret` is aborted by `auth.CheckNoCLISecrets` before flag evaluation begins:
```
[SECURITY ERROR] Passing credentials via CLI arguments (--password, --client-secret) is prohibited.
Store secrets in ~/.f5api_credentials or export F5_CLIENT_ID / F5_CLIENT_SECRET.
```

---

## Credential Storage Options

### 1. Credentials File (`~/.f5api_credentials`)

`myf5` supports INI-style credential profiles in `~/.f5api_credentials`:

```ini
[default]
client_id = your_oauth2_client_id
client_secret = your_oauth2_client_secret

[production]
client_id = prod_oauth2_client_id
client_secret = prod_oauth2_client_secret

[lab]
client_id = lab_oauth2_client_id
client_secret = lab_oauth2_client_secret
```

#### POSIX File Permissions
- Recommended file permission: `0600` (`-rw-------`).
- `myf5` permits `0640` and `0644` with a security warning if the file is world-readable (`perm & 0004 != 0`).
- **World-writable files (`perm & 0002 != 0`) are strictly rejected** to prevent unauthorized credential tampering.

```bash
chmod 600 ~/.f5api_credentials
```

#### Profile Selection
You can specify which profile to use with the `--profile` flag:
```bash
myf5 downloads families --profile production
```

---

### 2. Environment Variables (Recommended for CI/CD)

For automated pipelines (e.g., GitHub Actions, GitLab CI, Kubernetes jobs), provide credentials via environment variables:

```bash
export F5_CLIENT_ID="your_oauth2_client_id"
export F5_CLIENT_SECRET="your_oauth2_client_secret"
```

When set, environment variables take precedence over profile files.

---

### 3. BIG-IP Appliance Credentials

When managing BIG-IP TMOS appliances via `myf5 bigip`:

- **Username**: Sourced from `$BIGIP_USER`, the `--user` flag, or defaults to `"admin"`.
- **Password**:
  - May be provided via the `BIGIP_PASSWORD` environment variable.
  - If `BIGIP_PASSWORD` is not set and an interactive terminal (TTY) is detected, `myf5` prompts for the password with terminal echo suppressed via `golang.org/x/term`:
    ```
    Enter BIG-IP password for admin@10.1.1.4: 
    ```

---

## In-Memory Defense & Secret Redaction

1. **Stringer Redaction**: Go structs containing tokens or client secrets implement custom `fmt.Stringer` and `fmt.GoStringer` interfaces that redact sensitive strings to prevent leaks in stack traces or logs.
2. **Crash Dump Protection**: Error messages never format raw credential values into returned error strings.
3. **Memory Scrubbing**: Sensitive byte slices allocated during masked password entry are zeroed out after use.

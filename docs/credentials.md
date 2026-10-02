# Credential Management & Zero-Secret Security Architecture

Security is built into `myf5` at the architectural level. This document details credential storage, profile management, environment variable resolution, and the defense-in-depth zero-secret security guarantees.

---

## The Zero-Secret CLI Policy

**Flags that accept passwords or client secrets (`--password`, `-pw`, `--client-secret`) are strictly forbidden in `myf5`.** The short flag `-p` is not among them — it sets the port on appliance commands.

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

## Obtaining API Credentials

Before any of the storage options below will work, you need an API key — a `client_id` and `client_secret` pair — issued from your MyF5 account. Access is not enabled by default; it is granted per account on request.

1. **Log in to MyF5.** Sign in at [my.f5.com](https://my.f5.com) with the account associated with your organization's F5 entitlements.
2. **Request access to the API Catalog.** Raise a request with F5 Support asking for API Catalog access on your account. This is an entitlement grant rather than a self-service toggle, so it is not instantaneous — expect it to be handled on support's normal turnaround.
3. **Create an API key.** Once support confirms the grant, the API Catalog becomes available in MyF5. Create a key there; it is issued as a `client_id` and `client_secret` pair.

The `client_secret` is shown once at creation. Copy it straight into `~/.f5api_credentials` or your CI secret store — if it is lost, generate a new key rather than attempting to recover the old one.

### The `k` API usage key

Support Case and legacy Downloads endpoints on `support.apis.f5.com` require a **second** credential alongside the OAuth token: an "API usage key" sent as the `k` query parameter. F5's OpenAPI specification marks it required on every such operation, independently of bearer authentication.

It is issued with the same API Catalog entitlement as your client credentials. Supply it as `api_key`:

```ini
[default]
client_id = your_oauth2_client_id
client_secret = your_oauth2_client_secret
api_key = your_api_usage_key
```

or by environment variable, which takes precedence:

```bash
export F5_API_KEY="your_api_usage_key"
```

An `api_key` at the top level of the file applies to every profile; one inside a profile section overrides it.

> [!WARNING]
> `myf5` carries a built-in fallback key for backward compatibility, but **it does not work** — it is not associated with any current entitlement. Without your own key, Support Case commands fail with:
> ```
> 403: {"message":"The API Key to API association not exist."}
> ```
> Note that this is a `403`, distinct from the `401` you would see from a bad OAuth token. If you get a 403 where the token is known good, the `k` key is the cause.

> [!NOTE]
> These credentials authenticate to the F5 **cloud** APIs — Downloads, Support Cases, and iHealth. They are unrelated to BIG-IP and BIG-IQ appliance credentials, which are covered in [§3](#3-big-ip--big-iq-appliance-credentials) below and are never issued through MyF5.

Verify a new key end to end with:

```bash
myf5 doctor
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
Select an alternative profile with the `--profile` flag:
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

#### OAuth 2.0 Token Server Resolution
`myf5` supports both modern F5 CIAM token issuing infrastructures:
- **Auth0 (Primary)**: `https://idp.identity.f5.com/oauth/token` (standard client credentials grant).
- **Okta CIAM (Dedicated Server)**: `https://identity.account.f5.com/oauth2/ausp95yxb4ShxMGvW357/v1/token` with audience `api://ciam-api-myf5` and scope `myf5_scope`.

---

### 3. BIG-IP & BIG-IQ Appliance Credentials

When managing BIG-IP TMOS or BIG-IQ Centralized Management appliances via `myf5 bigip`, `myf5 bigiq`, `myf5 push`, or post-download push flags (`--push`):

- **Username Resolution**:
  - Command flag: `-u`, `--user <username>` or `--push-user <username>`.
  - Environment variable: `$BIGIQ_USER` (for BIG-IQ) or `$BIGIP_USER` (for BIG-IP and fallback).
  - Default: `"admin"`.

- **Password Resolution**:
  - Environment variables:
    * BIG-IQ: `$BIGIQ_PASSWORD` (falls back to `$BIGIP_PASSWORD`).
    * BIG-IP: `$BIGIP_PASSWORD`.
  - Interactive Masked Terminal Prompt:
    If no password environment variable is set and an interactive terminal (TTY) is detected, `myf5` securely prompts for the appliance password with terminal echo suppressed via `golang.org/x/term`:
    ```
    Enter BIG-IP password for admin@10.1.1.4: 
    ```
    In non-interactive environments (cron, headless CI/CD), if the password is not provided via environment variables or SSH keys, `myf5` halts execution with exit code `1` rather than hanging.

- **SSH Key & Agent Authentication (SCP Method)**:
  When using native Go SCP transfers (`--method scp` or `--push-method scp`):
  1. **Explicit Private Key**: Specify `-i`/`--identity <path>` (appliance `push`/`download`) or `--push-key <path>` (download-then-push), both supporting encrypted keys with passphrase. Note that `--key` is **not** a valid flag: it appears in `ForbiddenSecretFlags`, so passing it aborts the command before any work is done.
  2. **SSH Agent**: Connects automatically to `$SSH_AUTH_SOCK` if active.
  3. **Default Key Fallback**: Inspects standard user keys (`~/.ssh/id_ed25519`, `~/.ssh/id_rsa`, `~/.ssh/id_ecdsa`).
  4. **Password / PAM Fallback**: Uses password or interactive prompt if no keys are found.

> [!IMPORTANT]
> **TMOS User Shell Requirements for SCP**: On BIG-IP TMOS, the `admin` user is configured with `tmsh` shell by default (`Terminal Access: tmsh`), which restricts SCP operations to whitelisted directories (`/var/tmp/`). To transfer files directly to `/shared/images/` or system directories via SCP, either authenticate as `root` (which defaults to `bash`), grant the user Advanced Shell (`bash`) terminal access (`tmsh modify auth user <username> shell bash`), or utilize `--method icontrol` which operates independent of the user's interactive shell.

---

## In-Memory Defense & Secret Redaction

1. **Stringer Redaction**: Go structs containing tokens or client secrets implement custom `fmt.Stringer` and `fmt.GoStringer` interfaces that redact sensitive strings to prevent leaks in stack traces or logs.
2. **Crash Dump Protection**: Error messages never format raw credential values into returned error strings.
3. **Memory Scrubbing**: Sensitive byte slices allocated during masked password entry are zeroed out after use.

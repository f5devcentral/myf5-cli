# Credential Management & Zero-Secret Security Architecture

Security is built into `myf5` at the architectural level. This document details credential storage, profile management, environment variable resolution, and the defense-in-depth zero-secret security guarantees.

---

## The Two-Tier Zero-Secret CLI Policy

**Flags that accept passwords, tokens, API keys, or client secrets on the command line are strictly forbidden in `myf5`.** The short flag `-p` is not among them — it sets the port on appliance commands, and `--cert-key` / `--key-file` specifies a TLS client private key file path.

### Two-Tier Enforcement Architecture
1. **Tier 1 — Global Pre-Dispatch Rejection (`pkg/auth.CheckNoCLISecrets` on `os.Args[1:]`, Exit Code `2` before command dispatch)**:
   Before command dispatch or flag parsing begins, `auth.CheckNoCLISecrets` inspects `os.Args[1:]` and immediately aborts with exit code `2` if any of the **22 global secret flags** are present:
   - `--password`, `-password`, `--pw`, `-pw`
   - `--client-secret`, `-client-secret`, `--clientsecret`, `-clientsecret`
   - `--secret`, `-secret`
   - `--token`, `-token`, `--access-token`, `-access-token`, `--auth-token`, `-auth-token`
   - `--api-key`, `-api-key`, `--apikey`, `-apikey`
   - `--key`, `-key`
2. **Tier 2 — Appliance Subcommand Flag Rejection (`cmd/myf5.rejectSecretFlag`, Exit Code `1` in appliance subcommand flag parsers)**:
   Appliance subcommand flag parsers additionally trap and reject `--pass`, `-P`, and `--ssh-passphrase` (along with `--password`, `--token`, and `--secret`) with exit code `1`.

### Why Command-Line Secret Flags Are Prohibited
1. **Process Table Inspection**: On multi-user Unix/Linux jump hosts and bastion servers, command-line arguments are globally visible to all unprivileged users via `/proc/$PID/cmdline` and commands like `ps aux` or `top`.
2. **Persistent Shell Histories**: Commands entered on the terminal are written in plaintext to `~/.bash_history` and `~/.zsh_history`.
3. **Host Auditing & EDR Logs**: System telemetry daemons (such as `auditd`, Sysmon for Linux, and Falco) log command-line invocations, creating plaintext secret exposures in centralized logging pipelines.

In `myf5`, any command containing a Tier 1 global secret flag is aborted by `auth.CheckNoCLISecrets` before flag evaluation begins:
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

### iHealth credentials are separate

iHealth does **not** use your MyF5 client credentials. It is a distinct OAuth resource server (`api://ciam-api-ihealth`), and a MyF5 client holds no grant on it — using one produces:

```
403 Client "..." is not authorized to access resource server "api://ciam-api-myf5".
```

Per F5 article **K000130498**, generate a dedicated pair at [ihealth.f5.com/qkview-analyzer/settings](https://ihealth.f5.com/qkview-analyzer/settings) → **API Token** → **Generate New Credentials**.

> [!IMPORTANT]
> Key lifecycle considerations:
> - Generating new credentials **invalidates the previous pair** immediately.
> - The client secret is shown **once** and never again.
> - The pair **expires after six months**, after which you must regenerate.

Store them prefixed, alongside your MyF5 pair in the same file:

```ini
client_id = your_myf5_client_id
client_secret = your_myf5_client_secret

ihealth_client_id = your_ihealth_client_id
ihealth_client_secret = your_ihealth_client_secret
```

Or by environment variable:

```bash
export IHEALTH_CLIENT_ID="..."
export IHEALTH_CLIENT_SECRET="..."
```

`myf5` selects the prefixed pair (`IHEALTH_CLIENT_ID` / `IHEALTH_CLIENT_SECRET` or `ihealth_client_id` / `ihealth_client_secret`) for `ihealth` commands and the unprefixed pair (`F5_CLIENT_ID` / `F5_CLIENT_SECRET`) for everything else, falling back to the unprefixed pair when no iHealth-specific one is present. It also sends the correct audience and scope automatically:
- **MyF5 Cloud APIs (`downloads`, `cases`)**: `audience=api://ciam-api-myf5`, `scope=myf5_scope`
- **F5 iHealth API (`ihealth`, per K000130498)**: `audience=api://ciam-api-ihealth`, empty `scope=""` (an iHealth token request carries **no** `scope` parameter; access comes from the audience alone)

Bearer tokens last 30 minutes and are re-requested as needed.

---

### The `k` API usage key (`F5_API_KEY`)

Endpoints on `support.apis.f5.com` require a `?k=<APIKey>` query parameter — an "API usage key" — alongside the OAuth bearer token. F5's [published OpenAPI specification](https://my.f5.com/manage/myf5ApiReference) marks it required on every such operation.

**You do not normally need to configure this.** Despite the name, it is not a per-user secret — the value is published in F5's specification, and `myf5` includes the working default key automatically.

> [!NOTE]
> **Downloads primary vs. gateway fallback:** `api.software.downloads.f5.com` authenticates via the OAuth2 Bearer token on the primary gateway route, and `F5_API_KEY` (`api_key`) is also passed for automatic Downloads gateway fallback if required.

If you need to diagnose a `k` rejection, note that **the API is rate limited** — test with single requests spaced out rather than tight loops, as rate-limited responses can resemble key errors.

Should F5 rotate a key or issue a tenant-specific one, override it:

```bash
export F5_API_KEY="your_api_usage_key"
```

```ini
[default]
client_id = your_oauth2_client_id
client_secret = your_oauth2_client_secret
api_key = your_api_usage_key
```

The environment variable takes precedence. An `api_key` at the top level of the file applies to every profile; one inside a profile section overrides it.

> [!NOTE]
> A single override applies to whichever client is in use, so setting it will send that key to every family. It is intended for a rotation affecting the API you are calling, not as general configuration.

> [!NOTE]
> These credentials authenticate to the F5 **cloud** APIs — Downloads, Support Cases, and iHealth. They are unrelated to BIG-IP and BIG-IQ appliance credentials, which are covered in [§3](#3-big-ip--big-iq-appliance-credentials-ssh-known_hosts--tls-controls) below and are never issued through MyF5.

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
ihealth_client_id = your_ihealth_client_id
ihealth_client_secret = your_ihealth_client_secret

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
# MyF5 Cloud APIs (audience=api://ciam-api-myf5, scope=myf5_scope)
export F5_CLIENT_ID="your_oauth2_client_id"
export F5_CLIENT_SECRET="your_oauth2_client_secret"
export F5_API_KEY="optional_api_usage_key"

# F5 iHealth API (audience=api://ciam-api-ihealth, empty scope="" per K000130498)
export IHEALTH_CLIENT_ID="your_ihealth_client_id"
export IHEALTH_CLIENT_SECRET="your_ihealth_client_secret"
```

When set, environment variables take precedence over profile files.

#### OAuth 2.0 Token Server Resolution
`myf5` supports both modern F5 CIAM token issuing infrastructures:
- **Auth0 (Primary)**: `https://idp.identity.f5.com/oauth/token` (standard client credentials grant).
- **Okta CIAM (Dedicated Server)**: `https://identity.account.f5.com/oauth2/ausp95yxb4ShxMGvW357/v1/token` with `audience=api://ciam-api-myf5` and `scope=myf5_scope` (or `audience=api://ciam-api-ihealth` with empty `scope=""` for iHealth).

---

### 3. BIG-IP & BIG-IQ Appliance Credentials, SSH `known_hosts` & TLS Controls

When managing BIG-IP TMOS or BIG-IQ Centralized Management appliances via `myf5 bigip`, `myf5 bigiq`, `myf5 ready`, `myf5 info`, `myf5 serial`, `myf5 qkview`, `myf5 push`, `myf5 pull`, `myf5 geoip`, or post-download push flags (`--push`):

- **Username Resolution**:
  - Command flag: `-u`, `--user <username>` or `--push-user <username>`.
  - Environment variable: `$BIGIQ_USER` (for BIG-IQ) or `$BIGIP_USER` (for BIG-IP and fallback).
  - Default: `"admin"` for iControl REST, `"root"` for explicit SCP mode.
  - Optional Host Pinning: Set `$BIGIP_HOST` or `$BIGIQ_HOST` to bind environment credentials to a specific appliance IP/hostname.

- **Password & BIG-IQ Token Authentication**:
  - Environment variables:
    * BIG-IQ: `$BIGIQ_PASSWORD` (falls back to `$BIGIP_PASSWORD`).
    * BIG-IP: `$BIGIP_PASSWORD`.
  - **BIG-IQ Token Lifecycle & Lockout Protection**: On BIG-IQ, `myf5` authenticates via `POST /mgmt/shared/authn/login` with `{"username":"...","password":"..."}` (omitting `loginProviderName` per BIG-IQ REST API schema, with automatic fallback if required), refreshes tokens within 5 minutes of expiry via `POST /mgmt/shared/authn/exchange`, and stops immediately after 1 failed login attempt (`loginFailedErr`) to prevent appliance account lockout.
  - **Interactive Masked Terminal Prompt**:
    If no password environment variable is set and an interactive terminal (TTY) is detected, `myf5` securely prompts for the appliance password with terminal echo suppressed via `golang.org/x/term`:
    ```
    Enter BIG-IP password for admin@10.1.1.4: 
    ```
    In non-interactive environments (cron, headless CI/CD), if the password is not provided via environment variables or SSH keys, `myf5` halts execution with exit code `1` rather than hanging.
  - **F5 K2873 Character Policy & Shell Quoting**:
    Appliance passwords follow the **F5 K2873** specification: alphanumeric characters and all 32 allowed special characters (`!`, `@`, `#`, `$`, `%`, `^`, `&`, `*`, `(`, `)`, `` ` ``, `~`, `-`, `+`, `=`, `[`, `]`, `{`, `}`, `;`, `’`, `:`, `"`, `'`, `.`, `/`, `<`, `>`, `|`, `\`, `?`, `_`) are supported. Whitespace and control characters are prohibited. When exporting passwords containing shell-active special characters (such as `!`, `$`, `` ` ``, `\`, or `"`) in `bash` or `zsh`, wrap the value in **single quotes** to prevent shell expansion:
    ```bash
    export BIGIP_PASSWORD='MyP@ssw0rd!$`123'
    ```

- **SSH Key & Agent Authentication (SCP / SSH Methods)**:
  When using native Go SCP/SSH operations:
  1. **Explicit Private Key**: Specify `-i`/`--identity <path>` (appliance commands) or `--push-key <path>` (`downloads get --push`), or set `$BIGIP_SSH_KEY`, `$BIGIQ_SSH_KEY`, or `$SSH_KEY_PATH`. Encrypted SSH private keys with `-i` are supported via `SSH_AUTH_SOCK` (`ssh-agent`), as the CLI never prompts for an SSH key passphrase on the command line. Note that `--key` and `--ssh-passphrase` are **not** valid flags: `--key` is blocked by Tier 1 (`CheckNoCLISecrets`, exit code `2`) and `--ssh-passphrase` is blocked by Tier 2 (`rejectSecretFlag`, exit code `1`).
  2. **SSH Agent**: Connects automatically to `$SSH_AUTH_SOCK` if active.
  3. **Default Key Fallback**: Inspects standard user keys (`~/.ssh/id_ed25519`, `~/.ssh/id_rsa`, `~/.ssh/id_ecdsa`).
  4. **Password / PAM Fallback**: Uses password or interactive prompt if no keys are found.

- **SSH `known_hosts` Host-Key Verification**:
  - **Flags**: `--known-hosts <path>` (on `info`, `serial`, `qkview`, `push`, `pull`, `geoip`, and `bigip`/`bigiq` subcommands) and `--push-known-hosts <path>` (on `downloads get --push` and `downloads browse --push`).
  - **Environment Variables**: `BIGIP_KNOWN_HOSTS`, `BIGIQ_KNOWN_HOSTS`, `MYF5_SSH_KNOWN_HOSTS`.
  - **Strict Mode (`MYF5_STRICT_HOST_KEY_CHECKING=1` / `true` / `yes`)**: Enforces strict OpenSSH `known_hosts` verification before sending credentials—rejecting unknown, mismatched, and `@revoked` host keys.
  - **Default Mode (when strict mode is not set)**: Checks `~/.ssh/known_hosts` if present and rejects any key mismatch or `@revoked` marker before credentials are sent, while allowing unknown ephemeral lab hosts if no entry matches.

- **TLS Verification, PQC Hybrid Suites & Custom Root CA Bundles**:
  - **Cloud APIs**: Always enforce TLS 1.2–1.3 (`pkg/client.SecureTLSConfig` with PQC hybrid `X25519MLKEM768` and 256-bit AEAD cipher suites).
  - **Appliance iControl REST**: Defaults to `SkipTLSVerify: true` for self-signed management certificates unless `MYF5_STRICT_APPLIANCE_TLS=1` (`true`/`yes`) is set, or mTLS (`--cert` / `--cert-key`, `BIGIP_CERT` / `BIGIP_KEY`, `BIGIQ_CERT` / `BIGIQ_KEY`) is used.
  - **Custom Root CA Bundle (`MYF5_CA_BUNDLE` / `SSL_CERT_FILE`)**: Loads a custom PEM root CA bundle (e.g., corporate TLS inspection CA or private lab CA) into `SecureTLSConfig` across macOS, Linux, and Windows.

> [!IMPORTANT]
> **TMOS User Shell Requirements for SCP**: On BIG-IP TMOS, the `admin` user is configured with `tmsh` shell by default (`Terminal Access: tmsh`), which restricts SCP operations to whitelisted directories (`/var/tmp/`). To transfer files directly to `/shared/images/` or system directories via SCP, either authenticate as `root` (which defaults to `bash`), grant the user Advanced Shell (`bash`) terminal access (`tmsh modify auth user <username> shell bash`), or utilize `--method icontrol` which operates independent of the user's interactive shell.

---

## In-Memory Defense & Secret Redaction

1. **Stringer Redaction**: Go structs containing tokens or client secrets implement custom `fmt.Stringer` and `fmt.GoStringer` interfaces that redact sensitive strings to prevent leaks in stack traces or logs.
2. **Crash Dump Protection**: Error messages never format raw credential values into returned error strings.
3. **Memory Scrubbing**: Sensitive byte slices allocated during masked password entry are zeroed out after use.

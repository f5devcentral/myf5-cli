# Security & Vulnerability Policy

**Repository Classification:** F5 Proprietary & Confidential Internal Enterprise Tool  
**Author & Maintainer:** Greg Robinson (`g.robinson@f5.com`)  
**Permanent Policy:** This repository can **NEVER** be made public.

---

## 1. Internal Vulnerability Reporting & Disclosure

Because this repository is strictly internal and private to F5 Service Provider Sales Americas:

1. **DO NOT file public GitHub issues or public pull requests.**
2. Report any security vulnerabilities, memory safety concerns, or credential exposure vectors directly and confidentially to:
   - **Greg Robinson:** [g.robinson@f5.com](mailto:g.robinson@f5.com)
3. For enterprise-wide F5 infrastructure or public service vulnerabilities affecting upstream services, report to F5 PSIRT:
   - **Email:** [f5sirt@f5.com](mailto:f5sirt@f5.com) or [security@f5.com](mailto:security@f5.com)
   - **Security Portal:** [https://www.f5.com/company/policies/security-vulnerability-response-policy](https://www.f5.com/company/policies/security-vulnerability-response-policy)

---

## 2. Zero-Secret CLI Security Architecture

`myf5-api` strictly adheres to an enterprise **Zero-Secret CLI Policy** designed to eliminate credential leakage vectors in automated, multi-tenant, and interactive environments.

### Why Secret Flags Are Forbidden
Passing sensitive credentials (passwords, OAuth2 client secrets, private tokens) via command-line arguments creates severe security exposures:
- **Process Table Snooping (`ps aux`, `/proc/$PID/cmdline`):** On shared bastions, CI runners, and multi-tenant systems, any local user can view argument strings passed to running processes.
- **Shell History Leaks:** Plaintext secrets persist indefinitely in `~/.bash_history`, `~/.zsh_history`, and system syslog/audit logs.
- **CI/CD Console Logs:** Automated pipelines frequently print the exact command line invocation, exposing secrets to pipeline viewers.

### Enforcement
The CLI entry point strictly scans `os.Args` prior to command execution using `auth.CheckNoCLISecrets`:
- Any usage of flags such as `--password`, `-password`, `--client-secret`, `-client-secret`, `--pw`, `--secret` results in immediate termination with **Exit Code 2**.
- Output contains a descriptive security violation notice and zero-secret remediation guidance.

### Approved Credential Channels
Credentials must be supplied exclusively through secure channels:
1. **Protected File (Recommended for Local Dev):**
   - Store in `~/.f5api_credentials` with filesystem permissions set to `0600` (`chmod 600 ~/.f5api_credentials`).
   - Supports key-value pairs or multi-account `[profile]` INI blocks.
2. **Environment Variables (Recommended for CI/CD & Automation):**
   - `F5_CLIENT_ID`
   - `F5_CLIENT_SECRET`
   - `BIGIP_PASSWORD`
   - `BIGIQ_PASSWORD`
3. **SSH Key Identities & SSH Agent:**
   - Pre-authorized identity files (`-i / --identity ~/.ssh/id_ed25519`) or `SSH_AUTH_SOCK` agent forwarding.
4. **Interactive Masked Terminal Input:**
   - When managing BIG-IP/BIG-IQ appliances without environment variables, the CLI prompts interactively with terminal echo disabled (`term.ReadPassword`).

---

## 3. Cryptographic, Memory & Appliance Security

- **In-Memory Secret Scrubbing:** Sensitive byte slices from terminal password prompts and temporary cryptographic buffers are immediately overwritten with zeros via `auth.ZeroBytes` backed by `runtime.KeepAlive` to prevent compiler dead-store optimization and lingering heap persistence.
- **Appliance Shell & Path Traversal Neutralization:** Remote TMOS appliance paths are strictly sanitized with `ValidateRemoteAppliancePath` before invoking bash utility scripts or remote `scp` execution, blocking path traversal (`..`) and shell metacharacter injections.
- **Bounded HTTP Stream Safety:** All API JSON responses are constrained via `io.LimitReader` (1 MB for authentication and error diagnostics, 10 MB for metadata catalogs) to prevent memory exhaustion DoS attacks against the Go runtime.
- **Constant RAM Multi-GB Streaming:** Multi-gigabyte image downloads use streaming `io.TeeReader` pipelines to keep memory overhead bounded (< 10 MB RAM) regardless of artifact size.
- **TLS Transport Standard:** All outbound HTTPS connections enforce TLS 1.2 minimum with modern AEAD and Perfect Forward Secrecy (PFS) cipher suites (`ECDHE-ECDSA-AES128-GCM-SHA256`, `ECDHE-RSA-AES128-GCM-SHA256`, `ECDHE-ECDSA-AES256-GCM-SHA384`, `ECDHE-RSA-AES256-GCM-SHA384`, `CHACHA20-POLY1305`), along with socket-level `ResponseHeaderTimeout` and `ExpectContinueTimeout`.
- **Diagnostic Masking:** The `doctor` command and all diagnostic logs automatically mask sensitive token IDs and client secrets.

---

## 4. Supply Chain & Continuous Security Scanning

- **Automated Dependency Auditing:** GitHub Dependabot scans module dependencies weekly.
- **Vulnerability Scanning:** Automated CI runs `govulncheck` to detect known CVEs in the Go standard library and module dependencies.
- **Secret Scanning:** Repository commits and pull requests are validated using `gitleaks` to prevent accidental credential commits.
- **Static Analysis:** Automated CI runs `golangci-lint` (including `gosec`, `staticcheck`, `govet`, and `errcheck`).

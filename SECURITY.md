# Security & Vulnerability Policy

F5 takes the security and integrity of its software, infrastructure, and user credentials seriously. This document outlines the security policies, disclosure guidelines, and strict architectural standards implemented in `myf5-api`.

---

## 1. Vulnerability Reporting & Disclosure

If you discover a security vulnerability or potential credential exposure vulnerability within this repository:

1. **DO NOT file a public GitHub issue.** Public issues disclose vulnerabilities before mitigations can be deployed.
2. Report the vulnerability directly to the **F5 Product Security Incident Response Team (PSIRT)**:
   - **Email:** [f5sirt@f5.com](mailto:f5sirt@f5.com) or [security@f5.com](mailto:security@f5.com)
   - **Security Portal:** [https://www.f5.com/company/policies/security-vulnerability-response-policy](https://www.f5.com/company/policies/security-vulnerability-response-policy)
   - **PGP Encryption:** Please use the official F5 PSIRT PGP key available on the portal for encrypting sensitive disclosures.
3. For internal repository questions specific to `SalesAmerSP/myf5-api`, contact:
   - Greg Robinson: [g.robinson@f5.com](mailto:g.robinson@f5.com)

### Response SLA
- **Initial Acknowledgment:** Within 48 business hours.
- **Triage & Assessment:** Within 5 business days.
- **Coordinated Disclosure:** F5 coordinates fixes and releases before any disclosure is made.

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
3. **Interactive Masked Terminal Input:**
   - When managing BIG-IP TMOS appliances without environment variables, the CLI prompts interactively with terminal echo disabled (`term.ReadPassword`).

---

## 3. Cryptographic & Memory Security

- **TLS Transport Standard:** All outbound HTTPS connections enforce TLS 1.2 minimum with modern AEAD and Perfect Forward Secrecy (PFS) cipher suites (`ECDHE-ECDSA-AES128-GCM-SHA256`, `ECDHE-RSA-AES128-GCM-SHA256`, `ECDHE-ECDSA-AES256-GCM-SHA384`, `ECDHE-RSA-AES256-GCM-SHA384`, `CHACHA20-POLY1305`).
- **Memory Streaming:** Multi-gigabyte image downloads use streaming `io.TeeReader` pipelines to keep memory overhead bounded (< 10 MB RAM), preventing heap buffer overflows or denial-of-service through memory exhaustion.
- **Diagnostic Masking:** The `doctor` command and all diagnostic logs automatically mask sensitive token IDs and client secrets.

---

## 4. Supply Chain & Continuous Security Scanning

- **Automated Dependency Auditing:** GitHub Dependabot scans module dependencies weekly.
- **Vulnerability Scanning:** Automated CI runs `govulncheck` to detect known CVEs in the Go standard library and module dependencies.
- **Secret Scanning:** Repository commits and pull requests are validated using `gitleaks` to prevent accidental credential commits.

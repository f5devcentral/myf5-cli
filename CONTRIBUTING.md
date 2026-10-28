# Contributing to `myf5-cli`

Thank you for your interest in contributing to `myf5-cli`! This document outlines development environment setup, coding standards, security requirements, testing procedures, and the pull request workflow.

By submitting a contribution to this repository, you agree that your contribution is licensed under the [Apache License, Version 2.0](LICENSE).

---

## 1. Development Environment Setup

### Prerequisites
- **Go:** 1.26 or newer, as required by `go.mod` (recommended: latest stable)
- **Make:** GNU Make
- **Git:** 2.30+
- **Linter:** `golangci-lint` (v1.60+)
- **Vuln Scanner:** `govulncheck` (`go install golang.org/x/vuln/cmd/govulncheck@latest`)

### Clone and Verify
```bash
git clone https://github.com/f5devcentral/myf5-cli.git
cd myf5-cli
make all
```

---

## 2. Architectural Guidelines & Standards

### Zero-Secret CLI Mandate
Under no circumstances should any new CLI command or flag accept raw secrets (passwords, tokens, API secrets) as command line arguments.
- Always retain `auth.CheckNoCLISecrets(os.Args[1:])` in `cmd/myf5/main.go`.
- Resolve credentials using `auth.ResolveCredentials(profile)`.
- Never print unmasked secrets in log messages, debug output, or error wraps.

### Zero External Runtime Dependencies
`myf5-cli` is designed as a standalone static binary with minimal dependencies:
- Rely on Go standard library (`net/http`, `crypto/tls`, `crypto/sha256`, `io`, etc.) wherever possible.
- Any addition to `go.mod` must be justified for security, auditability, and binary size.

### Code Quality & Formatting
- **Code Style:** All Go code must be formatted using `gofmt -s -w .`.
- **Static Analysis:** Code must pass `go vet ./...` without warnings.
- **Error Handling:** Errors must be wrapped with contextual information using `fmt.Errorf("action failed: %w", err)`.
- **IPv6 Compliance:** Always use `net.JoinHostPort(host, port)` instead of string formatting (`"%s:%d"`) for network dials.

---

## 3. Testing Requirements

All contributions must include comprehensive unit tests:
```bash
# Run unit tests with race detection
make test

# Generate HTML coverage report
make test-coverage
```
- Tests must pass the race detector (`-race`).
- Mock external network calls using `net/http/httptest.Server`.
- Do not commit actual credentials, real private tokens, or live customer case IDs in test data.

---

## 4. Pull Request & Commit Workflow

### Branching Convention
- `feat/feature-name` for new capabilities
- `fix/issue-description` for bug fixes
- `sec/security-fix` for security patches
- `docs/documentation-update` for documentation

### Commit Message Guidelines (Conventional Commits)
```text
feat(downloads): add resume support for interrupted downloads
fix(doctor): use net.JoinHostPort for IPv6 dial compatibility
docs(readme): update security badges and cross-compilation matrix
ci(actions): add windows-latest to CI matrix
```

### Pull Request Checklist
1. Create a feature branch from `main`.
2. Ensure `make all` passes locally (format, vet, lint, test, build).
3. Open a Pull Request against `f5devcentral/myf5-cli:main`.
4. Ensure all CI workflow checks pass.
5. Obtain approval from repository code owners (`@grobf5`).

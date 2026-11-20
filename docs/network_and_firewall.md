# Network, DNS & Firewall Architecture

This document outlines the network egress requirements, DNS endpoints, IP allowlisting references, and outbound proxy configuration for integrations utilizing `myf5-cli`.

---

## 1. Required Outbound Firewall Rules (TCP Port 443)

Enterprise jump boxes, CI/CD runners, and edge orchestrators running `myf5` require outbound HTTPS (TCP 443) to the following F5 Cloud FQDNs:

| FQDN | Service Role | Notes & Endpoints |
| :--- | :--- | :--- |
| `idp.identity.f5.com` | F5 Identity / OAuth2 | Primary Auth0 token issuer (`POST /oauth/token`) |
| `identity.account.f5.com` | F5 Identity / Okta CIAM | Dedicated CIAM authorization server (`POST /oauth2/ausp95yxb4ShxMGvW357/v1/token`, audience `api://ciam-api-myf5` with `scope=myf5_scope` or `api://ciam-api-ihealth` with empty `scope=""`) |
| `api.software.downloads.f5.com` | MyF5 Downloads API | F5 software downloads catalog and signed CDN link generation (`/v1/downloads-management/v1/downloads/...`) |
| `*.cloudfront.net` | AWS CloudFront CDN | Signed regional CDN endpoints (`USA - EAST COAST`, `IRELAND`) delivering binary software images (`.iso`, `.qcow2`, `.ova`, `.zip`) returned by `api.software.downloads.f5.com` |
| `support.apis.f5.com` | MyF5 Support Case API | Support case management (`/case-management/v1/cases`, `?k=<APIKey>`) |
| `ihealth2-api.f5.com` | F5 iHealth API | QKView upload & analysis (`/qkview-analyzer/api/qkviews`, `Accept: application/vnd.f5.ihealth.api+json`) |
| `ihealth-api.f5.com` | F5 iHealth API (alternate) | Primary iHealth API gateway; [K15202](https://my.f5.com/manage/s/article/K15202) lists both iHealth hosts at `185.56.152.6` |
| `callhome.f5.com` | F5 Callhome Telemetry | Used by BIG-IP and BIG-IQ appliances for automated telemetry and signature updates |
| `api.f5.com` | F5 Cloud Services Gateway | Cloud telemetry and license usage reporting |

### Appliance Management Firewall Ports (Internal Egress)
When orchestrating file transfers, QKView generation, or health checks against internal BIG-IP and BIG-IQ appliances:
- **TCP 443 (HTTPS)**: iControl REST API chunked transfers (`512 KiB` upload chunks to `/mgmt/shared/file-transfer/uploads/` or BIG-IQ `/mgmt/cm/autodeploy/software-image-uploads/`; `1 MiB` `Range` or single-response `200 OK` downloads), readiness probes, and bash promotion utility.
- **TCP 22 (SSH)**: Verified native Go SCP streaming transfers (`64 KiB` chunks via `scp -t` / `scp -f`), pre-authentication `known_hosts` host-key verification, BIG-IQ QKView fallback, and remote CLI diagnostics.

You can verify reachability to all 8 audited F5 cloud endpoints using:
```bash
./bin/myf5 doctor
```

---

## 2. Cloud Service Endpoints & IP Allowlisting

Per F5 knowledge base articles **[K15202](https://my.f5.com/manage/s/article/K15202)** (*IP addresses for F5 hosted services*) and **[K000163521](https://my.f5.com/manage/s/article/K000163521)**, F5 cloud endpoints resolve via F5 Distributed Cloud (`ves.io`) to the following virtual IP addresses:

| Hostname | Published IP Address | Services Covered |
| :--- | :--- | :--- |
| `api.f5.com` | `185.56.152.6` | BIG-IP Automatic Phone Home, iHealth uploads, BIG-IQ license usage reporting |
| `ihealth-api.f5.com` / `ihealth2-api.f5.com` | `185.56.152.6` | F5 iHealth QKView upload and diagnostic analysis API |
| `callhome.f5.com` | `159.60.134.92` | BIG-IP ASM attack signature updates, Automatic Update Check, FPS signature updates |
| `api.software.downloads.f5.com` | `159.60.134.92` | MyF5 Software Downloads API catalog and metadata |
| `*.cloudfront.net` | *Dynamic AWS CloudFront IPs* | Signed binary image delivery for `myf5 downloads get` / `myf5 downloads browse` |

### Firewall Policy Recommendations
- **Prefer FQDN-Based Rules**: F5 recommends configuring outbound firewall policies by hostname (FQDN) rather than static IP addresses wherever supported, as cloud-hosted services and regional CDN mirrors (including Amazon CloudFront `*.cloudfront.net` distributions used for binary image delivery) may rotate underlying IP addresses.
- **Static IP Allowlists**: In environments that strictly require destination IP allowlisting or static DNS entries, consult [K15202](https://my.f5.com/manage/s/article/K15202) and [K000163521](https://my.f5.com/manage/s/article/K000163521) to keep firewall rules aligned with current F5 Distributed Cloud ingress addresses.

---

## 3. TLS 1.2–1.3, Custom Root CA Bundles & SSH `known_hosts` Controls

### Cloud & Appliance TLS Configuration
- **Cloud APIs (`pkg/client.SecureTLSConfig`)**: Always enforce TLS 1.2–1.3 with post-quantum hybrid key exchange (`X25519MLKEM768`) and 256-bit AEAD cipher suites. Certificate verification is never disabled for cloud endpoints.
- **Appliance iControl REST (`MYF5_STRICT_APPLIANCE_TLS`)**: Appliance iControl REST clients default to `SkipTLSVerify: true` to accommodate default self-signed BIG-IP / BIG-IQ management certificates unless `MYF5_STRICT_APPLIANCE_TLS=1` (`true`/`yes`) is set, or mTLS (`--cert` / `--cert-key`, `BIGIP_CERT` / `BIGIP_KEY`, `BIGIQ_CERT` / `BIGIQ_KEY`) is configured.
- **Custom PEM Root CA Bundles (`MYF5_CA_BUNDLE` & `SSL_CERT_FILE`)**: In environments using TLS-inspecting forward proxies or private enterprise PKI for BIG-IP / BIG-IQ appliances, set `MYF5_CA_BUNDLE` or `SSL_CERT_FILE` to load a custom PEM root CA bundle into `SecureTLSConfig` across macOS, Linux, and Windows:
  ```bash
  export MYF5_CA_BUNDLE="/etc/ssl/certs/corp-ca-bundle.pem"
  export MYF5_STRICT_APPLIANCE_TLS=1
  ```

### SSH Host-Key Verification (`known_hosts`)
- **CLI Flags**: `--known-hosts <path>` (on `info`, `serial`, `qkview`, `push`, `pull`, `geoip`, and `bigip`/`bigiq` subcommands) and `--push-known-hosts <path>` (on `downloads get --push` and `downloads browse --push`).
- **Environment Variables**: `BIGIP_KNOWN_HOSTS`, `BIGIQ_KNOWN_HOSTS`, `MYF5_SSH_KNOWN_HOSTS`.
- **Strict Host-Key Checking (`MYF5_STRICT_HOST_KEY_CHECKING=1` / `true` / `yes`)**: Enforces strict OpenSSH `known_hosts` verification before sending credentials—rejecting unknown, mismatched, and `@revoked` host keys.
- **Default Mode (when strict mode is not set)**: Checks `~/.ssh/known_hosts` if present and rejects any key mismatch or `@revoked` marker before credentials are sent, while allowing unknown ephemeral lab hosts if no entry matches.

---

## 4. Outbound Proxy Architecture (HTTP, HTTPS, SOCKS5)

In air-gapped data centers, DMZs, or secured corporate networks, direct Internet egress to F5 Cloud endpoints may be blocked. `myf5-cli` provides first-class support for routing all outbound traffic through forward proxies.

### Supported Protocols & Schemes

| Scheme | Description | Authentication | Remote DNS |
| :--- | :--- | :--- | :--- |
| `http://` | HTTP CONNECT forward proxy (e.g. Squid, NGINX) | Basic (`user:pass@host:port`) | Resolver-dependent |
| `https://` | Secure TLS forward proxy | Basic (`user:pass@host:port`) | Resolver-dependent |
| `socks5://` | SOCKS5 proxy (RFC 1928) | RFC 1929 username/password | Local DNS resolution |
| `socks5h://` | SOCKS5 proxy with remote DNS | RFC 1929 username/password | Remote proxy DNS resolution |

### Proxy Configuration Methods

`myf5` resolves proxy configuration using the following precedence hierarchy:

1. **CLI Flag (`--proxy <url>`)**: Highest priority; sets proxy for a single command invocation.
   ```bash
   myf5 --proxy socks5://10.99.1.50:1080 doctor
   myf5 --proxy http://proxy.corp.internal:8080 downloads families
   ```

2. **Process/Session Configuration (`myf5 proxy set`)**: Configures proxy for the current process only (intended for `myf5 interactive`). To configure a proxy for commands across your shell session, set `MYF5_PROXY` or pass `--proxy <url>` with each command.
   ```bash
   export MYF5_PROXY=socks5://127.0.0.1:1080
   myf5 proxy show
   ```

3. **Environment Variables**:
   - `MYF5_PROXY` / `F5_PROXY`: Tool-specific proxy overrides.
   - `ALL_PROXY`: Standard universal proxy specification (commonly used for SOCKS5).
   - `HTTPS_PROXY` / `HTTP_PROXY`: Standard protocol-specific environment variables.

### Proxy Bypass (`NO_PROXY`)

Traffic targeting local or internal appliances can bypass the configured proxy by setting `NO_PROXY` (or `no_proxy`):
```bash
export NO_PROXY="localhost,127.0.0.1,10.0.0.0/8,192.168.0.0/16,.corp.internal"
```
The bypass matcher supports:
- Exact IP addresses and IPv6 addresses (with automatic zone identifier stripping).
- CIDR network blocks (e.g. `10.0.0.0/8`, `172.16.0.0/12`).
- Domain suffixes (e.g. `.internal`, `f5.com`).
- Wildcards (`*`).


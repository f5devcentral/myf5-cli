# Network, DNS & Firewall Architecture

This document outlines the network egress requirements, DNS endpoints, IP allowlisting references, and outbound proxy configuration for integrations utilizing `myf5-cli`.

---

## 1. Required Outbound Firewall Rules (TCP Port 443)

Enterprise jump boxes, CI/CD runners, and edge orchestrators running `myf5` require outbound HTTPS (TCP 443) to the following F5 Cloud FQDNs:

| FQDN | Service Role | Notes & Endpoints |
| :--- | :--- | :--- |
| `idp.identity.f5.com` | F5 Identity / OAuth2 | Primary Auth0 token issuer (`POST /oauth/token`) |
| `identity.account.f5.com` | F5 Identity / Okta CIAM | Dedicated CIAM authorization server (`POST /oauth2/ausp95yxb4ShxMGvW357/v1/token`, audience `api://ciam-api-myf5`, scope `myf5_scope`) |
| `api.software.downloads.f5.com` | MyF5 Downloads API | F5 software downloads catalog and signed CDN link generation (`/v1/downloads-management/v1/downloads/...`) |
| `support.apis.f5.com` | MyF5 Support Case API | Support case management (`/case-management/v1/cases`) |
| `ihealth2-api.f5.com` | F5 iHealth API | QKView upload & analysis (`/qkview-analyzer/api/qkviews`, `Accept: application/vnd.f5.ihealth.api+json`) |
| `ihealth-api.f5.com` | F5 iHealth API (alternate) | Primary iHealth API gateway; [K15202](https://my.f5.com/manage/s/article/K15202) lists both iHealth hosts at `185.56.152.6` |
| `callhome.f5.com` | F5 Callhome Telemetry | Used by BIG-IP and BIG-IQ appliances for automated telemetry and signature updates |
| `api.f5.com` | F5 Cloud Services Gateway | Cloud telemetry and license usage reporting |

### Appliance Management Firewall Ports (Internal Egress)
When orchestrating file transfers, QKView generation, or health checks against internal appliances:
- **TCP 443 (HTTPS)**: iControl REST API chunked transfers (`/mgmt/shared/file-transfer/uploads/`), autodeploy downloads, and bash promotion utility.
- **TCP 22 (SSH)**: Native Go SCP streaming transfers (`scp -t` / `scp -f`) and remote CLI diagnostics.

You can verify reachability to all required cloud endpoints using:
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

### Firewall Policy Recommendations
- **Prefer FQDN-Based Rules**: F5 recommends configuring outbound firewall policies by hostname (FQDN) rather than static IP addresses wherever supported, as cloud-hosted services and regional CDN mirrors (such as Amazon CloudFront distributions used for binary image delivery) may rotate underlying IP addresses.
- **Static IP Allowlists**: In environments that strictly require destination IP allowlisting or static DNS entries, consult [K15202](https://my.f5.com/manage/s/article/K15202) and [K000163521](https://my.f5.com/manage/s/article/K000163521) to keep firewall rules aligned with current F5 Distributed Cloud ingress addresses.

---

## 3. Outbound Proxy Architecture (HTTP, HTTPS, SOCKS5)

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

2. **Persistent Configuration (`myf5 proxy set`)**: Writes default proxy to `~/.f5api_credentials` or config.
   ```bash
   myf5 proxy set socks5://127.0.0.1:1080
   myf5 proxy show
   myf5 proxy clear
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


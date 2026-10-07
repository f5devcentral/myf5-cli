# Network, DNS & Firewall Architecture

This document outlines the network egress requirements, DNS endpoints, and upcoming infrastructure changes for integrations utilizing the `myf5-cli` client.

---

## 1. Required Outbound Firewall Rules (TCP Port 443)

Enterprise jump boxes, CI/CD runners, and edge orchestrators running `myf5` require outbound HTTPS (TCP 443) to the following F5 Cloud FQDNs:

| FQDN | Service Role | Notes & Endpoints |
| :--- | :--- | :--- |
| `idp.identity.f5.com` | F5 Identity / OAuth2 | Primary Auth0 token issuer (`POST /oauth/token`) |
| `identity.account.f5.com` | F5 Identity / Okta CIAM | Dedicated CIAM authorization server (`POST /oauth2/ausp95yxb4ShxMGvW357/v1/token`, audience `api://ciam-api-myf5`, scope `myf5_scope`) |
| `api.software.downloads.f5.com` | MyF5 Downloads API | Listed in K15202 as "F5 software downloads" (`159.60.134.92`). Targets gateway prefix `/v1/downloads-management/v1/...` without `k` parameter (operational). |
| `support.apis.f5.com` | MyF5 Support Case API | Support case management (`/case-management/v1/cases`) |
| `ihealth2-api.f5.com` | F5 iHealth API | QKView upload & analysis (`/qkview-analyzer/api/qkviews`, `Accept: application/vnd.f5.ihealth.api`). The host used in K000130498's worked example and the client default |
| `ihealth-api.f5.com` | F5 iHealth API (alternate) | Same service; K15202 lists both at `185.56.152.6` |
| `callhome.f5.com` | F5 Callhome Telemetry | Used by BIG-IP and BIG-IQ appliances for automated telemetry |
| `api.f5.com` | F5 Cloud Services Gateway | Cloud telemetry and license reporting |

### Appliance Management Firewall Ports (Internal Egress)
When orchestrating file transfers, QKView generation, or health checks against internal appliances:
- **TCP 443 (HTTPS)**: iControl REST API chunked transfers (`/mgmt/shared/file-transfer/uploads/`), autodeploy downloads, and bash promotion utility.
- **TCP 22 (SSH)**: Native Go SCP streaming transfers (`scp -t` / `scp -f`) and remote CLI diagnostics.

You can verify reachability to all required cloud endpoints using:
```bash
./bin/myf5 doctor
```

---

## 2. Infrastructure Announcements & Cutover Notices

### A. MyF5 Downloads API Base URL Cutover (October 2, 2026) — UNSOURCED

> [!CAUTION]
> **No F5 bulletin supporting this notice has been located.** The text below has been carried in this repository without attribution — no article ID, URL, or archive link. The F5 articles actually published on 2026-10-02 are [K000163521](https://my.f5.com/manage/s/article/K000163521) and [K15202](https://my.f5.com/manage/s/article/K15202): both describe an **IP address change** for `api.f5.com` and `callhome.f5.com` (see §C) and neither mentions Downloads base URLs or the `k` parameter. K15202 does list `api.software.downloads.f5.com` as a live service, so the host is real — but that is the only part of this notice the sources support.
>
> Separately: this section previously recorded that `api.software.downloads.f5.com` returned `404` with an empty body on every path tried, and concluded the notice did not produce a working client. **That conclusion was wrong.** The `404`s were caused by this client omitting the gateway's leading `/v1` path segment; with the correct prefix `/v1/downloads-management/v1/downloads/...` the host returns `200` (verified live 2026-10-05). The host and the absent `k` are therefore both corroborated by observation — it is only the *bulletin* that remains uncited.

> **Action Required** (unverified): The MyF5 Downloads API base URL changes October 2. Update your integrations to use `https://api.software.downloads.f5.com` and remove the `"k"` value from API calls to help prevent service disruptions.

- **Implementation in `myf5-cli`**:
  - `myf5-cli` targets `https://api.software.downloads.f5.com` as its primary endpoint by default.
  - The `k` query parameter is not sent to this host; it does not accept one.
  - All paths carry the gateway's doubled `v1` prefix: `/v1/downloads-management/v1/downloads/...`. The leading segment is the gateway's route version, the inner one the API's. Omitting it returns an empty-bodied `404`.
  - If `api.software.downloads.f5.com` returns HTTP 403 or 404, `myf5-cli` falls back to `support.apis.f5.com` with rate-limit retry backoff. This fallback has never been observed succeeding, and cannot rescue a wrong path.

### B. XC DNS & Traffic Cutover for `callhome.f5.com` and `api.f5.com` (September 25, 2026)
> On Friday, September 25, starting at 9 AM PT for 2 hours, the following changes will be made to `callhome.f5.com` and `api.f5.com`:
> - Make DNS Change for `callhome.f5.com` and `api.f5.com` pointing to XC.
> - Disable the wide IPs for `callhome` and `api.f5.com` making cutover to XC Active.
> - Device testing of `callhome.f5.com` and `api.f5.com` to observe production traffic on the new ESDP systems.
> - Live traffic directed to new ESDP for 1 hour (9 - 10 AM PT).
> - Services remained available during testing.

### C. IP Address Change for `api.f5.com` and `callhome.f5.com` (October 2, 2026)

Source: **[K000163521](https://my.f5.com/manage/s/article/K000163521)** and **[K15202](https://my.f5.com/manage/s/article/K15202)**, both updated 2026-10-02. Unlike §A, this one is attributable.

Effective October 2, 2026:

| Hostname | New IP address | Services affected |
| :--- | :--- | :--- |
| `api.f5.com` | `185.56.152.6` | BIG-IP Automatic Phone Home, iHealth uploads, BIG-IQ license usage reporting |
| `callhome.f5.com` | `159.60.134.92` | BIG-IP ASM attack signature updates, Automatic Update Check, FPS signature updates |

This is a **firewall and allowlist concern, not an API change** — request shape, base URLs, and parameters are unaffected. It matters for environments that restrict outbound HTTPS by destination IP, or that hold stale DNS caches or static host entries.

Verified by resolution on 2026-10-02:

```
api.f5.com                     → 185.56.152.6    (ves-io-...ac.vh.ves.io)
callhome.f5.com                → 159.60.134.92   (ves-io-...ac.vh.ves.io)
api.software.downloads.f5.com  → 159.60.134.92   (ves-io-...ac.vh.ves.io)
```

All three resolve onto F5 Distributed Cloud (`ves.io`). K15202 lists `api.software.downloads.f5.com` at `159.60.134.92` as a published service for F5 software downloads.

F5's guidance is to prefer hostname-based firewall rules; where fixed-IP rules are required, validate them against K15202 periodically, since F5 notes hosted services are not bound to a single address.

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

### Automated Lab Testing (`testenv/`)

An isolated OpenTofu/Terraform testing environment is available in the gitignored `testenv/` directory to stand up:
- An **NGINX HTTP forward proxy** (port `8080`) and **Dante SOCKS5 proxy** (port `1080`).
- A tiny **BIG-IP VE 17.5.x BYOL** appliance for end-to-end proxy and appliance push/pull verification.
- Enforces strict security: locked strictly to caller IP (`/32`), never opens `0.0.0.0/0`, and includes dual 4-hour automatic power-off watchdogs (in-guest cron and AWS CloudWatch alarms).


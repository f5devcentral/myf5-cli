# Network, DNS & Firewall Architecture

This document outlines the network egress requirements, DNS endpoints, and upcoming infrastructure changes for integrations utilizing the `myf5-cli` client.

---

## 1. Required Outbound Firewall Rules (TCP Port 443)

Enterprise jump boxes, CI/CD runners, and edge orchestrators running `myf5` require outbound HTTPS (TCP 443) to the following F5 Cloud FQDNs:

| FQDN | Service Role | Notes & Endpoints |
| :--- | :--- | :--- |
| `idp.identity.f5.com` | F5 Identity / OAuth2 | Primary Auth0 token issuer (`POST /oauth/token`) |
| `identity.account.f5.com` | F5 Identity / Okta CIAM | Dedicated CIAM authorization server (`POST /oauth2/ausp95yxb4ShxMGvW357/v1/token`, audience `api://ciam-api-myf5`, scope `myf5_scope`) |
| `api.software.downloads.f5.com` | MyF5 Downloads API | Listed in K15202 as "F5 software downloads" (`159.60.134.92`). **Currently returns 404 on every path** — see §2A |
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

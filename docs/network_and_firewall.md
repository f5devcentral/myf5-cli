# Network, DNS & Firewall Architecture

This document outlines the network egress requirements, DNS endpoints, and upcoming infrastructure changes for integrations utilizing the `myf5-api` client.

---

## 1. Required Outbound Firewall Rules (TCP Port 443)

Enterprise jump boxes, CI/CD runners, and edge orchestrators running `myf5` require outbound HTTPS (TCP 443) to the following F5 Cloud FQDNs:

| FQDN | Service Role | Notes & Endpoints |
| :--- | :--- | :--- |
| `idp.identity.f5.com` | F5 Identity / OAuth2 | Primary Auth0 token issuer (`POST /oauth/token`) |
| `identity.account.f5.com` | F5 Identity / Okta CIAM | Dedicated CIAM authorization server (`POST /oauth2/ausp95yxb4ShxMGvW357/v1/token`, audience `api://ciam-api-myf5`, scope `myf5_scope`) |
| `api.software.downloads.f5.com` | MyF5 Downloads API | Authoritative API base URL (No `k` parameter, October 2 cutover) |
| `support.apis.f5.com` | MyF5 Support Case API | Support case management (`/case-management/v1/cases`, `/support/case-management/v1/cases`) |
| `ihealth-api.f5.com` | F5 iHealth API (Primary) | Authoritative diagnostic QKView upload & analysis (`/qkview-analyzer/api/v1/qkviews`) |
| `ihealth2-api.f5.com` | F5 iHealth API (Alternate) | Alternate / legacy diagnostic QKView gateway |
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

### A. MyF5 Downloads API Base URL Cutover (October 2, 2026)
> **Action Required**: The MyF5 Downloads API base URL changes October 2. Update your integrations to use `https://api.software.downloads.f5.com` and remove the `"k"` value from API calls to help prevent service disruptions.

- **Implementation in `myf5-api`**:
  - `myf5-api` targets `https://api.software.downloads.f5.com` as its primary endpoint by default.
  - The legacy `k` query parameter has been completely removed from Downloads API calls.
  - Prior to the cutover, if `api.software.downloads.f5.com` returns HTTP 403 or 404 (CloudFront distribution provisioning), `myf5-api` automatically and transparently falls back to `support.apis.f5.com` with rate-limit retry backoff.

### B. XC DNS & Traffic Cutover for `callhome.f5.com` and `api.f5.com` (September 25, 2026)
> On Friday, September 25, starting at 9 AM PT for 2 hours, the following changes will be made to `callhome.f5.com` and `api.f5.com`:
> - Make DNS Change for `callhome.f5.com` and `api.f5.com` pointing to XC.
> - Disable the wide IPs for `callhome` and `api.f5.com` making cutover to XC Active.
> - Device testing of `callhome.f5.com` and `api.f5.com` to observe production traffic on the new ESDP systems.
> - Live traffic directed to new ESDP for 1 hour (9 - 10 AM PT).
> - Services remained available during testing.

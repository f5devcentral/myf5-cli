# Network, DNS & Firewall Architecture

This document outlines the network egress requirements, DNS endpoints, and upcoming infrastructure changes for integrations utilizing the `myf5-api` client.

---

## 1. Required Outbound Firewall Rules (TCP Port 443)

Enterprise jump boxes, CI/CD runners, and edge orchestrators running `myf5` require outbound HTTPS (TCP 443) to the following F5 Cloud FQDNs:

| FQDN | Service Role | Notes |
| :--- | :--- | :--- |
| `idp.identity.f5.com` | F5 Identity / OAuth2 | Primary Auth0 token issuer (`/oauth/token`) |
| `identity.account.f5.com` | F5 Identity / Okta | Alternate identity provider issuer |
| `api.software.downloads.f5.com` | MyF5 Downloads API | Authoritative API base URL (No `k` parameter) |
| `support.apis.f5.com` | MyF5 Support Case API | Case creation, metadata, comments |
| `ihealth2-api.f5.com` | F5 iHealth API | Diagnostic QKView upload & analysis |
| `callhome.f5.com` | F5 Callhome Telemetry | Used by BIG-IP and BIG-IQ appliances |

You can verify reachability to all required endpoints using:
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

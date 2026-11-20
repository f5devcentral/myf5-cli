# MyF5 Software Downloads & GeoDB Automation Guide

The MyF5 Software Downloads engine provides programmatic access to the authoritative F5 software catalog and artifact delivery network.

---

## Catalog Architecture & Hierarchy

The MyF5 software catalog is structured hierarchically across five tiers:

```
Product Family (e.g., BIG-IP, NGINX, BIG-IQ)
  └── Product Line (e.g., big-ip_v17.x, big-ip_v16.x)
        └── Product Version (e.g., 17.1.1, 16.1.4)
              └── Container / Category (e.g., Virtual Edition, GeoLocationUpdates, Hotfix)
                    └── Download File (e.g., BIGIP-17.1.1-0.0.6.ALL.qcow2.tar.gz)
```

### Container Categories
`myf5` automatically tags and categorizes download containers based on artifact purpose:
- **Base ISO / Virtual Edition (`VE`)**: Full appliance installation images (`.iso`, `.ova`, `.qcow2`, `.vhd`).
- **GeoLocation Updates (`GEODB`)**: Digital Envoy NetAcuity GeoIP packages (Edge and Pulse variants, AskF5 K11176 / K78974041) for BIG-IP AFM, ASM/Advanced WAF, APM, and DNS.
- **Hotfix (`HOTFIX`)**: Maintenance patches and security point releases.
- **Supplemental (`SUPPLEMENTAL`)**: MIB files, signature packages, and utilities.

---

## Automated GeoDB Download Workflow

BIG-IP systems rely on regular updates to their GeoLocation databases for geo-blocking, traffic policy enforcement, and client localization. `myf5` enables headless, scheduled automation of these downloads.

### 1. Identify Latest GeoDB Container & File
```bash
# Query the available containers under TMOS v17.x
myf5 downloads containers BIG-IP big-ip_v17.x 17.1.1

# List files in the GeoLocationUpdates container
myf5 downloads files BIG-IP big-ip_v17.x 17.1.1 "GeoLocationUpdates"
```

### 2. Automated Download Script (Cron / CI/CD)

The following headless script can be executed as a weekly cron job or CI/CD task to retrieve the latest GeoDB package:

```bash
#!/usr/bin/env bash
set -euo pipefail

# Ensure credentials are in environment or ~/.f5api_credentials
export F5_CLIENT_ID="${F5_CLIENT_ID:-}"
export F5_CLIENT_SECRET="${F5_CLIENT_SECRET:-}"

TARGET_DIR="/var/downloads/geodb"
mkdir -p "${TARGET_DIR}"

echo "==> Fetching latest GeoLocation update package..."
# The container zip archive bundles Digital Envoy RPM components (Region, ISP, Org)
myf5 downloads get BIG-IP big-ip_v17.x 17.1.1 \
    "GeoLocationUpdates" \
    "ip-geolocation-default.zip" \
    -o "${TARGET_DIR}/ip-geolocation-default.zip"

echo "==> GeoDB downloaded and verified: ${TARGET_DIR}/ip-geolocation-default.zip"
# Note: On the appliance, unpack the archive in /shared/tmp and apply RPMs using geoip_update_data -f
```

---

## End-to-End Download & Appliance Push in One Step

Instead of executing download, checksum verification, and appliance file transfer as separate decoupled steps, `myf5 downloads get` natively supports post-download appliance push via `--push <host>`.

### Cryptographic Integrity Guarantee
When `--push` is specified:
1. The payload streams from the official MyF5 CDN mirror to local disk with constant memory footprint (< 10 MB RAM).
2. The SHA-256 hash is computed in real time via `io.TeeReader`.
3. The downloaded image is validated against the authoritative SHA-256 checksum from the MyF5 catalog.
4. **Only upon successful cryptographic validation**, `myf5` initiates the push to the target appliance. If the download checksum fails or is interrupted, the appliance transfer is aborted immediately.

```
MyF5 CDN ---> io.TeeReader ---> SHA-256 Checksum Verification
                  │                          │
                  ▼                          ▼ [Checksum Valid]
          Local Disk Image        ------------------------> Stream to Appliance
                                                             (iControl REST or SCP)
```

### Push Options on `myf5 downloads get`

| Flag | Default | Description |
| :--- | :--- | :--- |
| `--push <host>` | *Required for push* | Target BIG-IP or BIG-IQ appliance IP, hostname, or URL. |
| `--push-method <method>` | `auto` | Transfer protocol: `auto`, `icontrol` (`512 KiB` HTTPS chunks), or `scp` (`64 KiB` SSH chunks). |
| `--push-dest <path>` | `/shared/images` or `/var/tmp` | Remote directory on appliance (e.g., `/shared/images` or `/var/tmp`). |
| `--push-target <target>` | `BIG-IP` | Appliance type: `auto`, `bigip`, or `bigiq` (accepts `"BIG-IQ"`, `"big-iq"`, `"bigiq"`, `"BIG-IP"`, `"big-ip"`, `"bigip"`). |
| `--push-user <user>` | `$BIGIP_USER` / `"admin"` | Appliance administrative user. |
| `--push-port <port>` | `443` (iControl) / `22` (SCP) | Appliance management port. |
| `--push-key <key>` | *Auto-detected* | SSH private key path for SCP authentication (encrypted keys supported via `SSH_AUTH_SOCK` `ssh-agent`). |
| `--push-known-hosts <path>` | `~/.ssh/known_hosts` | OpenSSH `known_hosts` file for host-key verification before sending credentials (`BIGIP_KNOWN_HOSTS`, `BIGIQ_KNOWN_HOSTS`, `MYF5_SSH_KNOWN_HOSTS`). |
| `--push-cert <path>` / `--push-cert-key <path>` | *Optional* | TLS client certificate and private key for mTLS (`BIGIP_CERT` / `BIGIP_KEY`, `BIGIQ_CERT` / `BIGIQ_KEY`). |
| `--apply-geoip` | `false` | **BIG-IP only:** Stage `.zip` to `/shared/tmp`, unpack `.rpm` files into `/shared/GeoIP` via `rpm2cpio \| cpio -idmv`, and run `tmsh load sys geoip` (per K11176). Rejected on BIG-IQ. |
| `--keep-extracted` | `false` | Retain extracted GeoIP staging files on the BIG-IP appliance after installation. |

### Practical Examples

#### 1. Download BIG-IP ISO and Push Directly to `/shared/images`
```bash
myf5 downloads get BIG-IP big-ip_v17.x 17.1.1 \
    "Base Software & ISO Releases" "BIGIP-17.1.1-0.0.6.iso" \
    -o /var/tmp/BIGIP-17.1.1.iso \
    --push 10.1.1.4 \
    --push-method icontrol \
    --push-dest /shared/images \
    --push-known-hosts ~/.ssh/known_hosts
```

#### 2. Automated Download & One-Step GeoIP Activation on BIG-IP
Stage and activate the GeoIP archive automatically on a BIG-IP appliance (rejected on BIG-IQ because BIG-IQ Centralized Management does not host TMM GeoIP tables):
```bash
myf5 downloads get BIG-IP big-ip_v17.x 17.1.1 \
    "GeoLocationUpdates" "ip-geolocation-default.zip" \
    -o /var/tmp/ip-geolocation-default.zip \
    --push 10.1.1.4 \
    --apply-geoip \
    --push-known-hosts ~/.ssh/known_hosts
```
*Under the hood on BIG-IP*: Stages the `.zip` archive to `/shared/tmp`, unpacks the `.rpm` files into `/shared/GeoIP` via `rpm2cpio | cpio -idmv` (and `geoip_update_data -f`), and executes `tmsh load sys geoip` (per K11176; see [Appliance Push Guide](appliance_push.md)).

#### 3. Download Engineering Hotfix and Push to BIG-IQ Software Repository
```bash
myf5 downloads get BIG-IP big-ip_v17.x 17.1.1 \
    "Hotfixes & Patches" "Hotfix-BIGIP-17.1.1.1.0.0.2-ENG.iso" \
    -o /var/tmp/Hotfix-BIGIP-17.1.1.1.0.0.2-ENG.iso \
    --push 192.0.2.50 \
    --push-target bigiq \
    --push-method scp \
    --push-dest /shared/images \
    --push-known-hosts ~/.ssh/known_hosts
```

For complete technical details on appliance transfer mechanisms, refer to the [Appliance Push Guide](appliance_push.md).

---

## High-Performance Streaming Pipeline

Traditional HTTP clients buffer response payloads into system memory before writing to disk. For multi-gigabyte ISOs and disk images (2.5 GB to 10+ GB), this leads to high memory pressure or out-of-memory (OOM) faults.

`myf5` uses a constant-RAM streaming pipeline:

```
HTTP TLS Stream ---> io.TeeReader ---> SHA-256 Hasher
                            |
                            v
                    os.File (Disk Write)
```

### Key Engineering Properties
1. **Constant Memory Footprint**: Allocates a fixed 64 KB transfer buffer; memory usage stays under **10 MB RAM** regardless of image size.
2. **Single-Pass Checksumming**: Computes SHA-256 digests in real time as data streams from the network, eliminating a second disk read pass.
3. **Atomic File Promotion**: Streams data into a `.part` temporary file. The file is validated against authoritative API checksums before being atomically renamed to the final destination, preventing partially downloaded or corrupted files from being used.

---

## AWS CloudFront (`*.cloudfront.net`) Signed CDN Delivery & Regional Mirrors

The MyF5 Downloads API (`api.software.downloads.f5.com`) generates time-limited, cryptographically signed **Amazon CloudFront (`*.cloudfront.net`)** CDN download URLs distributed across global geographic regions:
- `USA - EAST COAST` (Default Americas AWS CloudFront distribution)
- `IRELAND` (European AWS CloudFront distribution)

`myf5` automatically selects the optimal regional CloudFront mirror for low latency and high bandwidth, streams the artifact over TLS 1.2–1.3 (`pkg/client.SecureTLSConfig` with PQC hybrid `X25519MLKEM768` and 256-bit AEAD suites), and validates the SHA-256 digest regardless of mirror location. Ensure outbound HTTPS (TCP 443) firewall policies permit `*.cloudfront.net` alongside `api.software.downloads.f5.com` (see [Network, DNS & Firewall Architecture](network_and_firewall.md)).

---

## API Endpoint & Gateway Routing

All MyF5 Software Downloads API endpoints are served from `https://api.software.downloads.f5.com` (documented in [K15202](https://my.f5.com/manage/s/article/K15202)) under the gateway route prefix:

```
/v1/downloads-management/v1/downloads/...
```

The leading `/v1` represents the API gateway route version and the inner `/v1` represents the Downloads service version (defined in `downloads.DownloadsPrefix`).

### Client Architecture
- **Authoritative Endpoint**: `myf5-cli` connects to `https://api.software.downloads.f5.com` by default.
- **Authentication & Gateway Fallback**: Requests authenticate using the OAuth 2.0 Bearer token (`F5_CLIENT_ID` / `F5_CLIENT_SECRET`, `audience=api://ciam-api-myf5`, `scope=myf5_scope`) without a `k` query parameter on the primary endpoint, and automatically include `F5_API_KEY` (`?k=<APIKey>`) if fallback gateway routing is invoked.
- **OAuth 2.0 CIAM Alignment**:
  - Primary token issuer (Auth0): `https://idp.identity.f5.com/oauth/token`
  - Dedicated Okta CIAM authorization server: `https://identity.account.f5.com/oauth2/ausp95yxb4ShxMGvW357/v1/token` with scope `myf5_scope` and audience `api://ciam-api-myf5`.
- **Resilience & Rate-Limit Backoff**: Automatically retries transient HTTP `429 Too Many Requests` responses with backoff and supports fallback routing if required.

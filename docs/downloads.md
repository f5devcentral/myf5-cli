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
- **GeoLocation Updates (`GEODB`)**: MaxMind GeoIP updates for BIG-IP AFM, ASM, and APM.
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
myf5 downloads get BIG-IP big-ip_v17.x 17.1.1 \
    "GeoLocationUpdates" \
    "ip-geolocation-default.zip" \
    -o "${TARGET_DIR}/ip-geolocation-default.zip"

echo "==> GeoDB downloaded and verified: ${TARGET_DIR}/ip-geolocation-default.zip"
```

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

## Regional Mirror Selection

The MyF5 Downloads API generates signed CDN download URLs distributed across global geographic regions:
- `USA - EAST COAST` (Default Americas CloudFront distribution)
- `IRELAND` (European CloudFront distribution)

`myf5` automatically selects the optimal mirror for low latency and high bandwidth, while validating the SHA-256 digest regardless of mirror location.

---

## October 2, 2026 Base URL Cutover Notice

> **Action Required**: The MyF5 Downloads API base URL changes October 2. Update your integrations to use `https://api.software.downloads.f5.com` and remove the `"k"` value from API calls to help prevent service disruptions.

### How `myf5` Implements This Change
- `myf5-api` connects to `https://api.software.downloads.f5.com` by default.
- The deprecated rate-limiting query parameter `k` has been completely excised from all HTTP requests.
- Automatic fallback to `support.apis.f5.com` is retained during pre-cutover testing if CloudFront distributions are provisioning.

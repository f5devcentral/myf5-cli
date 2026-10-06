#!/usr/bin/env bash
#
# Generate a CycloneDX 1.5 SBOM from a compiled myf5 binary.
#
# The binary already carries everything an SBOM needs: Go embeds the module
# graph, each dependency's version and h1: hash, the VCS revision, and the
# build settings. `go version -m` reads them back. That makes an external
# scanner unnecessary here and keeps the toolchain dependency-free, which is
# the same reason this project has three dependencies in the first place.
#
# Usage: scripts/gen-sbom.sh <binary> [output.json]

set -euo pipefail

BIN="${1:?usage: gen-sbom.sh <binary> [output.json]}"
OUT="${2:-sbom.json}"

[ -f "$BIN" ] || { echo "no such binary: $BIN" >&2; exit 1; }

meta=$(go version -m "$BIN")

# Main module and the version it was stamped with.
main_mod=$(awk '$1=="mod"{print $2; exit}' <<<"$meta")
main_ver=$(awk '$1=="mod"{print $3; exit}' <<<"$meta")
revision=$(awk '$1=="build" && $2 ~ /^vcs.revision=/{sub(/^vcs.revision=/,"",$2); print $2; exit}' <<<"$meta")
goversion=$(awk 'NR==1{print $2; exit}' <<<"$meta")

# RFC 3339, UTC. date -u is portable across GNU and BSD; %N is not, so seconds.
now=$(date -u +%Y-%m-%dT%H:%M:%SZ)

# Dependencies. Each "dep" line is: dep <path> <version> <h1-hash>
# The hash is omitted for a replaced or vendored module, so guard for it.
components=$(awk '
  $1=="dep" {
    path=$2; ver=$3; hash=(NF>=4 ? $4 : "")
    purl="pkg:golang/" path "@" ver
    gsub(/\//,"%2F",purl_enc)
    printf "%s\t%s\t%s\t%s\n", path, ver, hash, purl
  }' <<<"$meta")

{
  printf '{\n'
  printf '  "bomFormat": "CycloneDX",\n'
  printf '  "specVersion": "1.5",\n'
  printf '  "version": 1,\n'
  printf '  "metadata": {\n'
  printf '    "timestamp": "%s",\n' "$now"
  printf '    "tools": [{"name": "gen-sbom.sh", "vendor": "myf5-cli", "version": "1.0.0"}],\n'
  printf '    "component": {\n'
  printf '      "type": "application",\n'
  printf '      "bom-ref": "pkg:golang/%s@%s",\n' "$main_mod" "$main_ver"
  printf '      "name": "%s",\n' "$main_mod"
  printf '      "version": "%s",\n' "$main_ver"
  printf '      "purl": "pkg:golang/%s@%s",\n' "$main_mod" "$main_ver"
  printf '      "properties": [\n'
  printf '        {"name": "go:version", "value": "%s"},\n' "$goversion"
  printf '        {"name": "vcs:revision", "value": "%s"}\n' "$revision"
  printf '      ]\n'
  printf '    }\n'
  printf '  },\n'
  printf '  "components": [\n'

  first=1
  while IFS=$'\t' read -r path ver hash purl; do
    [ -z "$path" ] && continue
    [ $first -eq 1 ] || printf ',\n'
    first=0
    printf '    {\n'
    printf '      "type": "library",\n'
    printf '      "bom-ref": "%s",\n' "$purl"
    printf '      "name": "%s",\n' "$path"
    printf '      "version": "%s",\n' "$ver"
    printf '      "purl": "%s",\n' "$purl"
    printf '      "scope": "required"'
    if [ -n "$hash" ]; then
      # Go's h1: hash is a base64 SHA-256 of the module's file tree, not a
      # hex digest of a single file, so it does not fit CycloneDX "hashes".
      # Record it as a property where it stays verifiable against go.sum.
      printf ',\n      "properties": [{"name": "go:mod:h1", "value": "%s"}]\n' "$hash"
    else
      printf '\n'
    fi
    printf '    }'
  done <<<"$components"

  printf '\n  ]\n'
  printf '}\n'
} > "$OUT"

# Fail loudly rather than emit a malformed document.
if command -v python3 >/dev/null 2>&1; then
  python3 -c "import json,sys; json.load(open(sys.argv[1]))" "$OUT" \
    || { echo "generated SBOM is not valid JSON: $OUT" >&2; exit 1; }
fi

count=$(grep -c '"type": "library"' "$OUT" || true)
echo "==> $OUT ($count components, main module $main_mod@$main_ver)"

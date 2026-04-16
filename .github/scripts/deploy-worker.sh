#!/bin/bash
# Deploy the distribution Worker to Cloudflare.
# Idempotent: uploads script, ensures proxied DNS record + route binding.
#
# Required env:
#   CF_API_KEY     — Cloudflare API token with Workers Scripts:Edit,
#                    Workers Routes:Edit, DNS:Edit
#   CF_ACCOUNT_ID  — Cloudflare account ID
#   CF_ZONE_ID     — Cloudflare zone ID for the hostname
#
# Optional env:
#   WORKER_NAME    — Worker script name (default: nvoi-distribution)
#   TARGET_HOST       — hostname to bind (default: get.nvoi.to)

set -euo pipefail

: "${CF_API_KEY:?required}"
: "${CF_ACCOUNT_ID:?required}"
: "${CF_ZONE_ID:?required}"

WORKER_NAME="${WORKER_NAME:-nvoi-distribution}"
TARGET_HOST="${TARGET_HOST:-get.nvoi.to}"

cf() {
  curl -sf -H "Authorization: Bearer $CF_API_KEY" "$@"
}

echo "→ uploading worker script: $WORKER_NAME"
cf -X PUT \
  "https://api.cloudflare.com/client/v4/accounts/$CF_ACCOUNT_ID/workers/scripts/$WORKER_NAME" \
  -F 'metadata=@worker/metadata.json;type=application/json' \
  -F 'index.js=@worker/index.js;type=application/javascript+module' \
  >/dev/null
echo "  uploaded"

echo "→ ensuring proxied DNS record: $TARGET_HOST"
RESP=$(cf "https://api.cloudflare.com/client/v4/zones/$CF_ZONE_ID/dns_records?type=A&name=$TARGET_HOST")
ID=$(echo "$RESP" | jq -r '.result[0].id // empty')
# Dummy target — the Worker intercepts before traffic reaches the IP.
PAYLOAD="{\"type\":\"A\",\"name\":\"$TARGET_HOST\",\"content\":\"192.0.2.1\",\"proxied\":true,\"ttl\":1}"
if [ -z "$ID" ]; then
  cf -X POST \
    "https://api.cloudflare.com/client/v4/zones/$CF_ZONE_ID/dns_records" \
    -H "Content-Type: application/json" \
    -d "$PAYLOAD" >/dev/null
  echo "  created"
else
  cf -X PUT \
    "https://api.cloudflare.com/client/v4/zones/$CF_ZONE_ID/dns_records/$ID" \
    -H "Content-Type: application/json" \
    -d "$PAYLOAD" >/dev/null
  echo "  updated ($ID)"
fi

echo "→ ensuring route binding: $TARGET_HOST/*"
PATTERN="$TARGET_HOST/*"
ROUTES=$(cf "https://api.cloudflare.com/client/v4/zones/$CF_ZONE_ID/workers/routes")
if echo "$ROUTES" | jq -e --arg p "$PATTERN" '.result[] | select(.pattern == $p)' >/dev/null; then
  echo "  already bound"
else
  cf -X POST \
    "https://api.cloudflare.com/client/v4/zones/$CF_ZONE_ID/workers/routes" \
    -H "Content-Type: application/json" \
    -d "{\"pattern\":\"$PATTERN\",\"script\":\"$WORKER_NAME\"}" >/dev/null
  echo "  created"
fi

echo "✓ worker deployed at https://$TARGET_HOST"

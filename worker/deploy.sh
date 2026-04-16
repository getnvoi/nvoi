#!/bin/bash
# Idempotent Cloudflare deploy for the distribution worker.
#
# Modes:
#   ./deploy.sh                         # deploy worker only (no bucket/binaries)
#   ./deploy.sh release <tag> <dist>    # + upsert R2 bucket, upload <dist>/* under <tag>/
#
# Env:
#   CLOUDFLARE_API_TOKEN   (required) — token with Workers:Edit + R2:Edit + DNS:Edit
#   CLOUDFLARE_ACCOUNT_ID  (required)

set -euo pipefail

: "${CLOUDFLARE_API_TOKEN:?required}"
: "${CLOUDFLARE_ACCOUNT_ID:?required}"

cd "$(dirname "$0")"

BUCKET="nvoi-releases"
WRANGLER="npx --yes wrangler@latest"

# Idempotent bucket creation — "already exists" is success.
echo "→ R2 bucket: $BUCKET"
$WRANGLER r2 bucket create "$BUCKET" 2>&1 | grep -v "already exists" || true

if [ "${1:-}" = "release" ]; then
  VERSION="${2:?version required}"
  DIST="${3:?dist dir required}"

  echo "→ uploading binaries to R2 under $VERSION/"
  for f in "$DIST"/*; do
    [ -f "$f" ] || continue
    name=$(basename "$f")
    echo "  $name"
    $WRANGLER r2 object put "$BUCKET/$VERSION/$name" \
      --file "$f" \
      --content-type "application/octet-stream"
  done

  echo "→ deploying worker (VERSION=$VERSION)"
  $WRANGLER deploy --var "VERSION:$VERSION"
else
  echo "→ deploying worker (no version bump)"
  $WRANGLER deploy
fi

echo "✓ https://get.nvoi.to"

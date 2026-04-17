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

# Resolve DIST to an absolute path BEFORE we cd — otherwise the subsequent
# `cd "$(dirname "$0")"` makes relative paths (like "dist" passed from
# release.yml running in the repo root) point at worker/dist/ which doesn't
# exist, and the upload loop silently matches nothing. Every release from
# v0.0.1 onward skipped R2 binary uploads because of this.
if [ "${1:-}" = "release" ] && [ -n "${3:-}" ]; then
  case "$3" in
    /*) ;;                                 # already absolute
    *) set -- "$1" "$2" "$(cd "$3" && pwd)" ;;
  esac
fi

cd "$(dirname "$0")"

BUCKET="nvoi-releases"
WRANGLER="npx --yes wrangler@latest"

# Build the deploy bundle: prepend a `const installScript = atob(...)` line
# holding install.sh base64-encoded, then cat index.js. Base64 because
# Cloudflare's WAF on their own API 403s multipart uploads containing raw
# `curl | sh`, `sudo`, `chmod` patterns.
{
  printf 'const installScript = atob("%s");\n' "$(base64 < install.sh | tr -d '\n')"
  cat index.js
} > .deploy.js
trap 'rm -f .deploy.js' EXIT

# Idempotent bucket creation — "already exists" is success.
# --remote is mandatory from wrangler 4.x onward. Without it r2 commands
# default to a local-only simulated bucket under .wrangler/ that nobody
# ever reads — the binaries silently evaporate at job end.
echo "→ R2 bucket: $BUCKET"
$WRANGLER r2 bucket create "$BUCKET" --remote 2>&1 | grep -v "already exists" || true

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
      --content-type "application/octet-stream" \
      --remote
  done

  echo "→ deploying worker (VERSION=$VERSION)"
  $WRANGLER deploy .deploy.js --var "VERSION:$VERSION"
else
  echo "→ deploying worker (no version bump)"
  $WRANGLER deploy .deploy.js
fi

echo "✓ https://get.nvoi.to"

#!/usr/bin/env bash
# Polls the rolling 'latest' GitHub release and redeploys if the published
# commit differs from what's running. Runs as root via a systemd timer.
# No credentials needed — the repo is public, so the asset URLs are stable.
set -euo pipefail

REPO="NisoD/Ktannul"
DIR="/opt/mitayshvim"
BASE="https://github.com/${REPO}/releases/download/latest"

new_sha=$(curl -fsSL "${BASE}/version.txt" 2>/dev/null || true)
[ -n "$new_sha" ] || { echo "no version.txt yet"; exit 0; }

cur_sha=$(cat "${DIR}/version" 2>/dev/null || true)
[ "$new_sha" != "$cur_sha" ] || exit 0

echo "updating $cur_sha -> $new_sha"
tmp=$(mktemp)
curl -fsSL -o "$tmp" "${BASE}/mitayshvim"
chmod +x "$tmp"
mv "$tmp" "${DIR}/mitayshvim"
chown mitayshvim:mitayshvim "${DIR}/mitayshvim"
echo "$new_sha" > "${DIR}/version"
systemctl restart mitayshvim
echo "deployed $new_sha"

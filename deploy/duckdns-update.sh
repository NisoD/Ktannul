#!/usr/bin/env bash
# Refreshes the DuckDNS record so the free subdomain never lapses (DuckDNS
# expires domains after ~30 days without an update). Token is read from a
# root-only file, never baked into the repo.
set -euo pipefail

DOMAIN="mitayshvim"
TOKEN_FILE="/opt/mitayshvim/duckdns.token"
[ -f "$TOKEN_FILE" ] || { echo "missing $TOKEN_FILE"; exit 1; }
token=$(tr -d '[:space:]' < "$TOKEN_FILE")

# Empty ip= lets DuckDNS auto-detect the source address.
resp=$(curl -fsSL "https://www.duckdns.org/update?domains=${DOMAIN}&token=${token}&ip=")
echo "duckdns: $resp"
[ "$resp" = "OK" ]

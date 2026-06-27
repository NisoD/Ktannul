#!/usr/bin/env bash
# Refreshes the DuckDNS records so the free subdomains never lapse (DuckDNS
# expires a domain after ~30 days without an update). Both spellings point at
# this VM and live in SEPARATE DuckDNS accounts, so each needs its own token.
# Tokens live in root-only files, never baked into the repo.
set -euo pipefail

# domain -> token file. Add/remove pairs here.
declare -A TOKENS=(
	[mitayshvim]=/opt/mitayshvim/duckdns.token      # a-y account (GitHub)
	[mityashvim]=/opt/mitayshvim/duckdns-ya.token   # y-a account (Google)
)

rc=0
for domain in "${!TOKENS[@]}"; do
	tf="${TOKENS[$domain]}"
	if [ ! -f "$tf" ]; then
		echo "duckdns $domain: missing $tf" >&2
		rc=1
		continue
	fi
	token=$(tr -d '[:space:]' < "$tf")
	# Empty ip= lets DuckDNS auto-detect the source address.
	resp=$(curl -fsSL "https://www.duckdns.org/update?domains=${domain}&token=${token}&ip=")
	echo "duckdns $domain: $resp"
	[ "$resp" = "OK" ] || rc=1
done
exit $rc

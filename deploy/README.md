# Deploying to Oracle Cloud Always Free

## 1. VM
- Create an Ampere A1 instance (VM.Standard.A1.Flex, 1 OCPU / 6GB is plenty), Ubuntu 24.04.
- In the subnet's Security List add ingress rules for TCP 80 and 443 (0.0.0.0/0). 22 is there by default.

## 2. OS firewall gotcha
Oracle's Ubuntu images ship iptables REJECT rules beyond the security list:

    sudo iptables -I INPUT 5 -p tcp --dport 80 -j ACCEPT
    sudo iptables -I INPUT 5 -p tcp --dport 443 -j ACCEPT
    sudo netfilter-persistent save

## 3. DNS
Free hostname: https://www.duckdns.org → point your subdomain(s) at the VM's
public IP. This deploy serves two spellings — `mitayshvim.duckdns.org` (primary)
and `mityashvim.duckdns.org` (alias) — which live in separate DuckDNS accounts.

## 4. Caddy (TLS + reverse proxy)
    sudo apt install -y debian-keyring debian-archive-keyring apt-transport-https curl
    curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' | sudo gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
    curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' | sudo tee /etc/apt/sources.list.d/caddy-stable.list
    sudo apt update && sudo apt install caddy
    sudo cp deploy/Caddyfile /etc/caddy/Caddyfile   # edit hostname first
    sudo systemctl reload caddy

## 5. App
On your machine:

    GOOS=linux GOARCH=arm64 go build -o mitayshvim .
    scp mitayshvim deploy/mitayshvim.service ubuntu@<vm-ip>:~

On the VM:

    sudo useradd -r -s /usr/sbin/nologin mitayshvim
    sudo mkdir -p /opt/mitayshvim/data
    sudo mv ~/mitayshvim /opt/mitayshvim/
    sudo chown -R mitayshvim:mitayshvim /opt/mitayshvim
    sudo mv ~/mitayshvim.service /etc/systemd/system/
    sudo systemctl daemon-reload
    sudo systemctl enable --now mitayshvim

## 6. Verify
    curl https://mitayshvim.duckdns.org/healthz   # → ok
    curl https://mityashvim.duckdns.org/healthz   # → ok (alias)

Create a game in the browser, join from a phone (not on wifi) via the QR.

## 7. Auto-redeploy on merge (no secrets)

Pushes to `master` trigger `.github/workflows/release.yml`, which builds the
ARM64 binary and publishes it to a rolling `latest` GitHub release. The VM
polls that release every 5 minutes and self-updates. Because the repo is
public, no SSH key or CI secret is involved.

Install the updater on the VM (once):

    sudo cp deploy/auto-update.sh /opt/mitayshvim/
    sudo chmod +x /opt/mitayshvim/auto-update.sh
    sudo cp deploy/mitayshvim-update.service deploy/mitayshvim-update.timer /etc/systemd/system/
    sudo systemctl daemon-reload
    sudo systemctl enable --now mitayshvim-update.timer

Manual redeploy still works:

    GOOS=linux GOARCH=arm64 go build -o mitayshvim . && scp mitayshvim ubuntu@<vm-ip>:~
    ssh ubuntu@<vm-ip> 'sudo systemctl stop mitayshvim && sudo mv ~/mitayshvim /opt/mitayshvim/ && sudo chown mitayshvim:mitayshvim /opt/mitayshvim/mitayshvim && sudo systemctl start mitayshvim'

Running games survive either path: stop snapshots all rooms, start restores them.

## 8. DuckDNS keep-alive

Free DuckDNS domains lapse after ~30 days without an update. A timer refreshes
them twice a day. The two spellings live in separate DuckDNS accounts, so each
needs its own token (from duckdns.org, top of the page) in its own root-only
file. `duckdns-update.sh` maps domain → token file; edit the map if names change.

    echo "<a-y-token>" | sudo tee /opt/mitayshvim/duckdns.token    >/dev/null  # mitayshvim
    echo "<y-a-token>" | sudo tee /opt/mitayshvim/duckdns-ya.token >/dev/null  # mityashvim
    sudo chmod 600 /opt/mitayshvim/duckdns.token /opt/mitayshvim/duckdns-ya.token
    sudo cp deploy/duckdns-update.sh /opt/mitayshvim/
    sudo chmod +x /opt/mitayshvim/duckdns-update.sh
    sudo cp deploy/duckdns-update.service deploy/duckdns-update.timer /etc/systemd/system/
    sudo systemctl daemon-reload
    sudo systemctl enable --now duckdns-update.timer

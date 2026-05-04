#!/usr/bin/env bash
# Vernex Auth Relay — deploy script
# Run once on the vernex.net bootstrap node.
# Generates RSA keypair, prompts for Google credentials, creates systemd service.
#
# Usage: bash deploy.sh

set -euo pipefail

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; BOLD='\033[1m'; RESET='\033[0m'
ok()   { echo -e "${GREEN}  [✓] $*${RESET}"; }
warn() { echo -e "${YELLOW}  [!] $*${RESET}"; }
die()  { echo -e "${RED}  [✗] $*${RESET}" >&2; exit 1; }
step() { echo -e "\n${BOLD}━━━━ $* ━━━━${RESET}"; }

[[ "${EUID:-$(id -u)}" -eq 0 ]] && die "Run as a regular user — script uses sudo internally."
[[ "$(uname -s)" == "Linux" ]] || die "Linux only."

CONF_DIR="/etc/vernex-relay"
RELAY_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SERVICE_USER="${USER}"
RELAY_URL="https://vernex.net:5443"

# ─────────────────────────────────────────────────────────────────────────────
step "1 — System packages"
# ─────────────────────────────────────────────────────────────────────────────
sudo apt-get update -qq
sudo apt-get install -y python3 python3-pip python3-venv nginx certbot openssl
ok "Packages ready"

# ─────────────────────────────────────────────────────────────────────────────
step "2 — Python virtualenv"
# ─────────────────────────────────────────────────────────────────────────────
VENV="${RELAY_DIR}/venv"
python3 -m venv "${VENV}"
"${VENV}/bin/pip" install --quiet -r "${RELAY_DIR}/requirements.txt"
ok "venv at ${VENV}"

# ─────────────────────────────────────────────────────────────────────────────
step "3 — Config directory"
# ─────────────────────────────────────────────────────────────────────────────
sudo mkdir -p "${CONF_DIR}"
sudo chown "${SERVICE_USER}:${SERVICE_USER}" "${CONF_DIR}"
chmod 700 "${CONF_DIR}"
ok "${CONF_DIR} (mode 700)"

# ─────────────────────────────────────────────────────────────────────────────
step "4 — RSA keypair for JWT signing"
# ─────────────────────────────────────────────────────────────────────────────
if [[ -f "${CONF_DIR}/relay.key" ]]; then
    ok "relay.key already exists — skipping generation"
else
    openssl genrsa -out "${CONF_DIR}/relay.key" 2048 2>/dev/null
    openssl rsa -in "${CONF_DIR}/relay.key" -pubout -out "${CONF_DIR}/relay.pub" 2>/dev/null
    chmod 600 "${CONF_DIR}/relay.key"
    chmod 644 "${CONF_DIR}/relay.pub"
    ok "relay.key + relay.pub generated (RSA-2048)"
fi

# ─────────────────────────────────────────────────────────────────────────────
step "5 — TLS certificate"
# ─────────────────────────────────────────────────────────────────────────────
if [[ -f "/etc/letsencrypt/live/vernex.net/fullchain.pem" ]]; then
    ok "Let's Encrypt cert already present"
elif command -v certbot &>/dev/null; then
    echo
    echo "  Attempting Let's Encrypt certificate for vernex.net..."
    echo "  (requires port 80 to be open and DNS pointing to this server)"
    echo
    sudo certbot certonly --standalone \
        --non-interactive --agree-tos --register-unsafely-without-email \
        -d vernex.net && ok "Let's Encrypt cert issued" || {
        warn "certbot failed — falling back to self-signed cert"
        _SELFSIGNED=1
    }
else
    warn "certbot not available — generating self-signed cert"
    _SELFSIGNED=1
fi

if [[ -n "${_SELFSIGNED:-}" ]]; then
    SELF_DIR="${CONF_DIR}/tls"
    mkdir -p "${SELF_DIR}"
    openssl req -x509 -newkey rsa:2048 -keyout "${SELF_DIR}/key.pem" \
        -out "${SELF_DIR}/cert.pem" -days 3650 -nodes \
        -subj "/CN=vernex.net" 2>/dev/null
    chmod 600 "${SELF_DIR}/key.pem"
    ok "Self-signed cert at ${SELF_DIR}/ (upgrade with: sudo certbot certonly --standalone -d vernex.net)"
    # Patch nginx config to use self-signed paths
    NGINX_CONF="${RELAY_DIR}/nginx-relay.conf"
    NGINX_TMP="/tmp/nginx-relay-selfsigned.conf"
    sed "s|/etc/letsencrypt/live/vernex.net/fullchain.pem|${SELF_DIR}/cert.pem|g;
         s|/etc/letsencrypt/live/vernex.net/privkey.pem|${SELF_DIR}/key.pem|g" \
        "${NGINX_CONF}" > "${NGINX_TMP}"
    sudo cp "${NGINX_TMP}" /etc/nginx/sites-available/vernex-relay
else
    sudo cp "${RELAY_DIR}/nginx-relay.conf" /etc/nginx/sites-available/vernex-relay
fi
ok "nginx config installed at /etc/nginx/sites-available/vernex-relay"

sudo ln -sf /etc/nginx/sites-available/vernex-relay /etc/nginx/sites-enabled/vernex-relay
sudo nginx -t 2>/dev/null && sudo systemctl reload nginx
ok "nginx reloaded"

# ─────────────────────────────────────────────────────────────────────────────
step "6 — Google OAuth credentials"
# ─────────────────────────────────────────────────────────────────────────────
CONF_FILE="${CONF_DIR}/config.json"
if [[ -f "${CONF_FILE}" ]]; then
    ok "config.json already exists — skipping credential prompt"
else
    echo
    echo "  Google OAuth App setup:"
    echo "  1. Go to https://console.cloud.google.com → APIs & Services → Credentials"
    echo "  2. Create OAuth client ID (Web application)"
    echo "  3. Authorized redirect URI: ${RELAY_URL}/callback"
    echo
    read -rp "  google_client_id     : " _CLIENT_ID
    read -rsp "  google_client_secret : " _CLIENT_SECRET
    echo

    python3 -c "
import json, secrets
cfg = {
    'google_client_id': '${_CLIENT_ID}',
    'google_client_secret': '${_CLIENT_SECRET}',
    'relay_callback_url': '${RELAY_URL}/callback',
    'state_secret': secrets.token_hex(32),
}
print(json.dumps(cfg, indent=2))
" > "${CONF_FILE}"
    chmod 600 "${CONF_FILE}"
    ok "config.json written (mode 600)"
fi

# ─────────────────────────────────────────────────────────────────────────────
step "7 — systemd service"
# ─────────────────────────────────────────────────────────────────────────────
SERVICE_FILE="/etc/systemd/system/vernex-auth-relay.service"
sudo tee "${SERVICE_FILE}" > /dev/null <<SVCEOF
[Unit]
Description=Vernex OAuth Relay
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=${SERVICE_USER}
WorkingDirectory=${RELAY_DIR}
ExecStart=${VENV}/bin/python3 ${RELAY_DIR}/relay.py
Restart=on-failure
RestartSec=5
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
SVCEOF

sudo systemctl daemon-reload
sudo systemctl enable --now vernex-auth-relay
ok "vernex-auth-relay service enabled and started"

# ─────────────────────────────────────────────────────────────────────────────
echo
echo -e "${GREEN}${BOLD}  ── Relay deployed ──${RESET}"
echo "  URL     : ${RELAY_URL}"
echo "  /login  : ${RELAY_URL}/login?return=<node_url>/auth/complete"
echo "  /pubkey : ${RELAY_URL}/pubkey"
echo
echo "  Add the redirect URI in Google Cloud Console:"
echo "  ${RELAY_URL}/callback"
echo
echo "  journalctl -u vernex-auth-relay -f"

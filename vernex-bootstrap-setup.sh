#!/usr/bin/env bash
# Vernex Protocol — bootstrap node one-command installer
# Provisions this machine as a Vernex bootstrap node (public IP required).
# Runs on the user's own machine. No prior Vernex knowledge required.
#
# Usage (from GitHub):
#   curl -fsSL https://raw.githubusercontent.com/SuperSleeper/vernex-protocol/main/vernex-bootstrap-setup.sh | bash
#
# Usage (from a local bootstrap node):
#   curl -fsSL http://<bootstrap-ip>:5000/install-bootstrap | bash

set -euo pipefail

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'
CYAN='\033[0;36m'; BOLD='\033[1m'; RESET='\033[0m'
ok()   { echo -e "${GREEN}  [✓] $*${RESET}"; }
warn() { echo -e "${YELLOW}  [!] $*${RESET}"; }
err()  { echo -e "${RED}  [✗] $*${RESET}" >&2; }
step() { echo -e "\n${BOLD}${CYAN}━━━━ $* ━━━━${RESET}"; }
die()  { err "$*"; exit 1; }

INSTALL_DIR="${HOME}/vernex"
REPO_URL="https://github.com/SuperSleeper/vernex-protocol.git"
BINARY_DEST="/usr/local/bin/vernex-node"
DAEMON_SVC="/etc/systemd/system/vernex-daemon.service"
DASHBOARD_SVC="/etc/systemd/system/vernex-dashboard.service"
MDNS_SVC="/etc/systemd/system/vernex-mdns.service"
NODE_CONFIG="${INSTALL_DIR}/config/node.json"
TOKEN_FILE="${INSTALL_DIR}/config/enrollment_tokens.txt"
GO_MIN="1.21"
GO_INSTALL="1.22.5"
API_PORT=7701
P2P_PORT=7700
NETWORK_ID="vernex-mainnet"
TOKEN_COUNT=5

# ─────────────────────────────────────────────────────────────────────────────
step "1 — OS check"
# ─────────────────────────────────────────────────────────────────────────────
[[ "$(uname -s)" == "Linux" ]] || die "Vernex requires Linux."
[[ "${EUID:-$(id -u)}" -eq 0 ]] && die "Do not run as root — run as your regular user."
[[ -f /etc/os-release ]] && . /etc/os-release
ok "Linux: ${PRETTY_NAME:-$(uname -s)}"

# ─────────────────────────────────────────────────────────────────────────────
step "2 — System dependencies"
# ─────────────────────────────────────────────────────────────────────────────
sudo -v 2>/dev/null || warn "sudo password may be required during install"

_apt() { sudo apt-get install -y "$@" 2>/dev/null || warn "apt install $* failed — continuing"; }

for _bin in git curl jq; do
    command -v "${_bin}" &>/dev/null || _apt "${_bin}"
    ok "${_bin}"
done

# python3, pip, venv
for _pkg in python3 python3-pip python3-venv; do
    dpkg -s "${_pkg}" &>/dev/null 2>&1 || _apt "${_pkg}"
    ok "${_pkg}"
done

# avahi-daemon (mDNS server) + avahi-utils (avahi-browse, avahi-publish-service)
for _pkg in avahi-daemon avahi-utils dnsutils; do
    dpkg -s "${_pkg}" &>/dev/null 2>&1 || _apt "${_pkg}"
    ok "${_pkg}"
done

# Ensure avahi-daemon is running (needed for mDNS advertisement)
if ! systemctl is-active --quiet avahi-daemon 2>/dev/null; then
    sudo systemctl enable --now avahi-daemon 2>/dev/null || warn "Could not start avahi-daemon"
fi
ok "avahi-daemon active"

# Go
_go_version_ok() {
    local _v; _v="$(go version 2>/dev/null | awk '{print $3}' | sed 's/go//')" || return 1
    local _nmaj _nmin _cmaj _cmin
    IFS='.' read -r _nmaj _nmin _ <<< "${GO_MIN}"
    IFS='.' read -r _cmaj _cmin _ <<< "${_v}"
    (( _cmaj > _nmaj )) && return 0
    (( _cmaj == _nmaj && _cmin >= _nmin )) && return 0
    return 1
}
export PATH="${PATH}:/usr/local/go/bin"
if ! _go_version_ok; then
    _apt golang-go 2>/dev/null || true
    if ! _go_version_ok; then
        echo "  apt Go unavailable or too old — installing Go ${GO_INSTALL} from go.dev"
        _TARBALL="go${GO_INSTALL}.linux-amd64.tar.gz"
        [[ -f "/tmp/${_TARBALL}" ]] || curl -fsSL -o "/tmp/${_TARBALL}" \
            "https://go.dev/dl/${_TARBALL}"
        sudo rm -rf /usr/local/go
        sudo tar -C /usr/local -xzf "/tmp/${_TARBALL}"
        for _rc in "${HOME}/.bashrc" "${HOME}/.profile"; do
            grep -qF '/usr/local/go/bin' "${_rc}" 2>/dev/null || \
                printf '\nexport PATH="${PATH}:/usr/local/go/bin"\n' >> "${_rc}"
        done
        _go_version_ok || die "Go ${GO_INSTALL} installation failed"
    fi
fi
ok "Go: $(go version | awk '{print $3}')"

# Ollama
if ! command -v ollama &>/dev/null; then
    echo "  Installing Ollama..."
    curl -fsSL https://ollama.com/install.sh | sh
    ok "Ollama installed"
else
    ok "Ollama: $(ollama --version 2>/dev/null | head -1 || echo 'present')"
fi

# ─────────────────────────────────────────────────────────────────────────────
step "3 — Clone / update repository (HTTPS)"
# ─────────────────────────────────────────────────────────────────────────────
mkdir -p "${INSTALL_DIR}"
if [[ -d "${INSTALL_DIR}/.git" ]]; then
    git -C "${INSTALL_DIR}" pull origin main && ok "Repo updated"
else
    git clone "${REPO_URL}" "${INSTALL_DIR}" && ok "Repo cloned"
fi

# ─────────────────────────────────────────────────────────────────────────────
step "4 — Build daemon"
# ─────────────────────────────────────────────────────────────────────────────
cd "${INSTALL_DIR}/daemon"
_BUILD_TIME="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
if go build -ldflags "-X vernex/daemon/ca.BuildTime=${_BUILD_TIME}" -o vernex-node . 2>&1; then
    ok "Built — BuildTime=${_BUILD_TIME}"
else
    warn "ldflags build failed — retrying without BuildTime"
    go build -o vernex-node . || die "go build failed"
    ok "Built (no BuildTime)"
fi

# ─────────────────────────────────────────────────────────────────────────────
step "5 — Install binary to ${BINARY_DEST}"
# ─────────────────────────────────────────────────────────────────────────────
if sudo systemctl is-active --quiet vernex-daemon 2>/dev/null; then
    warn "Stopping vernex-daemon before binary replacement..."
    sudo systemctl stop vernex-daemon
fi
sudo cp "${INSTALL_DIR}/daemon/vernex-node" "${BINARY_DEST}"
sudo chmod 755 "${BINARY_DEST}"
ok "Binary installed: ${BINARY_DEST}"

# ─────────────────────────────────────────────────────────────────────────────
step "6 — Bootstrap configuration"
# ─────────────────────────────────────────────────────────────────────────────
# Detect public IP
PUBLIC_IP="$(curl -s --connect-timeout 5 https://api.ipify.org 2>/dev/null || \
             curl -s --connect-timeout 5 https://icanhazip.com 2>/dev/null || \
             echo '')"
if [[ -n "${PUBLIC_IP}" ]]; then
    ok "Public IP: ${PUBLIC_IP}"
else
    warn "Could not detect public IP — ensure port ${API_PORT} is reachable externally"
    PUBLIC_IP="<your-public-ip>"
fi

mkdir -p "${INSTALL_DIR}/config"
if [[ -f "${NODE_CONFIG}" ]]; then
    ok "Config exists — ensuring is_bootstrap: true"
    python3 - <<PYEOF 2>/dev/null || warn "Could not patch is_bootstrap into existing config"
import json, sys
path = "${NODE_CONFIG}"
try:
    with open(path) as f:
        cfg = json.load(f)
except Exception as e:
    print(f"  [!] Could not read config: {e}", file=sys.stderr)
    sys.exit(0)
changed = False
if not cfg.get("is_bootstrap"):
    cfg["is_bootstrap"] = True
    changed = True
if changed:
    with open(path, "w") as f:
        json.dump(cfg, f, indent=2)
        f.write("\n")
    print("  → set is_bootstrap: true")
else:
    print("  → is_bootstrap already true")
PYEOF
else
    cat > "${NODE_CONFIG}" <<JSONEOF
{
  "node_id": "",
  "p2p_port": ${P2P_PORT},
  "api_port": ${API_PORT},
  "personal_partition": 70,
  "social_partition": 30,
  "is_bootstrap": true,
  "ca_mode": "single",
  "ca_threshold_k": 3,
  "ca_threshold_n": 5,
  "peer_nodes": []
}
JSONEOF
    chmod 600 "${NODE_CONFIG}"
    ok "Config written (is_bootstrap: true)"
fi

# Run daemon briefly to generate keypairs on first boot
if [[ ! -f "${INSTALL_DIR}/config/node.key" ]]; then
    echo "  Generating keypairs (3s)..."
    cd "${INSTALL_DIR}/daemon"
    timeout 3 ./vernex-node 2>/dev/null || true
    ok "Keypairs generated"
fi

NODE_ID="$(python3 -c \
    "import json; print(json.load(open('${NODE_CONFIG}')).get('node_id',''))" \
    2>/dev/null || true)"
[[ -n "${NODE_ID}" ]] && ok "Node ID: ${NODE_ID}" || \
    warn "Node ID not yet in config — will appear after daemon first start"

# ─────────────────────────────────────────────────────────────────────────────
step "7 — Certificate Authority initialisation"
# ─────────────────────────────────────────────────────────────────────────────
cd "${INSTALL_DIR}/daemon"

if [[ -f "${INSTALL_DIR}/config/root.crt" ]]; then
    ok "Root CA already exists — skipping ca init"
else
    echo "  Generating root CA..."
    ./vernex-node ca init || die "ca init failed"
    ok "Root CA created: config/root.{key,crt}"
fi

if [[ -f "${INSTALL_DIR}/config/intermediate.crt" ]]; then
    ok "Intermediate CA already exists — skipping"
else
    if [[ ! -f "${INSTALL_DIR}/config/root.key" ]]; then
        die "root.key not found — run vernex-node ca init first"
    fi
    echo "  Generating intermediate CA..."
    ./vernex-node ca init-intermediate || die "ca init-intermediate failed"
    ok "Intermediate CA created: config/intermediate.{key,csr,crt}"
fi

# ─────────────────────────────────────────────────────────────────────────────
step "8 — Peer CA sync (optional — join existing network)"
# ─────────────────────────────────────────────────────────────────────────────
echo
echo -e "${CYAN}  If joining an EXISTING Vernex network, enter the URL of the existing bootstrap${RESET}"
echo -e "${CYAN}  node (e.g. https://1.2.3.4:7701). Press ENTER to skip (new network root).${RESET}"
echo
read -rp "  Existing bootstrap URL [ENTER to skip]: " _EXISTING_BOOTSTRAP

if [[ -n "${_EXISTING_BOOTSTRAP}" ]]; then
    _CA_RESPONSE="$(curl -sk --connect-timeout 10 "${_EXISTING_BOOTSTRAP}/ca-sync" 2>/dev/null || true)"
    if [[ -n "${_CA_RESPONSE}" ]] && \
       echo "${_CA_RESPONSE}" | python3 -c "import json,sys; json.load(sys.stdin)" &>/dev/null; then
        echo "${_CA_RESPONSE}" | python3 - <<PYEOF 2>/dev/null || warn "CA sync parse failed"
import json, sys, os
data = json.load(sys.stdin)
config_dir = "${INSTALL_DIR}/config"
for key, fname in [("root_cert", "root.crt"), ("intermediate_cert", "intermediate.crt")]:
    val = data.get(key)
    if val:
        path = os.path.join(config_dir, fname)
        if not os.path.exists(path):
            with open(path, "w") as f:
                json.dump(val, f, indent=2)
                f.write("\n")
            print(f"  → saved {fname}")
        else:
            print(f"  → {fname} already exists — leaving untouched")
PYEOF
        ok "Peer CA sync complete from ${_EXISTING_BOOTSTRAP}"
    else
        warn "Could not reach ${_EXISTING_BOOTSTRAP}/ca-sync — skipping peer CA sync"
    fi
else
    ok "Skipped — this node is the root of a new Vernex network"
fi

# ─────────────────────────────────────────────────────────────────────────────
step "9 — Generate enrollment tokens"
# ─────────────────────────────────────────────────────────────────────────────
cd "${INSTALL_DIR}/daemon"
if [[ -f "${TOKEN_FILE}" ]]; then
    _EXISTING_COUNT="$(grep -c '"network_id"' "${TOKEN_FILE}" 2>/dev/null || echo 0)"
    ok "Enrollment tokens already exist: ${_EXISTING_COUNT} token(s) in ${TOKEN_FILE}"
    warn "Delete ${TOKEN_FILE} and re-run to regenerate"
else
    echo "  Generating ${TOKEN_COUNT} enrollment tokens..."
    {
        echo "# Vernex enrollment tokens — generated $(date -u +%Y-%m-%dT%H:%M:%SZ)"
        echo "# Each token is single-use, 30-day validity. Share one per new node operator."
        echo "# Operator command:"
        echo "#   vernex-node ca enroll --bootstrap https://${PUBLIC_IP}:${API_PORT} --token '<paste token JSON>'"
        echo
        for _i in $(seq 1 "${TOKEN_COUNT}"); do
            echo "# ── Token ${_i} ──────────────────────────────────────────────────────────────"
            _CONFIRM="$(./vernex-node ca token "${NETWORK_ID}" 2>/dev/null)"
            _TOKEN_PATH="$(echo "${_CONFIRM}" | grep 'path' | awk '{print $NF}')"
            if [[ -n "${_TOKEN_PATH}" && -f "${_TOKEN_PATH}" ]]; then
                cat "${_TOKEN_PATH}"
                echo
            else
                warn "Token ${_i} generation failed"
            fi
        done
    } > "${TOKEN_FILE}"
    chmod 600 "${TOKEN_FILE}"
    ok "${TOKEN_COUNT} enrollment tokens saved to ${TOKEN_FILE}"
fi

# ─────────────────────────────────────────────────────────────────────────────
step "10 — Dashboard Python environment"
# ─────────────────────────────────────────────────────────────────────────────
VENV_DIR="${INSTALL_DIR}/dashboard/venv"
REQ_FILE="${INSTALL_DIR}/dashboard/requirements.txt"

if [[ ! -d "${VENV_DIR}" ]]; then
    python3 -m venv "${VENV_DIR}"
    ok "Venv created: ${VENV_DIR}"
else
    ok "Venv exists: ${VENV_DIR}"
fi

if [[ -f "${REQ_FILE}" ]]; then
    "${VENV_DIR}/bin/pip" install -q --upgrade pip
    "${VENV_DIR}/bin/pip" install -q -r "${REQ_FILE}"
    ok "Dashboard dependencies installed"
else
    "${VENV_DIR}/bin/pip" install -q flask requests
    ok "flask + requests installed (requirements.txt not found)"
fi

# ─────────────────────────────────────────────────────────────────────────────
step "11 — Systemd services"
# ─────────────────────────────────────────────────────────────────────────────

# vernex-daemon
_DAEMON_SVC="[Unit]
Description=Vernex Protocol Bootstrap Node Daemon
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=${USER}
WorkingDirectory=${INSTALL_DIR}/daemon
ExecStart=${BINARY_DEST}
Restart=on-failure
RestartSec=5
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target"

_write_service() {
    local _path="$1" _content="$2" _name="$3"
    local _existing=""
    [[ -f "${_path}" ]] && _existing="$(cat "${_path}")"
    if [[ "${_content}" == "${_existing}" ]]; then
        ok "${_name} already up to date"
    else
        printf '%s\n' "${_content}" | sudo tee "${_path}" > /dev/null
        ok "${_name} written"
    fi
}

_write_service "${DAEMON_SVC}" "${_DAEMON_SVC}" "vernex-daemon.service"

# vernex-dashboard (bound to 127.0.0.1 — bootstrap nodes are public-facing)
_DASH_SVC="[Unit]
Description=Vernex Protocol Dashboard
After=network.target vernex-daemon.service
Wants=vernex-daemon.service

[Service]
Type=simple
User=${USER}
WorkingDirectory=${INSTALL_DIR}/dashboard
Environment=VERNEX_DASHBOARD_HOST=127.0.0.1
ExecStart=${VENV_DIR}/bin/python3 ${INSTALL_DIR}/dashboard/app.py
Restart=on-failure
RestartSec=5
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target"

_write_service "${DASHBOARD_SVC}" "${_DASH_SVC}" "vernex-dashboard.service"

# vernex-mdns — advertise bootstrap node so LAN compute nodes find it automatically
# Uses %H (systemd hostname specifier) to avoid hardcoding $HOSTNAME
_MDNS_SVC="[Unit]
Description=Vernex mDNS Service Advertisement
After=network.target avahi-daemon.service
Wants=avahi-daemon.service
PartOf=vernex-daemon.service

[Service]
Type=simple
User=${USER}
ExecStart=/usr/bin/avahi-publish-service %H _vernex._tcp ${API_PORT} version=0.12.0
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target"

_write_service "${MDNS_SVC}" "${_MDNS_SVC}" "vernex-mdns.service"

sudo systemctl daemon-reload && ok "systemd daemon reloaded"

# ─────────────────────────────────────────────────────────────────────────────
step "12 — Enable and start all services"
# ─────────────────────────────────────────────────────────────────────────────
for _svc in vernex-daemon vernex-dashboard vernex-mdns; do
    sudo systemctl enable "${_svc}" 2>/dev/null
    sudo systemctl restart "${_svc}" 2>/dev/null || warn "${_svc} restart failed"
done
sleep 3

# ─────────────────────────────────────────────────────────────────────────────
step "13 — Verify services"
# ─────────────────────────────────────────────────────────────────────────────
_ALL_OK=true
for _svc in vernex-daemon vernex-dashboard vernex-mdns; do
    _ACTIVE=false
    for _i in 1 2 3 4 5; do
        if systemctl is-active --quiet "${_svc}" 2>/dev/null; then
            _ACTIVE=true; break
        fi
        sleep 2
    done
    if ${_ACTIVE}; then
        ok "${_svc}: active"
    else
        warn "${_svc}: not active — check: sudo journalctl -u ${_svc} -n 20"
        _ALL_OK=false
    fi
done

# Clock verification log
_CLOCK="$(sudo journalctl -u vernex-daemon -n 60 2>/dev/null | \
          grep -E 'Clock|drift|ntp|NTP|\[✓\] clock|\[!\] clock' | tail -5 || true)"
if [[ -n "${_CLOCK}" ]]; then
    ok "Clock log:"
    echo "${_CLOCK}"
fi

# ─────────────────────────────────────────────────────────────────────────────
step "14 — Dashboard health check"
# ─────────────────────────────────────────────────────────────────────────────
if curl -sf http://127.0.0.1:5000 -o /dev/null 2>/dev/null; then
    ok "Dashboard responding at http://127.0.0.1:5000"
else
    warn "Dashboard not yet responding — check: sudo journalctl -u vernex-dashboard -n 20"
fi

# ─────────────────────────────────────────────────────────────────────────────
# Summary
# ─────────────────────────────────────────────────────────────────────────────
_LAN_IP="$(hostname -I 2>/dev/null | awk '{print $1}' || echo '127.0.0.1')"

echo
echo -e "${BOLD}${GREEN}╔════════════════════════════════════════════╗${RESET}"
echo -e "${BOLD}${GREEN}║    VERNEX BOOTSTRAP NODE SETUP COMPLETE    ║${RESET}"
echo -e "${BOLD}${GREEN}╚════════════════════════════════════════════╝${RESET}"
echo
echo -e "${BOLD}${GREEN}Bootstrap node is live.${RESET}"
echo
echo -e "${BOLD}Node ID:    ${RESET}${NODE_ID:-"(see config/node.json)"}"
echo -e "${BOLD}Public IP:  ${RESET}${PUBLIC_IP}"
echo -e "${BOLD}LAN IP:     ${RESET}${_LAN_IP}"
echo -e "${BOLD}API:        ${RESET}https://${PUBLIC_IP}:${API_PORT}"
echo -e "${BOLD}Dashboard:  ${RESET}http://127.0.0.1:5000  (localhost only)"
echo
echo -e "${BOLD}To install a new compute node:${RESET}"
echo -e "  ${CYAN}Local (LAN):  curl -fsSL http://${_LAN_IP}:5000/install | bash${RESET}"
echo -e "  ${CYAN}Public (WAN): curl -fsSL https://raw.githubusercontent.com/SuperSleeper/vernex-protocol/main/vernex-node-setup.sh | bash${RESET}"
echo
echo -e "${BOLD}Firewall — open these ports on your router:${RESET}"
echo -e "  sudo ufw allow ${API_PORT}/tcp   ${CYAN}# HTTPS API${RESET}"
echo -e "  sudo ufw allow ${P2P_PORT}/tcp   ${CYAN}# P2P TCP${RESET}"
echo -e "  sudo ufw allow ${P2P_PORT}/udp   ${CYAN}# UDP hole punching${RESET}"
echo
echo -e "${BOLD}DNS TXT record (add to your DNS provider):${RESET}"
echo -e "  ${CYAN}_vernex._tcp.<yourdomain>  TXT  \"bootstrap=${PUBLIC_IP}:${API_PORT}\"${RESET}"
echo
echo -e "${BOLD}Enrollment tokens (token_id + expires_at only — full JSON in ${TOKEN_FILE}):${RESET}"
if [[ -f "${TOKEN_FILE}" ]]; then
    grep -E '"token_id"|"expires_at"' "${TOKEN_FILE}" | sed 's/^/  /'
else
    warn "Token file not found: ${TOKEN_FILE}"
fi
echo
echo -e "${BOLD}Service status:${RESET}"
for _svc in vernex-daemon vernex-dashboard vernex-mdns; do
    _st="$(systemctl is-active "${_svc}" 2>/dev/null || echo inactive)"
    [[ "${_st}" == "active" ]] && \
        echo -e "  ${GREEN}●${RESET} ${_svc}: ${GREEN}${_st}${RESET}" || \
        echo -e "  ${RED}●${RESET} ${_svc}: ${RED}${_st}${RESET}"
done
echo

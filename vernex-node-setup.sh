#!/usr/bin/env bash
# Vernex Protocol — compute node one-command installer
# Runs on the user's own machine. No prior Vernex knowledge required.
#
# Usage (from GitHub):
#   curl -fsSL https://raw.githubusercontent.com/SuperSleeper/vernex-protocol/main/vernex-node-setup.sh | bash
#
# Usage (from a local bootstrap node):
#   curl -fsSL http://<bootstrap-ip>:5000/install | bash

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
NODE_CONFIG="${INSTALL_DIR}/config/node.json"
GO_MIN="1.21"
GO_INSTALL="1.22.5"
API_PORT=7701
P2P_PORT=7700

# ── Parse command-line flags ──────────────────────────────────────────────────
_ENROLL_TOKEN=""
while [[ $# -gt 0 ]]; do
    if [[ "$1" == "--token" ]] && [[ $# -gt 1 ]]; then
        _ENROLL_TOKEN="$2"
        break
    fi
    shift
done

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

for _bin in git curl jq python3; do
    command -v "${_bin}" &>/dev/null || _apt "${_bin}"
    ok "${_bin}"
done

# avahi-utils provides avahi-browse; dnsutils provides dig
for _pkg in avahi-utils dnsutils; do
    dpkg -s "${_pkg}" &>/dev/null 2>&1 || _apt "${_pkg}"
    ok "${_pkg}"
done

# Go — try apt first, fall back to go.dev tarball if too old
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

# Ollama — required for AI inference
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
step "6 — Bootstrap discovery (DNS TXT → mDNS)"
# ─────────────────────────────────────────────────────────────────────────────
BOOTSTRAP_ENDPOINT=""

# Primary: DNS TXT record at _vernex._tcp.vernex.net
if command -v dig &>/dev/null; then
    _TXT="$(dig +short TXT _vernex._tcp.vernex.net 2>/dev/null | tr -d '"' | \
            grep 'bootstrap=' | head -1 || true)"
    if [[ -n "${_TXT}" ]]; then
        BOOTSTRAP_ENDPOINT="${_TXT#*bootstrap=}"
        BOOTSTRAP_ENDPOINT="${BOOTSTRAP_ENDPOINT%% *}"
        ok "Bootstrap via DNS TXT: ${BOOTSTRAP_ENDPOINT}"
    fi
fi

# Fallback: mDNS via avahi-browse
if [[ -z "${BOOTSTRAP_ENDPOINT}" ]] && command -v avahi-browse &>/dev/null; then
    warn "DNS TXT lookup returned nothing — trying mDNS (LAN)..."
    # -r = resolve, -t = terminate after browse, -p = parseable output
    _AVAHI="$(timeout 8 avahi-browse -rtp _vernex._tcp 2>/dev/null | grep '^=' | head -1 || true)"
    if [[ -n "${_AVAHI}" ]]; then
        _MDNS_IP="$(echo "${_AVAHI}" | cut -d';' -f8)"
        _MDNS_PORT="$(echo "${_AVAHI}" | cut -d';' -f9)"
        if [[ -n "${_MDNS_IP}" && -n "${_MDNS_PORT}" ]]; then
            BOOTSTRAP_ENDPOINT="${_MDNS_IP}:${_MDNS_PORT}"
            ok "Bootstrap via mDNS: ${BOOTSTRAP_ENDPOINT}"
        fi
    fi
fi

if [[ -z "${BOOTSTRAP_ENDPOINT}" ]]; then
    warn "No bootstrap found via DNS or mDNS."
    warn "Node will start standalone. Re-run after a bootstrap is reachable."
fi

BOOTSTRAP_IP=""
BOOTSTRAP_API_PORT="${API_PORT}"
if [[ -n "${BOOTSTRAP_ENDPOINT}" ]]; then
    BOOTSTRAP_IP="${BOOTSTRAP_ENDPOINT%%:*}"
    BOOTSTRAP_API_PORT="${BOOTSTRAP_ENDPOINT##*:}"
fi

# ─────────────────────────────────────────────────────────────────────────────
step "7 — Write node configuration"
# ─────────────────────────────────────────────────────────────────────────────
mkdir -p "${INSTALL_DIR}/config"
if [[ -f "${NODE_CONFIG}" ]]; then
    ok "Config exists — preserving: ${NODE_CONFIG}"
    # Patch in bootstrap peer if discovered and not already present
    if [[ -n "${BOOTSTRAP_IP}" ]]; then
        python3 - <<PYEOF 2>/dev/null || warn "Could not patch bootstrap into existing config"
import json, os, sys
path = "${NODE_CONFIG}"
try:
    with open(path) as f:
        cfg = json.load(f)
except Exception as e:
    print(f"  [!] Could not read config: {e}", file=sys.stderr)
    sys.exit(0)
peers = cfg.get("peer_nodes", [])
bootstrap_url = "http://${BOOTSTRAP_IP}:11434"
if not any(p.get("base_url") == bootstrap_url for p in peers):
    peers.append({"name": "bootstrap", "base_url": bootstrap_url})
    cfg["peer_nodes"] = peers
    with open(path, "w") as f:
        json.dump(cfg, f, indent=2)
        f.write("\n")
    print("  → added bootstrap peer to config")
else:
    print("  → bootstrap peer already in config")
PYEOF
    fi
else
    _PEERS="[]"
    if [[ -n "${BOOTSTRAP_IP}" ]]; then
        _PEERS="[{\"name\":\"bootstrap\",\"base_url\":\"http://${BOOTSTRAP_IP}:11434\"}]"
    fi
    cat > "${NODE_CONFIG}" <<JSONEOF
{
  "node_id": "",
  "p2p_port": ${P2P_PORT},
  "api_port": ${API_PORT},
  "personal_partition": 70,
  "social_partition": 30,
  "peer_nodes": ${_PEERS}
}
JSONEOF
    chmod 600 "${NODE_CONFIG}"
    ok "Config written: ${NODE_CONFIG}"
fi

# ─────────────────────────────────────────────────────────────────────────────
step "8 — Optional: CA enrollment"
# ─────────────────────────────────────────────────────────────────────────────
if [[ -f "${INSTALL_DIR}/config/node.crt" ]]; then
    ok "Node cert present — skipping enrollment"
elif [[ -n "${_ENROLL_TOKEN}" ]] && [[ -n "${BOOTSTRAP_IP}" ]]; then
    _BOOTSTRAP_API="https://${BOOTSTRAP_IP}:${BOOTSTRAP_API_PORT}"
    "${BINARY_DEST}" ca enroll --bootstrap "${_BOOTSTRAP_API}" --token "${_ENROLL_TOKEN}" && \
        ok "Enrolled — cert saved to ${INSTALL_DIR}/config/node.crt" || \
        warn "Enrollment failed. Retry: vernex-node ca enroll --bootstrap ${_BOOTSTRAP_API} --token '<json>'"
elif [[ -n "${BOOTSTRAP_IP}" ]]; then
    _BOOTSTRAP_API="https://${BOOTSTRAP_IP}:${BOOTSTRAP_API_PORT}"
    warn "Enrollment skipped — no token provided. Run later:"
    warn "  vernex-node ca enroll --bootstrap ${_BOOTSTRAP_API} --token '<json>'"
else
    warn "No bootstrap — enrollment skipped (no bootstrap found)"
fi

# ─────────────────────────────────────────────────────────────────────────────
step "9 — Systemd service"
# ─────────────────────────────────────────────────────────────────────────────
_SVC="[Unit]
Description=Vernex Protocol Node Daemon
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

_EXISTING=""
[[ -f "${DAEMON_SVC}" ]] && _EXISTING="$(cat "${DAEMON_SVC}")"
if [[ "${_SVC}" == "${_EXISTING}" ]]; then
    ok "vernex-daemon.service already up to date"
else
    printf '%s\n' "${_SVC}" | sudo tee "${DAEMON_SVC}" > /dev/null
    ok "vernex-daemon.service written"
fi

sudo systemctl daemon-reload && ok "systemd daemon reloaded"

# ─────────────────────────────────────────────────────────────────────────────
step "10 — Enable and start"
# ─────────────────────────────────────────────────────────────────────────────
sudo systemctl enable vernex-daemon
sudo systemctl restart vernex-daemon
sleep 2

# ─────────────────────────────────────────────────────────────────────────────
step "11 — Verify service is active"
# ─────────────────────────────────────────────────────────────────────────────
_ACTIVE=false
for _i in 1 2 3 4 5; do
    if systemctl is-active --quiet vernex-daemon 2>/dev/null; then
        _ACTIVE=true; break
    fi
    sleep 2
done
if ${_ACTIVE}; then
    ok "vernex-daemon is active"
else
    warn "vernex-daemon not active after 10s"
    warn "Check: sudo journalctl -u vernex-daemon -n 30"
fi

# ─────────────────────────────────────────────────────────────────────────────
step "12 — Clock verification log"
# ─────────────────────────────────────────────────────────────────────────────
_CLOCK="$(sudo journalctl -u vernex-daemon -n 60 2>/dev/null | \
          grep -E 'Clock|drift|ntp|NTP|\[✓\] clock|\[!\] clock' | tail -5 || true)"
if [[ -n "${_CLOCK}" ]]; then
    ok "Clock log entries:"
    echo "${_CLOCK}"
else
    warn "No clock log yet — daemon may still be starting"
fi

# ─────────────────────────────────────────────────────────────────────────────
# Summary
# ─────────────────────────────────────────────────────────────────────────────
_NODE_ID_FINAL="$(python3 -c \
    "import json; print(json.load(open('${NODE_CONFIG}')).get('node_id','(pending)'))" \
    2>/dev/null || echo '(pending)')"

echo
echo -e "${BOLD}${GREEN}╔════════════════════════════════════════════╗${RESET}"
echo -e "${BOLD}${GREEN}║     VERNEX COMPUTE NODE SETUP COMPLETE     ║${RESET}"
echo -e "${BOLD}${GREEN}╚════════════════════════════════════════════╝${RESET}"
echo
echo -e "${BOLD}Node ID:     ${RESET}${_NODE_ID_FINAL}"
echo -e "${BOLD}Install dir: ${RESET}${INSTALL_DIR}"
echo -e "${BOLD}Binary:      ${RESET}${BINARY_DEST}"
echo -e "${BOLD}Bootstrap:   ${RESET}${BOOTSTRAP_ENDPOINT:-"(none — re-run after bootstrap is reachable)"}"
echo -e "${BOLD}API:         ${RESET}https://localhost:${API_PORT}"
echo
echo -e "${BOLD}Service:${RESET}"
_st="$(systemctl is-active vernex-daemon 2>/dev/null || echo inactive)"
[[ "${_st}" == "active" ]] && \
    echo -e "  ${GREEN}●${RESET} vernex-daemon: ${GREEN}${_st}${RESET}" || \
    echo -e "  ${RED}●${RESET} vernex-daemon: ${RED}${_st}${RESET}"
echo
echo -e "${CYAN}Logs:   sudo journalctl -u vernex-daemon -f${RESET}"
echo -e "${CYAN}Status: curl -sk https://localhost:${API_PORT}/status | jq .${RESET}"
if ! ollama list 2>/dev/null | grep -q '.'; then
    echo
    warn "No Ollama models found. Pull one to enable inference:"
    warn "  ollama pull mistral:7b-instruct-q4_K_M"
fi
echo

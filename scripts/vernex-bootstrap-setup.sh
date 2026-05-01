#!/usr/bin/env bash
# Vernex Protocol — bootstrap node provisioning
# Provisions a fresh Pop!_OS or Ubuntu 24.04 system as a Vernex bootstrap node.
# Requires a public IP (or port-forwarded 7701/7700). Run as a normal user.
# Idempotent: safe to re-run after partial failures.

set -euo pipefail

# ── colours ────────────────────────────────────────────────────────────────────
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'
CYAN='\033[0;36m'; BOLD='\033[1m'; RESET='\033[0m'

ok()   { echo -e "${GREEN}  ✓ $*${RESET}"; }
info() { echo -e "${CYAN}  → $*${RESET}"; }
warn() { echo -e "${YELLOW}  ⚠ $*${RESET}"; }
err()  { echo -e "${RED}  ✗ $*${RESET}" >&2; }
step() { echo -e "\n${BOLD}${CYAN}━━ $* ━━${RESET}"; }
die()  { err "$*"; exit 1; }

# ── constants ──────────────────────────────────────────────────────────────────
VERNEX_USER="${USER}"
VERNEX_HOME="${HOME}/vernex"
REPO_URL="git@github.com:SuperSleeper/vernex-protocol.git"
GO_MIN_VERSION="1.21"
GO_INSTALL_VERSION="1.22.5"
GO_TARBALL="go${GO_INSTALL_VERSION}.linux-amd64.tar.gz"
GO_URL="https://go.dev/dl/${GO_TARBALL}"
DAEMON_SERVICE_DEST="/etc/systemd/system/vernex-daemon.service"
CONFIG_DIR="${VERNEX_HOME}/config"
NODE_CONFIG="${CONFIG_DIR}/node.json"
TOKEN_FILE="${CONFIG_DIR}/enrollment_tokens.txt"
API_PORT=7701
P2P_PORT=7700
TOKEN_COUNT=5
NETWORK_ID="vernex-mainnet"

# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
# 1. PRE-FLIGHT CHECKS
# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
step "1 — Preflight checks"

[[ "${EUID}" -eq 0 ]] && die "Do not run as root — run as your regular user (sudo invoked internally)."
info "Running as ${VERNEX_USER} on $(uname -n)"

if [[ -f /etc/os-release ]]; then
    . /etc/os-release
    ok "OS: ${PRETTY_NAME:-unknown}"
else
    warn "Cannot detect OS — proceeding anyway"
fi

for TOOL in curl git python3 jq; do
    if ! command -v "${TOOL}" &>/dev/null; then
        info "Installing missing tool: ${TOOL}"
        sudo apt-get install -y "${TOOL}" || die "Cannot install ${TOOL}"
    fi
    ok "${TOOL} available"
done

sudo -n true 2>/dev/null || warn "sudo requires a password for some steps — you may be prompted."

echo
read -rp "  Is this a NEW bootstrap node (not previously set up)? (y/N): " _CONFIRM
case "${_CONFIRM}" in
    [yY]|[yY][eE][sS]) ;;
    *)
        err "This script is for bootstrap nodes only."
        err "For compute nodes run: bash scripts/vernex-node-setup.sh"
        exit 1
        ;;
esac

info "Detecting public IP via api.ipify.org"
PUBLIC_IP="$(curl -s --connect-timeout 5 https://api.ipify.org 2>/dev/null || echo '')"
if [[ -z "${PUBLIC_IP}" ]]; then
    warn "Could not detect public IP — a reachable public IP is required for a bootstrap node"
    PUBLIC_IP="<your-public-ip>"
else
    ok "Public IP: ${PUBLIC_IP}"
fi

# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
# 2. GO INSTALLATION
# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
step "2 — Go installation"

_go_version_ok() {
    local ver
    ver="$(go version 2>/dev/null | awk '{print $3}' | sed 's/go//')"
    [[ -z "${ver}" ]] && return 1
    local need_maj need_min cur_maj cur_min
    IFS='.' read -r need_maj need_min _ <<< "${GO_MIN_VERSION}"
    IFS='.' read -r cur_maj cur_min  _ <<< "${ver}"
    (( cur_maj > need_maj )) && return 0
    (( cur_maj == need_maj && cur_min >= need_min )) && return 0
    return 1
}

export PATH="${PATH}:/usr/local/go/bin"
if _go_version_ok; then
    ok "Go already installed: $(go version)"
else
    info "Installing Go ${GO_INSTALL_VERSION} to /usr/local/go"
    cd /tmp
    if [[ ! -f "${GO_TARBALL}" ]]; then
        info "Downloading ${GO_URL}"
        curl -fsSL -o "${GO_TARBALL}" "${GO_URL}"
    fi
    sudo rm -rf /usr/local/go
    sudo tar -C /usr/local -xzf "${GO_TARBALL}"
    PROFILE_LINE='export PATH="${PATH}:/usr/local/go/bin"'
    for rc in "${HOME}/.bashrc" "${HOME}/.profile"; do
        grep -qF '/usr/local/go/bin' "${rc}" 2>/dev/null || echo "${PROFILE_LINE}" >> "${rc}"
    done
    ok "Go ${GO_INSTALL_VERSION} installed"
fi

# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
# 3. CLONE / UPDATE REPO + BUILD
# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
step "3 — Repository + build"

mkdir -p "${VERNEX_HOME}/"{config,logs,scripts,daemon,dashboard}

if [[ -d "${VERNEX_HOME}/.git" ]]; then
    info "Repo already cloned — pulling latest"
    git -C "${VERNEX_HOME}" pull origin main && ok "Repo up to date"
else
    info "Cloning ${REPO_URL} into ${VERNEX_HOME}"
    TMP_CLONE="$(mktemp -d)"
    git clone "${REPO_URL}" "${TMP_CLONE}"
    cp -rn "${TMP_CLONE}/." "${VERNEX_HOME}/" 2>/dev/null || true
    rm -rf "${TMP_CLONE}"
    ok "Repo cloned"
fi

info "Building vernex-node"
cd "${VERNEX_HOME}/daemon"
BUILD_TIME="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
if go build -ldflags "-X vernex/daemon/ca.BuildTime=${BUILD_TIME}" -o vernex-node . 2>&1; then
    ok "Daemon built: ${VERNEX_HOME}/daemon/vernex-node"
else
    # Retry without the ldflags if the variable doesn't exist yet
    if go build -o vernex-node . 2>&1; then
        ok "Daemon built: ${VERNEX_HOME}/daemon/vernex-node"
    else
        die "go build failed — check output above"
    fi
fi

# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
# 4. BOOTSTRAP CONFIG
# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
step "4 — Bootstrap configuration"

if [[ -f "${NODE_CONFIG}" ]]; then
    info "config/node.json exists — ensuring is_bootstrap: true"
    python3 - <<PYEOF
import json
path = "${NODE_CONFIG}"
with open(path) as f:
    cfg = json.load(f)
if not cfg.get("is_bootstrap"):
    cfg["is_bootstrap"] = True
    with open(path, "w") as f:
        json.dump(cfg, f, indent=2)
        f.write("\n")
    print("  → set is_bootstrap: true")
else:
    print("  → is_bootstrap already true")
PYEOF
    ok "config/node.json updated"
else
    info "Creating default bootstrap config/node.json"
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
    ok "config/node.json created (is_bootstrap: true)"
fi

# Run daemon briefly to generate keypairs and persist node_id on first boot
if [[ ! -f "${CONFIG_DIR}/node.key" ]]; then
    info "Generating node keypairs (running daemon for 3 seconds)"
    cd "${VERNEX_HOME}/daemon"
    timeout 3 ./vernex-node 2>/dev/null || true
    ok "Keypairs generated"
fi

NODE_ID="$(python3 -c "import json; d=json.load(open('${NODE_CONFIG}')); print(d.get('node_id',''))" 2>/dev/null || echo '')"
[[ -z "${NODE_ID}" ]] && warn "Node ID not yet visible in config — CA init will still proceed"
[[ -n "${NODE_ID}" ]] && ok "Node ID: ${NODE_ID}"

# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
# 5. CA INITIALISATION
# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
step "5 — Certificate Authority initialisation"

cd "${VERNEX_HOME}/daemon"

if [[ -f "${CONFIG_DIR}/root.crt" ]]; then
    ok "Root CA already exists (config/root.crt) — skipping ca init"
else
    info "Generating root CA"
    ./vernex-node ca init || die "ca init failed"
    ok "Root CA created: config/root.{key,crt}"
fi

if [[ -f "${CONFIG_DIR}/intermediate.crt" ]]; then
    ok "Intermediate CA already exists — skipping ca init-intermediate"
else
    if [[ ! -f "${CONFIG_DIR}/root.key" ]]; then
        err "[!] root.key not found — cannot create intermediate CA"
        err "    Run 'vernex-node ca init' first to create the root CA"
        exit 1
    fi
    info "Generating intermediate CA"
    ./vernex-node ca init-intermediate || die "ca init-intermediate failed"
    ok "Intermediate CA created: config/intermediate.{key,csr,crt}"
fi

# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
# 6. PEER CA SYNC (optional — join existing network as secondary bootstrap)
# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
step "6 — Peer CA sync (optional)"

echo
echo -e "${CYAN}  If this bootstrap node is joining an existing Vernex network, enter the${RESET}"
echo -e "${CYAN}  URL of an existing bootstrap node (e.g. https://76.244.40.49:7701).${RESET}"
echo -e "${CYAN}  Press ENTER to skip — this node will be the root of a new network.${RESET}"
echo
read -rp "  Existing bootstrap URL (or ENTER to skip): " EXISTING_BOOTSTRAP

if [[ -n "${EXISTING_BOOTSTRAP}" ]]; then
    info "Pulling CA certs from ${EXISTING_BOOTSTRAP}"
    CA_SYNC_RESPONSE="$(curl -sk --connect-timeout 10 "${EXISTING_BOOTSTRAP}/ca-sync" 2>/dev/null || echo '')"
    if [[ -z "${CA_SYNC_RESPONSE}" ]] || ! echo "${CA_SYNC_RESPONSE}" | python3 -c "import json,sys; json.load(sys.stdin)" &>/dev/null; then
        warn "Could not reach ${EXISTING_BOOTSTRAP}/ca-sync — skipping peer CA sync"
    else
        info "Saving CA certs from peer"
        echo "${CA_SYNC_RESPONSE}" | python3 - <<'PYEOF'
import json, sys, os
data = json.load(sys.stdin)
config_dir = os.path.expanduser("~/vernex/config")
saved = []
for key, filename in [("root_cert", "root.crt"), ("intermediate_cert", "intermediate.crt")]:
    val = data.get(key)
    if val:
        path = os.path.join(config_dir, filename)
        if not os.path.exists(path):
            with open(path, "w") as f:
                json.dump(val, f, indent=2)
                f.write("\n")
            saved.append(filename)
        else:
            print(f"  → {filename} already exists — leaving untouched")
if saved:
    print(f"  → saved: {', '.join(saved)}")
PYEOF
        ok "Peer CA sync complete"
    fi
else
    info "Skipped — this node is the root of a new Vernex network"
fi

# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
# 7. GENERATE ENROLLMENT TOKENS
# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
step "7 — Enrollment tokens"

cd "${VERNEX_HOME}/daemon"

if [[ -f "${TOKEN_FILE}" ]]; then
    EXISTING_COUNT="$(grep -c '"network_id"' "${TOKEN_FILE}" 2>/dev/null || echo 0)"
    ok "Enrollment tokens already exist: ${TOKEN_FILE} (${EXISTING_COUNT} token(s))"
    info "Delete ${TOKEN_FILE} and re-run to regenerate"
else
    info "Generating ${TOKEN_COUNT} enrollment tokens for network: ${NETWORK_ID}"
    {
        echo "# Vernex enrollment tokens — generated $(date -u +%Y-%m-%dT%H:%M:%SZ)"
        echo "# Each token is single-use, 30-day validity."
        echo "# Share one token per new node operator."
        echo "# Operator command: vernex-node ca enroll --bootstrap https://${PUBLIC_IP}:${API_PORT} --token '<paste token JSON below>'"
        echo
        for i in $(seq 1 "${TOKEN_COUNT}"); do
            echo "# ── Token ${i} ──────────────────────────────────────────────────────────────"
            ./vernex-node ca token "${NETWORK_ID}" 2>/dev/null | python3 -c "
import sys, json
text = sys.stdin.read()
start = text.find('{')
end = text.rfind('}') + 1
if start >= 0 and end > start:
    print(json.dumps(json.loads(text[start:end]), indent=2))
" || warn "Token ${i} generation failed"
            echo
        done
    } > "${TOKEN_FILE}"
    chmod 600 "${TOKEN_FILE}"
    ok "${TOKEN_COUNT} enrollment tokens saved to ${TOKEN_FILE}"
fi

# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
# 8. SYSTEMD SERVICE
# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
step "8 — Systemd daemon service"

DAEMON_SERVICE_CONTENT="[Unit]
Description=Vernex Protocol Bootstrap Node
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=${VERNEX_USER}
WorkingDirectory=${VERNEX_HOME}/daemon
ExecStart=${VERNEX_HOME}/daemon/vernex-node
Restart=always
RestartSec=5
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target"

EXISTING_SERVICE=""
[[ -f "${DAEMON_SERVICE_DEST}" ]] && EXISTING_SERVICE="$(cat "${DAEMON_SERVICE_DEST}")"
if [[ "${DAEMON_SERVICE_CONTENT}" == "${EXISTING_SERVICE}" ]]; then
    ok "vernex-daemon.service already up to date"
else
    info "Installing ${DAEMON_SERVICE_DEST}"
    echo "${DAEMON_SERVICE_CONTENT}" | sudo tee "${DAEMON_SERVICE_DEST}" > /dev/null
    ok "vernex-daemon.service installed"
fi

sudo systemctl daemon-reload
ok "systemd reloaded"

# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
# 9. START DAEMON + HEALTH CHECK
# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
step "9 — Start daemon + health check"

info "Enabling and starting vernex-daemon"
sudo systemctl enable vernex-daemon
sudo systemctl restart vernex-daemon

HEALTHY=false
for i in $(seq 1 5); do
    sleep 3
    HTTP_CODE="$(curl -sk -o /dev/null -w '%{http_code}' "https://localhost:${API_PORT}/health" 2>/dev/null || echo '000')"
    if [[ "${HTTP_CODE}" == "200" ]]; then
        HEALTHY=true
        break
    fi
    info "Waiting for daemon to start (attempt ${i}/5, HTTP ${HTTP_CODE})"
done

if ${HEALTHY}; then
    ok "Daemon healthy: https://localhost:${API_PORT}/health"
    LIVE_ID="$(curl -sk "https://localhost:${API_PORT}/status" 2>/dev/null | python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('node_id',''))" 2>/dev/null || true)"
    [[ -n "${LIVE_ID}" ]] && NODE_ID="${LIVE_ID}"
    ok "Running node ID: ${NODE_ID}"
else
    warn "Daemon did not respond within 15s — check: journalctl -u vernex-daemon -n 30"
fi

# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
# 10. SUMMARY
# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
echo
echo -e "${BOLD}${GREEN}╔══════════════════════════════════════════╗${RESET}"
echo -e "${BOLD}${GREEN}║   VERNEX BOOTSTRAP NODE SETUP COMPLETE   ║${RESET}"
echo -e "${BOLD}${GREEN}╚══════════════════════════════════════════╝${RESET}"
echo
echo -e "${BOLD}Node ID:    ${RESET}${NODE_ID:-"(see config/node.json)"}"
echo -e "${BOLD}Public IP:  ${RESET}${PUBLIC_IP}"
echo -e "${BOLD}API URL:    ${RESET}https://${PUBLIC_IP}:${API_PORT}"
echo -e "${BOLD}P2P Port:   ${RESET}${P2P_PORT}"
echo
echo -e "${BOLD}Firewall — ensure these ports are open:${RESET}"
echo -e "  sudo ufw allow ${API_PORT}/tcp   ${CYAN}# HTTPS API (peer registration, heartbeat, enrollment)${RESET}"
echo -e "  sudo ufw allow ${P2P_PORT}/tcp   ${CYAN}# P2P TCP${RESET}"
echo -e "  sudo ufw allow ${P2P_PORT}/udp   ${CYAN}# UDP hole punching${RESET}"
echo
echo -e "${BOLD}Next steps:${RESET}"
echo -e "  1. Open ports ${API_PORT} and ${P2P_PORT} in your router/firewall"
echo -e "  2. Share your bootstrap URL with node operators: ${CYAN}https://${PUBLIC_IP}:${API_PORT}${RESET}"
echo -e "  3. Give each new node operator one enrollment token from:"
echo -e "     ${CYAN}${TOKEN_FILE}${RESET}"
echo -e "  4. After nodes enroll, check: ${CYAN}curl -sk https://localhost:${API_PORT}/peers | jq .${RESET}"
echo -e "  5. Add to BOOTSTRAP_NODES in scripts/vernex-node-setup.sh once this node is stable"
echo
echo -e "${BOLD}${CYAN}━━ Enrollment tokens ━━${RESET}"
echo
if [[ -f "${TOKEN_FILE}" ]]; then
    cat "${TOKEN_FILE}"
else
    warn "Token file not found: ${TOKEN_FILE}"
fi
echo

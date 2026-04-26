#!/usr/bin/env bash
# Vernex Protocol — automated node provisioning
# Idempotent: safe to run multiple times on the same machine.
# Run as a normal user (sudo will be invoked internally where needed).

set -euo pipefail

# ── colours ────────────────────────────────────────────────────────────────────
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'
CYAN='\033[0;36m'; BOLD='\033[1m'; RESET='\033[0m'

ok()   { echo -e "${GREEN}  ✓ $*${RESET}"; }
info() { echo -e "${CYAN}  → $*${RESET}"; }
warn() { echo -e "${YELLOW}  ⚠ $*${RESET}"; }
err()  { echo -e "${RED}  ✗ $*${RESET}" >&2; }
step() { echo -e "\n${BOLD}${CYAN}━━ $* ━━${RESET}"; }

die() { err "$*"; exit 1; }

# ── constants ──────────────────────────────────────────────────────────────────
VERNEX_USER="${USER}"
VERNEX_HOME="${HOME}/vernex"
REPO_URL="git@github.com:SuperSleeper/vernex-protocol.git"
GO_MIN_VERSION="1.21"
GO_INSTALL_VERSION="1.22.5"
GO_TARBALL="go${GO_INSTALL_VERSION}.linux-amd64.tar.gz"
GO_URL="https://go.dev/dl/${GO_TARBALL}"
OLLAMA_OVERRIDE_DIR="/etc/systemd/system/ollama.service.d"
POLKIT_RULE_DEST="/etc/polkit-1/rules.d/90-vernex-inhibit.rules"
DAEMON_SERVICE_DEST="/etc/systemd/system/vernex-daemon.service"
DASHBOARD_SERVICE_DEST="/etc/systemd/system/vernex-dashboard.service"
MODELS=("mistral:7b-instruct-q4_K_M" "llama3.1:8b-instruct-q4_K_M")
VERSION="v0.7.0"

# ── Bootstrap nodes ────────────────────────────────────────────────────────────
# Add entries here as the network grows. Format: "IP:PORT" (port = API port 7701).
# New nodes receive these as pre-configured peers and send a trust request to each.
BOOTSTRAP_NODES=(
  "172.17.0.132:7701"
)
BOOTSTRAP_PUBKEYS=(
  "prAB8hQJaXoWoT+WO7jbCKBT0TAJPMLjiE4QlOr2D0I="
)

# ── preflight ──────────────────────────────────────────────────────────────────
step "Preflight checks"

[[ "${EUID}" -eq 0 ]] && die "Do not run this script as root. Run as your regular user."
info "Running as ${VERNEX_USER}"

command -v sudo &>/dev/null || die "sudo not found — install it first"
sudo -n true 2>/dev/null || {
    warn "sudo requires a password for some steps. You may be prompted."
}
ok "Preflight passed"

# ── GPU detection ──────────────────────────────────────────────────────────────
step "GPU detection"

GPU_TYPE="none"
if lspci 2>/dev/null | grep -qi "nvidia"; then
    GPU_TYPE="nvidia"
elif lspci 2>/dev/null | grep -qi "amd\|radeon\|advanced micro"; then
    GPU_TYPE="amd"
fi
info "Detected GPU type: ${GPU_TYPE}"

case "${GPU_TYPE}" in
  nvidia)
    if ! command -v nvidia-smi &>/dev/null || ! nvidia-smi &>/dev/null; then
        warn "NVIDIA GPU found but drivers missing — installing nvidia-driver-535"
        sudo apt-get update -qq
        sudo apt-get install -y nvidia-driver-535 nvidia-utils-535
        ok "NVIDIA drivers installed (reboot may be required)"
    else
        ok "NVIDIA drivers already present ($(nvidia-smi --query-gpu=driver_version --format=csv,noheader 2>/dev/null | head -1))"
    fi
    ;;
  amd)
    if ! command -v rocm-smi &>/dev/null; then
        warn "AMD GPU found — installing ROCm (rocm-hip-libraries)"
        sudo apt-get update -qq
        # ROCm on Ubuntu/Pop!_OS via the official apt repo
        wget -q -O /tmp/amdgpu-install.deb \
            "https://repo.radeon.com/amdgpu-install/6.1.3/ubuntu/jammy/amdgpu-install_6.1.60103-1_all.deb"
        sudo apt-get install -y /tmp/amdgpu-install.deb
        sudo amdgpu-install -y --usecase=rocm --no-32
        ok "AMD ROCm installed"
    else
        ok "AMD ROCm already present"
    fi
    ;;
  none)
    warn "No discrete GPU detected — Ollama will run on CPU"
    ;;
esac

# ── Go installation ────────────────────────────────────────────────────────────
step "Go installation"

_go_version_ok() {
    local ver
    ver="$(go version 2>/dev/null | awk '{print $3}' | sed 's/go//')"
    [[ -z "${ver}" ]] && return 1
    # compare major.minor only
    local need_maj need_min cur_maj cur_min
    IFS='.' read -r need_maj need_min _ <<< "${GO_MIN_VERSION}"
    IFS='.' read -r cur_maj cur_min  _ <<< "${ver}"
    (( cur_maj > need_maj )) && return 0
    (( cur_maj == need_maj && cur_min >= need_min )) && return 0
    return 1
}

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

    # Add to PATH for this session if not already there
    export PATH="${PATH}:/usr/local/go/bin"

    # Persist for future sessions
    PROFILE_LINE='export PATH="${PATH}:/usr/local/go/bin"'
    for rc in "${HOME}/.bashrc" "${HOME}/.profile"; do
        grep -qF '/usr/local/go/bin' "${rc}" 2>/dev/null || echo "${PROFILE_LINE}" >> "${rc}"
    done
    ok "Go ${GO_INSTALL_VERSION} installed"
fi

# ── Ollama ─────────────────────────────────────────────────────────────────────
step "Ollama installation"

if ! command -v ollama &>/dev/null; then
    info "Installing Ollama"
    curl -fsSL https://ollama.com/install.sh | sh
    ok "Ollama installed"
else
    ok "Ollama already installed: $(ollama --version 2>/dev/null || echo 'version unknown')"
fi

# ── Ollama systemd override (bind to 0.0.0.0) ─────────────────────────────────
step "Ollama network binding"

OVERRIDE_CONTENT="[Service]
Environment=\"OLLAMA_HOST=0.0.0.0\""

if [[ -f "${OLLAMA_OVERRIDE_DIR}/override.conf" ]] && \
   grep -q "OLLAMA_HOST=0.0.0.0" "${OLLAMA_OVERRIDE_DIR}/override.conf"; then
    ok "Ollama override already configured"
else
    info "Creating ${OLLAMA_OVERRIDE_DIR}/override.conf"
    sudo mkdir -p "${OLLAMA_OVERRIDE_DIR}"
    echo "${OVERRIDE_CONTENT}" | sudo tee "${OLLAMA_OVERRIDE_DIR}/override.conf" > /dev/null
    sudo systemctl daemon-reload
    sudo systemctl restart ollama || warn "Could not restart Ollama (may not be running yet)"
    ok "Ollama bound to 0.0.0.0"
fi

# ── Pull Ollama models ─────────────────────────────────────────────────────────
step "Pulling Ollama models"

# Ensure Ollama is running before pulling
if ! systemctl is-active --quiet ollama 2>/dev/null; then
    info "Starting Ollama service"
    sudo systemctl enable --now ollama || warn "Could not enable Ollama service"
    sleep 3
fi

for MODEL in "${MODELS[@]}"; do
    if ollama list 2>/dev/null | grep -q "$(echo "${MODEL}" | cut -d: -f1)"; then
        ok "Model already present: ${MODEL}"
    else
        info "Pulling ${MODEL} (this may take a while)"
        ollama pull "${MODEL}" && ok "Pulled ${MODEL}" || warn "Failed to pull ${MODEL} — skipping"
    fi
done

# ── Directory structure ────────────────────────────────────────────────────────
step "Vernex directory structure"

for DIR in "${VERNEX_HOME}" "${VERNEX_HOME}/config" "${VERNEX_HOME}/logs" \
           "${VERNEX_HOME}/scripts" "${VERNEX_HOME}/daemon" "${VERNEX_HOME}/dashboard"; do
    if [[ ! -d "${DIR}" ]]; then
        mkdir -p "${DIR}"
        ok "Created ${DIR}"
    else
        ok "Exists: ${DIR}"
    fi
done

# ── Clone or update repo ───────────────────────────────────────────────────────
step "Vernex repository"

if [[ -d "${VERNEX_HOME}/.git" ]]; then
    info "Repo already cloned — pulling latest"
    git -C "${VERNEX_HOME}" pull origin main && ok "Repo up to date"
else
    info "Cloning ${REPO_URL} into ${VERNEX_HOME}"
    # Clone into a temp dir then move contents so we don't clobber existing dirs
    TMP_CLONE="$(mktemp -d)"
    git clone "${REPO_URL}" "${TMP_CLONE}"
    # Copy everything including hidden files, skip existing
    cp -rn "${TMP_CLONE}/." "${VERNEX_HOME}/" 2>/dev/null || true
    rm -rf "${TMP_CLONE}"
    ok "Repo cloned"
fi

# ── Default config/node.json ───────────────────────────────────────────────────
step "Node configuration"

NODE_CONFIG="${VERNEX_HOME}/config/node.json"
if [[ -f "${NODE_CONFIG}" ]]; then
    ok "config/node.json already exists — leaving untouched"
else
    info "Creating default config/node.json with bootstrap peers"

    # Build peer_nodes JSON array from BOOTSTRAP_NODES / BOOTSTRAP_PUBKEYS
    _PEER_JSON="["
    _SEP=""
    for _i in "${!BOOTSTRAP_NODES[@]}"; do
        _HOST="${BOOTSTRAP_NODES[$_i]%%:*}"
        _PUBKEY="${BOOTSTRAP_PUBKEYS[$_i]:-}"
        _IDX=$((_i + 1))
        _PEER_JSON+="${_SEP}
    {\"name\": \"bootstrap-${_IDX}\", \"base_url\": \"http://${_HOST}:11434\", \"public_key\": \"${_PUBKEY}\"}"
        _SEP=","
    done
    _PEER_JSON+="
  ]"

    cat > "${NODE_CONFIG}" <<JSONEOF
{
  "node_id": "",
  "p2p_port": 7700,
  "api_port": 7701,
  "personal_partition": 70,
  "social_partition": 30,
  "peer_nodes": ${_PEER_JSON}
}
JSONEOF
    chmod 600 "${NODE_CONFIG}"
    if [[ ${#BOOTSTRAP_NODES[@]} -gt 0 ]]; then
        ok "Default config/node.json created with ${#BOOTSTRAP_NODES[@]} bootstrap peer(s)"
    else
        ok "Default config/node.json created (no bootstrap peers configured)"
    fi
fi

# ── Build daemon ───────────────────────────────────────────────────────────────
step "Building Vernex daemon"

export PATH="${PATH}:/usr/local/go/bin"
cd "${VERNEX_HOME}/daemon"
if go build -o vernex-node . 2>&1; then
    ok "Daemon built successfully"
else
    die "go build failed — check output above"
fi

# ── Dashboard Python venv ──────────────────────────────────────────────────────
step "Dashboard Python environment"

VENV_DIR="${VERNEX_HOME}/dashboard/venv"
REQ_FILE="${VERNEX_HOME}/dashboard/requirements.txt"

if ! command -v python3 &>/dev/null; then
    info "Installing python3"
    sudo apt-get install -y python3 python3-venv python3-pip
fi

if [[ ! -d "${VENV_DIR}" ]]; then
    info "Creating Python venv"
    python3 -m venv "${VENV_DIR}"
    ok "Venv created"
else
    ok "Venv already exists"
fi

if [[ -f "${REQ_FILE}" ]]; then
    info "Installing Python dependencies"
    "${VENV_DIR}/bin/pip" install -q --upgrade pip
    "${VENV_DIR}/bin/pip" install -q -r "${REQ_FILE}"
    ok "Python dependencies installed"
else
    warn "requirements.txt not found — skipping pip install"
fi

# ── Polkit rule ────────────────────────────────────────────────────────────────
step "Polkit inhibitor rule"

SRC_RULE="${VERNEX_HOME}/scripts/90-vernex-inhibit.rules"
if [[ ! -f "${SRC_RULE}" ]]; then
    warn "Source polkit rule not found at ${SRC_RULE} — skipping"
else
    # Substitute actual username into the rule
    RULE_CONTENT="$(sed "s/ericgeer/${VERNEX_USER}/g" "${SRC_RULE}")"
    if [[ -f "${POLKIT_RULE_DEST}" ]] && \
       grep -q "${VERNEX_USER}" "${POLKIT_RULE_DEST}" 2>/dev/null; then
        ok "Polkit rule already installed"
    else
        info "Installing polkit rule to ${POLKIT_RULE_DEST}"
        echo "${RULE_CONTENT}" | sudo tee "${POLKIT_RULE_DEST}" > /dev/null
        ok "Polkit rule installed"
    fi
fi

# ── Systemd service files ──────────────────────────────────────────────────────
step "Systemd service files"

_install_service() {
    local src="$1" dest="$2" name="$3"
    if [[ ! -f "${src}" ]]; then
        warn "Source service file not found: ${src} — skipping ${name}"
        return
    fi
    local content
    content="$(sed "s|ericgeer|${VERNEX_USER}|g; s|/home/ericgeer|${HOME}|g" "${src}")"
    local current=""
    [[ -f "${dest}" ]] && current="$(cat "${dest}")"
    if [[ "${content}" == "${current}" ]]; then
        ok "${name} service already up to date"
    else
        info "Installing ${dest}"
        echo "${content}" | sudo tee "${dest}" > /dev/null
        ok "${name} service installed"
    fi
}

_install_service "${VERNEX_HOME}/scripts/vernex-daemon.service"    "${DAEMON_SERVICE_DEST}"    "vernex-daemon"
_install_service "${VERNEX_HOME}/scripts/vernex-dashboard.service" "${DASHBOARD_SERVICE_DEST}" "vernex-dashboard"

sudo systemctl daemon-reload

# ── Enable and start services ──────────────────────────────────────────────────
step "Enabling and starting services"

for SVC in vernex-daemon vernex-dashboard; do
    if systemctl is-enabled --quiet "${SVC}" 2>/dev/null; then
        ok "${SVC} already enabled"
    else
        info "Enabling ${SVC}"
        sudo systemctl enable "${SVC}"
        ok "${SVC} enabled"
    fi

    info "Starting ${SVC}"
    if sudo systemctl restart "${SVC}"; then
        sleep 1
        if systemctl is-active --quiet "${SVC}"; then
            ok "${SVC} running"
        else
            warn "${SVC} started but is not active — check: journalctl -u ${SVC} -n 30"
        fi
    else
        warn "Failed to start ${SVC} — check: journalctl -u ${SVC} -n 30"
    fi
done

# ── Read generated node identity ───────────────────────────────────────────────
# Give the daemon a moment to write config/node.json on first boot
sleep 2

NODE_ID="(not yet generated)"
PUB_KEY="(not yet generated)"

if [[ -f "${NODE_CONFIG}" ]]; then
    _id="$(python3 -c "import json,sys; d=json.load(open('${NODE_CONFIG}')); print(d.get('node_id',''))" 2>/dev/null || true)"
    [[ -n "${_id}" ]] && NODE_ID="${_id}"
fi

if [[ -f "${VERNEX_HOME}/config/node.pub" ]]; then
    PUB_KEY="$(cat "${VERNEX_HOME}/config/node.pub")"
fi

# ── Bootstrap trust registration ──────────────────────────────────────────────
step "Bootstrap trust registration"

if [[ ${#BOOTSTRAP_NODES[@]} -eq 0 ]]; then
    info "No bootstrap nodes configured — skipping"
elif [[ "${NODE_ID}" == "(not yet generated)" ]]; then
    warn "Node ID not yet available — skipping trust request (re-run after daemon starts)"
else
    # Determine our outbound IP toward the first bootstrap host
    _FIRST_HOST="${BOOTSTRAP_NODES[0]%%:*}"
    MY_IP="$(python3 -c "
import socket
s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
try:
    s.connect(('${_FIRST_HOST}', 80))
    print(s.getsockname()[0])
except Exception:
    print('localhost')
finally:
    s.close()
" 2>/dev/null || echo 'localhost')"

    PUB_KEY_CLEAN="${PUB_KEY%$'\n'}"   # strip trailing newline from node.pub

    for _BOOTSTRAP in "${BOOTSTRAP_NODES[@]}"; do
        info "Sending trust request to https://${_BOOTSTRAP}"
        _RESULT="$(curl -sk --connect-timeout 5 -w '\n%{http_code}' \
            -X POST "https://${_BOOTSTRAP}/trust-request" \
            -H "Content-Type: application/json" \
            -d "{\"node_id\": \"${NODE_ID}\", \"public_key\": \"${PUB_KEY_CLEAN}\", \"api_url\": \"https://${MY_IP}:7701\"}" \
            2>/dev/null || echo 'curl_failed')"
        if echo "${_RESULT}" | grep -q '"status"'; then
            ok "Trust request sent — awaiting operator approval at https://${_BOOTSTRAP}"
        else
            warn "Bootstrap ${_BOOTSTRAP} unreachable — trust request not sent"
            warn "Retry manually: curl -sk -X POST https://${_BOOTSTRAP}/trust-request \\"
            warn "  -H 'Content-Type: application/json' \\"
            warn "  -d '{\"node_id\":\"${NODE_ID}\",\"public_key\":\"${PUB_KEY_CLEAN}\",\"api_url\":\"https://${MY_IP}:7701\"}'"
        fi
    done
fi

# ── Final status ───────────────────────────────────────────────────────────────
echo
echo -e "${BOLD}${GREEN}╔══════════════════════════════════════╗${RESET}"
echo -e "${BOLD}${GREEN}║   VERNEX NODE SETUP COMPLETE         ║${RESET}"
echo -e "${BOLD}${GREEN}╚══════════════════════════════════════╝${RESET}"
echo
echo -e "${BOLD}Node ID:    ${RESET}${NODE_ID}"
echo -e "${BOLD}Public Key: ${RESET}${PUB_KEY}"
echo -e "            ${YELLOW}(share this with your network operator)${RESET}"
echo -e "${BOLD}Version:    ${RESET}${VERSION}"
echo -e "${BOLD}Dashboard:  ${RESET}http://localhost:5000"
echo -e "${BOLD}API:        ${RESET}https://localhost:7701"
echo
echo -e "${BOLD}Service status:${RESET}"
for SVC in ollama vernex-daemon vernex-dashboard; do
    STATUS="$(systemctl is-active "${SVC}" 2>/dev/null || echo 'inactive')"
    if [[ "${STATUS}" == "active" ]]; then
        echo -e "  ${GREEN}●${RESET} ${SVC}: ${GREEN}${STATUS}${RESET}"
    else
        echo -e "  ${RED}●${RESET} ${SVC}: ${RED}${STATUS}${RESET}"
    fi
done
echo
if [[ "${GPU_TYPE}" == "nvidia" ]]; then
    warn "If NVIDIA drivers were just installed, a reboot may be required before Ollama uses the GPU."
fi
echo -e "${CYAN}Bootstrap trust request sent — approve via the operator's dashboard (http://localhost:5000).${RESET}"
echo -e "${CYAN}To add more peers manually, edit ~/vernex/config/node.json → peer_nodes[].${RESET}"
echo

#!/usr/bin/env bash
# Vernex Protocol — clean uninstall from a node
# Idempotent: safe to run multiple times.
# Does NOT remove Ollama, Go, GPU drivers, or Ollama models.
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

# ── preflight ──────────────────────────────────────────────────────────────────
step "Preflight checks"

[[ "${EUID}" -eq 0 ]] && die "Do not run this script as root. Run as your regular user."
command -v sudo &>/dev/null || die "sudo not found"
ok "Running as ${USER}"

VERNEX_HOME="${HOME}/vernex"

# ── stop and disable services ──────────────────────────────────────────────────
step "Stopping Vernex services"

for SVC in vernex-dashboard vernex-daemon; do
    if systemctl is-active --quiet "${SVC}" 2>/dev/null; then
        info "Stopping ${SVC}"
        sudo systemctl stop "${SVC}"
        ok "${SVC} stopped"
    else
        ok "${SVC} already stopped"
    fi

    if systemctl is-enabled --quiet "${SVC}" 2>/dev/null; then
        info "Disabling ${SVC}"
        sudo systemctl disable "${SVC}"
        ok "${SVC} disabled"
    else
        ok "${SVC} already disabled"
    fi
done

# ── remove service files ───────────────────────────────────────────────────────
step "Removing systemd service files"

REMOVED_SERVICES=()
for F in /etc/systemd/system/vernex-daemon.service \
         /etc/systemd/system/vernex-dashboard.service; do
    if [[ -f "${F}" ]]; then
        info "Removing ${F}"
        sudo rm -f "${F}"
        REMOVED_SERVICES+=("$(basename "${F}")")
        ok "Removed ${F}"
    else
        ok "Already absent: ${F}"
    fi
done

# ── remove polkit rule ─────────────────────────────────────────────────────────
step "Removing polkit rule"

POLKIT_RULE="/etc/polkit-1/rules.d/90-vernex-inhibit.rules"
if [[ -f "${POLKIT_RULE}" ]]; then
    info "Removing ${POLKIT_RULE}"
    sudo rm -f "${POLKIT_RULE}"
    ok "Polkit rule removed"
else
    ok "Polkit rule already absent"
fi

# ── remove Ollama systemd override ────────────────────────────────────────────
step "Restoring Ollama network binding"

OLLAMA_OVERRIDE="/etc/systemd/system/ollama.service.d/override.conf"
OLLAMA_OVERRIDE_DIR="/etc/systemd/system/ollama.service.d"

if [[ -f "${OLLAMA_OVERRIDE}" ]]; then
    info "Removing Ollama override (restoring localhost-only binding)"
    sudo rm -f "${OLLAMA_OVERRIDE}"
    # Remove the directory if now empty
    sudo rmdir "${OLLAMA_OVERRIDE_DIR}" 2>/dev/null || true
    ok "Ollama override removed — Ollama will bind to localhost only after restart"
    if systemctl is-active --quiet ollama 2>/dev/null; then
        info "Restarting Ollama to apply"
        sudo systemctl restart ollama
        ok "Ollama restarted"
    fi
else
    ok "Ollama override already absent"
fi

# ── systemctl daemon-reload ────────────────────────────────────────────────────
info "Reloading systemd daemon"
sudo systemctl daemon-reload
ok "systemd daemon reloaded"

# ── confirm ~/vernex/ removal ─────────────────────────────────────────────────
step "Removing ~/vernex/"

if [[ ! -d "${VERNEX_HOME}" ]]; then
    ok "${VERNEX_HOME} already absent — nothing to remove"
    REMOVED_VERNEX=false
else
    # Show what will be lost
    NODE_ID="(unknown)"
    NODE_CONFIG="${VERNEX_HOME}/config/node.json"
    if [[ -f "${NODE_CONFIG}" ]]; then
        _id="$(python3 -c "import json,sys; d=json.load(open('${NODE_CONFIG}')); print(d.get('node_id',''))" 2>/dev/null || true)"
        [[ -n "${_id}" ]] && NODE_ID="${_id}"
    fi

    echo
    echo -e "${BOLD}${RED}┌─────────────────────────────────────────────────────┐${RESET}"
    echo -e "${BOLD}${RED}│  ⚠  DESTRUCTIVE ACTION — READ CAREFULLY             │${RESET}"
    echo -e "${BOLD}${RED}└─────────────────────────────────────────────────────┘${RESET}"
    echo
    echo -e "  This will permanently delete ${BOLD}${VERNEX_HOME}/${RESET}"
    echo -e "  including your node keys and config."
    echo
    echo -e "  Current node identity: ${BOLD}${NODE_ID}${RESET}"
    echo -e "  ${YELLOW}Your node identity cannot be recovered after this.${RESET}"
    echo
    printf "  Type %bYES%b to confirm: " "${BOLD}${RED}" "${RESET}"
    read -r CONFIRM

    if [[ "${CONFIRM}" != "YES" ]]; then
        warn "Aborted — ~/vernex/ was NOT removed"
        echo
        echo -e "${YELLOW}Systemd services and polkit rule were still removed above.${RESET}"
        echo -e "${YELLOW}Re-run setup to restore them if needed.${RESET}"
        echo
        exit 0
    fi

    info "Removing ${VERNEX_HOME}/"
    rm -rf "${VERNEX_HOME}"
    ok "${VERNEX_HOME}/ removed"
    REMOVED_VERNEX=true
fi

# ── final status ───────────────────────────────────────────────────────────────
echo
echo -e "${BOLD}${RED}╔══════════════════════════════════════╗${RESET}"
echo -e "${BOLD}${RED}║   VERNEX NODE WIPED                  ║${RESET}"
echo -e "${BOLD}${RED}╚══════════════════════════════════════╝${RESET}"
echo
echo -e "${BOLD}Removed:${RESET}"
echo -e "  ${RED}✗${RESET} vernex-daemon.service (stopped, disabled, deleted)"
echo -e "  ${RED}✗${RESET} vernex-dashboard.service (stopped, disabled, deleted)"
echo -e "  ${RED}✗${RESET} polkit inhibitor rule"
echo -e "  ${RED}✗${RESET} Ollama 0.0.0.0 override (restored to localhost)"
if [[ "${REMOVED_VERNEX:-false}" == "true" ]]; then
    echo -e "  ${RED}✗${RESET} ~/vernex/ (node keys, config, code, binaries)"
else
    echo -e "  ${YELLOW}⚠${RESET} ~/vernex/ — skipped (not confirmed)"
fi
echo
echo -e "${BOLD}Preserved:${RESET}"
echo -e "  ${GREEN}✓${RESET} Ollama ($(ollama --version 2>/dev/null || echo 'installed'))"
echo -e "  ${GREEN}✓${RESET} Ollama models (not removed — use 'ollama rm <model>' manually if needed)"
echo -e "  ${GREEN}✓${RESET} Go ($(go version 2>/dev/null || echo 'installed'))"
echo -e "  ${GREEN}✓${RESET} GPU drivers"
echo
echo -e "${CYAN}This machine is no longer a Vernex node.${RESET}"
echo -e "${CYAN}To re-provision, run: scripts/vernex-node-setup.sh${RESET}"
echo

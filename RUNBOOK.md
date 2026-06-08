# Vernex Protocol — Development Runbook
> Living Working Document — Node Setup & Protocol Build
>
> Inventor / Lead: Eric John Geer
> U.S. Patent Application No. 64/015,885 — Patent Pending
> **CONFIDENTIAL — COVERED UNDER MUTUAL NDA**

---

## How to Use This Document

Single source of truth for building, running, and reproducing the Vernex Protocol node environment. Every setup step, command, configuration file, and decision is recorded here.

- Commands are shown in code blocks — run exactly as written unless a placeholder is noted
- `[DONE]` — completed and verified on at least one machine
- `[IN PROGRESS]` — actively being built
- `[PLANNED]` — future steps not yet started
- Update this document every time a step is completed or a decision is made

---

## Project Overview

Vernex Protocol is a distributed home node compute network with a two-class token priority system and a social compute layer. Each node runs a Go daemon that advertises itself on the network, tracks contribution, and manages the partition between personal and social compute.

| Item | Detail |
|------|--------|
| Protocol name | Vernex Protocol |
| Patent status | Pending — U.S. App. No. 64/015,885 |
| License model | BSL 1.1 with Guardian Clause → Apache 2.0 after 4 years per release |
| Repo | https://github.com/SuperSleeper/vernex-protocol (PUBLIC) |
| Core language | Go (node daemon) |
| Dashboard language | Python + Flask |
| Target hardware | Budget consumer hardware, Raspberry Pi capable |
| Current version | v0.12.45 (dashboard) / v0.12.18 (daemon) |

---

## Build Phase Tracker

| Phase | Goal | Status |
|-------|------|--------|
| Phase 1 | Single node running, dashboard live | DONE ✓ |
| Phase 2 | Two nodes communicating P2P | DONE ✓ |
| Phase 3 | Contribution tracking + token classes | DONE ✓ |
| Phase 4 | Commons Review consent mechanism + LLM + chat UI | DONE ✓ |
| Phase 5 | Node setup scripts, TLS, external connectivity, STUN/RELAYED | DONE ✓ |
| Phase 6 | Google OAuth, central auth relay, nginx proxy | DONE ✓ |
| Phase 7 | Signed node identity (ed25519), cryptographic job receipts, distributed CA PoC | IN PROGRESS |
| Phase 8 | ML-DSA + ML-KEM post-quantum upgrade | PLANNED |
| Phase 9 | DHT implementation, latency-aware routing | PLANNED |
| Phase 10 | Open source contributor network, investor demo | PLANNED |

---

## Machine Registry

| Node | Hostname | OS | Hardware | Node ID | Role | Status |
|------|----------|----|----------|---------|------|--------|
| vernex-node1 | vernex-node1 | Pop!_OS 24.04 | RTX 3070 8GB / 64GB RAM | VRX-54b89a1684e21ae4 | Bootstrap — daemon, dashboard, nginx, auth relay | Active v0.12.18 |
| vernex-node2 | vernex-node2 | Pop!_OS 24.04 COSMIC | HP Victus / RTX 4070 Max-Q / 60GB RAM | VRX-a5474b585793501c | Compute node | Active v0.12.18 |

---

## Network / Port Map

| Port | Service | Bind | Access |
|------|---------|------|--------|
| 7700 | P2P TCP/UDP | 0.0.0.0 | Public (OPNsense forwarded) |
| 7701 | HTTPS daemon API | 0.0.0.0 | Public (OPNsense forwarded) |
| 5000 | Flask dashboard | 127.0.0.1 | localhost only |
| 5001 | LAN install server | 0.0.0.0 | LAN only |
| 5002 | OAuth server thread | 127.0.0.1 | localhost only |
| 5003 | Auth relay Flask | 127.0.0.1 | localhost only (nginx proxies) |
| 5080 | nginx dashboard proxy | 0.0.0.0 | LAN — Google auth required |
| 5443 | nginx auth relay TLS | 0.0.0.0 | Public — Let's Encrypt cert |
| 11434 | Ollama | 127.0.0.1 | localhost only |

---

## Architecture

```
Go daemon (vernex-node)        — P2P mesh, token priority queue, LLM inference
Python Flask dashboard          — patent-critical consent gate UI (node1 only)
nginx (port 5080)               — reverse proxy to dashboard, Google OAuth gate
Auth relay (port 5003/5443)     — central Google OAuth relay for all nodes
Bootstrap nodes                 — STUN hole-punching for NAT traversal
Compute nodes                   — join via DNS TXT or mDNS, no manual config
```

### Discovery Flow
```
Same LAN   → mDNS (_vernex._tcp) → direct P2P
External   → DNS TXT → bootstrap public IP → STUN hole-punch → P2P
```

### Auth Flow (Google OAuth)
```
Browser → http://172.17.0.132:5080
  → nginx → /auth/login
  → https://vernex.net:5443/login?return=<node_url>/auth/complete
  → Google OAuth
  → relay issues RS256 JWT → redirect to node /auth/complete
  → node verifies JWT via relay pubkey → sets session cookie
  → dashboard rendered
```

---

## Services (vernex-node1 only)

| Service | Status Command |
|---------|---------------|
| vernex-daemon | `sudo systemctl status vernex-daemon` |
| vernex-dashboard | `sudo systemctl status vernex-dashboard` |
| vernex-auth-relay | `sudo systemctl status vernex-auth-relay` |
| nginx | `sudo systemctl status nginx` |
| ollama | `sudo systemctl status ollama` |

**NOTE:** vernex-dashboard, vernex-auth-relay, and nginx run on node1 (bootstrap) only. Never install them on compute nodes.

---

## Section 1: Machine Setup [DONE]

### 1.1 Operating System

| Item | Value |
|------|-------|
| OS | Pop!_OS 24.04 LTS |
| Boot manager | systemd-boot |

Pop!_OS chosen for out-of-the-box NVIDIA driver support, kernel stability, Go/Python compatibility.

### 1.2 Initial System Update

```bash
sudo apt update && sudo apt upgrade -y
sudo apt autoremove -y
```

### 1.3 Install Go

```bash
cd ~
wget https://go.dev/dl/go1.22.0.linux-amd64.tar.gz
sudo rm -rf /usr/local/go
sudo tar -C /usr/local -xzf go1.22.0.linux-amd64.tar.gz
rm go1.22.0.linux-amd64.tar.gz
```

Add to `~/.bashrc`:
```bash
export PATH=$PATH:/usr/local/go/bin
export GOPATH=$HOME/go
export PATH=$PATH:$GOPATH/bin
```

### 1.4 Install Python 3

```bash
sudo apt install -y python3-pip python3-venv python3-dev
```

### 1.5 Install Git

```bash
sudo apt install -y git
git config --global user.name "Eric Geer"
git config --global user.email "your@email.com"
```

### 1.6 Recommended Tools

```bash
sudo apt install -y curl jq htop net-tools nmap tmux
```

---

## Section 2: Vernex Daemon — Go Node [DONE]

Go daemon v0.12.18. Listens on port 7700 (P2P TCP/UDP) and port 7701 (HTTPS API). Stable VRX- node ID persisted to config/node.json. Tracks uptime, contribution score, peers. Exposes /status, /health, /submit, /consent, /queue, /peers, /register, /deregister, /stun endpoints. Graceful SIGTERM deregister — sends /deregister to peers, dashboard updates immediately.

### Build (compute node — vernex-node2)

```bash
cd ~/vernex && git pull origin main
cd daemon && go build -o vernex-node .
sudo systemctl stop vernex-daemon
sudo cp vernex-node /usr/local/bin/vernex-node
sudo systemctl start vernex-daemon
```

### Update Scripts (always use — never manual copy)

```bash
# vernex-node1 (bootstrap)
curl -fsSL https://raw.githubusercontent.com/SuperSleeper/vernex-protocol/main/vernex-bootstrap-setup.sh | bash
sudo systemctl restart vernex-dashboard

# vernex-node2 (compute)
curl -fsSL https://raw.githubusercontent.com/SuperSleeper/vernex-protocol/main/vernex-node-setup.sh | bash
```

**CRITICAL:** node1 uses `vernex-bootstrap-setup.sh`. node2 uses `vernex-node-setup.sh`. Never swap.

---

## Section 3: Dashboard — Python Flask [DONE — node1 only]

Flask dashboard at `http://172.17.0.132:5080` (nginx proxied, Google auth required). Patent-critical consent gate UI. Shows both nodes, contribution scores, connection types, token queue. Bound to 127.0.0.1:5000 — nginx handles external access.

**NOTE:** Dashboard runs on node1 (bootstrap) only. Do not install on compute nodes.

---

## Section 4: Two-Node P2P Setup [DONE]

Both nodes on v0.12.18. P2P confirmed over LAN (LOCAL connection type) and external networks (RELAYED via STUN bootstrap). Node-2 tested on T-Mobile hotspot — ONLINE with relay badge.

DNS: `_vernex._tcp.vernex.net TXT → bootstrap=76.244.40.49:7701` ✅

LAN DNS: `vernex.net → 172.17.0.132` via OPNsense Unbound host override ✅

---

## Section 5: Google OAuth + Auth Relay [DONE]

Central auth relay at `https://vernex.net:5443`. One Google OAuth app registration serves all nodes. Let's Encrypt cert — **expires 2026-08-02** ⚠️ (renew before then).

### Config Locations

```
node1: /etc/vernex-relay/config.json (mode 600) — Google client ID/secret
each node: ~/vernex/config/oauth.json (mode 600) — session secret, relay URL
```

### Auth Relay Pubkey Test

```bash
curl -s https://vernex.net:5443/pubkey
curl -s http://127.0.0.1:5003/pubkey
```

---

## Section 6: OPNsense / Network [DONE]

| Rule | Detail |
|------|--------|
| NAT port forward | 7700, 7701, 80, 5443 → 172.17.0.132 |
| Unbound host override | Host: `vernex` / Domain: `net` / IP: `172.17.0.132` |
| eero telemetry | Blocked via Host alias firewall rule |

**NOTE:** Unbound host override must split host/domain correctly. `vernex` in Host, `net` in Domain. Do NOT put `vernex.net` in the Host field.

---

## Section 7: Ollama Models (node1)

```
gemma4:e2b, gemma4:e4b, gemma4:31b, gemma4:26b
mistral, llama3.1:8b, qwen2.5-coder:7b, phi3:mini, llama3.2:3b
```

```bash
ollama list   # check available models
```

---

## Section 8: WSL2 Node Setup [DONE]

```powershell
# PowerShell as Administrator
wsl --install
# Reboot, then in Ubuntu WSL2:
```

```bash
echo -e "[boot]\nsystemd=true" | sudo tee /etc/wsl.conf
# wsl --shutdown from PowerShell, reopen
# Windows Firewall: open 7700+7701 TCP+UDP inbound
# netsh interface portproxy add v4tov4 listenport=7701 listenaddress=0.0.0.0 connectport=7701 connectaddress=<WSL2-eth0-ip>
# netsh interface portproxy add v4tov4 listenport=7700 listenaddress=0.0.0.0 connectport=7700 connectaddress=<WSL2-eth0-ip>
curl -fsSL https://raw.githubusercontent.com/SuperSleeper/vernex-protocol/main/vernex-node-setup.sh | bash
```

---

## Enrollment Tokens

Tokens expire 2026-06-02. Generate new batch when needed:

```bash
# On vernex-node1
vernex-node ca token --count 5
```

Zero-touch enrollment from LAN:
```bash
curl -fsSL "http://172.17.0.132:5001/install?token=<token-id>" | bash
```

---

## Key Design Rules (never violate)

| Rule | Detail |
|------|--------|
| Cryptography target | ML-DSA + ML-KEM (NIST FIPS 203/204). Never ed25519/X25519 as final target |
| Node onboarding | Zero-touch. Enrollment token based. Never manual key copy/paste |
| Bootstrap | STUN hole-punching — compute nodes behind NAT connect P2P without firewall changes |
| Dashboard bind | Always 127.0.0.1:5000 — never 0.0.0.0 |
| Scripts | Zero hardcoded values. $USER/$HOME/$HOSTNAME only |
| Token security | Never print signatures to stdout. config/token-<id>.json (mode 600) only |
| Patent-critical | Commons Review consent gate must visibly block ONLINE until operator approves |
| Node identity | Always keyed on VRX-... node_id. Never IP address |
| Dashboard install | node1 (bootstrap) only. Never install vernex-dashboard on compute nodes |
| Update scripts | node1 = vernex-bootstrap-setup.sh / node2 = vernex-node-setup.sh. Never swap |
| Claude Code | Always include `git push origin main` at end of every commit prompt |

---

## Version History

| Version | Change |
|---------|--------|
| v0.2.0 | Two-node dashboard live — both nodes ONLINE, contribution tracking |
| v0.3.0 | Sleep inhibitor via D-Bus, node config file, persistent node IDs, SIGTERM handling |
| v0.5.1 | Multi-node Ollama routing, Class 1/2 priority queue, Commons Review, chat UI, web search |
| v0.9.1 | Systemd services, auto-start, TOFU cert trust, ed25519 identity |
| v0.12.0 | STUN hole-punching, external node connectivity, RELAYED connection type |
| v0.12.3 | Clock verification, mDNS auto-trust, trusted_nodes.json persistence |
| v0.12.11 | Non-interactive enrollment via --token flag + /install LAN endpoint |
| v0.12.12 | Fix base_url, mDNS-first by node_id, LAN install server |
| v0.12.13 | nginx reverse proxy + Google OAuth + /ui chat page |
| v0.12.14 | Central OAuth relay at vernex.net:5443 |
| v0.12.15 | Relay port 5003 fix, requirements cleanup, /etc/hosts bootstrap step |
| v0.12.16 | git pull.rebase fix, Ollama model detection fix, redirect_base auto-detect |
| v0.12.18 | Fix binary replacement (Text file busy) + graceful SIGTERM /deregister |
| v0.12.18 | Phase 7a ML-DSA: mldsa_public_key in /status + /peers; own key written to node.json (additive, not yet enforced) |
| v0.12.33 | Combat system: enemy tables, 5 combat routes, special abilities, health bar, Game Over screen |
| v0.12.36 | Three-layer LLM context (universal + genre + custom), compact HUD format, multi-color stat bars |
| v0.12.37 | Auto-equip starting inventory per class, item condition (0–100%), loss/break narrative detection |
| v0.12.38 | NPC relationship system (7 states), depth tracking, memory, narrative NPC detection |
| v0.12.39 | NPC stat rolling (4d6 drop-lowest), recruitment check, talk-down mechanic, rival system |
| v0.12.40 | Party formation + team dynamics (diversity/weakness/duplicate scoring), group combat power, 3 party routes, auto-add ally on recruit |
| v0.12.41 | Server-side response length enforcement, narrative recruitment detection, empty-response retry UI |
| v0.12.43 | NPC compliance Charisma gap rules in pre-context, EFFECTIVE CHARISMA in LLM context, _eff_stat stale item_bonus fix |
| v0.12.44 | Persistent NPC context injected every turn as final system msg; history windowed to last 10 turns |
| v0.12.45 | Adaptive IQ tier matching, plot momentum stagnation breaking, NPC argument hard cap |

---

## Useful Commands

```bash
# Cluster status (run on vernex-node1)
curl -sk https://localhost:7701/peers | jq .
curl -sk https://localhost:7701/status | jq '{version, cert_verified, trust_approved}'
curl -sk https://172.17.0.182:7701/status | jq '{node_id, version}'

# Daemon logs
sudo journalctl -u vernex-daemon -f
sudo journalctl -u vernex-daemon -n 60 --no-pager | grep -E "trust|register|Clock|heartbeat"

# Auth relay logs
sudo journalctl -u vernex-auth-relay -f

# Dashboard logs
sudo journalctl -u vernex-dashboard -f

# All Vernex services (node1)
sudo systemctl status vernex-daemon vernex-dashboard vernex-auth-relay nginx

# DNS test
dig vernex.net @172.17.0.1 +short   # should return 172.17.0.132

# Enroll a new node (run ON the new node)
vernex-node ca enroll \
  --bootstrap https://172.17.0.132:7701 \
  --token "$(cat ~/vernex/config/token-<id>.json)"

# Generate new enrollment tokens (on node1)
vernex-node ca token --count 5
```

---

## Outstanding Items

| # | Task | Notes |
|---|------|-------|
| 1 | **Non-provisional patent prep** | Deadline March 24, 2027. Attorney needed Q3 2026 |
| 2 | **ML-DSA + ML-KEM upgrade** | Replace ed25519/X25519. NIST FIPS 203/204 |
| 3 | **Let's Encrypt cert renewal** | vernex.net:5443 cert expires 2026-08-02 |
| 4 | **IPv6 link-local filter** | mDNS fe80:: log noise only — not a functional blocker. Deferred indefinitely. Both nodes stable without it |

---

## Decisions Log

| Date | Decision | Reasoning |
|------|----------|-----------|
| Mar 2026 | Go for daemon, Python for dashboard | Go handles concurrent P2P efficiently on budget hardware |
| Mar 2026 | Port 7700 for P2P, 7701 for HTTP API | Avoids common dev port conflicts |
| Mar 2026 | Private GitHub repo | Patent pending, pre-investor |
| Apr 2026 | Multi-node Ollama routing | Picks lowest active-model count via /api/ps; 3s timeout; fallback on non-200 |
| Apr 2026 | node_id persisted on first run | Creates config/node.json with defaults + generated ID |
| Apr 2026 | Ollama bound to 0.0.0.0 on node2 | Required for cross-node routing via systemd override.conf |
| Apr 2026 | Chat UI at /ui route | Single HTML file, dark theme, Class 1/2 toggle, model selector |
| Apr 2026 | ed25519 cryptographic node identity | VRX- ID derived from SHA256(pubKey)[:8] — stable, verifiable |
| Apr 2026 | Replay protection on inter-node requests | 30-second timestamp window prevents replay attacks on /submit |
| Apr 2026 | No hardcoded IPs in source | peer_nodes in config/node.json drives routing and trust |
| Apr 2026 | Dynamic node discovery via /register + /peers | Nodes heartbeat every 60s; no hardcoded IPs anywhere |
| Apr 2026 | Dashboard compact mode | Toggle card/table view; localStorage persistence |
| Apr 2026 | Node setup + wipe scripts | vernex-node-setup.sh and vernex-node-wipe.sh — idempotent |
| Apr 2026 | vernex.net + vernex.org registered | Primary protocol home and open source community domains |
| Apr 2026 | ML-DSA 44 hybrid post-quantum signing planned | CRYSTALS-Dilithium NIST FIPS 204 — future upgrade |
| Apr 2026 | CONTINUITY.md auto-maintained by Claude Code | Session handoff doc updated at end of every Claude Code session |
| Apr 2026 | STUN endpoint discovery | /stun returns caller external IP:port; nodes discover own endpoint at startup |
| Apr 2026 | Push-based status relay for NAT nodes | Nodes push /status in heartbeat; bootstrap caches; dashboard shows ONLINE+RELAY |
| Apr 2026 | BSL 1.1 with Guardian Clause | Changed from Apache 2.0 — protects commercial exploitation pre-revenue |
| May 2026 | node1 uses vernex-bootstrap-setup.sh | Separate install scripts for bootstrap vs compute nodes |
| May 2026 | Graceful SIGTERM deregister | Daemon sends /deregister to peers; dashboard updates immediately vs 60s wait |
| May 2026 | Dashboard node1-only rule | vernex-dashboard never installed on compute nodes |
| May 2026 | RUNBOOK.md + ARCH_SPEC.md in repo | Replaces .docx uploads — Claude Code can update directly; versioned with code |
| May 2026 | Unbound DNS split host/domain | Host override requires `vernex` in Host field and `net` in Domain — not `vernex.net` in Host |

---

## Known Issues & Resolutions

| Issue | Resolution |
|-------|------------|
| Node-2 git pull — divergent branches | `git config pull.rebase false` then `git pull origin main --allow-unrelated-histories` |
| Node-2 config/node.json owned by root after systemd first run | `sudo chown ericgeer:ericgeer ~/vernex/config/node.json` |
| Node-2 COSMIC DE config wipes on improper shutdown | `sudo apt install --reinstall cosmic-comp cosmic-session cosmic-greeter`; backup with `cp -r ~/.config/cosmic ~/.config/cosmic.backup` |
| Binary replacement "Text file busy" | Daemon must be fully stopped before `cp` — fixed in v0.12.18 install script |
| Unbound DNS returning public IP for vernex.net | Host override had `vernex.net` in Host field — must be `vernex` in Host, `net` in Domain |
| vernex-dashboard crash-looping on node2 | Bootstrap setup script was accidentally run on compute node — dashboard removed from node2 |
| WSL2 enrollment — systemd not available | Add `[boot]\nsystemd=true` to `/etc/wsl.conf`, then `wsl --shutdown` and reopen |

---

*Vernex Protocol — Patent Pending — U.S. App. No. 64/015,885*
*CONFIDENTIAL*

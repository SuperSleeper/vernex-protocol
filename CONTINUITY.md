# Vernex Protocol — Session Continuity

## Last Updated
June 6, 2026 (End of Session)

## Current Version
v0.12.31

## Node Registry
| Node | ID | IP | Public Key | Status |
|------|----|----|------------|--------|
| vernex-node1 | VRX-54b89a1684e21ae4 | 172.17.0.132 (LAN) / 76.244.40.49 (public) | prAB8hQJaXoWoT+WO7jbCKBT0TAJPMLjiE4QlOr2D0I= | v0.12.18 ✓ (daemon); v0.12.31 dashboard |
| vernex-node2 | VRX-a5474b585793501c | 172.17.0.182 | /Lcqppk1jkHUVdgNNHaS15FDKurHO3jgPP3+oMfB83Y= | v0.12.18 ✓ (daemon) |

## Recently Completed (2026-06-06)

### v0.12.31 — fix save/load toolbar position (dashboard)
✅ Moved Quicksave / Save / Load toolbar below `#chat-log` and above the model selector, so the player never needs to scroll up to save; Character Sheet collapsible stays at top; all IDs and JS functionality unchanged

### v0.12.30 — fix word-wrap on game and chat log (dashboard)
✅ Added `white-space:pre-wrap`, `word-wrap:break-word`, `overflow-wrap:break-word` to `.msg.assistant` and `overflow-x:hidden` to `#chat-log` in both `/ui` and `/game` templates; pre-wrap preserves ASCII art HUD line breaks while wrapping long lines at the container edge; no horizontal scrollbar

### v0.12.29 — fix save row clicks + user identity in UI + per-user saves + version fix (dashboard)
✅ **Fix selection screen save rows**: renamed `data-load-id` → `data-save-id`; event delegation on static containers (`sel-saves-list`, `sel-autosaves-list`) now correctly fires on dynamically rendered child rows
✅ **User identity in header**: both `/ui` and `/game` fetch `GET /api/me` on page load; user email displayed in header (between GPU gauge and logout); game selection screen shows "Playing as: `<email>`" below subtitle; gracefully hidden on 401/failure
✅ **Version in header**: `/game` and `/ui` Flask routes now fetch daemon `/status` at render time and pass `version` as template variable; shown immediately before JS updates with full node_id + version; fallback to `v0.12.29`
✅ **Per-user save file isolation**: all save/load/prompt routes call `_get_current_user()` (forwards `_vsession` cookie to `127.0.0.1:5002/api/me`) and `_user_id()` (sanitizes email: `@` and `.` → `_`); save path: `~/vernex/config/game_saves/<user_id>/<id>.json`; prompt path: `~/vernex/config/game_prompts/<user_id>.json`; unauthenticated access falls back to `guest` directory

### v0.12.28 — Comedic Drama genre + stat modifiers + load game on selection screen (dashboard)
✅ **Comedic Drama genre** (🎭): 4th genre card; subtypes Workplace Comedy / Small Town Chaos / Royally Confused / Superhero Farce; stats Charisma / Wit / Luck / Clumsiness / Charm / Stubbornness; Embarrassment Meter replaces Status line; level escalation → absurdity; 1% musical number
✅ **Stat modifiers**: `STAT_MODIFIERS` table for all genres + subtypes; applied at roll time (4d6 drop-lowest + modifier, clamped 1–20); shown inline as `(+2)` (green) / `(-2)` (red) in stat block
✅ **Load game on selection screen**: "📂 Continue a Saved Game" + "⚡ Autosaves" sections below genre grid; fetches `/api/game/saves` on init and after reset; click-to-load goes directly to gameplay; `/api/game/saves` now returns `subtype` and `autosave` fields

### v0.12.27 — CSP inline script + game context JSON injection SyntaxError fix (dashboard)
✅ Fixed SyntaxError caused by special characters in game context strings being injected directly into `<script>` block; moved all game data to `/api/game/contexts` endpoint; JS fetches on init; resolves CSP issues

### v0.12.26 — multi-genre game + D&D character creator + fixed header + WWII era + genre card fix (dashboard)
✅ Fantasy, Sci-Fi, Action/Adventure genre selection screen with genre cards
✅ D&D-style 4d6-drop-lowest character stat rolling per genre
✅ Subtype picker: Fantasy (Warrior/Valkyrie/Elf Archer/Wizard/Thief), Sci-Fi (AI/Aliens/Space Travel/Time Travel), Action (Egyptian/Roman/Renaissance/Wild West/WWII)
✅ Fixed header (position:fixed) on `/game` with GPU gauge; auto-padding adjustment
✅ WWII era added to Action/Adventure
✅ Genre cards changed from inline `onclick` to `addEventListener` (CSP compliance)

### v0.12.25 — restore auto-scroll after GPU gauge header change (dashboard)
✅ Auto-scroll to bottom in `/game` chat log re-wired after fixed header layout change broke it

### v0.12.24 — GPU gauge bar on /ui and /game — node1 only (dashboard)
✅ Real-time VRAM used/total, GPU utilization %, temperature polled via `nvidia-smi` at `/api/gpu`
✅ Gauge bar in both `/ui` and `/game` headers; active (>20% util) turns green; 3s poll interval
✅ Node1 (RTX 3070) only — multi-node GPU dashboard deferred

### v0.12.23 — auto-scroll chat to bottom on send and response (dashboard)
✅ Fixed chat log scroll behaviour in both `/ui` and `/game`: `requestAnimationFrame` scroll after user message and after assistant response

### v0.12.22 — default model to gemma4:e4b on /ui and /game (dashboard)
✅ Model selector now prefers `gemma4:e4b` when available; falls back to first model in list

### v0.12.21 — /api/game/chat error handling + debug logging (dashboard)
✅ Non-JSON Ollama responses now return HTTP 502 with truncated body; unhandled exceptions logged with full traceback; `[game/chat]` log prefix for all backend messages

### v0.12.20 — Enter to send, Shift+Enter for newline on /ui and /game (dashboard)
✅ `keydown` handler on prompt textarea: Enter submits, Shift+Enter inserts newline

### v0.12.19 — fix Start Game button visibility when context collapsed (dashboard)
✅ Start Game button no longer hidden when `<details>` context panel is collapsed; CSS z-index fix

## Recently Completed (2026-06-03)

### v0.12.18 — game page at /game (dashboard)
✅ Text adventure game page added at `/game` (dashboard/app.py)
✅ `/api/models` endpoint — fetches available Ollama models from localhost:11434/api/tags
✅ `/api/game/chat` endpoint — multi-turn Ollama chat via messages array (bypasses vernex queue for full conversation history)
✅ `🎮 Game` button added to `/ui` header, linking to `/game`
✅ `/ui` model selector updated to load dynamically from `/api/models`
✅ Game features: collapsible context panel, locked-on-start context, markdown rendering (marked.js), dynamic model selector, full conversation history client-side, Reset Game button

## Recently Completed (2026-05-08)

✅ Unbound DNS fixed — vernex.net → 172.17.0.132 (OPNsense host override had wrong domain field)
✅ /etc/hosts workaround removed from node1
✅ NOTICE file added — BSL 1.1 compliance (commit 5cf4e5c)
✅ vernex-dashboard removed from node2 (accidentally installed via bootstrap script)
✅ RUNBOOK.md added to repo root — replaces vernex_runbook.docx
✅ ARCH_SPEC.md added to repo root — replaces vernex_architecture_spec.docx
✅ v0.12.18 Phase 7a ML-DSA upgrade — mldsa_public_key in /status and /peers responses; own key written to node.json; hybrid signing already in place (additive, not yet enforced per-peer)

## What Was Just Completed (v0.12.18 — Phase 7a ML-DSA keypair + hybrid signing)

- `mldsa_public_key` added to `/status` response (`statusResponse` struct in node.go)
- `mldsa_public_key` added to `/peers` response (`peerOut` struct in handlers.go)
- `MLDSAPublicKey` added to `PeerEntry` (peer.go); populated from `cfg.PeerNodes` on `/register`, preserved across re-registers
- `mldsa_public_key` added to `NodeConfig` (config.go); written to `node.json` on first startup after keypair load
- Hybrid signing (`X-Vernex-Signature-MLDSA`) already in place since v0.8.0; this phase surfaces keys via API
- ML-DSA enforcement remains per-peer opt-in (set `mldsa_public_key` in `peer_nodes[]` config to enforce)
- `cloudflare/circl v1.6.3` (mldsa44) already in go.mod — no new dependency needed

## What Was Just Completed (v0.12.17 — graceful shutdown deregister)

### daemon: /deregister endpoint (handlers.go)
- `POST /deregister {node_id}` — peer calls this on clean shutdown
- Calls `peerRegistry.Remove(nodeID)` to evict immediately from live-peers list
- Peer appears OFFLINE at once rather than waiting up to 90 s for heartbeat TTL
- No auth required (consistent with `/register`; worst case: spurious deregister is self-healing on next heartbeat)
- Logged as `[↓] deregister: peer offline id=<id>`

### daemon: PeerRegistry.Remove() (peer.go)
- New method: `Remove(nodeID string)` — locked delete from the entries map

### daemon: sendDeregisterToBootstrap() (peer.go)
- Called by SIGTERM/SIGINT handler in main.go before os.Exit
- Iterates static peer_nodes + dynamic mDNS peers (deduplicated by URL, mDNS preferred over static)
- POSTs `{node_id}` to each peer's `/deregister` with a 3 s timeout
- Failures logged but non-fatal — next missed heartbeat expires the entry anyway

### daemon: SIGTERM handler (main.go)
- Now calls `sendDeregisterToBootstrap(node)` before releasing inhibitor lock and exiting
- `systemctl stop vernex-daemon` → node notifies all peers → exits cleanly

## What Was Just Completed (v0.12.16 — three setup/oauth fixes)

### vernex-node-setup.sh — Step 3: prevent divergent-branch error on git pull
- Added `git -C "${INSTALL_DIR}" config pull.rebase false` before `git pull origin main`
- Prevents `fatal: Need to specify how to reconcile divergent branches` on re-runs

### vernex-node-setup.sh — Ollama model check: reliable detection
- Replaced `ollama list | grep -q '.'` (breaks when only header line is present)
- Now: `_MODEL_COUNT="$(ollama list 2>/dev/null | grep -v "^NAME" | grep -c . || echo 0)"`
- Only warns when count is 0 (no actual models, ignoring the NAME header line)

### dashboard/oauth.py — redirect_base auto-detection
- `_ensure_configs()` no longer writes a hardcoded `redirect_base` into new oauth.json
- New `_detect_lan_ip()`: tries daemon `/status` API (`https://localhost:7701/status`) first;
  falls back to UDP socket trick (`connect("8.8.8.8",80)` → `getsockname()`); final fallback `127.0.0.1`
- New `_resolve_redirect_base()`: reads oauth.json; if `redirect_base` is missing/empty,
  detects LAN IP, builds `http://<ip>:5080`, writes it back to oauth.json, returns it
- `auth_login()` uses `cfg.get("redirect_base","") or _resolve_redirect_base()` — existing
  explicit values are always honoured; detection only runs when field is absent or empty
- Added `import socket` to imports

## What Was Just Completed (v0.12.14 — central OAuth relay at vernex.net)

### scripts/vernex-auth-relay/ — new service (4 files)

**relay.py** — stateless Flask app on `127.0.0.1:5000` behind nginx:
- `/login?return=<url>` — validates `return` ends in `/auth/complete` + scheme is http/https; encodes return_url into HMAC-signed state parameter (no server-side state store)
- `/callback` — exchanges Google code, decodes state to recover return_url, issues RS256 JWT `{email, name, picture, iat, exp:24h}`, redirects to `<return_url>?token=<jwt>`
- `/pubkey` — returns relay RS256 public key as PEM JSON (cached by nodes for 1hr)
- State parameter: `base64url(ts|return_url) + "." + hmac-sha256[:24]` — stateless, 10min expiry

**deploy.sh** (executable):
1. apt-get: python3, pip, venv, nginx, certbot, openssl
2. Creates venv at `scripts/vernex-auth-relay/venv/`
3. Creates `/etc/vernex-relay/` (mode 700)
4. Generates RSA-2048 keypair: `relay.key` (mode 0600) + `relay.pub` (mode 0644)
5. TLS: tries `certbot certonly --standalone -d vernex.net`; falls back to self-signed at `/etc/vernex-relay/tls/`; patches nginx config automatically
6. Prompts once for `google_client_id` + `google_client_secret` → writes `/etc/vernex-relay/config.json` (mode 0600) with auto-generated `state_secret`
7. Installs + enables `vernex-auth-relay.service` (systemd, runs as current user)

**nginx-relay.conf**:
- `listen 5443 ssl` → proxy to `127.0.0.1:5000`
- Let's Encrypt at `/etc/letsencrypt/live/vernex.net/`
- Port 80 server: certbot ACME challenge + redirect to 5443

**requirements.txt**: `flask>=3.0`, `cryptography>=41.0`

### dashboard/oauth.py — completely rewritten (relay-based)

Removed: all local Google OAuth logic, `google_client_id`, `google_client_secret`, `/auth/callback`, state cookie, Google token exchange

Added:
- `/auth/login` → `{relay_url}/login?return={redirect_base}/auth/complete` (simple redirect)
- `/auth/complete?token=<jwt>` → fetch relay pubkey (1hr memory cache, `ssl.CERT_NONE` since JWT sig provides auth), verify RS256 signature + expiry via `cryptography`, provision user, set local HMAC session cookie, redirect
- `_fetch_relay_pubkey(relay_url)` with `_pubkey_cache` tuple `(pem, fetched_at)`
- `_verify_jwt(token, relay_url)` — loads PEM, verifies PKCS1v15/SHA256 sig, checks `exp`

Unchanged: `_verify_cookie`, `_make_cookie`, `/auth/verify`, `/auth/logout`, `/api/me`, `run_oauth_server()`

### config/oauth.json on each node simplifies to:
```json
{
  "session_secret": "<auto-generated>",
  "relay_url": "https://vernex.net:5443",
  "redirect_base": "http://172.17.0.132:5080"
}
```
No Google credentials on individual nodes — only on the relay at vernex.net.

### dashboard/requirements.txt
Added `cryptography>=41.0` (needed by oauth.py for RS256 JWT verification)

### Security model
- Google credentials live only on vernex.net relay (one place to rotate)
- JWT: RS256, 24hr expiry, signed with relay's RSA-2048 key
- Node verifies JWT against relay pubkey (fetched once, cached 1hr)
- Relay pubkey fetch uses `ssl.CERT_NONE` (self-signed relay cert OK; JWT sig is the real auth anchor)
- Local session: HMAC-SHA256 cookie, 7-day expiry, per-node secret
- `return=` URL validated: must end in `/auth/complete`, must be http/https scheme

### Deploy
Relay (vernex.net, one-time):
```bash
cd ~/vernex/scripts/vernex-auth-relay
bash deploy.sh
# Google Cloud Console: add redirect URI → https://vernex.net:5443/callback
```

Each node (after relay is live):
```bash
# Update ~/vernex/config/oauth.json:
# {
#   "session_secret": "<keep existing>",
#   "relay_url": "https://vernex.net:5443",
#   "redirect_base": "http://<node-ip>:5080"
# }
# (remove google_client_id / google_client_secret if present)
sudo systemctl restart vernex-dashboard
pip install cryptography>=41  # in dashboard venv
```

## What Was Just Completed (v0.12.13 — nginx reverse proxy + Google OAuth + Chat UI)

### Architecture: nginx on port 5080 as auth gateway
- `scripts/nginx-vernex.conf` — new nginx site config at `/etc/nginx/sites-available/vernex`
- Port 5080, proxying to three backends: 5000 (dashboard), 5001 (install), 5002 (oauth)
- nginx `auth_request /_auth_admin` / `/_auth_user` (internal subrequests to oauth.py `/auth/verify?role=`)
- Route map:
  - `/install` → 127.0.0.1:5001 — no auth (LAN install script delivery)
  - `/auth/*` → 127.0.0.1:5002 — no auth (OAuth flow)
  - `= /api/me` → 127.0.0.1:5002 — no auth (returns 401 itself if not logged in)
  - `/ui` → 127.0.0.1:5000 — user auth
  - `/api/` → 127.0.0.1:5000 — user auth
  - `/` → 127.0.0.1:5000 — admin auth
- `@login_redirect` named location: `return 302 /auth/login`

### dashboard/oauth.py — new file (Google OAuth 2.0 on 127.0.0.1:5002)
- HMAC-SHA256 signed cookies: `base64url(email|role|exp) + "." + hmac`
- `_ensure_configs()` creates `config/oauth.json` (mode 0600) and `config/users.json` (mode 0600) if missing
- `config/oauth.json`: google_client_id, google_client_secret, facebook stubs, session_secret (auto-generated), redirect_base
- First Google login → admin; subsequent → user; disabled users → 403
- Role hierarchy: admin ≥ user (admin can access /ui too)
- `run_oauth_server()` — started as daemon thread from app.py `__main__`
- Routes: `/auth/verify?role=`, `/auth/login`, `/auth/callback`, `/auth/logout`, `/api/me`

### dashboard/app.py changes
- `/ui` route replaced: now returns `_CHAT_HTML` (inline chat template)
- `/api/status` new route — proxies GET to daemon `/status`
- `/api/chat` new route — POST `{message, model}` → daemon `/submit` class 1 → returns `{response, node_id, routed_to, model}`
- oauth server started as daemon thread in `__main__` before main `app.run()`

### Chat UI (_CHAT_HTML inline template)
- Dark theme (1a1a2e background, e94560 accent)
- Header: VERNEX + node_id + version (fetched from /api/status) + logout link
- Model selector: mistral / llama3.1
- Scrollable chat log with user/assistant/error message bubbles
- Mobile-responsive (flexbox, viewport meta)
- Submits to `/api/chat`, displays routed_to in footer

### .gitignore additions
- `config/oauth.json`, `config/users.json`, `config/token-*.json`
- `config/trusted_certs/`, `config/last_seen_time.json`
- `config/node.mldsa.key`, `config/node.mldsa.pub`

### README.md — Google OAuth setup section
- console.cloud.google.com steps, redirect URI to set
- Where to paste client_id/secret in oauth.json
- Role table (admin vs user)
- First-login gets admin explanation

### Deploy (node1 only)
```bash
# Install nginx
sudo apt-get install -y nginx

# Install nginx site
sudo cp ~/vernex/scripts/nginx-vernex.conf /etc/nginx/sites-available/vernex
sudo ln -sf /etc/nginx/sites-available/vernex /etc/nginx/sites-enabled/vernex
sudo rm -f /etc/nginx/sites-enabled/default
sudo nginx -t && sudo systemctl enable --now nginx

# Restart dashboard (starts oauth + install servers as threads)
sudo systemctl restart vernex-dashboard

# Verify
curl -s http://127.0.0.1:5002/api/me            # → 401 (expected)
curl -s http://172.17.0.132:5080/install | head -3  # → script header
curl -s http://172.17.0.132:5080/auth/login         # → redirect to Google
```
- After deploy: fill in `~/vernex/config/oauth.json` with google_client_id and google_client_secret
- Redirect URI to register in Google Cloud Console: `http://172.17.0.132:5080/auth/callback`

## What Was Just Completed (v0.12.12 — four bug fixes: base_url, mDNS-first by node_id, trust by node_id, install server on LAN)

### Bug 1 — vernex-node-setup.sh Step 7: bootstrap base_url corrected
- Was writing `http://<bootstrap>:11434` (Ollama URL) as the peer base_url
- Fixed to `https://<bootstrap>:7701` (Vernex API URL) in both the Python heredoc patch path and the new-config default
- `peerAPIURL()` already correctly extracts just the hostname and appends `:7701`, so heartbeat routing is unaffected; `buildOllamaNodes` will no longer try to use the bootstrap as an Ollama peer (correct — bootstrap nodes don't run Ollama for compute nodes)

### Bug 2 — registerWithPeers: mDNS-first skip keyed on node_id (`peer.go`)
- Previous skip used `mDNSHosts[parsed.Hostname()]` — IP string comparison that breaks when DHCP/NAT changes addresses
- New skip derives `peerNodeID = nodeIDFromPublicKey(ed25519.PublicKey(raw))` from `peer.PublicKey`, checks `dynamicSnapshot[peerNodeID]` — cryptographic identity, not transport
- Hostname-based match kept as fallback for peers with no public key in config
- Log suppression: `[~]` only printed while `!existing.TrustApproved` (read from peerRegistry, keyed by node_id)

### Bug 3 — /register handler: trust lookup by node_id not name (`handlers.go`)
- Was checking only `p.Name == req.NodeID` — fails for manually-configured peers with human names like `"bootstrap"` in node.json
- Added secondary check: `nodeIDFromPublicKey(ed25519.PublicKey(raw)) == req.NodeID` for each peer with a public key
- Added `"crypto/ed25519"` import to handlers.go
- Result: a compute node's /register heartbeat correctly sets TrustApproved on the bootstrap even when the bootstrap's peer_nodes entry has `"name": "bootstrap"` rather than its VRX-... node_id

### Bug 4 — dashboard: LAN-accessible install server on port 5001 (`app.py`)
- Extracted shared `_install_handler()` function used by both `@app.route("/install")` and `_install_app`
- `_install_app = Flask("vernex_install")` — separate minimal Flask app with only `/install`
- Started as daemon thread via `_run_install_server()` at process start, binds `0.0.0.0:5001`
- Full dashboard now on `127.0.0.1:5000` (localhost only — was `0.0.0.0`)
- Bootstrap operator workflow: share `http://192.168.x.x:5001/install?token=<id>` with LAN worker operators

### Deploy (both nodes)
- Node1: `sudo systemctl stop vernex-daemon && sudo cp ~/vernex/daemon/vernex-node /usr/local/bin/vernex-node && sudo systemctl start vernex-daemon && sudo systemctl restart vernex-dashboard`
- Node2: `cd ~/vernex && git pull && cd daemon && go build -o vernex-node . && sudo systemctl stop vernex-daemon && sudo cp vernex-node /usr/local/bin/vernex-node && sudo systemctl start vernex-daemon`
- Verify install server: `curl -s http://172.17.0.132:5001/install | head -3`

## What Was Just Completed (v0.12.11 — non-interactive enrollment; /install returns curl one-liner with token)

### vernex-node-setup.sh — `--token` flag + non-interactive Step 8
- Added `$@` parser at top of script (after constants, before step 1): loops `$@`, extracts `--token <value>` into `_ENROLL_TOKEN`
- Step 8 rewritten with three branches:
  1. `node.crt` exists → skip (already enrolled)
  2. `_ENROLL_TOKEN` + `BOOTSTRAP_IP` set → `vernex-node ca enroll --bootstrap ... --token "$_ENROLL_TOKEN"` non-interactively
  3. `BOOTSTRAP_IP` set but no token → print skip hint with retry command
  4. No bootstrap → existing "no bootstrap found" warning
- Ctrl-D stdin read (`python3` loop reading `sys.stdin`) fully removed
- Works both ways: `bash vernex-node-setup.sh --token '...'` or `curl ... | bash -s -- --token '...'`

### dashboard/app.py — `/install` route: curl one-liner with injected token
- `GET /install` (no params) → serves raw `vernex-node-setup.sh` (existing behavior; for plain curl | bash)
- `GET /install?token=<token-id>` → reads `~/vernex/config/token-<token-id>.json`; returns plain-text curl one-liner:
  ```
  curl -fsSL 'http://<host>/install' | bash -s -- --token '<json>'
  ```
- Single-quotes in the token JSON are escaped with `'\\''` before embedding in the one-liner
- 404 if the token file doesn't exist (expired, wrong ID, or not yet generated)
- Bootstrap operator workflow: share `http://node1:5000/install?token=<id>` with the worker; worker runs the returned command verbatim

### Deploy (both nodes need restart for version bump; setup.sh has no binary)
- Node1: `sudo systemctl stop vernex-daemon && sudo cp ~/vernex/daemon/vernex-node /usr/local/bin/vernex-node && sudo systemctl start vernex-daemon`
- Node2: `cd ~/vernex && git pull && cd daemon && go build -o vernex-node . && sudo systemctl stop vernex-daemon && sudo cp vernex-node /usr/local/bin/vernex-node && sudo systemctl start vernex-daemon`

## What Was Just Completed (v0.12.10 — trust-request implemented; PushedStatus preserved; manual-trust peerRegistry fix)

### Bug 1 — outbound /trust-request implemented (`peer.go`)
- `sendTrustRequestIfNeeded(node, extIP)` added; called every heartbeat tick alongside `registerWithPeers`
- Short-circuits if `config/node.crt` exists (CA-enrolled nodes use cert-chain trust, not manual approval)
- Short-circuits if `cfg.TrustApproved == true` (persisted after first acknowledged delivery)
- Prefers mDNS LAN URL: derives peer node_id from `peer.PublicKey` in `cfg.PeerNodes`, looks up `dynamicPeers` — falls back to static `peerAPIURL` if not on LAN
- Payload: `node_id`, `public_key`, `mldsa_public_key`, `api_url` — matches inbound handler at `handlers.go:382`
- On HTTP 200: sets `node.cfg.TrustApproved = true` + calls `saveConfig()` — persisted to `node.json`
- `config.go`: added `TrustApproved bool \`json:"trust_approved,omitempty"\`` to `NodeConfig`

### Bug 2 — PushedStatus preserved on mDNS rescan (`mdns.go`)
- All three branches (trusted-peer `mdns.go:207`, auto-trust `mdns.go:227`, manual-trust `mdns.go:268`) now call `GetByNodeID` before `Register` and carry forward `PushedStatus`, `CertVerified`, `TrustApproved`
- The 30s blind overwrite that was clearing `PushedStatus` (and thus breaking the `public_ip → mDNSHosts` bridge) is gone

### Bug 3 — manual-trust branch registers in peerRegistry (`mdns.go`)
- Manual-trust branch now calls `peerRegistry.Register()` (preserving existing fields) before `fetchAndStorePeerStatus`
- `fetchAndStorePeerStatus` at `mdns.go:302` was exiting early on `!ok` from `GetByNodeID` because the peer was in `dynamicPeers` but not `peerRegistry`
- With the fix, `PushedStatus` is populated and `public_ip` enters `mDNSHosts` on the next heartbeat tick

### Expected observable behavior after deploy
- Node2 logs `[✓] trust request: delivered to bootstrap-0 via https://172.17.0.132:7701` at ~15s
- Node1 dashboard shows Approve/Deny for VRX-a5474b585793501c
- No more `[!] heartbeat: could not reach` for the public IP after the first mDNS cycle

## What Was Just Completed (v0.12.9 — cert_verified + trust_approved in /status)

### daemon/node.go — statusResponse + getOwnStatus
- Added `cert_verified bool` and `trust_approved bool` to `statusResponse` struct
- `getOwnStatus()` now computes both from the local filesystem on every call:
  - `cert_verified = true` if `config/node.crt` exists (written by `ca enroll`; never exists on TOFU nodes)
  - `trust_approved = true` if `node.crt` + `root.crt` both exist (full CA chain enrolled)
- Filesystem check is always accurate — survives daemon restarts with no in-memory warm-up period
- Added `"path/filepath"` to node.go imports

### daemon/handlers.go — /status DRY refactor
- `/status` handler now calls `getOwnStatus(node)` directly — was duplicating the same peer-count and IP logic inline
- No functional change to the handler; eliminates ~20 lines of duplication

### /peers — already correct
- `/peers` already exposes `cert_verified` and `trust_approved` per peer (set async on each heartbeat via `FetchPeerCert` + `VerifyCert`); no changes needed

### Deploy instructions (both nodes need restart)
- Node1: `sudo systemctl stop vernex-daemon && sudo cp ~/vernex/daemon/vernex-node /usr/local/bin/vernex-node && sudo systemctl start vernex-daemon`
- Node2: `cd ~/vernex && git pull && cd daemon && go build -o vernex-node . && sudo systemctl stop vernex-daemon && sudo cp vernex-node /usr/local/bin/vernex-node && sudo systemctl start vernex-daemon`
- Verify: `curl -sk https://localhost:7701/status | jq '{version, cert_verified, trust_approved}'`
  - Node1 expects: `"cert_verified": true, "trust_approved": true` (has node.crt + root.crt)
  - Node2 expects: `"cert_verified": true, "trust_approved": true` (enrolled via token)

## What Was Just Completed (session 15 continued — security: token signature exposure + CA init)

### Bootstrap node CA initialized ✅
- `vernex-bootstrap-setup.sh` ran to completion on Node-1 (2026-05-03)
- `config/root.crt` exists and valid
- `config/intermediate.crt` exists and valid
- `vernex-dashboard` service active; `http://127.0.0.1:5000` → HTTP 200

### Token signature exposure — revoked + fixed ✅
- **Incident**: 5 enrollment tokens (including ML-DSA signatures) were printed to stdout by `vernex-node ca token` and appeared in a chat log
- **Revoked**: `config/enrollment_tokens.txt` deleted; 5 old token files removed
- **Fix in `daemon/main.go`**: `ca token` now writes full JSON to `config/token-<token_id>.json` (mode 0600); stdout shows only `token_id`, `expires_at`, and `path` — signature never reaches terminal or logs
- **Fix in `vernex-bootstrap-setup.sh`**: token generation loop reads JSON from the saved file (not stdout); end-of-script summary prints only `token_id` + `expires_at` (not full token file)
- **5 fresh tokens generated** and saved to `config/enrollment_tokens.txt` (mode 600); individual token files in `config/token-<id>.json`
- The old 5 token IDs are not burned in `used_tokens.json` — they were never used for enrollment, so they are safe to discard rather than burn

## What Was Just Completed (v0.12.5–v0.12.8 — mDNS-first heartbeat, eliminate "Bootstrap unreachable" warning)

### Problem solved
Nodes configured with a peer's public IP (76.244.40.49) in `peer_nodes` were logging
`[!] heartbeat: could not reach bootstrap` even when the same peer was already reachable
on the LAN via mDNS. Fixed over four incremental versions.

### v0.12.5 — daemon/peer.go: mDNS-first skip by hostname
- `registerWithPeers()`: snapshot `dynamicPeers` before the static loop; build `mDNSHosts` set of LAN hostnames
- Static peer loop: skip (with `[~]` log) if peer hostname already in `mDNSHosts`
- Bug: `mDNSHosts` used LAN IP; static config used public IP — hostnames never matched

### v0.12.6 — peer.go + mdns.go: bridge via public_ip in PushedStatus
- `fetchAndStorePeerStatus(node, nodeID, apiURL)` new function in mdns.go: async GET `/status` on mDNS discovery, stores response in `PeerEntry.PushedStatus`
- Called (non-blocking) in trusted-peer, auto-trust, and manual-trust-queue branches when `!alreadyKnown`
- `registerWithPeers()`: extracts `public_ip` from PushedStatus of mDNS live peers → adds to `mDNSHosts`
- `startMDNS` moved before `startHeartbeatLoop` in main.go; heartbeat initial delay 2s → 15s
- `mDNSTrustApproved` map: silent skip (no log) once peer is trust_approved

### v0.12.7 — peer.go + main.go: move PullCASync to mDNS loop; node.configDir field
- Removed synchronous `PullCASync` block from `main()` startup (was hitting public IP before mDNS)
- PullCASync now runs inside the mDNS dynamic-peers heartbeat loop, retrying every 60s tick until `root.crt` exists — always uses LAN URL
- `configDir string` field added to `Node` struct and wired in `NewNode()`; peer.go uses it for `root.crt` existence check
- Bug: `dynamicPeers` still empty for unenrolled nodes — mDNS-first skip never fired

### v0.12.8 — mdns.go: add pending-trust peers to dynamicPeers (root cause fix)
- Root cause: manual-trust-queue branch in `startMDNS` never added peers to `dynamicPeers`
- Fix: in the manual-trust-queue branch, add peer to `node.dynamicPeers` for outbound routing even while trust is pending; inbound trust (signature verification, request acceptance) remains gated
- Result: `mDNSHosts` is populated before first heartbeat → skip fires → "Bootstrap unreachable" warning fully eliminated for LAN nodes (daemon side)

### scripts/vernex-node-setup.sh — remove script-side trust request ✅ FULLY RESOLVED
- Root cause of remaining warning: the script itself had a "Bootstrap trust registration" step that curl-POSTed `/trust-request` directly to each bootstrap node after install
- Fix: removed the entire section; trust-request is the daemon's job via the 60s heartbeat loop
- `vernex-bootstrap-setup.sh` had no equivalent section — no change needed

### Canonical script locations — duplicate files removed
- Root copies (`/vernex-node-setup.sh`, `/vernex-bootstrap-setup.sh`) are canonical — these are what `curl | bash` fetches
- `scripts/vernex-node-setup.sh` and `scripts/vernex-bootstrap-setup.sh` were diverged duplicates — deleted via `git rm`
- Remaining in `scripts/`: `vernex-node-wipe.sh`, `test-priority.sh`, `vernex-daemon.service`, `vernex-dashboard.service`, `90-vernex-inhibit.rules` (no root counterparts)

### scripts/vernex-node-setup.sh + scripts/vernex-bootstrap-setup.sh — "Text file busy" fix
- Step 5: added `sudo systemctl stop vernex-daemon` before `sudo cp vernex-node /usr/local/bin/vernex-node`
- Step 10 restarts the service after install (existing behavior preserved)

### Version bumps across all four versions
- `daemon/node.go` Version field + banner: 0.12.4 → 0.12.5 → 0.12.6 → 0.12.7 → 0.12.8
- `daemon/mdns.go` TXT record: version=0.12.4 → … → version=0.12.8

## What Was Just Completed (v0.12.0 — clock verification, /time endpoint, CA ops gated)

### daemon/ca/clockcheck.go — new file

Four-step system clock verification gating CA operations:

**Step A — build-timestamp consistency**: if `-ldflags "-X vernex/daemon/ca.BuildTime=<RFC3339>"` is set at build time and `time.Now()` is before that timestamp, `BlockCAOps=true`. Prevents ops on a clock-rewound system.

**Step B — last-known-good regression**: `config/last_seen_time.json` stores the last verified UTC time. If `time.Now()` is more than 24h *before* the stored time, `BlockCAOps=true`. Prevents backwards clock jumps between restarts.

**Step C — NTP median consensus (pure UDP, RFC 5905)**: queries `time.cloudflare.com:123`, `0.pool.ntp.org:123`, `1.pool.ntp.org:123` in parallel (3-second timeout each). Sends 48-byte NTP request (LI=0, VN=4, Mode=3), reads transmit timestamp from bytes 40–47, converts NTP epoch (Jan 1 1900, constant 2208988800s) to Unix time. Takes median of successful responses. Drift >5 min → `BlockCAOps=true`; drift >1 min → warning only; all timeout → fall through to Step D.

**Step D — bootstrap /time fallback**: GET `{bootstrapURL}/time` (5s timeout). Verifies ML-DSA signature over `utc+"|"+node_id` using the peer's VernexCert when `TrustStore.RootCert != nil` (enrolled mode); accepts without verification in TOFU mode. Saves `last_seen_time.json` on success.

`BlockIfClockInvalid(status ClockStatus) error` — returns error iff `BlockCAOps=true`.

### daemon/ca/clockcheck_test.go — 7 tests, all passing

1. `TestBuildTimeUnset` — no block
2. `TestBuildTimeFuture` — BlockCAOps=true, Source="build"
3. `TestClockBackwardsMoreThan24h` — BlockCAOps=true, Source="persisted"
4. `TestNTPDriftSmall` — mock NTP +30s, Verified=true, no block
5. `TestNTPDriftLarge` — mock NTP +10min, BlockCAOps=true, Source="ntp"
6. `TestAllNTPTimeoutNoBootstrap` — empty ntpServers + nil bootstrap, Source="unverified", no block
7. `TestLastSeenTimeWritten` — file written after successful NTP check

Mock NTP server in test helper: UDP listener on random port, returns caller-controlled timestamp. `ntpServers` var overridable per-test.

### daemon/handlers.go — /time endpoint + clock guards

**GET /time** (public, rate-limited): returns `{"utc","unix","node_id","signature"}` where signature is ML-DSA over `utc+"|"+node_id`. Used by peers for Step D bootstrap time consensus.

**Clock guards** on `/sign-intermediate`, `/enroll`, `/token-gen`: each reads `node.clockStatus` under `node.mu.RLock()` and calls `BlockIfClockInvalid`; returns HTTP 503 if blocked.

### daemon/node.go — clockStatus field

`clockStatus vernexca.ClockStatus` added to Node struct. Written under `node.mu.Lock()` in main() and background goroutine; read under `node.mu.RLock()` in CA handlers.

### daemon/main.go — wiring

- `resolveBootstrapNodes(cfg NodeConfig) []string` — converts peer_nodes BaseURLs to HTTPS API URLs for clock check Step D
- Clock check runs synchronously after `printBanner()`; result printed as `[✓]/[~]/[!]` clock line
- Background goroutine re-checks every 30 minutes; logs `[!]` if drift exceeds threshold

### Build note
Set `BuildTime` for production builds:
```bash
go build -ldflags "-X vernex/daemon/ca.BuildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)" -o vernex-node .
```
Leave unset (default `""`) for local dev to skip the build-time check.

## What Was Just Completed (v0.11.5 — bootstrap provisioning script + enrollment in node-setup)

### scripts/vernex-bootstrap-setup.sh — new file

Full bootstrap node provisioning from a fresh Pop!_OS or Ubuntu 24.04 system. 10-section idempotent script:

1. **Pre-flight**: non-root check, OS detection, required tools (curl git python3 jq), public IP via ipify
2. **Go install**: version check, download go1.22.5 if absent, PATH persistence
3. **Repo + build**: clone or pull, `go build -ldflags "-X vernex/daemon/ca.BuildTime=..."` (fallback to plain build if variable absent)
4. **Bootstrap config**: creates or patches `config/node.json` with `is_bootstrap: true`; runs daemon for 3s to generate keypairs and persist node_id
5. **CA init**: `vernex-node ca init` (root CA) + `vernex-node ca init-intermediate`; both idempotent
6. **Peer CA sync**: interactive prompt for existing bootstrap URL; pulls `/ca-sync` and saves root.crt + intermediate.crt via python3; skippable (new network root)
7. **Enrollment tokens**: generates 5 single-use 30-day tokens via `vernex-node ca token`; extracts JSON via python3; saves to `config/enrollment_tokens.txt` (mode 600); idempotent
8. **Systemd service**: writes `/etc/systemd/system/vernex-daemon.service` directly via `sudo tee`; idempotent diff check
9. **Start + health check**: enables + restarts service; polls `/health` up to 5× (3s each) for HTTP 200
10. **Summary box**: PUBLIC_IP, API URL, firewall commands, next-steps checklist, then `cat enrollment_tokens.txt`

### scripts/vernex-node-setup.sh — two sections added after `go build`

**Section A — DNS bootstrap discovery**
- Tries `dns.resolver.resolve('_vernex._tcp.vernex.net', 'TXT')` via python3/dnspython
- Extracts `bootstrap=<url>` from TXT record
- Falls back to hardcoded `https://76.244.40.49:7701` on any failure (import error, NXDOMAIN, etc.)
- Sets `$BOOTSTRAP_URL` for Section B

**Section B — Certificate enrollment**
- Skips if `config/node.crt` already exists
- Prompts operator to paste multi-line enrollment token JSON (Ctrl-D to submit or skip)
- Validates JSON via python3 before passing to `./vernex-node ca enroll --bootstrap $BOOTSTRAP_URL --token "$TOKEN"`
- Prints retry instructions on failure or skip

### Version bump
- `daemon/node.go`: Version field `"0.11.5"`, banner `v0.11.5`
- `daemon/mdns.go`: mDNS TXT record `version=0.11.5`

## What Was Just Completed (v0.11.4 — mDNS auto-trust for CA-enrolled peers)

### daemon/mdns.go — feature addition

**mDNS auto-trust via CA cert check**
- In `startMDNS()`, the `else` branch (unknown peer, not in `cfg.PeerNodes`) now attempts a CA cert check before queuing a manual trust request:
  1. `vernexca.FetchPeerCert(peerAPIURL, node.buildPeerTLSClient(5s))` — fetches the peer's VernexCert from `/ca-sync`
  2. `node.trustStore.VerifyCert(*cert)` — validates the cert chain against the local TrustStore
  3. **If chain valid** — peer is registered directly into `peerRegistry` with `CertVerified: true` and added to `dynamicPeers`; logs `[✓] mDNS auto-trust: <id> cert chain verified`
  4. **If cert invalid or not found** — falls through to the existing `trustRequests` queue for manual operator approval; logs `[↑] mDNS discovered unknown peer: <id> — no valid cert, queued for manual approval`
- Adds `vernexca "vernex/daemon/ca"` import to `mdns.go`
- CA-enrolled LAN peers now join the cluster without operator intervention; unenrolled peers still require manual `/trust-approve`

## What Was Just Completed (v0.11.3 — IPv6 URL bracketing fix in mDNS)

### daemon/mdns.go — bug fix only

**IPv6 URL bracketing**
- `avahiPeer` struct gains `addrFamily string` ("IPv4" or "IPv6"), detected by whether addr contains `:`
- `discoverAvahiPeers`: builds a dedup map (`byNodeID`) instead of a slice; IPv4 beats IPv6 — an existing IPv6 entry is overwritten only when a new IPv4 entry arrives for the same `node_id`
- `startMDNS` discovery loop: `peerAPIURL` now wraps IPv6 addresses in brackets (`https://[addr]:port`) while leaving IPv4 unchanged

## What Was Just Completed (v0.11.2 — daemon/main.go split into 9 focused files)

### Refactor: daemon/main.go → 9 focused source files (REFACTOR ONLY — no logic changes)

`daemon/main.go` was 2801 lines. Split into package main files, all in `daemon/`:

| File | Contents | Lines |
|------|----------|-------|
| config.go | PeerNode, NodeConfig, loadConfig, saveConfig, generateNodeID | 106 |
| node.go | Node struct, NodeStats, statusResponse, NewNode, outboundIP, fetchPublicIP, startContributionTicker, startPublicIPRefresher | 300 |
| peer.go | PeerEntry, PeerRegistry, isPrivateIP, peerAPIURL, deriveOllamaURL, registerWithPeers, startHeartbeatLoop | 216 |
| mdns.go | registerMDNSViaAvahi, avahiPeer, discoverAvahiPeers, connectionType, startMDNS | 226 |
| punch.go | sendHolePunchPackets, initiatePunch, stunResponse, discoverExternalEndpoint, signalPunch, isLocalhost, startUDPListener, startIPWatchdog, startAutoPunch | 192 |
| scheduler.go | TokenRequest, Scheduler, pendingReview, commonsAssessment, assessCommunityBenefit, RateLimiter, rateLimitKey, startCommonsReviewExpiry, startRateLimiterPrune | 317 |
| inference.go | defaultModel, ollamaNode, buildOllamaNodes, callOllamaAt, routedCallOllama, ContextTurn, buildPromptWithContext, searchWeb, needsWebSearch | 225 |
| tls.go | mldsaScheme, nodeIDFromPublicKey, loadOrGenerateKeypair, loadOrGenerateMLDSAKeypair, buildTLSConfig, signRequest, verifyPeerRequest, peerPublicKey, peerMLDSAPublicKey | 266 |
| handlers.go | startHTTPServer (all 16 endpoints), handleConnection | 739 |
| main.go | takeInhibitorLock, runCACommand, main() | 311 |

Build passes, `go vet` clean, all logic unchanged.

## What Was Just Completed (v0.11.1 — CertVerified race fix, PullCASync on startup, mDNS heartbeat)

### daemon/main.go — bug fixes + wiring

**CertVerified race fix (two-part)**
- `PeerRegistry.SetCertVerified(nodeID, verified)` — new method that atomically updates
  CertVerified under write lock; avoids the GetByNodeID → Register read-modify-write race
- `/register` handler: before calling `Register(entry)`, checks if existing entry has
  `CertVerified=true`; if so, preserves it on the new entry — heartbeat re-register no
  longer resets verified state
- Async cert-verify goroutine: now calls `SetCertVerified` directly instead of
  GetByNodeID + Register; eliminates second overwrite race window

**PullCASync wired at daemon startup**
- In `main()`, after keypair load, before goroutines: if `config/root.crt` does not
  exist and peer_nodes is non-empty, calls `vernexca.PullCASync()` against each peer
- Runs synchronously so TrustStore is populated before first heartbeat fires
- Logs `[✓] CA certs pulled from {peer}` on success or `[!]` on error

**mDNS-discovered peers added to heartbeat sweep**
- `Node` struct gains `dynamicPeers map[string]string` (nodeID → api_url) + `dynamicPeersMu`
- mDNS discovery loop: when a trusted peer is found, its api_url is stored in `dynamicPeers`
  in addition to being registered in `peerRegistry`
- `registerWithPeers()` extended: after the static peer_nodes loop, snapshots `dynamicPeers`
  under read lock and heartbeats to each — mDNS peers are now reached automatically without
  manual node.json entries

## What Was Just Completed (v0.11.0 — InsecureSkipVerify=false, Cert Chain Verification)

### daemon/ca/verify.go — new file
- `TrustStore struct { RootCert *VernexCert; Intermediates []VernexCert; configDir string }`
- `LoadTrustStore(configDir) (*TrustStore, error)` — loads root.crt, trusted_intermediates.json, local intermediate.crt
- `(*TrustStore).VerifyCert(cert VernexCert) error` — finds issuer intermediate by CN, verifies ML-DSA sig + validity window
- `(*TrustStore).AddIntermediate(cert VernexCert) error` — root-verifies + persists to trusted_intermediates.json
- `(*TrustStore).VerifyTLSPeerCert(rawCerts [][]byte, _ [][]*x509.Certificate) error` — TOFU: logs peer cert CN+serial, always allows
- `(*TrustStore).NewTLSClient(timeout) *http.Client` — centralized peer HTTP client; TOFU TLS; VerifyTLSPeerCert installed

### daemon/ca/sync.go — updated
- `CASyncPayload` gains `NodeCert json.RawMessage` — this node's own VernexCert in /ca-sync response
- `HandleCASync` now includes `config/node.crt` in payload
- `FetchPeerCert(peerURL string, client *http.Client) (*VernexCert, error)` — fetches peer's cert from /ca-sync

### daemon/ca/verify_test.go — new file (6 test cases, all passing)
- TestLoadTrustStore_NoFiles — TOFU mode on empty dir
- TestLoadTrustStore_WithChain — loads root + intermediate
- TestVerifyCert_ValidChain — full chain verify passes
- TestVerifyCert_UnknownIssuer — unknown issuer rejected
- TestVerifyCert_TamperedSignature — corrupted ML-DSA sig rejected
- TestVerifyCert_ExpiredCert — expired cert rejected
- TestAddIntermediate — AddIntermediate persists to trusted_intermediates.json, survives reload

### daemon/main.go — zero InsecureSkipVerify: true instances
- `Node` struct gains `trustStore *vernexca.TrustStore`
- `NewNode` signature adds `configDir string`; initializes TrustStore at startup
- `(*Node).buildPeerTLSClient(timeout) *http.Client` — all peer HTTP clients go through TrustStore.NewTLSClient
- All 5 `InsecureSkipVerify: true` occurrences replaced:
  - `discoverExternalEndpoint()` — now takes `ts *vernexca.TrustStore` parameter
  - `signalPunch()` — now takes `ts *vernexca.TrustStore` parameter
  - `registerWithPeers()` — uses `node.buildPeerTLSClient()`
  - `/peer-status/{node_id}` handler — uses `node.buildPeerTLSClient()`
  - `ComputeNodeEnroll()` in enrollment.go — uses `LoadTrustStore(configDir).NewTLSClient()`
- `PeerEntry` gains `CertVerified bool` (json: `cert_verified`)
- `/register` handler: async goroutine calls `FetchPeerCert` + `VerifyCert` on each new peer; updates registry on success
- `/peers` response includes `cert_verified` per peer
- grep -r "InsecureSkipVerify: true" ~/vernex/ → empty (zero instances in source)
- Version bumped to v0.11.0 in stats, banner, mDNS TXT record

### Note on TLS approach
TLS still uses `InsecureSkipVerify = true` (field assignment, not struct literal) inside
`TrustStore.NewTLSClient()` because nodes use ed25519 self-signed TLS certs with no CA chain.
Application-layer trust is enforced by ML-DSA payload signatures (X-Vernex-Signature-MLDSA).
The behavior is now centralized + logged (TOFU). Full TLS chain verification comes when
buildTLSConfig is upgraded to issue CA-signed ML-DSA TLS certs.

## Previously Completed (v0.10.0 — Distributed CA Layer)

### daemon/ca/ package — 4 new files
- `ca/root.go`: VernexCert + VernexCSR types; GF(256) Shamir split/combine (AES field);
  RootCA struct with GenerateRootCA / LoadRootCA / LoadRootCAFromShares / SignIntermediateCSR
- `ca/intermediate.go`: IntermediateCA with GenerateIntermediateCA / LoadIntermediateCA /
  SignComputeNodeCSR; VernexCSR self-sign + verify; UnmarshalPublicKey helper
- `ca/enrollment.go`: EnrollmentToken (GenerateEnrollmentToken / VerifyEnrollmentToken /
  BurnEnrollmentToken / ComputeNodeEnroll); used_tokens.json burn-on-use registry
- `ca/sync.go`: HandleCASync HTTP handler + PullCASync gossip pull with chain validation

### VernexCert format (application-layer ML-DSA X.509-like certs)
- JSON-encoded credential with X.509-like fields: CN, O, OU, validity, SAN extensions
- All CA keys ML-DSA 44 (cloudflare/circl, already in go.mod)
- Signatures: ML-DSA over canonical TBS JSON (excluding Signature field)
- Self-signed root cert → root signs intermediate → intermediate signs compute nodes
- Chain verified end-to-end: root self-signed PASS, intermediate chain PASS
- Note: JSON format (not DER X.509) — Go 1.22 stdlib doesn't support ML-DSA in x509.
  Will migrate to DER when Go 1.24+ adds native ML-DSA x509 support.

### CLI subcommands added (vernex-node ca <sub>)
- `ca init` — generates root CA (single mode: saves root.key; threshold mode: Shamir shares to stdout)
- `ca init-intermediate` — generates intermediate CA keypair + CSR, signs with local root
- `ca token [network-id]` — generates enrollment token (is_bootstrap=true required)
- `ca enroll --bootstrap <url> --token '<json>'` — enrolls compute node, replaces ML-DSA keypair

### HTTP endpoints (daemon)
- `GET /ca-sync` — gossip: returns root.crt + intermediate.crt (all nodes)
- `POST /sign-intermediate` — bootstrap only: root signs intermediate CSR
- `POST /enroll` — bootstrap only: intermediate signs compute node CSR with token
- `POST /token-gen` — bootstrap + localhost only: generates enrollment token via API

### NodeConfig additions (STEP 1)
- `is_bootstrap bool` — enables /sign-intermediate, /enroll, /token-gen
- `ca_mode string` — "single" (default) or "threshold"
- `ca_threshold_k int` — Shamir shares required (default 3)
- `ca_threshold_n int` — Shamir total shares (default 5)

### Bootstrap CA setup workflow (Node-1, single mode)
```bash
vernex-node ca init               # creates config/root.{key,crt}
vernex-node ca init-intermediate  # creates config/intermediate.{key,csr,crt}
vernex-node ca token              # prints 30-day enrollment token
# share token with new node operator; they run:
vernex-node ca enroll --bootstrap https://76.244.40.49:7701 --token '<json>'
```

### Previously Completed
- mDNS via avahi D-Bus (v0.9.2) — replaces hashicorp/mdns which conflicted with system avahi

## Previously Completed
- Push-based status in heartbeat — remote nodes visible behind NAT
- PeerEntry.PushedStatus json.RawMessage: stores last /status payload received on heartbeat
- getOwnStatus(node *Node) statusResponse: builds full status response without HTTP round-trip
- registerWithPeers() signature changed to (node *Node, extIP, extPort) — derives cfg internally
- Heartbeat payload now includes "status": full statusResponse JSON
- /register handler: accepts status json.RawMessage, stores in PeerEntry.PushedStatus
- /peer-status/{node_id}: tries direct fetch first; falls back to PushedStatus if within peerLiveTTL; 503 only if both unavailable

## Previously Completed
- Relay status polling for remote nodes via bootstrap proxy
- /peer-status/{node_id} endpoint: proxies /status to a registered peer's api_url; 503 if unreachable
- dashboard index(): direct poll with 1s timeout for remote nodes, falls back to /peer-status/{node_id} relay
- LOCAL nodes still use 2s direct timeout (no relay needed)
- via_relay flag tracked per node; dashboard shows ↔ RELAY badge (blue) next to ONLINE for relayed nodes
- relay badge CSS: .badge.relay { background: #1a2a3a; color: #58a6ff }
- Compact table status cell also shows ↔ RELAY indicator

## Previously Completed (v0.9.1)
- IP change watchdog + NAT registration fix
- registerWithPeers(): api_url now uses external IP when behind NAT (extIP != LAN IP)
- lastLANIP + lastPublicIP atomic.Value fields on Node; initialized in NewNode()
- IP watchdog goroutine (30s tick): compares LAN + cached public IP against last known
- On change: re-runs discoverExternalEndpoint(), re-runs registerWithPeers(), clears peerHoles map
- Uses cached public IP (not ipify) to avoid hammering external API every 30s
- Version bumped to v0.9.1

## Previously Completed (v0.9.0)
- UDP hole punching — Phase 2 of Vernex P2P
- peerHoles map + peerHolesMu + udpConn added to Node struct
- UDP listener on port 7700 (coexists with TCP): records confirmed direct connections when VERNEX-PUNCH packet received
- connectionType(peer): "local" (RFC1918 API URL), "direct" (UDP packet received), "relayed" (default)
- sendHolePunchPackets(): sends 5 VERNEX-PUNCH datagrams at 50ms spacing
- initiatePunch(): signals peer via /punch-signal + sends packets simultaneously
- Auto-punch goroutine: 15s delay, then every 5 min, punches toward RELAYED peers with known external endpoint
- /punch-request endpoint: bootstrap receives coordinated punch request, looks up both peers, signals each
- /punch-signal endpoint: node receives instruction to punch toward IP:port
- isPrivateIP(): RFC1918 CIDR check for same-LAN detection
- signalPunch(): HTTPS POST helper to /punch-signal on peer
- PeerRegistry.GetByNodeID(): lookup by node ID for /punch-request
- /status: now includes direct_peers + local_peers counts
- /peers: each entry now includes connection_type
- Dashboard: CONNECTION stat card + compact table column (green=direct, blue=local, amber=relayed)
- Version bumped to v0.9.0

## Previously Completed
- Brave Search API replacing DDG Instant Answers — live web results injected into LLM context
- brave_api_key field added to NodeConfig (omitempty, loaded from config/node.json, gitignored)
- braveSearchResponse struct parses web.results[].title/url/description from Brave API
- searchWeb(query, apiKey) — GET api.search.brave.com with Accept + X-Subscription-Token headers, count=5
- Graceful fallback: empty key or any request failure → answer without web context (logs warning)
- needsWebSearch intent detection unchanged; comment updated to reference Brave
- Tested: web_searched=true with live AI news results; non-search prompts correctly skip Brave

## Previously Completed
- ML-DSA 44 (CRYSTALS-Dilithium, NIST FIPS 204) hybrid post-quantum crypto upgrade
- Both nodes now generate ed25519 + ML-DSA 44 keypairs at startup
- New key files: config/node.mldsa.key (2560B, mode 0600), config/node.mldsa.pub (base64, shareable)
- All inter-node requests now carry both X-Vernex-Signature (ed25519) and X-Vernex-Signature-MLDSA headers
- ML-DSA enforcement is opt-in per peer via mldsa_public_key in peer_nodes[] config (rolling upgrade)
- New config field: peer_nodes[].mldsa_public_key (omitempty — optional until operator activates it)
- Trust request / approve flow updated to capture and persist mldsa_public_key
- Banner updated to show truncated ML-DSA 44 public key alongside ed25519 key
- circl v1.6.3 dependency added (github.com/cloudflare/circl)
- 9-case test suite passes: round-trip, sign/verify, tampered-sig rejection, wrong-key rejection, replay protection
- Version bumped to v0.8.0

## Security Stack (in place)
- **Hybrid post-quantum identity**: ed25519 + ML-DSA 44 — both sigs on inter-node requests
- **Distributed CA (v0.10.0)**: Root CA → Intermediate CA → Compute Node cert chain; ML-DSA 44 signed;
  VernexCert JSON format (DER migration deferred to Go 1.24+); Shamir K-of-N for threshold root
- TLS on port 7701 — self-signed cert from ed25519 keypair; InsecureSkipVerify centralized in TrustStore.NewTLSClient() with TOFU logging (v0.11.0); full chain enforcement pending CA-signed TLS certs
- **System clock verification (v0.12.0)**: four-step guard (build timestamp → last-known-good regression → NTP median → bootstrap /time); BlockCAOps gates /sign-intermediate, /enroll, /token-gen; `last_seen_time.json` persists verified time; build with `-ldflags "-X vernex/daemon/ca.BuildTime=..."` for production
- Sliding window rate limiter — 60 req/min, per node ID or IP
- Replay protection — 30s timestamp window on inter-node requests
- Trust request approval — operator must approve new node public keys via dashboard

## Outstanding Items

| # | Task | Notes |
|---|------|-------|
| 1 | **Non-provisional patent prep** | Deadline March 24, 2027. Attorney needed Q3 2026. |
| 2 | **ML-DSA + ML-KEM upgrade** | Replace ed25519/X25519. NIST FIPS 203/204. |
| 3 | **Let's Encrypt cert renewal** | vernex.net:5443 expires 2026-08-02. |
| 4 | **IPv6 link-local filter** | mDNS fe80:: log noise only — deferred indefinitely. Both nodes stable. |

## Key Design Rules
- **Dashboard:** node1 (bootstrap) only. Never install vernex-dashboard on compute nodes.
- **Update scripts:** node1 = `vernex-bootstrap-setup.sh` / node2 = `vernex-node-setup.sh`. Never swap.
- **Game saves:** per-user under `~/vernex/config/game_saves/<user_id>/`; game prompts under `~/vernex/config/game_prompts/<user_id>.json`; `<user_id>` = email with `@` and `.` replaced by `_`; unauthenticated uses `guest/`.
- **Docs:** RUNBOOK.md and ARCH_SPEC.md live in repo root. Claude Code must update both alongside CONTINUITY.md after every session commit.

## Workflow Preferences
- Claude Code must update RUNBOOK.md and ARCH_SPEC.md after every session alongside CONTINUITY.md.

## Design Constraints (never violate)
- All cryptography must become post-quantum resistant (ML-DSA + ML-KEM, NIST FIPS 203/204)
- Node onboarding must be zero-touch — no manual key copy/paste for end users
- No hardcoded IPs in source code — all addresses in config/node.json
- The Commons Review consent gate is the core patented mechanism — AI suggests only, human decides
- Class 1/2 token priority must be preserved in all scheduler changes

## Patent Status
- U.S. Provisional Application No. 64/015,885
- Filed: March 24, 2026
- **Non-provisional deadline: March 24, 2027** ← ~10 months away; begin formal claim drafting by Q3 2026
- Six new patent extension claims drafted (hierarchical DHT, distributed CA, post-quantum identity, zero-touch provisioning, threshold signing, distributed contribution ledger)
- NOTICE file (BSL 1.1 compliance) — outstanding, see next steps

## Key Ports
| Port | Service | Notes |
|------|---------|-------|
| 7700 | P2P TCP | Plaintext — no sensitive data yet |
| 7701 | HTTPS API | TLS on all endpoints |
| 11434 | Ollama inference | Plaintext LAN — future: TLS |
| 5000 | Dashboard | Flask HTTP |

## How to Resume Development
```bash
# On Node-1
cd ~/vernex
claude  # Claude Code reads CLAUDE.md + CONTINUITY.md automatically

# Verify both nodes
curl -sk https://localhost:7701/status | jq '{version, node_id}'
curl -sk https://172.17.0.182:7701/status | jq '{version, node_id}'
curl -sk https://localhost:7701/peers | jq .

# Check CA status
ls ~/vernex/config/*.{key,crt,csr} 2>/dev/null
curl -sk https://localhost:7701/ca-sync | jq '{root_present: (.root_cert != null), int_present: (.intermediate_cert != null)}'
```

## Planned Architecture — Bootstrap Node Tier

### Node Types
- **Compute Node** — standard contributor node, runs LLM inference, earns contribution score
- **Regional Bootstrap Node** — serves a geographic region, coordinates peer discovery via STUN-like hole punching, higher score multiplier
- **Global Bootstrap Node** — root rendezvous, always-on, public IP required, highest score multiplier

### Why Bootstrap Nodes
Compute nodes behind home NAT cannot accept inbound connections without port forwarding.
Bootstrap nodes solve this via UDP hole punching (BitTorrent-style):
1. Both nodes connect outbound to bootstrap
2. Bootstrap shares their external IP:port with each other
3. Nodes initiate UDP simultaneously — NATs open holes on both sides
4. Direct P2P connection established — no firewall changes needed

### Contribution Score Multipliers (planned)
| Node Type | Multiplier | Reason |
|-----------|------------|--------|
| Compute Node | 1.0x | Base |
| Regional Bootstrap | 2.5x | Always-on, public IP, coordination overhead |
| Global Bootstrap | 5.0x | Root infrastructure, maximum reliability required |

### Scripts Needed
- `scripts/vernex-node-setup.sh` — exists, compute nodes ✓
- `scripts/vernex-bootstrap-setup.sh` — TODO: regional/global bootstrap provisioning
- `scripts/vernex-node-wipe.sh` — exists ✓

### Patent Relevance
Zero-configuration P2P connectivity via application-layer STUN/ICE-inspired hole punching
combined with ML-DSA cryptographic trust establishment — novel in distributed compute context.
Add as patent extension claim before March 24, 2027 non-provisional deadline.

---

## Continuity Note for Claude Chat (paste at start of new session)
*Vernex Protocol — daemon v0.12.18 (both nodes), dashboard v0.12.29 (node1 only). Two-node cluster (vernex-node1: 172.17.0.132 / 76.244.40.49, vernex-node2: 172.17.0.182). Full security stack: hybrid ed25519 + ML-DSA 44 post-quantum signing, TLS on 7701, rate limiting, trust request approval via dashboard, distributed CA (Root → Intermediate → Compute Node, Shamir K-of-N), clock verification (4-step NTP guard). Dashboard (node1 only): multi-genre text adventure at /game (Fantasy/Sci-Fi/Action/ComedyDrama), D&D-style 4d6 stat rolling with subtype modifiers, per-user save isolation under ~/vernex/config/game_saves/<user_id>/, Google OAuth via vernex.net relay, GPU gauge (RTX 3070). Update scripts: node1 = vernex-bootstrap-setup.sh, node2 = vernex-node-setup.sh. Patent pending US App. 64/015,885, deadline March 24 2027.*

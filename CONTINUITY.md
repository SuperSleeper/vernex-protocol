# Vernex Protocol — Session Continuity

## Last Updated
April 27, 2026 (session 6)

## Current Version
v0.9.1

## Node Registry
| Node | ID | IP | Public Key | Status |
|------|----|----|------------|--------|
| vernex-node1 | VRX-54b89a1684e21ae4 | 172.17.0.132 | prAB8hQJaXoWoT+WO7jbCKBT0TAJPMLjiE4QlOr2D0I= | systemd auto-start |
| vernex-node2 | VRX-a5474b585793501c | 172.17.0.182 | /Lcqppk1jkHUVdgNNHaS15FDKurHO3jgPP3+oMfB83Y= | systemd auto-start |

## What Was Just Completed
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
- TLS on port 7701 — self-signed cert from ed25519 keypair (ML-DSA in X.509 deferred to distributed CA)
- Sliding window rate limiter — 60 req/min, per node ID or IP
- Replay protection — 30s timestamp window on inter-node requests
- Trust request approval — operator must approve new node public keys via dashboard
- InsecureSkipVerify TEMPORARY — pending distributed CA (replaces after ML-DSA CA phase)

## Immediate Next Steps (in priority order)
1. Deploy v0.9.1 to Node-2: git pull, go build, restart daemon
2. Verify /stun returns correct external IPs on both nodes
3. Exchange ML-DSA public keys: copy node.mldsa.pub from each node into the other's peer_nodes[].mldsa_public_key in config — activates hybrid enforcement
4. Test hole punching: watch for [✓] UDP hole punched in daemon logs after both nodes restart
5. Test IP watchdog: change network on one node, watch for [→] IP change detected + [✓] Re-registered
5. Distributed CA — threshold-signed, no single point of failure (ML-DSA certs in X.509)
6. WireGuard remote node connectivity — OPNsense firewall rules for external nodes
7. Rename "Social" → "Compute Donation" in dashboard and daemon
8. Version string auto-detection from source instead of hardcoded

## Design Constraints (never violate)
- All cryptography must become post-quantum resistant (ML-DSA + ML-KEM, NIST FIPS 203/204)
- Node onboarding must be zero-touch — no manual key copy/paste for end users
- No hardcoded IPs in source code — all addresses in config/node.json
- The Commons Review consent gate is the core patented mechanism — AI suggests only, human decides
- Class 1/2 token priority must be preserved in all scheduler changes

## Patent Status
- U.S. Provisional Application No. 64/015,885
- Filed: March 24, 2026
- Non-provisional deadline: March 24, 2027
- Six new patent extension claims drafted (hierarchical DHT, distributed CA, post-quantum identity, zero-touch provisioning, threshold signing, distributed contribution ledger)

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
*Vernex Protocol v0.9.1. Two-node cluster (vernex-node1: 172.17.0.132, vernex-node2: 172.17.0.182). Full security stack: hybrid ed25519 + ML-DSA 44 (CRYSTALS-Dilithium NIST FIPS 204) post-quantum signing, TLS on 7701, rate limiting, trust request approval via dashboard. ML-DSA enforcement is per-peer opt-in (add mldsa_public_key to peer config to activate). Phase 1 (STUN) + Phase 2 (UDP hole punching) + IP watchdog complete. Next: deploy v0.9.1 on Node-2, verify hole punch + watchdog logs. Patent pending US App. 64/015,885, deadline March 24 2027. CONTINUITY.md and CLAUDE.md in repo root have full context.*

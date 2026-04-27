# Vernex Protocol — Session Continuity

## Last Updated
April 26, 2026 (session 4)

## Current Version
v0.8.0

## Node Registry
| Node | ID | IP | Public Key | Status |
|------|----|----|------------|--------|
| vernex-node1 | VRX-54b89a1684e21ae4 | 172.17.0.132 | prAB8hQJaXoWoT+WO7jbCKBT0TAJPMLjiE4QlOr2D0I= | systemd auto-start |
| vernex-node2 | VRX-a5474b585793501c | 172.17.0.182 | /Lcqppk1jkHUVdgNNHaS15FDKurHO3jgPP3+oMfB83Y= | systemd auto-start |

## What Was Just Completed
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
1. Deploy v0.8.0 to Node-2: git pull, go build, restart daemon — Node-2 generates its own ML-DSA keypair
2. Exchange ML-DSA public keys: copy node.mldsa.pub from each node into the other's peer_nodes[].mldsa_public_key in config — activates hybrid enforcement
3. Distributed CA — threshold-signed, no single point of failure (ML-DSA certs in X.509)
5. WireGuard remote node connectivity — OPNsense firewall rules for external nodes
6. Rename "Social" → "Compute Donation" in dashboard and daemon
7. Version string auto-detection from source instead of hardcoded

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
*Vernex Protocol v0.8.0. Two-node cluster (vernex-node1: 172.17.0.132, vernex-node2: 172.17.0.182). Full security stack: hybrid ed25519 + ML-DSA 44 (CRYSTALS-Dilithium NIST FIPS 204) post-quantum signing, TLS on 7701, rate limiting, trust request approval via dashboard. ML-DSA enforcement is per-peer opt-in (add mldsa_public_key to peer config to activate). Next: deploy v0.8.0 on Node-2, exchange ML-DSA public keys, activate hybrid enforcement. Patent pending US App. 64/015,885, deadline March 24 2027. CONTINUITY.md and CLAUDE.md in repo root have full context.*

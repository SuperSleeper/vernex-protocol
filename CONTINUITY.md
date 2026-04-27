# Vernex Protocol — Session Continuity

## Last Updated
April 26, 2026

## Current Version
v0.7.0

## Node Registry
| Node | ID | IP | Public Key | Status |
|------|----|----|------------|--------|
| vernex-node1 | VRX-54b89a1684e21ae4 | 172.17.0.132 | prAB8hQJaXoWoT+WO7jbCKBT0TAJPMLjiE4QlOr2D0I= | systemd auto-start |
| vernex-node2 | VRX-a5474b585793501c | 172.17.0.182 | /Lcqppk1jkHUVdgNNHaS15FDKurHO3jgPP3+oMfB83Y= | systemd auto-start |

## What Was Just Completed
- Trust request system: new nodes auto-submit public key to bootstrap node on install
- Operator approves via dashboard UI yellow banner with APPROVE/DENY buttons
- Setup script bootstraps peer_nodes from BOOTSTRAP_NODES array
- Wipe + reinstall validated end-to-end on Node-2
- Dynamic node discovery via /register + /peers heartbeat
- IP fields computed live in /status — always current after IP change
- Dashboard compact mode toggle, public IP display
- Node setup script (vernex-node-setup.sh) and wipe script (vernex-node-wipe.sh)
- Domains registered: vernex.net, vernex.org

## Security Stack (in place)
- ed25519 cryptographic node identity — VRX- ID derived from public key hash
- TLS on port 7701 — self-signed cert from ed25519 keypair
- Sliding window rate limiter — 60 req/min, per node ID or IP
- Replay protection — 30s timestamp window on inter-node requests
- Trust request approval — operator must approve new node public keys via dashboard
- InsecureSkipVerify TEMPORARY — pending distributed CA + ML-DSA upgrade

## Immediate Next Steps (in priority order)
1. Push latest commit (trust request system) and test on Node-2
2. ML-DSA post-quantum crypto — upgrade ed25519 to CRYSTALS-Dilithium (cloudflare/circl)
3. Brave Search API — live web results replacing DDG Instant Answers
4. Distributed CA — threshold-signed, no single point of failure
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

## Continuity Note for Claude Chat (paste at start of new session)
*Vernex Protocol v0.7.0. Two-node cluster (vernex-node1: 172.17.0.132, vernex-node2: 172.17.0.182) running with full security stack: ed25519 identity, TLS on 7701, rate limiting, trust request approval via dashboard. Dynamic node discovery via heartbeat. Setup/wipe scripts validated. Domains: vernex.net, vernex.org. Patent pending US App. 64/015,885, deadline March 24 2027. Next: push trust request commit, test on Node-2, then ML-DSA post-quantum upgrade. CONTINUITY.md and CLAUDE.md in repo root have full context.*

# Vernex Protocol — Architecture Specification
> Version 1.1 — Updated May 2026
>
> **CONFIDENTIAL — Patent Pending — U.S. Provisional Application No. 64/015,885**
> Non-provisional filing deadline: March 24, 2027
> Inventor: Eric John Geer

---

## 1. Executive Summary

The Vernex Protocol is a distributed home node compute network enabling individuals to contribute idle GPU compute capacity to a global AI inference cluster. Unlike centralized cloud compute providers, Vernex operates as a fully decentralized, peer-to-peer network where each node is a self-contained compute unit capable of independent operation and collaborative inference.

Key architectural innovations:

- Latency-aware hierarchical DHT for peer discovery with automatic regional failover
- Threshold-signed distributed Certificate Authority with air-gapped root key — no single point of failure
- Post-quantum cryptographic node identity using NIST-standardized ML-DSA and ML-KEM algorithms
- Zero-touch node provisioning via one-time enrollment tokens
- Class 1/2 token priority scheduler with Commons Review consent gate — the core patented mechanism
- Distributed contribution ledger stored in DHT with cryptographic job receipts
- Automatic disaster recovery routing with sub-90-second regional failover

---

## 2. Current Implementation — v0.12.39

### 2.1 Hardware Configuration

| Node | Hardware | Node ID | Role |
|------|----------|---------|------|
| vernex-node1 | RTX 3070 8GB / 64GB RAM / Pop!_OS 24.04 | VRX-54b89a1684e21ae4 | Bootstrap — scheduler, dashboard, auth relay, nginx |
| vernex-node2 | HP Victus / RTX 4070 Max-Q / 60GB RAM / Pop!_OS 24.04 COSMIC | VRX-a5474b585793501c | Compute node |

### 2.2 Daemon Capabilities (Go — v0.12.18)

- P2P TCP/UDP listener on port 7700
- HTTPS API on port 7701 — /status, /health, /submit, /consent, /queue, /peers, /register, /deregister, /stun, /install
- Class 1/2 token priority queue — heap-based, Class 1 before Class 2, FIFO within class
- Commons Review consent gate — AI suggests upgrade, human must explicitly consent via /consent
- Multi-node Ollama routing — /api/ps load balancing, automatic fallback on node failure
- STUN hole-punching — external nodes connect P2P without firewall changes
- Push-based relay — NAT nodes push status in heartbeat; dashboard shows ONLINE+RELAY badge
- Graceful SIGTERM — sends /deregister to peers, dashboard updates immediately
- Conversation context via sliding 10-turn window with Mistral [INST] prompt format
- Brave Search API on both nodes — live web results injected into LLM context
- Node ID persisted to config/node.json on first boot
- systemd-logind sleep inhibitor via D-Bus — nodes stay awake during inference
- Zero-touch enrollment via one-time token (--token flag or /install LAN endpoint)
- Google OAuth via central relay at https://vernex.net:5443 — RS256 JWT, Let's Encrypt cert

### 2.3 LLM Stack (node1)

- Inference engine: Ollama — model-agnostic, GPU-native, REST API
- Models available: gemma4:e2b, gemma4:e4b, gemma4:31b, gemma4:26b, mistral, llama3.1:8b, qwen2.5-coder:7b, phi3:mini, llama3.2:3b
- RTX 3070 — GPU inference, 1–3s response time

### 2.4 Network Endpoints

| Endpoint | Method | Description |
|----------|--------|-------------|
| /submit | POST | Submit LLM inference request with class, prompt, model, context |
| /consent | POST | Respond to Commons Review upgrade suggestion |
| /status | GET | Node health, uptime, contribution score, queue depth |
| /health | GET | Simple liveness check |
| /queue | GET | Current priority queue depth by class |
| /peers | GET | Live peer list with connection type and last_seen |
| /register | POST | Node heartbeat registration |
| /deregister | POST | Graceful peer removal on shutdown |
| /stun | GET | Returns caller external IP:port for NAT discovery |
| /install | GET | LAN zero-touch enrollment endpoint (token required) |

### 2.5 Infrastructure

| Component | Detail |
|-----------|--------|
| DNS | _vernex._tcp.vernex.net TXT → bootstrap=76.244.40.49:7701 |
| LAN DNS | vernex.net → 172.17.0.132 (OPNsense Unbound override) |
| Public IP | 76.244.40.49 (both nodes share via OPNsense NAT) |
| Auth relay | https://vernex.net:5443 (nginx TLS, Let's Encrypt, expires 2026-08-02) |
| Dashboard | http://172.17.0.132:5080 (nginx proxy, Google auth required) |
| License | BSL 1.1 with Guardian Clause → Apache 2.0 after 4 years per release |

---

## 3. Core Patented Mechanisms

> U.S. Provisional Application No. 64/015,885. Preserve in all implementations. Do not simplify or remove.

### 3.1 Two-Class Token Priority System

All compute requests are classified as either Class 1 (Community) or Class 2 (Personal). The priority scheduler ensures Class 1 requests execute before Class 2 requests, with FIFO ordering within each class.

- Class 1 — Community benefit: higher priority, contribution delta +2.0, optimistic execution
- Class 2 — Personal use: standard priority, contribution delta +1.0, subject to Commons Review
- Priority queue implemented as a heap — O(log n) insertion and extraction

### 3.2 Commons Review Consent Gate

When a Class 2 request is assessed by the local LLM as having community benefit, the system presents an upgrade suggestion. The AI may only suggest — it cannot reclassify without explicit human consent.

- Class 2 submitted → Mistral assesses community benefit via structured JSON prompt
- If benefit detected → return `status: commons_review` with review_id and 60-second TTL
- User POSTs to /consent with `upgrade: true` or `upgrade: false`
- On consent → request enqueued at final class determined solely by user
- On timeout → auto-execute as Class 2 — user never loses their request
- **CRITICAL:** The class written to TokenRequest is determined solely by the consent response, never by the AI
- This consent requirement is enforced structurally in code — not advisory

### 3.3 Compute Partition

Each node enforces a configurable partition between personal compute and social (network contribution) compute. Default: 70% personal, 30% social. Configurable per-node via config/node.json.

### 3.4 Graceful Degradation [PLANNED — Phase 4b]

When a job exceeds its runtime ceiling: checkpoint current state, attempt local execution, re-queue for network execution. Never terminates work without attempting local fallback first.

---

## 4. Target Architecture — Production Design

### 4.1 Hierarchical Network Topology

| Tier | Latency | Scope | Function |
|------|---------|-------|----------|
| Local | < 5ms | Neighborhood / ISP area | Primary routing target |
| Regional | 5–20ms | Metro area | Overflow and failover |
| National | 20–80ms | Country | Cross-region failover |
| International | 80–200ms | Global | Disaster recovery |

Routing algorithm: `latency_to_requestor ASC → load ASC → class priority`

### 4.2 Automatic Disaster Recovery

Regional health monitor detects mass node dropout (>50% unreachable within 90s) → broadcasts degraded status to national tier → national tier redistributes tokens to next-lowest-latency region → no service interruption to users.

### 4.3 Node Hardware Standard

| Class | Hardware | Approx. Cost | Response Time | Max Model |
|-------|----------|-------------|---------------|-----------|
| Minimal | Mini PC + iGPU, 16GB RAM | $250 | 8–15s | 7B Q4 |
| Standard | SFF PC + RTX 4060 8GB, 32GB RAM | $600–800 | 1–3s | 7B Q4 |
| Enhanced | SFF PC + RTX 4070 12GB, 32GB RAM | $900–1200 | < 1s | 13B Q4 |
| Developer | Workstation + RTX 3070+ 8GB, 64GB RAM | $800–1000 | 1–3s | 7B Q4 |

---

## 5. Distributed Peer Discovery — Latency-Aware DHT [PLANNED]

### 5.1 Overview

Kademlia-based DHT modified to store latency metadata alongside peer identity. Indexes peers by measured network latency — enables scheduler to select lowest-latency available node per request. Self-healing — nodes join and leave dynamically with no manual configuration.

### 5.2 DHT Node Entry Schema

- vrx_id — cryptographic node identity (public key hash)
- ip — current routable IP address
- port — daemon P2P port (default 7700)
- region — ISO 3166-1 country + subdivision code (e.g., US-CA)
- avg_latency_ms — rolling average RTT to 5 nearest peers
- gpu_tier — hardware capability class
- active_jobs — current in-flight inference requests
- models — list of available Ollama model IDs
- last_seen — Unix timestamp of last heartbeat
- cert_fingerprint — SHA-3 hash of node TLS certificate

### 5.3 Node Lifecycle

On boot:
1. Generate or load ed25519 + ML-DSA keypair from persistent storage
2. Contact bootstrap node to obtain initial peer list
3. Enroll with Vernex CA if no valid certificate exists
4. Announce presence to DHT
5. Begin heartbeat loop — update DHT every 30s
6. Begin latency measurement loop — ping 5 random peers every 60s

On shutdown (SIGTERM):
1. Send /deregister to all peers — immediate removal from dashboards
2. Release sleep inhibitor lock
3. Drain in-flight requests (up to 30s graceful window)

### 5.4 Sybil and Eclipse Attack Resistance

- Sybil: One-time enrollment tokens control node registration — attacker cannot register unlimited fake nodes
- Eclipse: Max 30% of peer table from same /24 subnet; periodic probing for diverse peers

---

## 6. Distributed Certificate Authority [PLANNED]

### 6.1 Architecture

Three-tier hierarchy with threshold signing. No single CA server can issue or revoke certificates independently.

- Root CA — air-gapped, offline. Signs intermediate CAs only. Never network-connected after initial ceremony
- Intermediate CAs — three regional servers (Americas, Europe, Asia-Pacific). 2-of-3 threshold to issue or revoke
- Node certificates — valid 365 days, auto-renewed. Contain public key, VRX- ID, capability class, timestamps

### 6.2 Threshold Signing

Intermediate CA signing key split into 3 Shamir shares. Signing requires reconstruction from any 2 shares. A compromised or offline regional CA cannot act alone.

### 6.3 Zero-Touch Node Provisioning

1. Vernex generates batch of one-time 256-bit enrollment tokens
2. Daemon generates ed25519 + ML-DSA keypair on first boot
3. Daemon sends CSR + enrollment token to nearest intermediate CA
4. CA validates token (single-use, burned immediately)
5. 2-of-3 regional CAs co-sign node certificate
6. Daemon receives certificate, derives VRX- node ID
7. Node joins DHT as trusted peer — total provisioning < 60s
8. Enrollment token cryptographically burned

---

## 7. Post-Quantum Cryptographic Architecture

### 7.1 Threat Model

Harvest-now-decrypt-later attacks are occurring today. Vernex is designed quantum-resistant from inception using NIST-standardized PQC algorithms finalized in 2024. Hybrid approach: both classical and post-quantum run simultaneously — attacker must break both.

### 7.2 Algorithm Selection

| Purpose | Classical | Post-Quantum | Standard |
|---------|-----------|-------------|----------|
| Node identity signing | ed25519 | ML-DSA (CRYSTALS-Dilithium) | NIST FIPS 204 |
| Key encapsulation | X25519 (ECDH) | ML-KEM (CRYSTALS-Kyber) | NIST FIPS 203 |
| P2P TLS session | TLS 1.3 | TLS 1.3 + X25519MLKEM768 hybrid | RFC 9420 / Go 1.23+ |
| CA cert signing | ECDSA P-256 | ML-DSA + SLH-DSA dual signature | NIST FIPS 204/205 |
| Contribution ledger hashing | SHA-256 | SHA-3 / SHAKE-256 | NIST FIPS 202 |
| Job receipt signing | ed25519 | ML-DSA | NIST FIPS 204 |

**Current status:** ed25519/X25519 in production. ML-DSA + ML-KEM upgrade is next major milestone.

### 7.3 Go Implementation

- `golang.org/x/crypto` — ML-KEM (Kyber) natively in Go 1.23+
- `cloudflare/circl` — full NIST PQC suite including ML-DSA, SLH-DSA, hybrid constructions
- `crypto/tls` (Go 1.23+) — X25519MLKEM768 hybrid key exchange natively
- `liboqs-go` — Open Quantum Safe project, comprehensive PQC suite

### 7.4 VRX- Node Identity

- Node generates ed25519 + ML-DSA keypair on first boot
- `VRX-ID = 'VRX-' + hex(SHAKE-256(ML-DSA_public_key)[:8])`
- Stable — same keypair always produces same VRX- ID
- Verifiable — any peer can verify a signature against the claimed VRX- ID
- Unforgeable — cannot be claimed without the corresponding private key

---

## 8. Security Model

### 8.1 Attack Surface and Mitigations

| Attack Vector | Risk | Mitigation |
|--------------|------|-----------|
| Sybil attack | Fake nodes control routing | One-time enrollment tokens — controlled supply |
| Eclipse attack | Malicious peers isolate DHT view | Max 30% peer table from same /24 subnet |
| Prompt injection | Crafted prompts expose config | Input sanitization, token length limits |
| Contribution fraud | Faked job completions | Cryptographic job receipts — requesting node verifies |
| API abuse | /submit flooded | Node-to-node auth tokens, rate limiting per peer |
| MITM / eavesdropping | Prompts intercepted | Mutual TLS on all P2P — post-quantum hybrid |
| Resource exhaustion | GPU saturated | Token limits, runtime ceiling, graceful degradation |
| CA compromise | Fraudulent certs issued | 2-of-3 threshold — one CA compromise cannot act alone |
| Quantum decryption | Classical crypto broken | Hybrid ML-DSA/ML-KEM — both must be broken |

### 8.2 Security Build Order

1. Node auth tokens on /submit — prevents API abuse ✅ (done)
2. TLS on P2P connections — encrypts all node traffic ✅ (done)
3. Signed node identity — cryptographic VRX- ID ✅ (done — ed25519)
4. Cryptographic job receipts — tamper-proof contribution scoring [PLANNED]
5. Distributed CA with enrollment tokens — before open source release [PLANNED]
6. DHT implementation with Sybil/Eclipse resistance [PLANNED]
7. Post-quantum ML-DSA + ML-KEM integration [NEXT MAJOR MILESTONE]

---

## 9. USB Node Image — Zero-Touch Deployment [PLANNED]

### 9.1 Design Goals

- Plug in USB, boot, done — no technical knowledge required
- Identical environment on every node — reproducible, auditable
- No risk to host machine — existing OS untouched
- Persistent — node ID, config, models survive reboots

### 9.2 Image Contents

- Base OS: Pop!_OS (Ubuntu-based, NVIDIA GPU support out-of-box)
- Vernex daemon — auto-starts via systemd on boot
- Ollama — pre-installed, auto-detects GPU or falls back to CPU
- Pre-downloaded models: mistral:7b-instruct-q4_K_M (baseline)
- One-time enrollment token — unique per image
- Bootstrap peer list — initial DHT entry points in node.json
- Persistent overlay filesystem — writes survive reboots

### 9.3 GPU Support Matrix

| GPU Type | Backend | Performance | Notes |
|----------|---------|------------|-------|
| NVIDIA RTX/GTX | CUDA via Ollama | 1–3s (7B) | Auto-detected, drivers pre-installed |
| AMD RX 6000+ | ROCm via Ollama | 2–5s (7B) | ROCm pre-installed |
| Intel Arc | SYCL via Ollama | 3–8s (7B) | Experimental |
| No GPU / iGPU | CPU via Ollama | 15–60s (7B) | Functional — reported in DHT capability tier |

---

## 10. Patent Extension Claims — Attorney Review Required

> Evaluate for inclusion in non-provisional filing before March 24, 2027.

### 10.1 Latency-Aware Hierarchical DHT
Distributed peer discovery organized into hierarchical tiers based on measured round-trip latency. DHT stores latency metadata alongside peer identity. Routing algorithm selects compute peers by latency-to-requestor as primary key.

### 10.2 Automatic Regional Failover
Method for automatic rerouting of compute tokens on regional cluster degradation. Regional health monitor detects mass dropout via DHT heartbeat analysis. Threshold-based trigger. Automatic redistribution to next-lowest-latency region. No human intervention or centralized coordination.

### 10.3 Threshold-Signed Distributed Certificate Authority
CA architecture requiring threshold consensus among geographically distributed intermediate CA servers for certificate issuance and revocation. Revocation records distributed via DHT — no central CRL server required.

### 10.4 Zero-Touch Node Provisioning with One-Time Enrollment Tokens
Bootable storage medium containing unique one-time enrollment token. Automated first-boot sequence generates keypair, submits CSR, receives signed certificate, joins peer network — no manual configuration required. Token cryptographically burned on first use.

### 10.5 Post-Quantum Cryptographic Node Identity
Hybrid classical + post-quantum node identity system using ML-DSA for signing and ML-KEM for key encapsulation. VRX- node identifier derived as hash of post-quantum public key. Requires adversary to break both classical and post-quantum algorithms to compromise identity.

### 10.6 Distributed Contribution Ledger with Cryptographic Job Receipts
Contribution tracking system where each completed inference job generates a cryptographically signed receipt signed by both requesting and executing nodes. Score increments applied only on dual-signature verification. Ledger stored in DHT — tamper-evident without blockchain or central server.

---

## 11. Development Roadmap

| Phase | Status | Deliverables |
|-------|--------|-------------|
| Phase 1 | DONE ✓ | Single node daemon, P2P TCP, HTTP API, contribution tracking |
| Phase 2 | DONE ✓ | Two-node P2P, live dashboard, stable node IDs, systemd auto-start |
| Phase 3 | DONE ✓ | Contribution tracking, dashboard UI, 70/30 compute partition display |
| Phase 4 | DONE ✓ | Class 1/2 priority queue, Commons Review consent gate, Ollama LLM, multi-node routing, chat UI |
| Phase 5 | DONE ✓ | Node setup scripts, TLS, STUN hole-punching, external connectivity, RELAYED connection type |
| Phase 6 | DONE ✓ | Google OAuth, central auth relay, nginx proxy, zero-touch enrollment, LAN install server |
| Phase 7 | IN PROGRESS | Cryptographic job receipts, distributed CA PoC, ML-DSA + ML-KEM upgrade |
| Phase 8 | PLANNED | DHT implementation, latency-aware routing, regional health monitoring |
| Phase 9 | PLANNED | Threshold CA, production USB image, open source contributor network |

---

## 12. Glossary

| Term | Definition |
|------|-----------|
| Class 1 (Community) | Token class for requests that benefit the broader network. Higher priority, contribution delta +2.0 |
| Class 2 (Personal) | Token class for requests that benefit only the requester. Subject to Commons Review |
| Commons Review | Patented consent gate: AI suggests Class 1 upgrade for Class 2 requests with community benefit. Human must explicitly accept. AI cannot reclassify autonomously |
| Compute Donation | Portion of node compute allocated to social/network partition (default 30%) |
| DHT | Distributed Hash Table — decentralized peer-to-peer data structure for peer discovery |
| ML-DSA | Module-Lattice Digital Signature Algorithm (CRYSTALS-Dilithium) — NIST FIPS 204 |
| ML-KEM | Module-Lattice Key Encapsulation Mechanism (CRYSTALS-Kyber) — NIST FIPS 203 |
| Ollama | Local LLM inference engine. Model-agnostic, GPU-native, REST API |
| Sybil attack | Adversary creates many fake identities to control peer network routing |
| Threshold signing | Cryptographic scheme requiring M-of-N parties to agree before an operation can proceed |
| VRX- ID | Vernex node identity — unique identifier derived from post-quantum public key hash. Stable, verifiable, unforgeable |

---

*END OF DOCUMENT — CONFIDENTIAL — Patent Pending U.S. App. 64/015,885*

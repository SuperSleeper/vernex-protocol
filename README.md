# Vernex Protocol

Distributed home-node compute network with value-aware token priority scheduling, social compute partitioning, and intelligent request classification.

> Patent Pending — USPTO Provisional No. 64/015,885 (filed March 24, 2026)

## What Is Vernex?

Vernex is a peer-to-peer compute protocol that lets ordinary computers share AI inference workloads — without cloud infrastructure, without central servers, and without trusting any single operator.

Each node runs a Go daemon that handles:
- Routing AI requests across enrolled peers using a priority queue.
- Enforcing compute fairness via a patented Class 1/2 token scheduler with Commons Review consent gate.
- Securing all traffic with hybrid post-quantum cryptography (ed25519 + ML-DSA 44).
- Auto-discovering and trusting peers on the LAN via mDNS + a distributed CA chain.

The goal: democratize inference compute the same way BitTorrent democratized file distribution.

## Who Is Vernex For?

Vernex is designed for those seeking compute sovereignty — running AI workloads on hardware they own, without paying cloud bills or surrendering data to third parties.

- **Home Lab Enthusiasts:** Pool idle compute across your own machines.
- **Privacy-Focused Developers:** AI inference without cloud data exposure.
- **Startups:** Avoid high AWS/GCP inference costs at small scales.
- **Security Researchers:** Interested in post-quantum mesh protocols.

## Architecture

```
        [ PUBLIC INTERNET ]                   [ HOME NETWORK / NAT ]
+---------------------+               +-----------------------+
|   Bootstrap Node    |               |     Compute Node      |
|  (Port 7701/7700)   |               |   (Behind Firewall)   |
+----------+----------+               +-----------+-----------+
           |                                     |
           | <--- (1) Discovery/Enrollment ----  |
           |      (Signed Single-use Token)      |
           |                                     |
           | <--- (2) UDP Hole Punching -------  |
           |      (STUN-style Handshake)         |
           |                                     |
+----------v----------+               +-----------v-----------+
|  Distributed Root   |               |  Token Scheduler      |
|  CA (Shamir K-of-N) |               |  (Class 1 vs Class 2) |
+---------------------+               +-----------+-----------+
                                                  |
                                      +-----------v-----------+
                                      |  Local AI Inference   |
                                      |  (Ollama / Port 11434)|
                                      +-----------------------+
```

## Token Priority Scheduler (Patented)

Requests are classified into two classes:

| Class | Description | Examples |
|---|---|---|
| Class 1 | Personal compute — operator's own workloads | Local AI assistant, private queries |
| Class 2 | Social compute — shared network workloads | Peer-routed requests, Commons pool |

A Commons Review consent gate governs the Class 1/2 boundary. Operators set their own partition ratio (default 70/30). This scheduling mechanism ensures you always get priority on your own hardware, while social compute only runs in the capacity you explicitly allow.

## Security Stack

Vernex assumes no peer is trustworthy until the CA chain says otherwise. The stack is designed to remain secure even against future quantum adversaries.

| Layer | Mechanism |
|---|---|
| Transport | TLS 1.2+ on all inter-node HTTPS (7701); raw TCP (7700) protected by app-layer signatures |
| Identity | ed25519 node keypairs (deterministic node ID) |
| Post-quantum | ML-DSA 44 (NIST FIPS 204) on all request signatures |
| Hybrid Signing | Every request carries both ed25519 + ML-DSA 44 headers |
| CA Chain | Root → Intermediate → Compute Node (distributed CA) |
| Enrollment | Zero-touch via signed single-use tokens |
| LAN Trust | mDNS auto-trust — CA-enrolled peers join automatically |
| Clock | NTP + bootstrap /time consensus — CA ops gated on drift < 5 min |
| Replay Protection | 30-second timestamp window on all signed requests |
| Trust Persistence | Verified peer CNs persisted across restarts |

## Quick Start

### 1. Bootstrap Node (Public IP required)

```bash
curl -fsSL https://raw.githubusercontent.com/SuperSleeper/vernex-protocol/main/scripts/vernex-bootstrap-setup.sh | bash
```

### 2. Compute Node

Requirement: Ollama must be installed and running.

```bash
curl -fsSL https://raw.githubusercontent.com/SuperSleeper/vernex-protocol/main/scripts/vernex-node-setup.sh | bash
# Enter bootstrap address and enrollment token when prompted
```

### 3. Build from Source

```bash
git clone https://github.com/SuperSleeper/vernex-protocol
cd vernex-protocol/daemon
go build -ldflags "-X vernex/daemon/ca.BuildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)" -o vernex-node .
./vernex-node
```

**Requirements:** Go 1.21+, avahi-daemon (mDNS), Ollama (inference)

## Project Structure

```
daemon/
  main.go        — Entry point & signal handling (start here for bug triage)
  scheduler.go   — Token scheduler & Commons Review (core patented logic)
  ca/            — Root, Intermediate, and Sync logic for the distributed CA
scripts/         — Provisioning and node-wipe utilities
```

## Roadmap

- [ ] ML-KEM hybrid key exchange (NIST FIPS 203) — full post-quantum transport
- [ ] Multi-operator root CA with Shamir threshold signing
- [ ] Zero-touch USB provisioning image
- [ ] Contribution scoring + node incentive model
- [ ] Web dashboard (replace Flask)
- [ ] Public bootstrap network

## DNS Discovery

Bootstrap nodes publish a DNS TXT record:

```
_vernex._tcp.vernex.net  →  bootstrap=76.244.40.49:7701
```

Compute nodes resolve this automatically during enrollment.

## Contributing & Patent Note

Contributions are welcome, particularly in Go backend logic and security auditing.

**Regarding the Patent:** The core scheduling and consent-gate mechanism is patent pending (USPTO 64/015,885). We intend to provide a permissive patent grant for non-commercial and open-source use — details to be formalized before v1.0.

## Author

Eric Geer — Senior Network Engineer  
vernex.net · vernex.org · GitHub: @SuperSleeper

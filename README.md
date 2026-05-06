# Vernex Protocol

Distributed home-node compute network with value-aware token
priority scheduling, social compute partitioning, and intelligent
request classification.

**Patent Pending — USPTO Provisional No. 64/015,885**
*(Filed March 24, 2026)*

---

## Philosophy: AI for the People

Vernex is built to provide true anonymity and compute sovereignty.
We believe powerful AI tools should belong to individuals, not
centralized entities. Our architecture is designed to resist
surveillance, data harvesting, and centralized control.
We are building a Guardian protocol for private intelligence.

---

## Architecture

```
        [ PUBLIC INTERNET ]                   [ HOME NETWORK / NAT ]
+---------------------+               +-----------------------+
|   Bootstrap Node    |               |     Compute Node      |
|  (Port 7701/7700)   |               |   (Behind Firewall)   |
+----------+----------+               +-----------+-----------+
           |                                      |
           | <-- (1) Discovery / Enrollment ----  |
           |       Signed single-use token        |
           |                                      |
           | <-- (2) UDP Hole Punching ---------- |
           |       STUN-style NAT traversal       |
           |                                      |
+----------v----------+               +-----------v-----------+
|  Distributed Root   |               |    Token Scheduler    |
|  CA  (Shamir K-of-N)|               |  (Class 1 / Class 2)  |
+---------------------+               +-----------+-----------+
                                                  |
                                      +-----------v-----------+
                                      |   Local AI Inference  |
                                      |  Ollama  port 11434   |
                                      +-----------------------+
```

---

## Token Priority Scheduler (Patent Pending)

Per USPTO No. 64/015,885, requests are classified to ensure
network health and operator sovereignty:

| Class | Description | Priority | Examples |
|-------|-------------|----------|---------|
| Class 1 | Community Compute — benefits the network | Higher | Peer-routed requests, Commons tasks |
| Class 2 | Personal Compute — benefits only the requester | Lower | Private queries, local AI assistant |

The **Commons Review consent gate** ensures the AI may *suggest*
upgrading a Class 2 request to Class 1 but cannot reclassify
without explicit user consent. This mechanism is the core
patented invention.

---

## Security Stack

| Layer | Technology |
|-------|-----------|
| Post-quantum signatures | ML-DSA 44 (NIST FIPS 204 / CRYSTALS-Dilithium) |
| Node identity | Hybrid ed25519 + ML-DSA 44 keypair |
| Enrollment | Distributed CA — Root → Intermediate → Node cert chain |
| NAT traversal | UDP hole punching (STUN-style, no port forwarding needed) |
| Replay protection | 30-second timestamp window on all signed requests |
| Clock integrity | NTP consensus + ML-DSA signed bootstrap /time endpoint |
| Token burn | Single-use enrollment tokens, burned on first use |

---

## Quick Start

### Bootstrap Node (public IP required)

```bash
curl -fsSL https://raw.githubusercontent.com/SuperSleeper/vernex-protocol/main/scripts/vernex-bootstrap-setup.sh | bash
```

### Compute Node (home network, behind NAT)

```bash
curl -fsSL https://raw.githubusercontent.com/SuperSleeper/vernex-protocol/main/scripts/vernex-node-setup.sh | bash
```

### Build from Source

```bash
git clone https://github.com/SuperSleeper/vernex-protocol
cd vernex-protocol/daemon
go build -ldflags \
  "-X vernex/daemon/ca.BuildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  -o vernex-node .
```

---

## Requirements

- Go 1.21+
- Linux (Pop!_OS or Ubuntu 22.04/24.04 recommended)
- avahi-daemon (mDNS peer discovery)
- Ollama (local AI inference)

---

## Project Structure

```
daemon/
  main.go        — Startup and orchestration
  config.go      — NodeConfig, loadConfig
  node.go        — Node struct, stats, banner
  peer.go        — PeerRegistry, heartbeat
  mdns.go        — mDNS registration and auto-trust discovery
  punch.go       — UDP hole punching, IP watchdog
  scheduler.go   — Token scheduler, Commons Review (patented)
  inference.go   — Ollama routing, Brave Search
  tls.go         — TLS config, keypair management
  handlers.go    — HTTP API endpoints
  ca/
    root.go           — Root CA, Shamir K-of-N split
    intermediate.go   — Intermediate CA, CSR signing
    enrollment.go     — Token lifecycle, zero-touch enroll
    sync.go           — /ca-sync gossip
    verify.go         — TrustStore chain validation
    clockcheck.go     — NTP + bootstrap time consensus
scripts/
  vernex-bootstrap-setup.sh  — Bootstrap node provisioning
  vernex-node-setup.sh       — Compute node setup + enrollment
```

---

## Contributing

Contributions that advance privacy, decentralization, and
compute sovereignty are welcome.

**Patent Note:** The core scheduling and Commons Review consent
gate mechanism is protected under USPTO Provisional Application
No. 64/015,885. The patent serves as a legal shield to ensure
the protocol is not misappropriated by actors who do not respect
user privacy.

---

## License

Vernex Protocol is released under the
**Business Source License 1.1**.

- **Individuals and non-commercial use:** Free to use, modify,
  and distribute for personal or research purposes.
- **Prohibited uses:** See LICENSE file and The Guardian Clause.
- **Commercial use:** Entities using Vernex for direct profit
  or as a managed service must obtain a commercial license.
- **The Commons Promise:** Every release of Vernex automatically
  converts to the Apache 2.0 license four (4) years after its
  initial publication date.

---

## Google OAuth Setup (Dashboard Login)

The dashboard uses Google OAuth 2.0 for authentication. To enable it:

### 1. Create a Google OAuth App

1. Go to [console.cloud.google.com](https://console.cloud.google.com)
2. Create a new project (or select an existing one)
3. Navigate to **APIs & Services → Credentials**
4. Click **Create Credentials → OAuth client ID**
5. Application type: **Web application**
6. Name: `Vernex Dashboard` (or anything)
7. Under **Authorized redirect URIs**, add:
   ```
   http://172.17.0.132:5080/auth/callback
   ```
   Replace `172.17.0.132` with your node1's LAN IP if different.
8. Click **Create** — copy the **Client ID** and **Client Secret**

### 2. Configure the Node

Edit `~/vernex/config/oauth.json` on node1 (created automatically on first dashboard start):

```json
{
  "google_client_id": "YOUR_CLIENT_ID.apps.googleusercontent.com",
  "google_client_secret": "YOUR_CLIENT_SECRET",
  "facebook_client_id": "",
  "facebook_client_secret": "",
  "session_secret": "<leave as-is — auto-generated>",
  "redirect_base": "http://127.0.0.1:5080"
}
```

### 3. Restart the Dashboard

```bash
sudo systemctl restart vernex-dashboard
```

### 4. First Login = Admin

The first Google account to log in is automatically assigned the `admin` role and can access the full dashboard. Subsequent accounts get `user` role (chat UI only). To promote a user to admin, edit `~/vernex/config/users.json`:

```json
{
  "user@example.com": {"role": "admin", "enabled": true}
}
```

### Roles

| Role | Access |
|------|--------|
| `admin` | Full dashboard (`/`) + chat UI (`/ui`) |
| `user` | Chat UI only (`/ui`) |

### Install Script (No Auth)

The install script endpoint (`/install`) is always public — no login required. Workers fetch it to join the network:

```bash
curl -fsSL http://172.17.0.132:5080/install | bash
# or with enrollment token:
curl -fsSL "http://172.17.0.132:5080/install?token=<token-id>"
```

---

## Author

**Eric Geer** — Senior Network Engineer
[vernex.net](https://vernex.net) · [vernex.org](https://vernex.org) · GitHub: [@SuperSleeper](https://github.com/SuperSleeper)

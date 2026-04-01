# Vernex Protocol — Project Context for Claude Code

## What This Project Is

Vernex Protocol is a distributed home node compute network with a two-class token priority
system and a social compute layer. Each node runs a Go daemon that advertises itself on the
network, tracks contribution, and manages the partition between personal and social compute.
A Python dashboard provides a real-time view of node status and token activity.

**Patent Pending — U.S. Application No. 64/015,885**
**Inventor: Eric John Geer**
**GitHub: github.com/SuperSleeper/vernex-protocol (private)**

---

## Current State (as of March 31, 2026)

### Working
- Go daemon v0.4.0 running on Node-1 (Node-2 pending pull/build)
- P2P TCP listener on port 7700
- HTTP API on port 7701: `/status`, `/health`, `/submit`, `/consent`, `/queue`
- Contribution score tracking (increments over time, per connection, and per LLM job)
- Python Flask dashboard at localhost:5000 polling both nodes every 5 seconds
- Both nodes ONLINE and visible in dashboard
- Private GitHub repo, SSH key auth on both nodes
- Sleep/idle inhibitor via systemd-logind D-Bus (Node-1 ✓, Node-2 deferred)
- Node config loaded from `~/vernex/config/node.json` at startup
- Node ID persisted across restarts (written back to config on first run)
- Ports, partition percentages configurable per-node via config file
- SIGINT/SIGTERM handled cleanly — inhibitor released on shutdown
- Ollama/Mistral LLM running locally on Node-1 (RTX 3070, 4978MiB VRAM)
- Class 1/2 token priority queue — Class 1 runs before Class 2, FIFO within class
- Priority ordering validated under concurrent load
- Commons Review — Class 2 requests assessed by Mistral for community benefit;
  upgrade to Class 1 suggested but requires explicit `/consent` call; 60s TTL expiry

### Node Registry
- **vernex-node1**: 172.17.0.132 — RTX 3070 / 64GB RAM — primary dev machine (Pop!_OS)
- **vernex-node2**: 172.17.0.198 — HP Victus — second node (Pop!_OS)

---

## Project Structure

```
~/vernex/
  daemon/
    main.go          ← Go daemon source (v0.4.0)
    go.mod           ← Go module file
    go.sum           ← dependency checksums
    vernex-node      ← compiled binary (gitignored)
  dashboard/
    app.py           ← Python Flask dashboard
    requirements.txt ← pip dependencies (flask, requests)
    venv/            ← Python virtualenv (gitignored)
  config/
    node.json        ← per-node config: ports, partition, node_id (gitignored)
  logs/              ← runtime logs (empty for now)
  scripts/           ← helper scripts (empty for now)
  .gitignore
  CLAUDE.md          ← this file
```

---

## Architecture

### Daemon (Go)
- Loads config from `~/vernex/config/node.json` on startup; falls back to defaults if missing
- Generates a unique `VRX-{hex}` Node ID on first run; persists it to config for stable identity
- Takes systemd-logind sleep/idle inhibitor lock via D-Bus on startup (gracefully skipped if unavailable)
- Handles SIGINT/SIGTERM cleanly — releases inhibitor and exits
- Listens on configurable P2P port (default 7700) for TCP connections
- Serves HTTP on configurable API port (default 7701) for dashboard polling
- Tracks: uptime, total connections, contribution score, queue depth
- Partition: configurable per-node (default 70% personal / 30% social)
- Token priority queue: Class 1 before Class 2, FIFO within class (heap-based)
- LLM backend: Ollama at localhost:11434, model: mistral
- Commons Review: Class 2 requests assessed for community benefit via Mistral;
  upgrade suggested via `status: commons_review` response; explicit `/consent` required;
  pending reviews expire after 60s and auto-execute as Class 2

### HTTP API Endpoints
| Endpoint | Method | Description |
|----------|--------|-------------|
| `/status` | GET | Node stats JSON |
| `/health` | GET | Liveness check |
| `/submit` | POST | Submit LLM job (class 1 or 2) |
| `/consent` | POST | Respond to Commons Review upgrade suggestion |
| `/queue` | GET | Live queue depth and class counts |

### Dashboard (Python Flask)
- Polls `http://{node_ip}:7701/status` every 5 seconds (via meta refresh)
- Nodes defined in `NODES` dict at top of app.py
- Shows: node ID, online/offline status, uptime, connections, contribution score, partition bar

---

## Patented Architecture (do not remove or simplify)

The following are the core patented mechanisms. Preserve them in all implementations:

### Two-Class Token Priority System
- **Class 1 (Community)**: Requests that benefit the broader network. Optimistic execution
  with async reclassification. Lower cost, higher priority.
- **Class 2 (Personal)**: Requests that benefit only the requester. Requires justification,
  dynamic cost estimate, and local insufficiency proof.

### Commons Review
- When a Class 2 request could benefit the community, the system presents an upgrade prompt
- The AI may SUGGEST upgrading to Class 1 but CANNOT reclassify without explicit user consent
- This is a core patented mechanism — the consent requirement is legally significant

### Compute Partition
- Each node has a firmware-enforced partition: personal vs social compute
- Social partition contributes to the network; personal partition is private
- Current split: 70% personal / 30% social

### Graceful Degradation
- When a job hits its runtime ceiling, it migrates to local execution then re-queues
- Never terminates work without attempting local fallback first

---

## Completed

### ✓ Sleep inhibitor in Go daemon
- Uses `github.com/godbus/dbus/v5` to call `org.freedesktop.login1.Manager.Inhibit`
- Inhibits `sleep:idle` in `block` mode; releases on daemon exit
- Gracefully skips if D-Bus unavailable (SSH sessions without active logind session)
- Node-1: working. Node-2: skipped until systemd service is set up (item 5)

### ✓ requirements.txt for dashboard
`~/vernex/dashboard/requirements.txt` — install with:
```bash
python3 -m venv venv && source venv/bin/activate && pip install -r requirements.txt
```

### ✓ Node config file
`~/vernex/config/node.json` — gitignored, machine-specific. Daemon reads on startup,
generates and persists `node_id` on first run.
- vernex-node1 ID: `VRX-e225eb210cc3949e`
- vernex-node2 ID: `VRX-e4fa4b7574465b58`

### ✓ Ollama/Mistral LLM pipeline
- Ollama installed on Node-1; mistral model pulled and running 100% GPU (RTX 3070, 4978MiB)
- `/submit` endpoint routes prompts to Ollama at localhost:11434
- Class 1 earns contribution_delta=2.0, Class 2 earns 1.0

### ✓ Class 1/2 token priority queue (Phase 4 foundation)
- `TokenRequest` struct: class, prompt, justification, estimated_cost, runtime_ceiling
- Heap-based priority queue: Class 1 before Class 2, FIFO within class via sequence number
- Single scheduler worker goroutine; `queue_depth` exposed in `/status`
- Priority ordering validated under concurrent load (3x Class 2 + 2x Class 1 simultaneous)

### ✓ Commons Review (patented mechanism)
- Class 2 `/submit` triggers Mistral assessment of community benefit
- If benefit detected: returns `status: commons_review` with `review_id` and suggestion
- Request held in memory; NOT executed until explicit `/consent` response
- `/consent` accepts `upgrade: true` (run as Class 1) or `upgrade: false` (run as Class 2)
- `upgrade` field is a pointer — must be explicitly provided (cannot be omitted)
- Pending reviews expire after 60s → auto-execute as Class 2
- End-to-end tested and working

---

## Immediate Next Steps (in priority order)

### 1. Graceful degradation (Phase 4 final piece)
When a job hits its `runtime_ceiling`, checkpoint → run local → re-queue.
Deferred until after investor demo validation.

### 2. Systemd service files
Create service files so the daemon starts automatically on boot (also fixes Node-2 sleep inhibitor):

```
/etc/systemd/system/vernex-daemon.service
/etc/systemd/system/vernex-dashboard.service
```

### 3. Backup strategy
- Automated daily backup of ~/vernex to external drive or second node
- Git already handles code backup via GitHub
- Config and logs need separate backup

---

## How to Run

### Start daemon (Node-1)
```bash
cd ~/vernex/daemon
./vernex-node
```

### Start dashboard (Node-1)
```bash
cd ~/vernex/dashboard
source venv/bin/activate
python3 app.py
```

### Start daemon (Node-2, via SSH)
```bash
ssh ericgeer@172.17.0.198 "cd ~/vernex/daemon && ./vernex-node &"
```

### View dashboard
Open browser: http://localhost:5000

### Query node status directly
```bash
curl http://localhost:7701/status | jq        # Node-1
curl http://172.17.0.198:7701/status | jq     # Node-2
```

### Build daemon after code changes
```bash
cd ~/vernex/daemon
go build -o vernex-node .
```

---

## Git Workflow

```bash
cd ~/vernex
git add -A
git commit -m "description of change"
git push origin main

# On Node-2 after pushing from Node-1:
ssh ericgeer@172.17.0.198
cd ~/vernex && git pull origin main
cd daemon && go build -o vernex-node .
```

---

## Known Issues / Watch Out For

- `dashboard/venv/` is gitignored — must run `pip install flask requests` on each new node
- `daemon/vernex-node` binary is gitignored — must run `go build` on each node
- Both nodes have the same hostname `pop-os` by default — renamed to vernex-node1/vernex-node2
  via `sudo hostnamectl set-hostname vernex-nodeX`
- Node-2 SSH key passphrase required for GitHub operations
- Power save can take nodes offline — sleep inhibitor works when running interactively;
  deferred fix via systemd service PAMName=login or running as root
- systemd service dashboard (203/EXEC) deferred — venv path issue in service context
- Node-2 does not have Ollama — /submit only works on Node-1 currently

---

## Key Decisions Made

| Date | Decision | Reason |
|------|----------|--------|
| Mar 2026 | Go for daemon, Python for dashboard | Go handles concurrent P2P efficiently on budget hardware |
| Mar 2026 | Port 7700 for P2P, 7701 for HTTP API | Avoids common dev port conflicts |
| Mar 2026 | Single-node first, P2P after | Faster to investor-ready demo |
| Mar 2026 | Private GitHub repo | Patent pending, pre-investor |
| Mar 2026 | Apache 2.0 license (planned) | Open source with patent clause protection |
| Mar 2026 | venv excluded from git | Each node installs its own dependencies |
| Mar 2026 | godbus/dbus for sleep inhibitor | Pure Go, no CGO, works on Pop!_OS/systemd |
| Mar 2026 | node_id persisted to node.json | Stable identity across daemon restarts |
| Mar 2026 | Inhibitor skips gracefully if unavailable | SSH-launched daemons lack logind session; resolved by systemd service (item 2) |
| Mar 2026 | Mistral via Ollama for LLM backend | Runs 100% GPU on RTX 3070; no API key needed; fits in 8GB VRAM |
| Mar 2026 | Commons Review uses Mistral for assessment | Same local model for both assessment and generation; no external dependency |
| Mar 2026 | upgrade field is pointer (*bool) in consent | Prevents implicit consent — omitting upgrade returns 400; legally significant |
| Mar 2026 | Pending reviews expire as Class 2, not dropped | User gets their answer even if they miss the consent window |

---

## Context for Claude Code

When working on this project:
- Always build and test after code changes: `go build -o vernex-node .`
- The patented mechanisms (Commons Review, two-class scheduler, graceful degradation)
  must be preserved exactly as designed — do not simplify them away
- Keep the code budget-hardware friendly — it must run on a Raspberry Pi eventually
- Document significant changes in CLAUDE.md under Key Decisions
- Commit messages follow: `type: description` (feat, fix, milestone, refactor, docs)
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
- Go daemon v0.2.0 running on two nodes (vernex-node1 and vernex-node2)
- P2P TCP listener on port 7700
- HTTP status API on port 7701 (`/status` and `/health`)
- Contribution score tracking (increments over time and per connection)
- Python Flask dashboard at localhost:5000 polling both nodes every 5 seconds
- Both nodes ONLINE and visible in dashboard
- Private GitHub repo, SSH key auth on both nodes

### Node Registry
- **vernex-node1**: 172.17.0.132 — RTX 3070 / 64GB RAM — primary dev machine (Pop!_OS)
- **vernex-node2**: 172.17.0.198 — HP Victus — second node (Pop!_OS)

---

## Project Structure

```
~/vernex/
  daemon/
    main.go          ← Go daemon source (v0.2.0)
    go.mod           ← Go module file
    vernex-node      ← compiled binary (gitignored)
  dashboard/
    app.py           ← Python Flask dashboard
    venv/            ← Python virtualenv (gitignored)
  config/            ← node config (empty for now)
  logs/              ← runtime logs (empty for now)
  scripts/           ← helper scripts (empty for now)
  .gitignore
  CLAUDE.md          ← this file
```

---

## Architecture

### Daemon (Go)
- Generates a unique `VRX-{hex}` Node ID on startup
- Listens on port 7700 for P2P TCP connections
- Serves HTTP on port 7701 for dashboard polling
- Tracks: uptime, total connections, contribution score
- Partition: 70% personal / 30% social (hardcoded for now, will be configurable)

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

## Immediate Next Steps (in priority order)

### 1. Sleep inhibitor in Go daemon
Prevent nodes from sleeping while daemon is running. Use systemd-logind D-Bus inhibitor.
Release automatically on daemon exit.

```go
// Add to daemon startup
import "github.com/godbus/dbus/v5"
// Take inhibitor lock: what=sleep:idle, who=Vernex Node, why=Contributing compute
// Store fd, defer close on shutdown
```

Install dependency first: `go get github.com/godbus/dbus/v5`

### 2. requirements.txt for dashboard
Add `~/vernex/dashboard/requirements.txt` so any new node can install Flask without
needing the venv to be in git:

```
flask==3.1.3
requests==2.33.1
```

### 3. Node config file
Create `~/vernex/config/node.json` (gitignored) with machine-specific settings:

```json
{
  "node_id": "",
  "social_partition_pct": 30,
  "personal_partition_pct": 70,
  "daemon_port": 7700,
  "api_port": 7701,
  "dashboard_port": 5000
}
```

Daemon should read this on startup instead of hardcoding values.

### 4. Class 1 / Class 2 token scheduler (Phase 4)
Implement the two-class token priority system in the daemon:
- Add `TokenRequest` struct with class, justification, estimated cost, runtime ceiling
- Add priority queue with Class 1 ahead of Class 2
- Add async validator goroutine for Class 1 reclassification
- Add Commons Review prompt mechanism (suggest upgrade, require consent)
- Add graceful degradation: checkpoint → run local → re-queue

### 5. Systemd service files
Create service files so the daemon starts automatically on boot:

```
/etc/systemd/system/vernex-daemon.service
/etc/systemd/system/vernex-dashboard.service
```

### 6. Backup strategy
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
- Power save can take nodes offline — sleep inhibitor is next priority item

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

---

## Context for Claude Code

When working on this project:
- Always build and test after code changes: `go build -o vernex-node .`
- The patented mechanisms (Commons Review, two-class scheduler, graceful degradation)
  must be preserved exactly as designed — do not simplify them away
- Keep the code budget-hardware friendly — it must run on a Raspberry Pi eventually
- Document significant changes in CLAUDE.md under Key Decisions
- Commit messages follow: `type: description` (feat, fix, milestone, refactor, docs)
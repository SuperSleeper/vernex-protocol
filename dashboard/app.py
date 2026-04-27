from flask import Flask, render_template_string, jsonify, request, send_from_directory
import requests
import json
import os
import threading
import time

app = Flask(__name__)

LOCAL_URL = "https://localhost:7701"
_PEERS_TTL = 5.0  # seconds between /peers refreshes

_peers_lock = threading.Lock()
_peers_cache: dict = {}
_peers_last_fetch: float = 0.0


def get_nodes() -> dict:
    """Return {name: {"url": api_url, "connection_type": str}} for all live peers plus local.

    Calls the local daemon's /peers endpoint and caches the result for
    _PEERS_TTL seconds. Always includes localhost. Falls back to
    localhost-only if the daemon is unreachable.
    """
    global _peers_cache, _peers_last_fetch
    now = time.monotonic()
    with _peers_lock:
        if now - _peers_last_fetch < _PEERS_TTL:
            return dict(_peers_cache)
        nodes = {"local": {"url": LOCAL_URL, "connection_type": "local"}}
        try:
            r = requests.get(f"{LOCAL_URL}/peers", timeout=2, verify=False)
            for entry in r.json():
                node_id = entry.get("node_id", "")
                api_url = entry.get("api_url", "")
                conn_type = entry.get("connection_type", "relayed")
                if node_id and api_url:
                    nodes[node_id] = {"url": api_url, "connection_type": conn_type}
        except Exception:
            pass
        _peers_cache = nodes
        _peers_last_fetch = now
        return dict(nodes)

DASHBOARD_HTML = """
<!DOCTYPE html>
<html>
<head>
  <title>Vernex Protocol — Network Dashboard</title>
  <meta http-equiv="refresh" content="5">
  <style>
    * { margin: 0; padding: 0; box-sizing: border-box; }
    body { background: #0d1117; color: #e6edf3; font-family: 'Courier New', monospace; padding: 2rem; }
    h1 { color: #58a6ff; font-size: 1.4rem; margin-bottom: 0.25rem; letter-spacing: 2px; }
    .subtitle { color: #8b949e; font-size: 0.8rem; }
    .top-bar { display: flex; justify-content: space-between; align-items: flex-start; margin-bottom: 2rem; }
    .top-right { display: flex; align-items: center; gap: 1rem; padding-top: 0.2rem; }
    .refresh-note { color: #8b949e; font-size: 0.7rem; }
    .toggle-btn { background: none; border: 1px solid #30363d; color: #8b949e; font-family: 'Courier New', monospace; font-size: 0.75rem; padding: 4px 10px; cursor: pointer; border-radius: 4px; letter-spacing: 1px; }
    .toggle-btn:hover { border-color: #58a6ff; color: #58a6ff; }
    /* ── Cards ── */
    .grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(420px, 1fr)); gap: 1.5rem; }
    .card { background: #161b22; border: 1px solid #30363d; border-radius: 8px; padding: 1.5rem; }
    .card.online  { border-left: 4px solid #3fb950; }
    .card.offline { border-left: 4px solid #f85149; opacity: 0.6; }
    .card.pending { border-left: 4px solid #d29922; background: #1a1600; }
    .node-header { display: flex; justify-content: space-between; align-items: center; margin-bottom: 1.2rem; }
    .node-name { font-size: 1.1rem; font-weight: bold; color: #e6edf3; }
    .node-id { font-size: 0.75rem; color: #58a6ff; margin-top: 2px; }
    .badges { display: flex; gap: 0.4rem; align-items: center; flex-wrap: wrap; justify-content: flex-end; }
    .badge { font-size: 0.7rem; padding: 3px 10px; border-radius: 12px; font-weight: bold; }
    .badge.online   { background: #1a4a1f; color: #3fb950; }
    .badge.offline  { background: #3d1a1a; color: #f85149; }
    .badge.pending  { background: #2a1f00; color: #d29922; }
    .stats { display: grid; grid-template-columns: 1fr 1fr; gap: 0.8rem; margin-bottom: 1.2rem; }
    .stat { background: #0d1117; border-radius: 6px; padding: 0.8rem; }
    .stat-label { font-size: 0.7rem; color: #8b949e; text-transform: uppercase; letter-spacing: 1px; margin-bottom: 4px; }
    .stat-value { font-size: 1.3rem; color: #e6edf3; font-weight: bold; }
    .stat-value.blue  { color: #58a6ff; }
    .stat-value.green { color: #3fb950; }
    .stat-value.amber { color: #d29922; }
    .stat-value.ip    { font-size: 0.95rem; color: #8b949e; }
    .partition-bar { margin-top: 0.5rem; }
    .partition-label { font-size: 0.7rem; color: #8b949e; margin-bottom: 6px; text-transform: uppercase; letter-spacing: 1px; }
    .bar-track { background: #0d1117; border-radius: 4px; height: 20px; overflow: hidden; display: flex; }
    .bar-personal { background: #1f6feb; height: 100%; display: flex; align-items: center; justify-content: center; font-size: 0.65rem; color: #e6edf3; }
    .bar-social   { background: #3fb950; height: 100%; display: flex; align-items: center; justify-content: center; font-size: 0.65rem; color: #0d1117; font-weight: bold; }
    .card-trust-info { margin-top: 1rem; padding-top: 0.75rem; border-top: 1px solid #3d2e00; font-size: 0.72rem; color: #8b949e; line-height: 1.6; }
    .card-trust-actions { display: flex; gap: 0.5rem; margin-top: 0.75rem; }
    /* ── Compact table ── */
    .compact-table { width: 100%; border-collapse: collapse; font-size: 0.8rem; }
    .compact-table th { color: #8b949e; text-transform: uppercase; letter-spacing: 1px; font-size: 0.65rem; padding: 0.5rem 0.8rem; text-align: left; border-bottom: 1px solid #30363d; white-space: nowrap; }
    .compact-table td { padding: 0.55rem 0.8rem; border-bottom: 1px solid #21262d; white-space: nowrap; }
    .compact-table tbody tr:nth-child(odd)  td { background: #0d1117; }
    .compact-table tbody tr:nth-child(even) td { background: #161b22; }
    .compact-table tbody tr.ct-pending      td { background: #1a1600 !important; }
    .ct-online  { color: #3fb950; font-weight: bold; }
    .ct-offline { color: #f85149; font-weight: bold; }
    /* ── Shared approve/deny buttons ── */
    .btn-approve { background: #1a4a1f; border: 1px solid #3fb950; color: #3fb950; font-family: 'Courier New', monospace; font-size: 0.72rem; padding: 3px 10px; border-radius: 4px; cursor: pointer; font-weight: bold; letter-spacing: 1px; }
    .btn-approve:hover { background: #3fb950; color: #0d1117; }
    .btn-deny    { background: #3d1a1a; border: 1px solid #f85149; color: #f85149; font-family: 'Courier New', monospace; font-size: 0.72rem; padding: 3px 10px; border-radius: 4px; cursor: pointer; font-weight: bold; letter-spacing: 1px; }
    .btn-deny:hover    { background: #f85149; color: #0d1117; }
    /* ── Footer ── */
    .footer  { margin-top: 2rem; color: #8b949e; font-size: 0.75rem; text-align: center; }
    .version { color: #8b949e; font-size: 0.75rem; }
  </style>
</head>
<body>
  <div class="top-bar">
    <div>
      <h1>⬡ VERNEX PROTOCOL</h1>
      <p class="subtitle">Network Dashboard — Patent Pending US App. 64/015,885</p>
    </div>
    <div class="top-right">
      <span class="refresh-note">Auto-refreshes every 5s</span>
      <button class="toggle-btn" id="toggle-btn" onclick="toggleMode()">[COMPACT]</button>
    </div>
  </div>

  <!-- Cards view (default) -->
  <div class="grid" id="cards-view">
    {% for name, data in nodes.items() %}
    {% set pending = data.online and data.node_id in trust_map %}
    <div class="card {% if pending %}pending{% elif data.online %}online{% else %}offline{% endif %}"
         id="tr-{{ data.node_id if data.online else name }}">
      <div class="node-header">
        <div>
          <div class="node-name">{{ name }}</div>
          {% if data.online %}
          <div class="node-id">{{ data.node_id }}</div>
          {% else %}
          <div class="node-id" style="color:#f85149">Unreachable</div>
          {% endif %}
        </div>
        <div class="badges">
          <span class="badge {{ 'online' if data.online else 'offline' }}">
            {{ 'ONLINE' if data.online else 'OFFLINE' }}
          </span>
          {% if pending %}<span class="badge pending">⚠ PENDING APPROVAL</span>{% endif %}
        </div>
      </div>

      {% if data.online %}
      <div class="stats">
        <div class="stat">
          <div class="stat-label">Uptime</div>
          <div class="stat-value blue">{{ data.uptime }}</div>
        </div>
        <div class="stat">
          <div class="stat-label">Connections</div>
          <div class="stat-value amber">{{ data.total_connections }}</div>
        </div>
        <div class="stat">
          <div class="stat-label">Contribution Score</div>
          <div class="stat-value green">{{ "%.1f"|format(data.contribution_score) }}</div>
        </div>
        <div class="stat">
          <div class="stat-label">Version</div>
          <div class="stat-value version">v{{ data.version }}</div>
        </div>
        <div class="stat">
          <div class="stat-label">IP Address</div>
          <div class="stat-value ip">{{ data.ip_address }}</div>
        </div>
        <div class="stat">
          <div class="stat-label">Public IP</div>
          <div class="stat-value ip">{{ data.public_ip }}</div>
        </div>
        <div class="stat">
          <div class="stat-label">External IP</div>
          <div class="stat-value ip">{{ data.external_ip }}</div>
        </div>
        <div class="stat">
          <div class="stat-label">Connection</div>
          <div class="stat-value {% if data.connection_type == 'direct' %}green{% elif data.connection_type == 'local' %}blue{% else %}amber{% endif %}">{{ data.connection_type | upper }}</div>
        </div>
      </div>

      <div class="partition-bar">
        <div class="partition-label">Compute Partition</div>
        <div class="bar-track">
          <div class="bar-personal" style="width: {{ data.personal_partition_pct }}%">
            {{ data.personal_partition_pct }}% Personal
          </div>
          <div class="bar-social" style="width: {{ data.social_partition_pct }}%">
            {{ data.social_partition_pct }}% Social
          </div>
        </div>
      </div>

      {% if pending %}
      {% set tr = trust_map[data.node_id] %}
      <div class="card-trust-info">
        key: {{ tr.public_key[:28] }}… &nbsp;|&nbsp;
        from {{ tr.source_ip }} &nbsp;|&nbsp;
        {{ tr.requested_at[:19] }} &nbsp;|&nbsp;
        {{ tr.api_url }}
      </div>
      <div class="card-trust-actions">
        <button class="btn-approve" onclick="approveTrust('{{ data.node_id }}')">APPROVE</button>
        <button class="btn-deny"    onclick="denyTrust('{{ data.node_id }}')">DENY</button>
      </div>
      {% endif %}
      {% endif %}
    </div>
    {% endfor %}
  </div>

  <!-- Compact view -->
  <div id="compact-view" style="display:none">
    <table class="compact-table">
      <thead>
        <tr>
          <th>Node ID</th>
          <th>Status</th>
          <th>IP</th>
          <th>Public IP</th>
          <th>External IP</th>
          <th>Connection</th>
          <th>Uptime</th>
          <th>Score</th>
          <th>Version</th>
          <th></th>
        </tr>
      </thead>
      <tbody>
        {% for name, data in nodes.items() %}
        {% set pending = data.online and data.node_id in trust_map %}
        <tr class="{{ 'ct-pending' if pending else '' }}"
            id="tr-ct-{{ data.node_id if data.online else name }}">
          <td>
            <div style="font-weight:bold; color:#e6edf3;">{{ name }}</div>
            {% if data.online %}<div style="font-size:0.65rem; color:#58a6ff; margin-top:2px;">{{ data.node_id }}</div>{% endif %}
          </td>
          <td>
            <span class="{{ 'ct-online' if data.online else 'ct-offline' }}">
              {{ 'ONLINE' if data.online else 'OFFLINE' }}
            </span>
            {% if pending %}&nbsp;<span class="badge pending" style="font-size:0.6rem;">⚠ PENDING</span>{% endif %}
          </td>
          <td style="color:#8b949e;">{{ data.ip_address if data.online else '—' }}</td>
          <td style="color:#8b949e;">{{ data.public_ip if data.online else '—' }}</td>
          <td style="color:#8b949e;">{{ data.external_ip if data.online else '—' }}</td>
          <td style="color:{% if data.online %}{% if data.connection_type == 'direct' %}#3fb950{% elif data.connection_type == 'local' %}#58a6ff{% else %}#d29922{% endif %}{% else %}#8b949e{% endif %}; font-weight:bold;">{{ data.connection_type | upper if data.online else '—' }}</td>
          <td style="color:#58a6ff;">{{ data.uptime if data.online else '—' }}</td>
          <td style="color:#3fb950;">{{ "%.1f"|format(data.contribution_score) if data.online else '—' }}</td>
          <td style="color:#8b949e;">{{ 'v' + data.version if data.online else '—' }}</td>
          <td>
            {% if pending %}
            <div style="display:flex; gap:0.35rem;">
              <button class="btn-approve" onclick="approveTrust('{{ data.node_id }}')">APPROVE</button>
              <button class="btn-deny"    onclick="denyTrust('{{ data.node_id }}')">DENY</button>
            </div>
            {% endif %}
          </td>
        </tr>
        {% endfor %}
      </tbody>
    </table>
  </div>

  <div class="footer">
    Vernex Protocol v0.2.0 &nbsp;|&nbsp; {{ total_online }} of {{ total_nodes }} nodes online
    &nbsp;|&nbsp; Network score: {{ "%.1f"|format(network_score) }}
  </div>

  <script>
    function applyMode(mode) {
      var cards = document.getElementById('cards-view');
      var compact = document.getElementById('compact-view');
      var btn = document.getElementById('toggle-btn');
      if (mode === 'compact') {
        cards.style.display = 'none';
        compact.style.display = 'block';
        btn.textContent = '[CARDS]';
      } else {
        cards.style.display = 'grid';
        compact.style.display = 'none';
        btn.textContent = '[COMPACT]';
      }
    }
    function toggleMode() {
      var next = (localStorage.getItem('vernex-view') || 'cards') === 'compact' ? 'cards' : 'compact';
      localStorage.setItem('vernex-view', next);
      applyMode(next);
    }
    applyMode(localStorage.getItem('vernex-view') || 'cards');

    function approveTrust(nodeId) {
      ['tr-' + nodeId, 'tr-ct-' + nodeId].forEach(function(id) {
        var el = document.getElementById(id);
        if (el) { el.style.opacity = '0.4'; el.style.pointerEvents = 'none'; }
      });
      fetch('/api/trust-approve', {
        method: 'POST',
        headers: {'Content-Type': 'application/json'},
        body: JSON.stringify({node_id: nodeId})
      }).then(function() { location.reload(); });
    }
    function denyTrust(nodeId) {
      ['tr-' + nodeId, 'tr-ct-' + nodeId].forEach(function(id) {
        var el = document.getElementById(id);
        if (el) { el.style.opacity = '0.4'; el.style.pointerEvents = 'none'; }
      });
      fetch('/api/trust-deny', {
        method: 'POST',
        headers: {'Content-Type': 'application/json'},
        body: JSON.stringify({node_id: nodeId})
      }).then(function() { location.reload(); });
    }
  </script>
</body>
</html>
"""

def fmt_uptime(seconds):
    h = seconds // 3600
    m = (seconds % 3600) // 60
    s = seconds % 60
    if h > 0:
        return f"{h}h {m}m {s}s"
    elif m > 0:
        return f"{m}m {s}s"
    return f"{s}s"

@app.route("/")
def index():
    nodes_map = get_nodes()
    nodes = {}
    total_online = 0
    network_score = 0.0

    for name, node_info in nodes_map.items():
        url = node_info["url"] if isinstance(node_info, dict) else node_info
        conn_type = node_info.get("connection_type", "relayed") if isinstance(node_info, dict) else "local"
        try:
            r = requests.get(f"{url}/status", timeout=2, verify=False)
            d = r.json()
            nodes[name] = {
                "online": True,
                "node_id": d["node_id"],
                "uptime": fmt_uptime(d["uptime_seconds"]),
                "total_connections": d["total_connections"],
                "contribution_score": d["contribution_score"],
                "version": d["version"],
                "personal_partition_pct": d["personal_partition_pct"],
                "social_partition_pct": d["social_partition_pct"],
                "ip_address": d.get("ip_address", "—"),
                "public_ip": d.get("public_ip", "—"),
                "external_ip": d.get("external_ip", "—"),
                "connection_type": conn_type,
            }
            total_online += 1
            network_score += d["contribution_score"]
        except Exception:
            nodes[name] = {"online": False}

    trust_map = {}
    try:
        tr = requests.get(f"{LOCAL_URL}/trust-requests", timeout=2, verify=False)
        trust_map = {t["node_id"]: t for t in (tr.json() or [])}
    except Exception:
        pass

    return render_template_string(
        DASHBOARD_HTML,
        nodes=nodes,
        total_online=total_online,
        total_nodes=len(nodes_map),
        network_score=network_score,
        trust_map=trust_map,
    )

@app.route("/ui")
def ui():
    return send_from_directory(os.path.dirname(__file__), "index.html")


@app.route("/api/nodes")
def api_nodes():
    nodes = {}
    for name, node_info in get_nodes().items():
        url = node_info["url"] if isinstance(node_info, dict) else node_info
        try:
            r = requests.get(f"{url}/status", timeout=2, verify=False)
            d = r.json()
            nodes[name] = {
                "online": True,
                "node_id": d["node_id"],
                "uptime": fmt_uptime(d["uptime_seconds"]),
                "contribution_score": d["contribution_score"],
                "version": d["version"],
            }
        except Exception:
            nodes[name] = {"online": False}
    return jsonify(nodes)


@app.route("/api/submit", methods=["POST"])
def api_submit():
    try:
        r = requests.post(
            f"{LOCAL_URL}/submit",
            json=request.get_json(),
            timeout=120,
            verify=False,
        )
        return r.content, r.status_code, {"Content-Type": "application/json"}
    except Exception as e:
        return jsonify({"error": str(e)}), 503


@app.route("/api/consent", methods=["POST"])
def api_consent():
    try:
        r = requests.post(
            f"{LOCAL_URL}/consent",
            json=request.get_json(),
            timeout=120,
            verify=False,
        )
        return r.content, r.status_code, {"Content-Type": "application/json"}
    except Exception as e:
        return jsonify({"error": str(e)}), 503


@app.route("/api/trust-requests")
def api_trust_requests():
    try:
        r = requests.get(f"{LOCAL_URL}/trust-requests", timeout=2, verify=False)
        return r.content, r.status_code, {"Content-Type": "application/json"}
    except Exception as e:
        return jsonify({"error": str(e)}), 503


@app.route("/api/trust-approve", methods=["POST"])
def api_trust_approve():
    try:
        r = requests.post(
            f"{LOCAL_URL}/trust-approve",
            json=request.get_json(),
            timeout=5,
            verify=False,
        )
        return r.content, r.status_code, {"Content-Type": "application/json"}
    except Exception as e:
        return jsonify({"error": str(e)}), 503


@app.route("/api/trust-deny", methods=["POST"])
def api_trust_deny():
    try:
        r = requests.post(
            f"{LOCAL_URL}/trust-deny",
            json=request.get_json(),
            timeout=5,
            verify=False,
        )
        return r.content, r.status_code, {"Content-Type": "application/json"}
    except Exception as e:
        return jsonify({"error": str(e)}), 503


if __name__ == "__main__":
    app.run(host="0.0.0.0", port=5000, debug=False)

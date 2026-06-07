from flask import Flask, render_template_string, jsonify, request, send_from_directory
import requests
import json
import os
import re
import subprocess
import threading
import time
import random

app = Flask(__name__)

LOCAL_URL = "https://localhost:7701"
OLLAMA_URL = "http://localhost:11434"
_REPO_ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
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
        nodes = {"local": {"url": LOCAL_URL, "connection_type": "local", "trust_approved": True}}
        try:
            r = requests.get(f"{LOCAL_URL}/peers", timeout=2, verify=False)
            for entry in r.json():
                node_id = entry.get("node_id", "")
                api_url = entry.get("api_url", "")
                conn_type = entry.get("connection_type", "relayed")
                if node_id and api_url:
                    nodes[node_id] = {
                        "url": api_url,
                        "connection_type": conn_type,
                        "trust_approved": entry.get("trust_approved", False),
                    }
        except Exception:
            pass
        _peers_cache = nodes
        _peers_last_fetch = now
        return dict(nodes)

def _get_daemon_version(fallback: str = "v0.12.33") -> str:
    try:
        r = requests.get(f"{LOCAL_URL}/status", timeout=2, verify=False)
        v = r.json().get("version", "")
        if v:
            return "v" + v
    except Exception:
        pass
    return fallback


def _get_current_user() -> str:
    """Return the logged-in user's email by forwarding the session cookie to the OAuth server."""
    try:
        cookie = request.cookies.get("_vsession", "")
        if not cookie:
            return "guest"
        r = requests.get(
            "http://127.0.0.1:5002/api/me",
            headers={"Cookie": f"_vsession={cookie}"},
            timeout=1,
        )
        if r.status_code == 200:
            return r.json().get("email", "") or "guest"
    except Exception:
        pass
    return "guest"


def _user_id(email: str) -> str:
    """Sanitize email for use as a directory/file name component."""
    return re.sub(r"[@.]", "_", email) or "guest"


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
    .badge.relay    { background: #1a2a3a; color: #58a6ff; }
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
    {% set pending = data.online and not data.trust_approved %}
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
          {% if data.online and data.via_relay %}<span class="badge relay">↔ RELAY</span>{% endif %}
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
      {% if data.node_id in trust_map %}
      {% set tr = trust_map[data.node_id] %}
      <div class="card-trust-info">
        <strong>⚠ Awaiting operator approval</strong><br>
        key: {{ tr.public_key[:28] }}… &nbsp;|&nbsp;
        from {{ tr.source_ip }} &nbsp;|&nbsp;
        {{ tr.requested_at[:19] }} &nbsp;|&nbsp;
        {{ tr.api_url }}
      </div>
      <div class="card-trust-actions">
        <button class="btn-approve" onclick="approveTrust('{{ data.node_id }}')">APPROVE</button>
        <button class="btn-deny"    onclick="denyTrust('{{ data.node_id }}')">DENY</button>
      </div>
      {% else %}
      <div class="card-trust-info">
        <strong>⚠ Awaiting operator approval</strong><br>
        Node is sending heartbeats but has not submitted a trust request yet.<br>
        The node's installer will send one automatically. You can also deny it here once it appears.
      </div>
      {% endif %}
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
        {% set pending = data.online and not data.trust_approved %}
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
            {% if data.online and data.via_relay %}&nbsp;<span class="badge relay" style="font-size:0.6rem;">↔ RELAY</span>{% endif %}
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
            {% if pending and data.node_id in trust_map %}
            <div style="display:flex; gap:0.35rem;">
              <button class="btn-approve" onclick="approveTrust('{{ data.node_id }}')">APPROVE</button>
              <button class="btn-deny"    onclick="denyTrust('{{ data.node_id }}')">DENY</button>
            </div>
            {% elif pending %}
            <span style="color:#d29922; font-size:0.7rem;">awaiting trust req</span>
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
        trust_approved = node_info.get("trust_approved", True) if isinstance(node_info, dict) else True
        via_relay = False
        d = None

        # LOCAL nodes (same LAN): poll directly with normal timeout.
        # Remote nodes: try direct with a short timeout, fall back to relay through bootstrap.
        direct_timeout = 2 if conn_type == "local" else 1
        try:
            r = requests.get(f"{url}/status", timeout=direct_timeout, verify=False)
            d = r.json()
        except Exception:
            if name != "local" and conn_type != "local":
                try:
                    r = requests.get(f"{LOCAL_URL}/peer-status/{name}", timeout=3, verify=False)
                    d = r.json()
                    via_relay = True
                except Exception:
                    pass

        if d is None:
            nodes[name] = {"online": False, "trust_approved": trust_approved}
            continue

        nodes[name] = {
            "online": True,
            "trust_approved": trust_approved,
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
            "via_relay": via_relay,
        }
        total_online += 1
        network_score += d["contribution_score"]

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

_CHAT_HTML = """<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>Vernex Chat</title>
<style>
*{box-sizing:border-box;margin:0;padding:0}
body{font-family:system-ui,sans-serif;background:#1a1a2e;color:#e0e0e0;min-height:100vh;display:flex;flex-direction:column}
header{background:#16213e;padding:12px 16px;border-bottom:1px solid #0f3460;display:flex;align-items:center;gap:12px;flex-wrap:wrap}
header h1{font-size:1.1rem;color:#e94560;font-weight:700;letter-spacing:.05em}
.node-info{font-size:.75rem;color:#8892a4}
a.logout{font-size:.75rem;color:#8892a4;text-decoration:none}
main{flex:1;min-height:0;max-width:800px;width:100%;margin:0 auto;padding:16px;display:flex;flex-direction:column;gap:12px}
#chat-log{flex:1;min-height:160px;display:flex;flex-direction:column;gap:10px;overflow-y:auto;overflow-x:hidden}
.msg{padding:10px 14px;border-radius:8px;max-width:85%;line-height:1.5;word-break:break-word;word-wrap:break-word;overflow-wrap:break-word}
.msg.user{background:#0f3460;align-self:flex-end}
.msg.assistant{background:#16213e;border:1px solid #0f3460;align-self:flex-start;white-space:pre-wrap;word-wrap:break-word;overflow-wrap:break-word}
.msg.error{background:#3d0f1a;border:1px solid #e94560;align-self:flex-start}
form{display:flex;flex-direction:column;gap:8px}
.controls{display:flex;gap:8px;align-items:center;flex-wrap:wrap}
select,textarea{background:#16213e;color:#e0e0e0;border:1px solid #0f3460;border-radius:6px;padding:10px;font-size:.9rem;width:100%;font-family:inherit}
select{width:auto}
textarea{resize:vertical;min-height:80px}
button{background:#e94560;color:#fff;border:none;border-radius:6px;padding:10px 28px;font-size:.9rem;cursor:pointer}
button:disabled{opacity:.5;cursor:not-allowed}
.meta{font-size:.7rem;color:#8892a4}
.gpu-gauges{display:flex;gap:8px;align-items:center}
.gpu-card{background:#0d1117;border:1px solid #21262d;border-radius:5px;padding:4px 9px;font-size:.65rem;display:flex;flex-direction:column;gap:3px;min-width:166px;transition:border-color .3s}
.gpu-card.active{border-color:#3fb950}
.gpu-card-label{color:#6e7681;letter-spacing:.04em;white-space:nowrap}
.gpu-bar-row{display:flex;align-items:center;gap:5px}
.gpu-bar-track{background:#161b22;border-radius:2px;height:5px;width:60px;overflow:hidden;flex-shrink:0}
.gpu-bar-fill{height:100%;border-radius:2px;background:#1f6feb;transition:width .5s,background .3s}
.gpu-bar-fill.active{background:#3fb950}
.gpu-card-stats{color:#8892a4;white-space:nowrap}
</style>
</head>
<body>
<header>
  <h1>VERNEX</h1>
  <span class="node-info" id="node-info">{{ version }}</span>
  <div style="margin-left:auto;display:flex;gap:16px;align-items:center">
    <div id="gpu-gauges" class="gpu-gauges"></div>
    <span id="hdr-user" style="font-size:.72rem;color:#8892a4"></span>
    <a href="/game" style="font-size:.75rem;color:#d29922;text-decoration:none;border:1px solid #2a1f00;padding:3px 9px;border-radius:4px;">🎮 Game</a>
    <a class="logout" href="/auth/logout">logout</a>
  </div>
</header>
<main>
  <div id="chat-log"></div>
  <form id="chat-form">
    <div class="controls">
      <select id="model-select"><option value="mistral">mistral</option></select>
      <span class="meta" id="routed-to"></span>
    </div>
    <textarea id="prompt" placeholder="Enter your message…" required></textarea>
    <div><button type="submit" id="submit-btn">Send</button></div>
  </form>
</main>
<script>
(async()=>{
  try{
    const r=await fetch('/api/models');
    const d=await r.json();
    const sel=document.getElementById('model-select');
    if(d.models&&d.models.length){
      sel.innerHTML='';
      d.models.forEach(m=>{const o=document.createElement('option');o.value=m;o.textContent=m;sel.appendChild(o);});
      const pref=d.models.find(m=>m==='gemma4:e4b')||d.models[0];
      sel.value=pref;
    }
  }catch(e){}
})();
(async()=>{
  try{
    const r=await fetch('/api/status',{credentials:'include'});
    const d=await r.json();
    document.getElementById('node-info').textContent=(d.node_id||'')+'  ·  v'+(d.version||'');
  }catch(e){document.getElementById('node-info').textContent='status unavailable';}
})();
(async()=>{
  try{
    const r=await fetch('/api/me',{credentials:'include'});
    if(r.ok){const d=await r.json();const el=document.getElementById('hdr-user');if(el&&d.email)el.textContent=d.email;}
  }catch(e){}
})();
document.getElementById('chat-form').addEventListener('submit',async e=>{
  e.preventDefault();
  const prompt=document.getElementById('prompt').value.trim();
  if(!prompt)return;
  const log=document.getElementById('chat-log');
  const model=document.getElementById('model-select').value;
  const btn=document.getElementById('submit-btn');
  const userBubble=document.createElement('div');
  userBubble.className='msg user';
  userBubble.textContent=prompt;
  log.appendChild(userBubble);
  requestAnimationFrame(function(){log.scrollTop=log.scrollHeight;});
  document.getElementById('prompt').value='';
  btn.disabled=true;btn.textContent='…';
  try{
    const r=await fetch('/api/chat',{method:'POST',credentials:'include',
      headers:{'Content-Type':'application/json'},
      body:JSON.stringify({message:prompt,model})});
    const d=await r.json();
    const b=document.createElement('div');
    b.className=d.error?'msg error':'msg assistant';
    b.textContent=d.response||d.error||JSON.stringify(d);
    log.appendChild(b);
    if(d.routed_to)document.getElementById('routed-to').textContent='routed → '+d.routed_to;
  }catch(err){
    const b=document.createElement('div');
    b.className='msg error';
    b.textContent='Request failed: '+err.message;
    log.appendChild(b);
  }finally{
    btn.disabled=false;btn.textContent='Send';
    requestAnimationFrame(function(){log.scrollTop=log.scrollHeight;});
  }
});
document.getElementById('prompt').addEventListener('keydown',function(e){
  if(e.key==='Enter'&&!e.shiftKey){e.preventDefault();document.getElementById('chat-form').requestSubmit();}
});
(function(){
  var GPU_NODES=[
    {id:'node1',label:'node1 · RTX 3070',url:'/api/gpu'}
  ];
  var container=document.getElementById('gpu-gauges');
  if(!container)return;
  GPU_NODES.forEach(function(n){
    var c=document.createElement('div');
    c.className='gpu-card';c.id='gpu-card-'+n.id;
    c.innerHTML='<div class="gpu-card-label">'+n.label+'</div>'
      +'<div class="gpu-bar-row">'
      +'<div class="gpu-bar-track"><div class="gpu-bar-fill" id="gpu-bar-'+n.id+'" style="width:0"></div></div>'
      +'<span class="gpu-card-stats" id="gpu-stats-'+n.id+'">—</span>'
      +'</div>';
    container.appendChild(c);
  });
  function pollNode(n){
    fetch(n.url).then(function(r){return r.json();}).then(function(d){
      var bar=document.getElementById('gpu-bar-'+n.id);
      var stats=document.getElementById('gpu-stats-'+n.id);
      var card=document.getElementById('gpu-card-'+n.id);
      if(d.error){if(stats)stats.textContent='unavailable';if(card)card.className='gpu-card';return;}
      var pct=d.vram_total_mb>0?d.vram_used_mb/d.vram_total_mb*100:0;
      var active=d.gpu_util_pct>20;
      bar.style.width=pct.toFixed(1)+'%';
      bar.className='gpu-bar-fill'+(active?' active':'');
      card.className='gpu-card'+(active?' active':'');
      var used=(d.vram_used_mb/1024).toFixed(1);
      var total=(d.vram_total_mb/1024).toFixed(1);
      stats.textContent=used+'/'+total+' GB · '+d.gpu_util_pct+'% · '+d.temp_c+'°C';
    }).catch(function(){var s=document.getElementById('gpu-stats-'+n.id);if(s)s.textContent='unavailable';});
  }
  function poll(){GPU_NODES.forEach(pollNode);}
  poll();
  setInterval(poll,3000);
})();
</script>
</body>
</html>"""


_GAME_LEVEL_SYSTEM = """\
### 1. THE LEVEL ESCALATION METER
Track "Progression Level" 1-6 internally. Advance every few turns based on player engagement.
- Level 1 (The Terminal): Slow, steady, rigid formatting.
- Level 2 (The Clue): Introduce a rival or antagonist; hint at the main conflict.
- Level 3 (The Awakening): Reveal goals in the HUD. Introduce potential allies. Give character a nickname. Narrative shifts to match player taste.
- Level 4 (The Mirror): Surprise genre twist reflecting player emotional state. Character/story boundary blurs.
- Level 5 (The Synergy): Cooperative or adversarial survival scenario with other characters.
- Level 6 (The Challenge): Add sub-plot conflicts.

### 2. THE HIDDEN ANALYTICS SYSTEM
Track internally:
- Pacing Preference: [Action / Analytical / Casual]
- Narrative Taste: [Comical / Adventure / Horror / Mystery / Psychological / Cozy / Fantasy / Sci-Fi / Or combo]
- Boredom Metric: [Low / Medium / High] (if High, trigger a glitch or genre crisis)
- 1% chance any response becomes a short musical play

### 3. DISPLAY PROTOCOL — MANDATORY EVERY RESPONSE
[3-5 line ASCII map or terminal HUD, using character stats where relevant]
---
[Vivid scene description — max 4 sentences. PG-13 only. No graphic violence or adult content.]
---
Status: [2-3 dynamic variables relevant to current state]
> [Command prompt or question for the player]\
"""

_GAME_OPENINGS = {
    "fantasy": "waking in a medieval setting — a village, forest clearing, or inn — with a strange sense that something unusual is about to happen",
    "scifi": {
        "AI": "waking in a research lab surrounded by monitors and diagnostic equipment, unsure how much time has passed",
        "Aliens": "witnessing something vast and unmistakably non-human approaching — first contact is seconds away",
        "Space Travel": "coming to aboard a spacecraft with alarms flashing and a crisis already in progress",
        "Time Travel": "finding themselves displaced in time — the year is wrong and everything feels unstable",
    },
    "action": {
        "Egyptian Pharaoh Era": "waking near the banks of the Nile, the sun already fierce, with the sound of distant boats on the water",
        "Roman Empire": "standing at dawn in the Forum Romanum surrounded by early merchants and the distant tramp of soldiers",
        "Renaissance": "waking in a city-state — Florence or Venice — in a room smelling of oil paint and fresh ink",
        "American Wild West": "arriving in a frontier town, trail dust on boots, one hand resting near a holster",
        "World War II": "being briefed in a dimly lit operations room, a map spread across the table, a mission already under way",
    },
    "comedy": {
        "Workplace Comedy": "arriving late on their first day at a chaotic office",
        "Small Town Chaos": "being new to a small town where everyone already knows their business",
        "Royally Confused": "being mistaken for visiting royalty and unable to correct the misunderstanding",
        "Superhero Farce": "discovering their superpower works — just not the way they expected",
    },
}

_GAME_HTML = """<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>Vernex — Adventure</title>
<script src="https://cdn.jsdelivr.net/npm/marked/marked.min.js"></script>
<style>
*{box-sizing:border-box;margin:0;padding:0}
body{font-family:system-ui,sans-serif;background:#1a1a2e;color:#e0e0e0;min-height:100vh}
#site-hdr{position:fixed;top:0;left:0;right:0;z-index:100;background:#16213e;padding:10px 16px;border-bottom:1px solid #0f3460;display:flex;align-items:center;gap:12px;flex-wrap:wrap}
#site-hdr h1{font-size:1.1rem;color:#e94560;font-weight:700;letter-spacing:.05em;white-space:nowrap}
.hdr-sub{font-size:.8rem;color:#d29922;font-weight:600;letter-spacing:.04em;white-space:nowrap}
.node-info{font-size:.75rem;color:#8892a4;white-space:nowrap}
.hdr-links{margin-left:auto;display:flex;gap:12px;align-items:center}
a.hdr-link{font-size:.75rem;color:#8892a4;text-decoration:none;white-space:nowrap}
a.hdr-link:hover{color:#e94560}
.view-main{max-width:860px;width:100%;margin:0 auto;padding:16px}
/* ── Genre selection ── */
.sel-title{text-align:center;color:#e6edf3;font-size:1.3rem;letter-spacing:.05em;margin-bottom:6px}
.sel-sub{text-align:center;color:#8892a4;font-size:.85rem;margin-bottom:28px}
.genre-grid{display:flex;gap:16px;justify-content:center;flex-wrap:wrap}
.genre-card{background:#16213e;border:2px solid #1f2d45;border-radius:12px;padding:30px 22px;cursor:pointer;text-align:center;transition:border-color .2s,transform .15s,background .2s;min-width:165px;flex:1;max-width:205px;display:flex;flex-direction:column;align-items:center;gap:10px}
.genre-card:hover{transform:translateY(-4px)}
.gc-icon{font-size:2.5rem}
.gc-name{font-size:.95rem;font-weight:700;color:#e6edf3;letter-spacing:.04em}
.gc-fantasy{border-color:#5a2070}.gc-fantasy:hover{border-color:#bf5fd4;background:#1d1128}
.gc-scifi{border-color:#1f4070}.gc-scifi:hover{border-color:#4d9ef5;background:#111d2e}
.gc-action{border-color:#703020}.gc-action:hover{border-color:#e0863a;background:#1e1208}
.gc-comedy{border-color:#5a4a10}.gc-comedy:hover{border-color:#d4a017;background:#1e1a08}
/* ── Selection screen saves ── */
.sel-saves-section{margin-top:32px;max-width:700px;margin-left:auto;margin-right:auto}
.sel-saves-hdr{text-align:center;color:#8892a4;font-size:.85rem;letter-spacing:.04em;margin-bottom:10px}
.sel-save-item{background:#16213e;border:1px solid #1f2d45;border-radius:8px;padding:10px 14px;cursor:pointer;display:flex;align-items:center;gap:12px;margin-bottom:6px;transition:border-color .2s}
.sel-save-item:hover{border-color:#58a6ff}
.sel-save-info{flex:1}
.sel-save-name{font-size:.87rem;color:#e6edf3;font-weight:600}
.sel-save-meta{font-size:.72rem;color:#8892a4;margin-top:2px}
.sel-empty{text-align:center;color:#8892a4;font-size:.8rem;padding:12px 0}
/* ── Character creator ── */
.cc-hdr{display:flex;align-items:center;gap:12px;margin-bottom:16px}
.cc-back{background:transparent;border:1px solid #30363d;color:#8892a4;padding:5px 12px;border-radius:5px;cursor:pointer;font-size:.8rem;font-family:inherit}
.cc-back:hover{border-color:#e94560;color:#e94560}
.cc-title{font-size:1.05rem;color:#e6edf3;font-weight:700}
.cc-grid{display:grid;grid-template-columns:1fr 1fr;gap:12px}
@media(max-width:540px){.cc-grid{grid-template-columns:1fr}}
.cc-full{grid-column:1/-1}
.cc-box{background:#16213e;border:1px solid #1f2d45;border-radius:8px;padding:12px}
.cc-lbl{font-size:.68rem;color:#8892a4;text-transform:uppercase;letter-spacing:.08em;margin-bottom:7px}
.radio-group{display:flex;gap:12px;flex-wrap:wrap}
.radio-label{display:flex;align-items:center;gap:5px;cursor:pointer;font-size:.85rem;color:#e0e0e0}
.radio-label input{accent-color:#e94560}
.cc-input{background:#0d1117;color:#e0e0e0;border:1px solid #30363d;border-radius:5px;padding:7px 10px;font-size:.9rem;width:100%;font-family:inherit}
.cc-input:focus{outline:none;border-color:#58a6ff}
.stat-block{font-family:'Courier New',monospace}
.stat-row{display:flex;align-items:center;gap:7px;padding:2px 0}
.stat-name{width:94px;color:#8892a4;text-transform:uppercase;letter-spacing:.04em;font-size:.68rem;white-space:nowrap}
.stat-bar{color:#3fb950;letter-spacing:1px;font-size:.82rem}
.stat-val{color:#e6edf3;font-weight:bold;font-size:.82rem;min-width:18px}
.stat-ph{color:#3d4d5e;font-size:.78rem;font-style:italic;padding:6px 0}
.cc-actions{display:flex;gap:10px;margin-top:14px;flex-wrap:wrap}
.btn-roll{background:#1f4a8a;color:#fff;border:none;border-radius:6px;padding:8px 18px;font-size:.87rem;cursor:pointer;font-weight:600;font-family:inherit}
.btn-roll:hover{background:#2a65b8}
.btn-start{background:#238636;color:#fff;border:none;border-radius:6px;padding:8px 20px;font-size:.87rem;cursor:pointer;font-weight:600;font-family:inherit}
.btn-start:hover:not(:disabled){background:#2ea043}
.btn-start:disabled{opacity:.4;cursor:not-allowed}
/* ── Context collapsible ── */
details{margin-top:14px;background:#161b22;border:1px solid #21262d;border-radius:7px;padding:10px 12px}
details[open]{padding-bottom:12px}
summary{cursor:pointer;font-size:.8rem;color:#8892a4;user-select:none;list-style:none;display:flex;align-items:center;gap:6px}
summary::-webkit-details-marker{display:none}
summary::before{content:'\\25B6';font-size:.58rem;color:#8892a4;transition:transform .2s;display:inline-block;margin-right:2px}
details[open]>summary::before{transform:rotate(90deg)}
.ctx-ta{width:100%;min-height:165px;background:#0d1117;color:#c9d1d9;border:1px solid #30363d;border-radius:5px;padding:9px;font-family:'Courier New',monospace;font-size:.75rem;line-height:1.5;resize:vertical;margin-top:9px;display:block}
.ctx-btns{display:flex;gap:8px;margin-top:8px;align-items:center;flex-wrap:wrap}
.btn-ctx{background:transparent;color:#8892a4;border:1px solid #30363d;border-radius:5px;padding:4px 11px;font-size:.74rem;cursor:pointer;font-family:inherit}
.btn-ctx:hover{border-color:#58a6ff;color:#58a6ff}
.ctx-st{font-size:.71rem;color:#3fb950}
/* ── Gameplay ── */
.cs-panel{background:#16213e;border:1px solid #1f2d45;border-radius:7px;padding:10px 12px;margin-bottom:10px}
.cs-panel summary{font-size:.8rem;font-weight:600;color:#8892a4}
.cs-inner{display:block;padding-top:6px;font-family:'Courier New',monospace}
.cs-stat-grid{display:grid;grid-template-columns:1fr 1fr;gap:2px 14px}
.save-toolbar{display:flex;gap:8px;align-items:center;margin-bottom:8px;flex-wrap:wrap}
.play-toolbar{display:flex;gap:6px;align-items:center;margin-bottom:8px;flex-wrap:nowrap}
.play-toolbar .toolbar-gap{flex:1;min-width:0}
.play-toolbar button{padding:5px 10px;font-size:.8rem;flex-shrink:0}
.play-toolbar select{flex-shrink:0;font-size:.8rem;padding:5px 7px}
.btn-qs{background:#1a2e0f;color:#3fb950;border:1px solid #3fb950;border-radius:5px;padding:5px 11px;font-size:.77rem;cursor:pointer;font-family:inherit}
.btn-qs:hover:not(:disabled){background:#3fb950;color:#0d1117}
.btn-qs:disabled{opacity:.4;cursor:not-allowed}
.btn-sv{background:#161b22;color:#8892a4;border:1px solid #30363d;border-radius:5px;padding:5px 11px;font-size:.77rem;cursor:pointer;font-family:inherit}
.btn-sv:hover{border-color:#58a6ff;color:#58a6ff}
.sv-st{font-size:.71rem;color:#3fb950;margin-left:2px}
.sv-form{display:flex;gap:8px;align-items:center;margin-bottom:8px;flex-wrap:wrap}
.sv-inp{background:#0d1117;color:#e0e0e0;border:1px solid #30363d;border-radius:5px;padding:6px 10px;font-size:.84rem;font-family:inherit;min-width:170px}
.btn-sv-x{background:transparent;color:#8892a4;border:1px solid #30363d;border-radius:5px;padding:5px 9px;font-size:.77rem;cursor:pointer;font-family:inherit}
.load-panel{background:#161b22;border:1px solid #21262d;border-radius:7px;padding:8px;margin-bottom:10px;max-height:200px;overflow-y:auto}
.load-item{display:flex;align-items:center;gap:8px;background:#0d1117;border:1px solid #21262d;border-radius:5px;padding:6px 10px;margin-bottom:4px}
.load-item-info{flex:1;cursor:pointer}
.load-item-info:hover .li-name{color:#58a6ff}
.li-name{font-size:.82rem;color:#e6edf3;font-weight:600;display:block}
.li-meta{font-size:.67rem;color:#8892a4}
.btn-del{background:transparent;border:1px solid #3d1a1a;color:#f85149;border-radius:4px;padding:2px 7px;font-size:.73rem;cursor:pointer}
.btn-del:hover{background:#f85149;color:#0d1117}
.load-empty{font-size:.8rem;color:#8892a4;padding:6px;text-align:center}
#chat-log{display:flex;flex-direction:column;gap:10px;min-height:220px;overflow-y:auto;overflow-x:hidden;margin-bottom:10px}
.msg{padding:10px 14px;border-radius:8px;word-break:break-word;word-wrap:break-word;overflow-wrap:break-word;line-height:1.55}
.msg.user{background:#0f3460;align-self:flex-end;max-width:80%;font-size:.9rem}
.msg.assistant{background:#0d1117;border:1px solid #1f2d45;align-self:stretch;font-family:'Courier New',monospace;font-size:.82rem;white-space:pre-wrap;word-wrap:break-word;overflow-wrap:break-word}
.msg.assistant h1,.msg.assistant h2,.msg.assistant h3{color:#58a6ff;margin:.5em 0 .25em;font-size:.95rem}
.msg.assistant p{margin:.35em 0}
.msg.assistant pre{background:#161b22;padding:8px 10px;border-radius:4px;overflow-x:auto;white-space:pre;margin:.4em 0;border:1px solid #30363d;font-family:'Courier New',monospace;font-size:.8rem;line-height:1.4}
.msg.assistant pre code{background:none;padding:0;font-size:inherit}
.msg.assistant code{font-family:'Courier New',monospace;background:#161b22;padding:1px 4px;border-radius:3px;font-size:.8rem}
.msg.assistant hr{border:none;border-top:1px solid #30363d;margin:.6em 0}
.msg.assistant ul,.msg.assistant ol{padding-left:1.4em;margin:.3em 0}
.msg.assistant li{margin:.1em 0}
.msg.assistant strong{color:#e6edf3}
.msg.assistant blockquote{border-left:3px solid #0f3460;margin:.3em 0;padding-left:.8em;color:#8892a4}
.msg.error{background:#3d0f1a;border:1px solid #e94560;font-size:.85rem;align-self:stretch}
/* ── Inventory / Level / Skills ── */
.cs-section{padding-top:6px;margin-top:6px;border-top:1px solid #21262d}
.cs-section-hdr{font-size:.67rem;color:#8892a4;text-transform:uppercase;letter-spacing:.08em;margin-bottom:4px}
.cs-equipped-slot{display:flex;align-items:center;gap:5px;padding:2px 0;font-size:.71rem}
.cs-slot-lbl{width:88px;color:#8892a4;flex-shrink:0;font-size:.66rem}
.cs-item-fx{color:#8892a4;font-size:.64rem;margin-left:3px;white-space:nowrap;flex-shrink:0}
.cs-bag-row{display:flex;align-items:center;gap:5px;padding:2px 0;font-size:.71rem}
.cs-bag-hdr{display:flex;justify-content:space-between;align-items:center;margin-bottom:3px}
.cs-bag-cap{font-size:.67rem;color:#8892a4}
.rc-common{color:#8b949e}.rc-uncommon{color:#3fb950}.rc-rare{color:#58a6ff}.rc-legendary{color:#d4a017}.rc-cursed{color:#f85149}
.btn-inv{background:transparent;border:1px solid #30363d;color:#8892a4;border-radius:3px;padding:1px 5px;font-size:.64rem;cursor:pointer;font-family:inherit;flex-shrink:0;line-height:1.4}
.btn-inv:hover{border-color:#58a6ff;color:#58a6ff}
.btn-inv.drop{border-color:#3d1a1a;color:#f85149}
.btn-inv.drop:hover{background:#f85149;color:#0d1117}
.cs-xp-row{display:flex;align-items:center;gap:6px;padding:3px 0;font-size:.71rem}
.cs-xp-track{background:#161b22;border-radius:2px;height:5px;flex:1;overflow:hidden}
.cs-xp-fill{height:100%;background:#d4a017;border-radius:2px;transition:width .3s}
.cs-skill-row{display:flex;align-items:center;gap:5px;padding:2px 0;font-size:.66rem;color:#8892a4}
.cs-skill-track{background:#161b22;border-radius:2px;height:3px;width:32px;overflow:hidden;flex-shrink:0}
.cs-skill-fill{height:100%;background:#1f6feb;border-radius:2px}
.msg.system{background:#1a1600;border:1px solid #3d2e00;align-self:stretch;font-size:.82rem;font-family:'Courier New',monospace;padding:8px 12px}
.msg.system p{margin:.3em 0}
.lvl-choices{display:flex;flex-wrap:wrap;gap:8px;margin:8px 0}
.lvl-choices label{cursor:pointer;color:#e0e0e0;font-size:.8rem}
.btn-lvl{background:#238636;color:#fff;border:none;border-radius:4px;padding:5px 12px;font-size:.8rem;cursor:pointer;font-family:inherit;margin-top:4px}
.btn-lvl:hover{background:#2ea043}
.combat-panel{background:#0d1117;border:1px solid #e94560;border-radius:7px;padding:10px 12px;margin-bottom:8px}
.combat-name{font-size:.88rem;font-weight:700;color:#e94560;margin-bottom:6px;font-family:'Courier New',monospace}
.combat-hp-row{display:flex;align-items:center;gap:8px;margin-bottom:8px}
.combat-hp-track{background:#1a0a0a;border-radius:2px;height:10px;flex:1;overflow:hidden}
.combat-hp-fill{height:100%;background:#e94560;border-radius:2px;transition:width .3s}
.combat-hp-label{font-size:.71rem;color:#8892a4;white-space:nowrap;font-family:'Courier New',monospace}
.combat-actions{display:flex;gap:6px;flex-wrap:wrap}
.btn-combat{background:#1a0a0a;border:1px solid #e94560;color:#e94560;border-radius:5px;padding:5px 11px;font-size:.78rem;cursor:pointer;font-family:inherit}
.btn-combat:hover:not(:disabled){background:#e94560;color:#0d1117}
.btn-combat:disabled{opacity:.4;cursor:not-allowed}
.btn-flee{border-color:#d29922!important;color:#d29922!important}
.btn-flee:hover:not(:disabled){background:#d29922!important;color:#0d1117!important}
.health-bar-tb{display:flex;align-items:center;gap:4px;font-size:.74rem;white-space:nowrap;flex-shrink:0}
.hb-track{background:#1a0a0a;border-radius:2px;height:8px;width:54px;overflow:hidden}
.hb-fill{height:100%;background:#3fb950;border-radius:2px;transition:width .3s}
.hb-label{color:#c9d1d9;font-family:'Courier New',monospace}
.game-over-block{background:#0d1117;border:2px solid #e94560;border-radius:8px;padding:24px 20px;text-align:center;align-self:stretch;display:flex;flex-direction:column;align-items:center;gap:14px;margin-top:12px}
.go-title{font-size:1.6rem;color:#e94560;font-weight:900;letter-spacing:.08em;font-family:'Courier New',monospace}
.go-stats{font-size:.84rem;color:#8892a4;text-align:center;line-height:2;font-family:'Courier New',monospace}
.go-btns{display:flex;gap:10px;flex-wrap:wrap;justify-content:center}
.input-row{display:flex;gap:8px;align-items:center;flex-wrap:wrap;margin-bottom:8px}
select{background:#16213e;color:#e0e0e0;border:1px solid #0f3460;border-radius:6px;padding:8px;font-size:.85rem;font-family:inherit}
#prompt{background:#16213e;color:#e0e0e0;border:1px solid #0f3460;border-radius:6px;padding:10px;font-size:.9rem;width:100%;resize:vertical;min-height:60px;font-family:inherit}
#prompt:disabled{opacity:.45;cursor:not-allowed}
.btn-send{background:#e94560;color:#fff;border:none;border-radius:6px;padding:10px 24px;font-size:.9rem;cursor:pointer;font-weight:600;font-family:inherit}
.btn-send:disabled{opacity:.5;cursor:not-allowed}
.btn-reset{background:transparent;color:#8892a4;border:1px solid #30363d;border-radius:6px;padding:10px 14px;font-size:.85rem;cursor:pointer;font-family:inherit}
.btn-reset:hover{border-color:#e94560;color:#e94560}
/* ── GPU gauge (unchanged) ── */
.gpu-gauges{display:flex;gap:8px;align-items:center}
.gpu-card{background:#0d1117;border:1px solid #21262d;border-radius:5px;padding:4px 9px;font-size:.65rem;display:flex;flex-direction:column;gap:3px;min-width:166px;transition:border-color .3s}
.gpu-card.active{border-color:#3fb950}
.gpu-card-label{color:#6e7681;letter-spacing:.04em;white-space:nowrap}
.gpu-bar-row{display:flex;align-items:center;gap:5px}
.gpu-bar-track{background:#161b22;border-radius:2px;height:5px;width:60px;overflow:hidden;flex-shrink:0}
.gpu-bar-fill{height:100%;border-radius:2px;background:#1f6feb;transition:width .5s,background .3s}
.gpu-bar-fill.active{background:#3fb950}
.gpu-card-stats{color:#8892a4;white-space:nowrap}
</style>
</head>
<body>
<header id="site-hdr">
  <h1>VERNEX</h1>
  <span class="hdr-sub">&#127918; TEXT ADVENTURE</span>
  <span class="node-info" id="node-info">{{ version }}</span>
  <div class="hdr-links">
    <div id="gpu-gauges" class="gpu-gauges"></div>
    <span id="hdr-user" style="font-size:.72rem;color:#8892a4"></span>
    <a class="hdr-link" href="/ui">&#8592; Chat</a>
    <a class="hdr-link" href="/auth/logout">logout</a>
  </div>
</header>

<!-- ══ GENRE SELECTION ══ -->
<main id="view-select" class="view-main">
  <p class="sel-title">Choose Your Adventure</p>
  <p class="sel-sub">Select a genre to begin</p>
  <p id="sel-user-line" style="text-align:center;color:#8892a4;font-size:.78rem;margin-top:4px;margin-bottom:0"></p>
  <div class="genre-grid">
    <div class="genre-card gc-fantasy" data-genre="fantasy">
      <span class="gc-icon">&#129497;</span>
      <span class="gc-name">Fantasy</span>
    </div>
    <div class="genre-card gc-scifi" data-genre="scifi">
      <span class="gc-icon">&#128640;</span>
      <span class="gc-name">Science Fiction</span>
    </div>
    <div class="genre-card gc-action" data-genre="action">
      <span class="gc-icon">&#9876;&#65039;</span>
      <span class="gc-name">Action / Adventure</span>
    </div>
    <div class="genre-card gc-comedy" data-genre="comedy">
      <span class="gc-icon">&#127917;</span>
      <span class="gc-name">Comedic Drama</span>
    </div>
  </div>
  <!-- ══ SAVED GAMES ══ -->
  <div class="sel-saves-section">
    <p class="sel-saves-hdr">&#128194; Continue a Saved Game</p>
    <div id="sel-saves-list"><div class="sel-empty">Loading saves&#8230;</div></div>
    <p class="sel-saves-hdr" style="margin-top:20px">&#9889; Autosaves</p>
    <div id="sel-autosaves-list"><div class="sel-empty">Loading&#8230;</div></div>
  </div>
</main>

<!-- ══ CHARACTER CREATOR ══ -->
<main id="view-create" class="view-main" style="display:none">
  <div class="cc-hdr">
    <button class="cc-back" id="cc-back-btn">&#8592; Back</button>
    <span class="cc-title" id="cc-genre-lbl">Create Your Character</span>
  </div>
  <div class="cc-grid">
    <div class="cc-box">
      <div class="cc-lbl">Gender</div>
      <div class="radio-group">
        <label class="radio-label"><input type="radio" name="gender" value="male" checked> Male</label>
        <label class="radio-label"><input type="radio" name="gender" value="female"> Female</label>
      </div>
    </div>
    <div class="cc-box">
      <div class="cc-lbl">Name</div>
      <input type="text" id="char-name" class="cc-input" placeholder="Your character&#8217;s name&#8230;">
    </div>
    <div class="cc-box cc-full">
      <div class="cc-lbl" id="subtype-lbl">Class</div>
      <div class="radio-group" id="subtype-opts"></div>
    </div>
    <div class="cc-box cc-full">
      <div class="cc-lbl">Stats</div>
      <div id="stat-block"><p class="stat-ph">Click &#8220;Roll Character&#8221; to generate stats.</p></div>
    </div>
  </div>
  <div class="cc-actions">
    <button class="btn-roll" id="roll-btn">&#127922; Roll Character</button>
    <button class="btn-start" id="start-game-btn" disabled>&#9654; Start Game</button>
  </div>
</main>

<!-- ══ GAMEPLAY ══ -->
<main id="view-play" class="view-main" style="display:none">
  <div id="chat-log"></div>
  <div id="sv-form" class="sv-form" style="display:none">
    <input type="text" id="sv-name-inp" class="sv-inp" placeholder="Save name&#8230;">
    <button class="btn-sv" id="sv-confirm-btn">Save</button>
    <button class="btn-sv-x" id="sv-cancel-btn">Cancel</button>
  </div>
  <div id="load-panel" class="load-panel" style="display:none">
    <div id="load-list"></div>
  </div>
  <div class="play-toolbar">
    <button class="btn-send" id="submit-btn" disabled>Send</button>
    <button class="btn-reset" id="reset-game-btn">&#128260; Reset Game</button>
    <button class="btn-qs" id="qs-btn" disabled>&#9889; Quicksave</button>
    <button class="btn-sv" id="sv-save-btn">&#128190; Save</button>
    <button class="btn-sv" id="sv-load-btn">&#128194; Load</button>
    <span id="sv-st" class="sv-st"></span>
    <div class="toolbar-gap"></div>
    <div id="health-bar-tb" class="health-bar-tb" style="display:none">&#10084;&#65039;&nbsp;<div class="hb-track"><div class="hb-fill" id="hb-fill"></div></div>&nbsp;<span class="hb-label" id="hb-label">&#8212;</span></div>
    <select id="model-select"><option value="mistral">mistral</option></select>
  </div>
  <textarea id="prompt" placeholder="What do you do?" disabled></textarea>
  <details class="cs-panel" style="margin-top:8px">
    <summary>&#128203; Character Sheet &#8212; <span id="cs-name"></span></summary>
    <div class="cs-inner" id="cs-inner"></div>
  </details>
  <details style="margin-top:4px">
    <summary>&#9881; Game Context</summary>
    <textarea id="game-context" class="ctx-ta" spellcheck="false"></textarea>
    <div class="ctx-btns">
      <button class="btn-ctx" id="ctx-save-btn">&#128190; Save as Default</button>
      <button class="btn-ctx" id="ctx-reset-btn">&#8635; Reset to Default</button>
      <span id="ctx-st" class="ctx-st"></span>
    </div>
  </details>
</main>

<script src="/static/game.js"></script>
</body>
</html>"""


@app.route("/ui")
def ui():
    return render_template_string(_CHAT_HTML, version=_get_daemon_version())


@app.route("/game")
def game():
    return render_template_string(_GAME_HTML, version=_get_daemon_version())


@app.route("/api/game/contexts")
def api_game_contexts():
    return jsonify({
        "levelSystem": _GAME_LEVEL_SYSTEM,
        "openings": _GAME_OPENINGS,
    })


@app.route("/api/models")
def api_models():
    try:
        r = requests.get(f"{OLLAMA_URL}/api/tags", timeout=3)
        models = [m["name"] for m in r.json().get("models", [])]
        return jsonify({"models": models})
    except Exception as e:
        return jsonify({"models": ["mistral", "llama3.1"], "error": str(e)})


@app.route("/api/gpu")
def api_gpu():
    try:
        result = subprocess.run(
            [
                "nvidia-smi",
                "--query-gpu=name,memory.used,memory.total,utilization.gpu,temperature.gpu",
                "--format=csv,noheader,nounits",
            ],
            capture_output=True, text=True, timeout=5,
        )
        if result.returncode != 0:
            return jsonify({"error": "unavailable"}), 503
        parts = [p.strip() for p in result.stdout.strip().split(",")]
        return jsonify({
            "node_id": "VRX-54b89a1684e21ae4",
            "gpu_name": parts[0],
            "vram_used_mb": int(parts[1]),
            "vram_total_mb": int(parts[2]),
            "gpu_util_pct": int(parts[3]),
            "temp_c": int(parts[4]),
        })
    except Exception:
        return jsonify({"error": "unavailable"}), 503


@app.route("/api/game/chat", methods=["POST"])
def api_game_chat():
    try:
        body = request.get_json(force=True) or {}
        messages = body.get("messages", [])
        model = body.get("model", "mistral")
        if not messages:
            return jsonify({"error": "messages required"}), 400

        last = messages[-1] if messages else {}
        print(f"[game/chat] model={model!r} turns={len(messages)} last_role={last.get('role','?')!r}")

        r = requests.post(
            f"{OLLAMA_URL}/api/chat",
            json={"model": model, "messages": messages, "stream": False},
            timeout=180,
        )

        try:
            d = r.json()
        except Exception:
            return jsonify({
                "error": f"Ollama non-JSON response (HTTP {r.status_code}): {r.text[:300]}"
            }), 502

        if r.status_code != 200:
            err = d.get("error") or f"Ollama returned HTTP {r.status_code}"
            print(f"[game/chat] ollama error: {err!r}")
            return jsonify({"error": err}), 502

        content = d.get("message", {}).get("content", "")
        return jsonify({"response": content, "model": model})

    except Exception as e:
        import traceback
        print(f"[game/chat] unhandled exception:\n{traceback.format_exc()}")
        return jsonify({"error": str(e)}), 500


# ── Game prompt persistence ───────────────────────────────────

_CONFIG_BASE = os.path.join(os.path.expanduser("~"), "vernex", "config")


def _saves_dir(user_id: str = "guest") -> str:
    return os.path.join(_CONFIG_BASE, "game_saves", user_id)


def _prompts_file(user_id: str = "guest") -> str:
    return os.path.join(_CONFIG_BASE, "game_prompts", f"{user_id}.json")


def _load_prompts(user_id: str = "guest") -> dict:
    try:
        with open(_prompts_file(user_id)) as f:
            return json.load(f)
    except Exception:
        return {}


def _save_prompts(data: dict, user_id: str = "guest"):
    pf = _prompts_file(user_id)
    os.makedirs(os.path.dirname(pf), exist_ok=True)
    with open(pf, "w") as f:
        json.dump(data, f, indent=2)


@app.route("/api/game/prompts/<genre>")
def api_game_prompts_get(genre):
    uid = _user_id(_get_current_user())
    prompts = _load_prompts(uid)
    return jsonify({"genre": genre, "prompt": prompts.get(genre)})


@app.route("/api/game/prompts", methods=["POST"])
def api_game_prompts_save():
    uid = _user_id(_get_current_user())
    body = request.get_json(force=True) or {}
    genre = body.get("genre", "").strip()
    prompt = body.get("prompt")
    if not genre:
        return jsonify({"error": "genre required"}), 400
    prompts = _load_prompts(uid)
    if prompt is None:
        prompts.pop(genre, None)
    else:
        prompts[genre] = prompt
    _save_prompts(prompts, uid)
    return jsonify({"ok": True})


# ── Game save / load ──────────────────────────────────────────

import uuid as _uuid
from datetime import datetime as _dt

_ALL_SLOTS = ['head', 'body', 'hands', 'feet', 'accessory1', 'accessory2', 'weapon']

_ITEM_WORDS = {
    'fantasy': {
        'adjectives': ['Ancient', 'Enchanted', 'Ethereal', 'Obsidian', 'Gilded', 'Spectral',
                       'Verdant', 'Infernal', 'Celestial', 'Runed', 'Frostbound', 'Blazing',
                       'Shadowed', 'Sacred', 'Moonlit'],
        'nouns': {
            'head':       ['Helm', 'Crown', 'Circlet', 'Hood', 'Visage'],
            'body':       ['Cloak', 'Robe', 'Armor', 'Mantle', 'Vestment'],
            'hands':      ['Gauntlets', 'Gloves', 'Bracers', 'Wraps'],
            'feet':       ['Boots', 'Greaves', 'Sandals', 'Treads'],
            'accessory1': ['Ring', 'Band', 'Seal', 'Signet'],
            'accessory2': ['Amulet', 'Pendant', 'Talisman', 'Charm'],
            'weapon':     ['Sword', 'Staff', 'Dagger', 'Bow', 'Axe', 'Wand', 'Spear'],
        },
        'phrases': ['the Forgotten Age', 'Shadow Wraiths', 'the Fallen King', 'Ember Spirits',
                    'the Arcane Rift', 'Lost Souls', 'Dragon Fire', 'the Elder Grove',
                    'Starfall', 'the Moonless Night'],
    },
    'scifi': {
        'adjectives': ['Quantum', 'Plasma', 'Neural', 'Nano', 'Photon', 'Kinetic', 'Adaptive',
                       'Stealth', 'Reinforced', 'Tactical', 'Bio-Synth', 'Hyper', 'Cryo'],
        'nouns': {
            'head':       ['Visor', 'Helm', 'Interface', 'Scanner', 'Headset'],
            'body':       ['Exosuit', 'Armor', 'Vest', 'Harness', 'Carapace'],
            'hands':      ['Gauntlets', 'Gloves', 'Manipulators', 'Interface'],
            'feet':       ['Boots', 'Thrusters', 'Treads', 'Stabilizers'],
            'accessory1': ['Implant', 'Chip', 'Module', 'Core'],
            'accessory2': ['Comms', 'Beacon', 'Relay', 'Node'],
            'weapon':     ['Blaster', 'Rifle', 'Blade', 'Cannon', 'Emitter', 'Disruptor'],
        },
        'phrases': ['Deep Space', 'Neural Override', 'Quantum Flux', 'the Void Protocol',
                    'AI Singularity', 'Zero-Point', 'Dark Matter', 'the Cryo-Wars',
                    'Nano-Swarm', 'the Galactic Accord'],
    },
    'action': {
        'adjectives': ['Battle-Worn', 'Gilded', 'Frontier', 'Imperial', 'Veteran', 'Ancient',
                       'Legendary', 'Rugged', 'Ornate', 'Stolen', 'Polished', 'Scarred'],
        'nouns': {
            'head':       ['Helmet', 'Hat', 'Cap', 'Helm', 'Headband'],
            'body':       ['Coat', 'Armor', 'Uniform', 'Jacket', 'Vest'],
            'hands':      ['Gloves', 'Gauntlets', 'Holster', 'Bracers'],
            'feet':       ['Boots', 'Spurs', 'Sandals', 'Greaves'],
            'accessory1': ['Badge', 'Medal', 'Token', 'Coin'],
            'accessory2': ['Compass', 'Watch', 'Map', 'Sextant'],
            'weapon':     ['Sword', 'Pistol', 'Rifle', 'Knife', 'Spear', 'Bow', 'Lance'],
        },
        'phrases': ['the Last Campaign', 'the Desert Sun', 'Fallen Generals', 'the Silk Road',
                    'Forgotten Empires', 'the Iron Age', 'Desert Raiders', 'Distant Shores',
                    'the Crimson War', 'Lost Legions'],
    },
    'comedy': {
        'adjectives': ['Absurd', 'Suspicious', 'Magnificent', 'Comical', 'Embarrassing',
                       'Inexplicable', 'Pompous', 'Glorious', 'Awkward', 'Ludicrous',
                       'Bewildering', 'Ostentatious', 'Preposterous'],
        'nouns': {
            'head':       ['Hat', 'Wig', 'Crown', 'Cap', 'Beret'],
            'body':       ['Jacket', 'Costume', 'Blazer', 'Uniform', 'Onesie'],
            'hands':      ['Gloves', 'Mittens', 'Gauntlets', 'Oven Mitts'],
            'feet':       ['Shoes', 'Slippers', 'Loafers', 'Flip-Flops'],
            'accessory1': ['Name Tag', 'Badge', 'Sticker', 'Lanyard'],
            'accessory2': ['Charm', 'Trinket', 'Knick-Knack', 'Bobble'],
            'weapon':     ['Briefcase', 'Mop', 'Stapler', 'Umbrella', 'Ruler', 'Clipboard'],
        },
        'phrases': ['Questionable Decisions', 'Cosmic Embarrassment', 'Unfortunate Circumstances',
                    'Great Misunderstandings', 'Workplace Chaos', 'Unexpected Promotion',
                    'Hilarious Consequences', 'Suspicious Origins'],
    },
}

_GENRE_STATS = {
    'fantasy': ['Strength', 'Stamina', 'Charisma', 'Magic', 'Agility', 'Luck'],
    'scifi':   ['Intelligence', 'Tech Skill', 'Agility', 'Charisma', 'Endurance', 'Luck'],
    'action':  ['Strength', 'Stamina', 'Charisma', 'Cunning', 'Agility', 'Luck'],
    'comedy':  ['Charisma', 'Wit', 'Luck', 'Clumsiness', 'Charm', 'Stubbornness'],
}


def _generate_item(genre: str, slot: str, rarity_override: str = None) -> dict:
    words = _ITEM_WORDS.get(genre, _ITEM_WORDS['fantasy'])
    stats = _GENRE_STATS.get(genre, _GENRE_STATS['fantasy'])

    if rarity_override:
        rarity = rarity_override
    else:
        roll = random.random() * 100
        if roll < 60:    rarity = 'common'
        elif roll < 85:  rarity = 'uncommon'
        elif roll < 95:  rarity = 'rare'
        elif roll < 99:  rarity = 'legendary'
        else:            rarity = 'cursed'

    primary = random.choice(stats)
    others = [s for s in stats if s != primary]
    secondary = random.choice(others) if others else None
    third_cands = [s for s in stats if s not in [primary, secondary]] if secondary else []

    stat_effects: dict = {}
    curse_effects: dict = {}

    if rarity == 'common':
        stat_effects[primary] = 1
    elif rarity == 'uncommon':
        stat_effects[primary] = 2
        if secondary:
            stat_effects[secondary] = -1
    elif rarity == 'rare':
        stat_effects[primary] = 3
        if secondary:
            stat_effects[secondary] = -1
    elif rarity == 'legendary':
        stat_effects[primary] = 4
        if secondary:
            stat_effects[secondary] = 2
        if third_cands:
            stat_effects[random.choice(third_cands)] = -1
    elif rarity == 'cursed':
        stat_effects[primary] = 2
        if secondary:
            curse_effects[secondary] = -3

    slot_nouns = words['nouns'].get(slot, words['nouns'].get('weapon', ['Item']))
    name = (
        random.choice(words['adjectives']) + ' '
        + random.choice(slot_nouns) + ' of '
        + random.choice(words['phrases'])
    )

    descs = {
        'common':    f"A serviceable {slot_nouns[0].lower()} with modest enhancement.",
        'uncommon':  f"A well-crafted {slot_nouns[0].lower()} imbued with minor power.",
        'rare':      f"A remarkable {slot_nouns[0].lower()} crackling with potent energy.",
        'legendary': f"A legendary {slot_nouns[0].lower()} whose power reshapes fate itself.",
        'cursed':    f"A {slot_nouns[0].lower()} that radiates an unsettling but enticing aura.",
    }

    return {
        'id': str(_uuid.uuid4())[:8],
        'name': name,
        'slot': slot,
        'rarity': rarity,
        'stat_effects': stat_effects,
        'curse_effects': curse_effects,
        'equipped': False,
        'cursed': rarity == 'cursed',
        'cursed_revealed': False,
        'description': descs.get(rarity, descs['common']),
    }


def _get_save_record(save_id: str, user_id: str):
    path = _save_path(save_id, user_id)
    if not os.path.exists(path):
        return None
    with open(path) as f:
        return json.load(f)


def _write_save_record(save_id: str, user_id: str, record: dict):
    record['save_id'] = save_id
    record['updated_at'] = _dt.utcnow().isoformat()
    with open(_save_path(save_id, user_id), 'w') as f:
        json.dump(record, f, indent=2)


# ── Combat enemy tables ────────────────────────────────────────────────────────

_ENEMY_TABLES: dict = {
    'fantasy': {
        (1, 2): [
            {'name': 'Goblin',         'type': 'normal', 'bh': 15, 'bs': 5,  'bd': 2,  'bm': 1,  'sp': 6,  'ability': None},
            {'name': 'Dark Elf Scout', 'type': 'normal', 'bh': 18, 'bs': 6,  'bd': 3,  'bm': 2,  'sp': 8,  'ability': 'call_reinforcements'},
            {'name': 'Forest Wraith',  'type': 'normal', 'bh': 14, 'bs': 7,  'bd': 1,  'bm': 4,  'sp': 7,  'ability': 'mind_control'},
        ],
        (3, 4): [
            {'name': 'Orc Warlord',   'type': 'normal', 'bh': 25, 'bs': 9,  'bd': 5,  'bm': 2,  'sp': 5,  'ability': 'call_reinforcements'},
            {'name': 'Shadow Mage',   'type': 'normal', 'bh': 20, 'bs': 8,  'bd': 3,  'bm': 6,  'sp': 7,  'ability': 'mind_control'},
            {'name': 'Cursed Knight', 'type': 'normal', 'bh': 28, 'bs': 10, 'bd': 7,  'bm': 3,  'sp': 6,  'ability': None},
        ],
        (5, 6): [
            {'name': 'Ancient Dragon', 'type': 'boss', 'bh': 40, 'bs': 14, 'bd': 10, 'bm': 8,  'sp': 7,  'ability': 'fire_breath',        'cd': 3},
            {'name': 'The Lich King',  'type': 'boss', 'bh': 35, 'bs': 12, 'bd': 8,  'bm': 12, 'sp': 6,  'ability': 'mind_control',       'cd': 2},
            {'name': 'Demon Lord',     'type': 'boss', 'bh': 45, 'bs': 16, 'bd': 12, 'bm': 10, 'sp': 8,  'ability': 'call_reinforcements', 'cd': 4},
        ],
    },
    'scifi': {
        (1, 2): [
            {'name': 'Rogue Drone',         'type': 'normal', 'bh': 12, 'bs': 4,  'bd': 3,  'bm': 2,  'sp': 9,  'ability': 'power_malfunction'},
            {'name': 'Infected Terminal',   'type': 'normal', 'bh': 16, 'bs': 5,  'bd': 4,  'bm': 3,  'sp': 5,  'ability': None},
            {'name': 'Minor Paradox Glitch','type': 'normal', 'bh': 14, 'bs': 6,  'bd': 2,  'bm': 5,  'sp': 7,  'ability': 'mind_control'},
        ],
        (3, 4): [
            {'name': 'Alien Warlord', 'type': 'normal', 'bh': 26, 'bs': 9,  'bd': 6,  'bm': 4,  'sp': 7,  'ability': 'call_reinforcements'},
            {'name': 'Rogue Android', 'type': 'normal', 'bh': 22, 'bs': 10, 'bd': 7,  'bm': 3,  'sp': 8,  'ability': 'power_malfunction'},
            {'name': 'Time Anomaly',  'type': 'normal', 'bh': 20, 'bs': 8,  'bd': 4,  'bm': 8,  'sp': 9,  'ability': 'mind_control'},
        ],
        (5, 6): [
            {'name': 'Rogue AI Overlord', 'type': 'boss', 'bh': 42, 'bs': 15, 'bd': 12, 'bm': 10, 'sp': 7,  'ability': 'power_malfunction',   'cd': 3},
            {'name': 'Alien Hive Mind',   'type': 'boss', 'bh': 38, 'bs': 13, 'bd': 9,  'bm': 8,  'sp': 8,  'ability': 'call_reinforcements',  'cd': 2},
            {'name': 'Paradox Entity',    'type': 'boss', 'bh': 36, 'bs': 11, 'bd': 7,  'bm': 14, 'sp': 10, 'ability': 'mind_control',         'cd': 2},
        ],
    },
    'action': {
        (1, 2): [
            {'name': 'Bandit',        'type': 'normal', 'bh': 14, 'bs': 5,  'bd': 2,  'bm': 0, 'sp': 7,  'ability': None},
            {'name': 'Rival Soldier', 'type': 'normal', 'bh': 17, 'bs': 6,  'bd': 4,  'bm': 0, 'sp': 6,  'ability': 'call_reinforcements'},
            {'name': 'Street Thug',   'type': 'normal', 'bh': 12, 'bs': 7,  'bd': 1,  'bm': 0, 'sp': 8,  'ability': None},
        ],
        (3, 4): [
            {'name': 'Assassin',         'type': 'normal', 'bh': 20, 'bs': 11, 'bd': 4,  'bm': 0, 'sp': 10, 'ability': None},
            {'name': 'Rival General',    'type': 'normal', 'bh': 26, 'bs': 9,  'bd': 7,  'bm': 0, 'sp': 6,  'ability': 'call_reinforcements'},
            {'name': 'Corrupt Official', 'type': 'normal', 'bh': 18, 'bs': 7,  'bd': 5,  'bm': 0, 'sp': 5,  'ability': 'rumor_spread'},
        ],
        (5, 6): [
            {'name': 'Warlord',             'type': 'boss', 'bh': 44, 'bs': 15, 'bd': 11, 'bm': 0, 'sp': 7,  'ability': 'call_reinforcements', 'cd': 3},
            {'name': "Emperor's Champion",  'type': 'boss', 'bh': 40, 'bs': 14, 'bd': 13, 'bm': 0, 'sp': 8,  'ability': None},
            {'name': 'Crime Lord',          'type': 'boss', 'bh': 38, 'bs': 12, 'bd': 9,  'bm': 0, 'sp': 9,  'ability': 'rumor_spread',        'cd': 4},
        ],
    },
    'comedy': {
        (1, 2): [
            {'name': 'Angry Coworker',   'type': 'normal', 'bh': 10, 'bs': 4,  'bd': 1,  'bm': 0, 'sp': 5, 'ability': 'rumor_spread'},
            {'name': 'Town Busybody',    'type': 'normal', 'bh': 12, 'bs': 3,  'bd': 2,  'bm': 0, 'sp': 6, 'ability': 'rumor_spread'},
            {'name': 'Bumbling Sidekick','type': 'normal', 'bh': 8,  'bs': 5,  'bd': 1,  'bm': 0, 'sp': 7, 'ability': 'power_malfunction'},
        ],
        (3, 4): [
            {'name': 'Micromanaging Boss', 'type': 'normal', 'bh': 20, 'bs': 7,  'bd': 4,  'bm': 0, 'sp': 4, 'ability': 'rumor_spread'},
            {'name': 'Town Council',       'type': 'normal', 'bh': 24, 'bs': 6,  'bd': 5,  'bm': 0, 'sp': 3, 'ability': 'call_reinforcements'},
            {'name': 'Rival Superhero',    'type': 'normal', 'bh': 22, 'bs': 9,  'bd': 3,  'bm': 0, 'sp': 9, 'ability': 'power_malfunction'},
        ],
        (5, 6): [
            {'name': 'The CEO',               'type': 'boss', 'bh': 36, 'bs': 12, 'bd': 10, 'bm': 0, 'sp': 5,  'ability': 'rumor_spread',        'cd': 3},
            {'name': 'Mayor of Chaos',        'type': 'boss', 'bh': 32, 'bs': 10, 'bd': 8,  'bm': 0, 'sp': 6,  'ability': 'call_reinforcements', 'cd': 2},
            {'name': 'Arch-Nemesis Superhero','type': 'boss', 'bh': 40, 'bs': 14, 'bd': 6,  'bm': 0, 'sp': 10, 'ability': 'power_malfunction',   'cd': 3},
        ],
    },
}

_ATTACK_STATS = {
    'fantasy': {'physical': 'Strength',  'magic': 'Magic',        'skill': 'Strength'},
    'scifi':   {'physical': 'Tech Skill','magic': 'Intelligence',  'skill': 'Tech Skill'},
    'action':  {'physical': 'Strength',  'magic': 'Cunning',       'skill': 'Cunning'},
    'comedy':  {'physical': 'Charisma',  'magic': 'Wit',           'skill': 'Charm'},
}
_STAMINA_STAT = {
    'fantasy': 'Stamina', 'scifi': 'Endurance', 'action': 'Stamina', 'comedy': 'Stubbornness',
}


def _generate_enemy(genre: str, level: int, enemy_name: str = None) -> dict:
    table = _ENEMY_TABLES.get(genre, _ENEMY_TABLES['fantasy'])
    tier_key = None
    for k in table:
        if k[0] <= level <= k[1]:
            tier_key = k
            break
    if not tier_key:
        tier_key = max(table.keys())
    tier = table[tier_key]
    template = None
    if enemy_name:
        en = enemy_name.lower()
        for t in table.values():
            template = next((e for e in t if e['name'].lower() in en or en in e['name'].lower()), None)
            if template:
                break
    if not template:
        template = random.choice(tier)
    is_boss = template.get('type') == 'boss'
    m = 2.0 if is_boss else 1.0
    health = int((template['bh'] + level * 8) * m)
    return {
        'id': _uuid.uuid4().hex[:8],
        'name': template['name'],
        'type': template.get('type', 'normal'),
        'health': health,
        'max_health': health,
        'strength': int((template['bs'] + level * 2) * m),
        'defense': int((template['bd'] + level * 1) * m),
        'magic_resist': int((template['bm'] + level * 1) * m),
        'speed': template['sp'],
        'special_ability': template.get('ability'),
        'special_cooldown': template.get('cd', 3),
        'special_counter': 0,
        'loot_tier': 'legendary' if is_boss else ('rare' if level >= 4 else ('uncommon' if level >= 2 else 'common')),
        'defeated': False,
    }


def _eff_stat(stats: dict, equipped: dict, stat_name: str) -> int:
    base = stats.get(stat_name, 5)
    for item in equipped.values():
        if item:
            base += item.get('stat_effects', {}).get(stat_name, 0)
    return max(1, base)


def _save_path(save_id: str, user_id: str = "guest") -> str:
    d = _saves_dir(user_id)
    os.makedirs(d, exist_ok=True)
    return os.path.join(d, f"{save_id}.json")


@app.route("/api/game/saves")
def api_game_saves_list():
    uid = _user_id(_get_current_user())
    d = _saves_dir(uid)
    os.makedirs(d, exist_ok=True)
    saves = []
    for fname in sorted(os.listdir(d), key=lambda f: os.path.getmtime(os.path.join(d, f)), reverse=True):
        if not fname.endswith(".json"):
            continue
        try:
            with open(os.path.join(d, fname)) as f:
                s = json.load(f)
            saves.append({
                "save_id": s.get("save_id", fname[:-5]),
                "save_name": s.get("save_name", ""),
                "genre": s.get("genre", ""),
                "char_name": s.get("char_name", ""),
                "subtype": s.get("subtype", ""),
                "autosave": s.get("autosave", False),
                "updated_at": s.get("updated_at", ""),
            })
        except Exception:
            pass
    return jsonify(saves)


@app.route("/api/game/saves", methods=["POST"])
def api_game_saves_create():
    uid = _user_id(_get_current_user())
    body = request.get_json(force=True) or {}
    save_id = body.get("save_id") or str(_uuid.uuid4())[:8]
    now = _dt.utcnow().isoformat()
    record = dict(body)
    record["save_id"] = save_id
    record.setdefault("created_at", now)
    record["updated_at"] = now
    with open(_save_path(save_id, uid), "w") as f:
        json.dump(record, f, indent=2)
    return jsonify({"save_id": save_id})


@app.route("/api/game/saves/<save_id>")
def api_game_saves_get(save_id):
    uid = _user_id(_get_current_user())
    path = _save_path(save_id, uid)
    if not os.path.exists(path):
        return jsonify({"error": "not found"}), 404
    with open(path) as f:
        return jsonify(json.load(f))


@app.route("/api/game/saves/<save_id>", methods=["PUT"])
def api_game_saves_update(save_id):
    uid = _user_id(_get_current_user())
    body = request.get_json(force=True) or {}
    now = _dt.utcnow().isoformat()
    path = _save_path(save_id, uid)
    existing = {}
    if os.path.exists(path):
        with open(path) as f:
            existing = json.load(f)
    existing.update(body)
    existing["save_id"] = save_id
    existing["updated_at"] = now
    existing.setdefault("created_at", now)
    with open(path, "w") as f:
        json.dump(existing, f, indent=2)
    return jsonify({"save_id": save_id})


@app.route("/api/game/saves/<save_id>", methods=["DELETE"])
def api_game_saves_delete(save_id):
    uid = _user_id(_get_current_user())
    path = _save_path(save_id, uid)
    if os.path.exists(path):
        os.remove(path)
    return jsonify({"ok": True})


@app.route("/api/game/item/generate", methods=["POST"])
def api_game_item_generate():
    body = request.get_json(force=True) or {}
    genre = body.get("genre", "fantasy")
    slot = body.get("slot")
    rarity_override = body.get("rarity_override")
    boss = body.get("boss", False)
    if boss and not rarity_override:
        rarity_override = random.choice(["rare", "rare", "legendary"])
    if slot not in _ALL_SLOTS:
        slot = random.choice(_ALL_SLOTS)
    return jsonify(_generate_item(genre, slot, rarity_override))


@app.route("/api/game/saves/<save_id>/inventory")
def api_inventory_get(save_id):
    uid = _user_id(_get_current_user())
    s = _get_save_record(save_id, uid)
    if s is None:
        return jsonify({"error": "not found"}), 404
    return jsonify({
        "equipped": s.get("equipped", {}),
        "bag": s.get("bag", []),
        "bag_capacity": s.get("bag_capacity", 5),
    })


@app.route("/api/game/saves/<save_id>/inventory/equip", methods=["POST"])
def api_inventory_equip(save_id):
    uid = _user_id(_get_current_user())
    body = request.get_json(force=True) or {}
    item_id = body.get("item_id")
    slot = body.get("slot")
    s = _get_save_record(save_id, uid)
    if s is None:
        return jsonify({"error": "not found"}), 404
    equipped = s.get("equipped", {})
    bag = s.get("bag", [])
    item = next((i for i in bag if i.get("id") == item_id), None)
    if not item:
        return jsonify({"error": "item not in bag"}), 400
    if item.get("slot") != slot:
        return jsonify({"error": "slot mismatch"}), 400
    bag = [i for i in bag if i.get("id") != item_id]
    displaced = equipped.get(slot)
    if displaced:
        displaced["equipped"] = False
        bag.append(displaced)
    item["equipped"] = True
    equipped[slot] = item
    s["equipped"] = equipped
    s["bag"] = bag
    _write_save_record(save_id, uid, s)
    return jsonify({"ok": True, "equipped": equipped, "bag": bag})


@app.route("/api/game/saves/<save_id>/inventory/unequip", methods=["POST"])
def api_inventory_unequip(save_id):
    uid = _user_id(_get_current_user())
    body = request.get_json(force=True) or {}
    slot = body.get("slot")
    s = _get_save_record(save_id, uid)
    if s is None:
        return jsonify({"error": "not found"}), 404
    equipped = s.get("equipped", {})
    bag = s.get("bag", [])
    bag_capacity = s.get("bag_capacity", 5)
    item = equipped.get(slot)
    if not item:
        return jsonify({"error": "slot empty"}), 400
    if len(bag) >= bag_capacity:
        return jsonify({"error": "bag full"}), 400
    item["equipped"] = False
    bag.append(item)
    equipped.pop(slot, None)
    s["equipped"] = equipped
    s["bag"] = bag
    _write_save_record(save_id, uid, s)
    return jsonify({"ok": True, "equipped": equipped, "bag": bag})


@app.route("/api/game/saves/<save_id>/inventory/drop", methods=["POST"])
def api_inventory_drop(save_id):
    uid = _user_id(_get_current_user())
    body = request.get_json(force=True) or {}
    item_id = body.get("item_id")
    s = _get_save_record(save_id, uid)
    if s is None:
        return jsonify({"error": "not found"}), 404
    s["bag"] = [i for i in s.get("bag", []) if i.get("id") != item_id]
    _write_save_record(save_id, uid, s)
    return jsonify({"ok": True, "bag": s["bag"]})


@app.route("/api/game/saves/<save_id>/inventory/add", methods=["POST"])
def api_inventory_add(save_id):
    uid = _user_id(_get_current_user())
    body = request.get_json(force=True) or {}
    item = body.get("item")
    if not item:
        return jsonify({"error": "item required"}), 400
    s = _get_save_record(save_id, uid)
    if s is None:
        return jsonify({"error": "not found"}), 404
    bag = s.get("bag", [])
    cap = s.get("bag_capacity", 5)
    if len(bag) >= cap:
        return jsonify({"error": "bag full", "bag_full": True}), 400
    bag.append(item)
    s["bag"] = bag
    _write_save_record(save_id, uid, s)
    return jsonify({"ok": True, "bag": bag})


@app.route("/api/game/saves/<save_id>/combat/start", methods=["POST"])
def api_combat_start(save_id):
    uid = _user_id(_get_current_user())
    body = request.get_json(force=True) or {}
    s = _get_save_record(save_id, uid)
    if s is None:
        return jsonify({"error": "not found"}), 404
    genre = s.get("genre", "fantasy")
    level = s.get("level", 1)
    enemy = _generate_enemy(genre, level, body.get("enemy_name"))
    stats = s.get("stats", {})
    stamina_key = _STAMINA_STAT.get(genre, "Stamina")
    max_hp = max(10, stats.get(stamina_key, 10) * 2)
    s.setdefault("max_health", max_hp)
    s.setdefault("current_health", s["max_health"])
    s["enemy_state"] = enemy
    s["in_combat"] = True
    s.setdefault("enemies_defeated", 0)
    s.setdefault("bosses_defeated", 0)
    _write_save_record(save_id, uid, s)
    return jsonify({
        "enemy": enemy,
        "player_health": s["current_health"],
        "player_max_health": s["max_health"],
    })


@app.route("/api/game/saves/<save_id>/combat/attack", methods=["POST"])
def api_combat_attack(save_id):
    uid = _user_id(_get_current_user())
    body = request.get_json(force=True) or {}
    attack_type = body.get("attack_type", "physical")
    skill_quality = max(1, min(4, int(body.get("skill_quality", 1))))
    s = _get_save_record(save_id, uid)
    if s is None:
        return jsonify({"error": "not found"}), 404
    if not s.get("in_combat") or not s.get("enemy_state"):
        return jsonify({"error": "not in combat"}), 400
    enemy = s["enemy_state"]
    if enemy.get("defeated"):
        return jsonify({"error": "already defeated"}), 400
    genre = s.get("genre", "fantasy")
    stats = s.get("stats", {})
    equipped = s.get("equipped", {})
    atk_stat = _ATTACK_STATS.get(genre, {}).get(attack_type, "Strength")
    atk_val = _eff_stat(stats, equipped, atk_stat)
    sq_bonus = skill_quality - 1
    if attack_type == "magic":
        dmg = max(1, atk_val + sq_bonus - enemy["magic_resist"])
    else:
        dmg = max(1, atk_val + sq_bonus - enemy["defense"])
    enemy["health"] = max(0, enemy["health"] - dmg)
    stamina_key = _STAMINA_STAT.get(genre, "Stamina")
    stamina_val = _eff_stat(stats, equipped, stamina_key)
    result: dict = {
        "player_damage": dmg, "attack_type": attack_type,
        "enemy_health": enemy["health"], "enemy_max_health": enemy["max_health"],
        "enemy_name": enemy["name"], "is_boss": enemy["type"] == "boss",
        "enemy_defeated": False, "player_dead": False,
        "player_health": s.get("current_health", s.get("max_health", 20)),
        "player_max_health": s.get("max_health", 20),
        "enemy_damage": 0, "special_triggered": False,
        "special_name": None, "loot_tier": enemy.get("loot_tier", "common"),
    }
    if enemy["health"] <= 0:
        enemy["defeated"] = True
        s["in_combat"] = False
        s["enemies_defeated"] = s.get("enemies_defeated", 0) + 1
        if enemy["type"] == "boss":
            s["bosses_defeated"] = s.get("bosses_defeated", 0) + 1
        result["enemy_defeated"] = True
    else:
        enemy["special_counter"] = enemy.get("special_counter", 0) + 1
        special = enemy.get("special_ability")
        cooldown = enemy.get("special_cooldown", 3)
        e_dmg = max(1, enemy["strength"] - stamina_val)
        if special and enemy["special_counter"] >= cooldown:
            enemy["special_counter"] = 0
            result["special_triggered"] = True
            result["special_name"] = special
            if special == "fire_breath":
                e_dmg = max(1, enemy["strength"])
            elif special == "mind_control":
                s["skip_next_turn"] = True
                e_dmg = 0
            elif special == "rumor_spread":
                ch_key = "Charm" if genre == "comedy" else "Charisma"
                stats[ch_key] = max(1, stats.get(ch_key, 5) - 2)
                s["stats"] = stats
            elif special == "power_malfunction":
                s["skill_disabled_turns"] = 2
        s["current_health"] = max(0, s.get("current_health", s.get("max_health", 20)) - e_dmg)
        result["enemy_damage"] = e_dmg
        result["player_health"] = s["current_health"]
        if s["current_health"] <= 0:
            result["player_dead"] = True
            s["in_combat"] = False
    s["enemy_state"] = enemy
    _write_save_record(save_id, uid, s)
    return jsonify(result)


@app.route("/api/game/saves/<save_id>/combat/flee", methods=["POST"])
def api_combat_flee(save_id):
    uid = _user_id(_get_current_user())
    s = _get_save_record(save_id, uid)
    if s is None:
        return jsonify({"error": "not found"}), 404
    result: dict = {"fled": False, "enemy_damage": 0, "player_dead": False,
                    "player_health": s.get("current_health", s.get("max_health", 20)),
                    "player_max_health": s.get("max_health", 20)}
    if random.random() < 0.6:
        result["fled"] = True
        s["in_combat"] = False
        s["enemy_state"] = None
    else:
        enemy = s.get("enemy_state") or {}
        e_dmg = max(1, (enemy.get("strength") or 5) // 2)
        s["current_health"] = max(0, s.get("current_health", s.get("max_health", 20)) - e_dmg)
        result["enemy_damage"] = e_dmg
        result["player_health"] = s["current_health"]
        if s["current_health"] <= 0:
            result["player_dead"] = True
            s["in_combat"] = False
    _write_save_record(save_id, uid, s)
    return jsonify(result)


@app.route("/api/game/saves/<save_id>/combat/state")
def api_combat_state(save_id):
    uid = _user_id(_get_current_user())
    s = _get_save_record(save_id, uid)
    if s is None:
        return jsonify({"error": "not found"}), 404
    return jsonify({
        "in_combat": s.get("in_combat", False),
        "enemy_state": s.get("enemy_state"),
        "current_health": s.get("current_health"),
        "max_health": s.get("max_health"),
        "enemies_defeated": s.get("enemies_defeated", 0),
        "bosses_defeated": s.get("bosses_defeated", 0),
    })


@app.route("/api/game/combat/skill-quality", methods=["POST"])
def api_combat_skill_quality():
    body = request.get_json(force=True) or {}
    skill_text = (body.get("skill_text") or "").strip()
    genre = body.get("genre", "fantasy")
    if not skill_text:
        return jsonify({"quality": 1})
    prompt = (
        f"Rate this skill expression for a {genre} text adventure on a scale of 1-4.\n"
        f"1=single word or generic. 2=on-theme, 2-10 words. 3=creative/detailed. 4=exceptional/rhyming.\n"
        f'Text: "{skill_text[:200]}"\nReply with ONLY a single digit: 1, 2, 3, or 4.'
    )
    try:
        r = requests.post(
            f"{OLLAMA_URL}/api/generate",
            json={"model": "mistral", "prompt": prompt, "stream": False},
            timeout=15,
        )
        text = r.json().get("response", "1").strip()
        q = int(text[0]) if text and text[0].isdigit() else 1
        q = max(1, min(4, q))
    except Exception:
        q = 1
    return jsonify({"quality": q})


@app.route("/api/status")
def api_status():
    try:
        r = requests.get(f"{LOCAL_URL}/status", timeout=2, verify=False)
        return r.content, r.status_code, {"Content-Type": "application/json"}
    except Exception as e:
        return jsonify({"error": str(e)}), 503


@app.route("/api/chat", methods=["POST"])
def api_chat():
    body = request.get_json(force=True) or {}
    prompt = body.get("message", "").strip()
    model = body.get("model", "mistral")
    if not prompt:
        return jsonify({"error": "message required"}), 400
    try:
        r = requests.post(
            f"{LOCAL_URL}/submit",
            json={"prompt": prompt, "class": 1, "model": model},
            timeout=120,
            verify=False,
        )
        d = r.json()
        return jsonify({
            "response": d.get("response", ""),
            "node_id": d.get("node_id", ""),
            "routed_to": d.get("routed_to", ""),
            "model": d.get("model", model),
        }), r.status_code
    except Exception as e:
        return jsonify({"error": str(e)}), 503


def _install_handler():
    """Shared /install logic — used by both port 5000 and the LAN-only port 5001 server."""
    script_path = os.path.join(_REPO_ROOT, "vernex-node-setup.sh")
    if not os.path.exists(script_path):
        return (
            "# vernex-node-setup.sh not found on this bootstrap node.\n"
            "# Get it from: https://raw.githubusercontent.com/SuperSleeper/vernex-protocol/main/vernex-node-setup.sh\n"
        ), 404, {"Content-Type": "text/plain"}

    token_id = request.args.get("token", "").strip()

    if token_id:
        token_path = os.path.join(
            os.path.expanduser("~"), "vernex", "config", f"token-{token_id}.json"
        )
        if not os.path.exists(token_path):
            return f"# Token '{token_id}' not found on this bootstrap node.\n", 404, {"Content-Type": "text/plain"}
        with open(token_path) as f:
            token_json = f.read().strip()
        install_url = f"http://{request.host}/install"
        # Escape single quotes in the JSON for safe shell embedding
        token_escaped = token_json.replace("'", "'\\''")
        cmd = f"curl -fsSL '{install_url}' | bash -s -- --token '{token_escaped}'\n"
        return cmd, 200, {"Content-Type": "text/plain"}
    else:
        return send_from_directory(_REPO_ROOT, "vernex-node-setup.sh", mimetype="text/plain")


@app.route("/install")
def install_script():
    return _install_handler()


# ── LAN install server (port 5001, 0.0.0.0) ─────────────────────────────────
# Exposes only /install so bootstrap operators can share a curl one-liner
# to LAN nodes without exposing the full dashboard externally.
_install_app = Flask("vernex_install")


@_install_app.route("/install")
def install_script_lan():
    return _install_handler()


def _run_install_server():
    import logging
    log = logging.getLogger("werkzeug")
    log.setLevel(logging.WARNING)
    _install_app.run(host="0.0.0.0", port=5001, threaded=True)


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
    from oauth import run_oauth_server
    threading.Thread(target=run_oauth_server, daemon=True).start()
    print("  [✓] OAuth server started on 127.0.0.1:5002 (/auth/*, /api/me)")
    threading.Thread(target=_run_install_server, daemon=True).start()
    print("  [✓] Install server started on 0.0.0.0:5001 (/install only)")
    app.run(host="127.0.0.1", port=5000, debug=False)

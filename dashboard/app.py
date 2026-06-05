from flask import Flask, render_template_string, jsonify, request, send_from_directory
import requests
import json
import os
import subprocess
import threading
import time

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
#chat-log{flex:1;min-height:160px;display:flex;flex-direction:column;gap:10px;overflow-y:auto}
.msg{padding:10px 14px;border-radius:8px;max-width:85%;line-height:1.5;word-break:break-word}
.msg.user{background:#0f3460;align-self:flex-end}
.msg.assistant{background:#16213e;border:1px solid #0f3460;align-self:flex-start;white-space:pre-wrap}
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
  <span class="node-info" id="node-info">loading…</span>
  <div style="margin-left:auto;display:flex;gap:16px;align-items:center">
    <div id="gpu-gauges" class="gpu-gauges"></div>
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
      if(d.error)return;
      var bar=document.getElementById('gpu-bar-'+n.id);
      var stats=document.getElementById('gpu-stats-'+n.id);
      var card=document.getElementById('gpu-card-'+n.id);
      var pct=d.vram_total_mb>0?d.vram_used_mb/d.vram_total_mb*100:0;
      var active=d.gpu_util_pct>20;
      bar.style.width=pct.toFixed(1)+'%';
      bar.className='gpu-bar-fill'+(active?' active':'');
      card.className='gpu-card'+(active?' active':'');
      var used=(d.vram_used_mb/1024).toFixed(1);
      var total=(d.vram_total_mb/1024).toFixed(1);
      stats.textContent=used+'/'+total+' GB · '+d.gpu_util_pct+'% · '+d.temp_c+'°C';
    }).catch(function(){});
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
.cs-inner{display:grid;grid-template-columns:1fr 1fr;gap:2px 14px;padding-top:8px;font-family:'Courier New',monospace}
.save-toolbar{display:flex;gap:8px;align-items:center;margin-bottom:8px;flex-wrap:wrap}
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
#chat-log{display:flex;flex-direction:column;gap:10px;min-height:220px;overflow-y:auto;margin-bottom:10px}
.msg{padding:10px 14px;border-radius:8px;word-break:break-word;line-height:1.55}
.msg.user{background:#0f3460;align-self:flex-end;max-width:80%;font-size:.9rem}
.msg.assistant{background:#0d1117;border:1px solid #1f2d45;align-self:stretch;font-family:'Courier New',monospace;font-size:.82rem}
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
  <span class="node-info" id="node-info">loading&#8230;</span>
  <div class="hdr-links">
    <div id="gpu-gauges" class="gpu-gauges"></div>
    <a class="hdr-link" href="/ui">&#8592; Chat</a>
    <a class="hdr-link" href="/auth/logout">logout</a>
  </div>
</header>

<!-- ══ GENRE SELECTION ══ -->
<main id="view-select" class="view-main">
  <p class="sel-title">Choose Your Adventure</p>
  <p class="sel-sub">Select a genre to begin</p>
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
  </div>
</main>

<!-- ══ CHARACTER CREATOR ══ -->
<main id="view-create" class="view-main" style="display:none">
  <div class="cc-hdr">
    <button class="cc-back" onclick="backToSelect()">&#8592; Back</button>
    <span class="cc-title" id="cc-genre-lbl">Create Your Character</span>
  </div>
  <div class="cc-grid">
    <div class="cc-box">
      <div class="cc-lbl">Gender</div>
      <div class="radio-group">
        <label class="radio-label"><input type="radio" name="gender" value="male" checked onchange="onGenderChange()"> Male</label>
        <label class="radio-label"><input type="radio" name="gender" value="female" onchange="onGenderChange()"> Female</label>
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
    <button class="btn-roll" onclick="rollCharacter()">&#127922; Roll Character</button>
    <button class="btn-start" id="start-game-btn" onclick="startGame()" disabled>&#9654; Start Game</button>
  </div>
  <details>
    <summary>&#9881; Game Context</summary>
    <textarea id="game-context" class="ctx-ta" spellcheck="false"></textarea>
    <div class="ctx-btns">
      <button class="btn-ctx" onclick="savePromptDefault()">&#128190; Save as Default</button>
      <button class="btn-ctx" onclick="resetPromptDefault()">&#8635; Reset to Default</button>
      <span id="ctx-st" class="ctx-st"></span>
    </div>
  </details>
</main>

<!-- ══ GAMEPLAY ══ -->
<main id="view-play" class="view-main" style="display:none">
  <details class="cs-panel">
    <summary>&#128203; Character Sheet &#8212; <span id="cs-name"></span></summary>
    <div class="cs-inner" id="cs-inner"></div>
  </details>
  <div class="save-toolbar">
    <button class="btn-qs" id="qs-btn" onclick="quickSave()" disabled>&#9889; Quicksave</button>
    <button class="btn-sv" onclick="toggleSaveForm()">&#128190; Save</button>
    <button class="btn-sv" onclick="toggleLoadPanel()">&#128194; Load</button>
    <span id="sv-st" class="sv-st"></span>
  </div>
  <div id="sv-form" class="sv-form" style="display:none">
    <input type="text" id="sv-name-inp" class="sv-inp" placeholder="Save name&#8230;">
    <button class="btn-sv" onclick="confirmSave()">Save</button>
    <button class="btn-sv-x" onclick="hideSaveForm()">Cancel</button>
  </div>
  <div id="load-panel" class="load-panel" style="display:none">
    <div id="load-list"></div>
  </div>
  <div id="chat-log"></div>
  <div class="input-row">
    <select id="model-select"><option value="mistral">mistral</option></select>
  </div>
  <textarea id="prompt" placeholder="What do you do?" disabled></textarea>
  <div class="input-row" style="margin-top:8px">
    <button class="btn-send" id="submit-btn" disabled onclick="sendTurn(event)">Send</button>
    <button class="btn-reset" onclick="resetGame()">&#128260; Reset Game</button>
  </div>
</main>

<script>
var GAME_DATA = {{ game_data | tojson }};

var STAT_NAMES = {
  fantasy:['Strength','Stamina','Charisma','Magic','Agility','Luck'],
  scifi:  ['Intelligence','Tech Skill','Agility','Charisma','Endurance','Luck'],
  action: ['Strength','Stamina','Charisma','Cunning','Agility','Luck']
};
var SUBTYPES = {
  fantasy:{lbl:'Class',byG:true, opts:{male:['Warrior','Elf Archer','Wizard','Thief'],female:['Valkyrie','Elf Archer','Wizard','Thief']}},
  scifi:  {lbl:'Scenario',byG:false,opts:['AI','Aliens','Space Travel','Time Travel']},
  action: {lbl:'Era',byG:false,opts:['Egyptian Pharaoh Era','Roman Empire','Renaissance','American Wild West','World War II']}
};

var _genre=null,_rolledStats=null,_character=null;
var _history=[],_gameStarted=false,_currentSaveId=null,_turnCount=0;

// ── View management ──────────────────────────────────────────
function showView(v){
  ['view-select','view-create','view-play'].forEach(function(id){
    document.getElementById(id).style.display=(id===v)?'block':'none';
  });
  adjustPadding();
}
function adjustPadding(){
  var h=document.getElementById('site-hdr').offsetHeight;
  document.querySelectorAll('.view-main').forEach(function(el){el.style.paddingTop=(h+14)+'px';});
}

// ── Genre selection ──────────────────────────────────────────
function selectGenre(g){
  _genre=g;
  var lbl={fantasy:'Fantasy Adventure',scifi:'Science Fiction',action:'Action / Adventure'};
  document.getElementById('cc-genre-lbl').textContent=lbl[g]||g;
  renderSubtypeOpts();
  document.getElementById('stat-block').innerHTML='<p class="stat-ph">Click “Roll Character” to generate stats.</p>';
  document.getElementById('start-game-btn').disabled=true;
  document.getElementById('game-context').value='';
  _rolledStats=null;
  loadSavedPrompt();
  showView('view-create');
}
function backToSelect(){_genre=null;showView('view-select');}

// ── Character creator ────────────────────────────────────────
function getGender(){var el=document.querySelector('input[name="gender"]:checked');return el?el.value:'male';}
function onGenderChange(){if(_genre==='fantasy')renderSubtypeOpts();}
function getSubtype(){var el=document.querySelector('input[name="subtype"]:checked');return el?el.value:'';}

function renderSubtypeOpts(){
  var st=SUBTYPES[_genre];
  var opts=st.byG?st.opts[getGender()]:st.opts;
  document.getElementById('subtype-lbl').textContent=st.lbl;
  document.getElementById('subtype-opts').innerHTML=opts.map(function(o,i){
    return '<label class="radio-label"><input type="radio" name="subtype" value="'+o+'"'+(i===0?' checked':'')+'>'+o+'</label>';
  }).join('');
}

function roll4d6(){var d=[0,0,0,0].map(function(){return Math.floor(Math.random()*6)+1;});d.sort(function(a,b){return a-b;});return d[1]+d[2]+d[3];}
function statBar(v){var f=Math.min(8,Math.max(0,Math.round((v-3)/15*8)));return '\\u2588'.repeat(f)+'\\u2591'.repeat(8-f);}

function rollCharacter(){
  var names=STAT_NAMES[_genre];
  _rolledStats={};
  names.forEach(function(n){_rolledStats[n]=roll4d6();});
  document.getElementById('stat-block').innerHTML=Object.keys(_rolledStats).map(function(n){
    return '<div class="stat-row"><span class="stat-name">'+n+'</span><span class="stat-bar">'+statBar(_rolledStats[n])+'</span><span class="stat-val">&nbsp;'+_rolledStats[n]+'</span></div>';
  }).join('');
  var ctx=document.getElementById('game-context').value.trim();
  if(!ctx)document.getElementById('game-context').value=buildContext();
  document.getElementById('start-game-btn').disabled=false;
}

// ── Prompt persistence ───────────────────────────────────────
function loadSavedPrompt(){
  fetch('/api/game/prompts/'+_genre).then(function(r){return r.json();}).then(function(d){
    if(d.prompt)document.getElementById('game-context').value=d.prompt;
  }).catch(function(){});
}
function savePromptDefault(){
  var ctx=document.getElementById('game-context').value.trim();
  if(!ctx){alert('Roll character first to generate a context.');return;}
  fetch('/api/game/prompts',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({genre:_genre,prompt:ctx})})
    .then(function(){showCtxStatus('\\u2713 Saved as default');}).catch(function(){showCtxStatus('Save failed');});
}
function resetPromptDefault(){
  fetch('/api/game/prompts',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({genre:_genre,prompt:null})})
    .then(function(){document.getElementById('game-context').value=_rolledStats?buildContext():'';showCtxStatus('Reset to default');}).catch(function(){});
}
function showCtxStatus(m){var e=document.getElementById('ctx-st');e.textContent=m;setTimeout(function(){e.textContent='';},2500);}

// ── Context generation ───────────────────────────────────────
function sbLines(){
  return Object.keys(_rolledStats).map(function(n){
    return (n+'              ').slice(0,14)+statBar(_rolledStats[n])+' '+_rolledStats[n];
  }).join('\\n');
}
function buildContext(){
  var name=document.getElementById('char-name').value.trim()||'the adventurer';
  var gender=getGender();var sub=getSubtype();
  var pr=gender==='male'?'He':'She';
  var sb=sbLines();var ls=GAME_DATA.levelSystem;
  var ch='CHARACTER\\nName:   '+name+'\\nGender: '+gender+'\\n';
  if(_genre==='fantasy'){
    ch+='Class:  '+sub+'\\n';
    return 'You are a dynamic, adaptive text-adventure game engine set in a classic fantasy world.\\nNever break character or explain your mechanics.\\n\\n'+ch+'\\nSTAT BLOCK\\n'+sb+'\\n\\n'+ls
      +'\\n\\n### 4. GENRE MECHANICS\\nSpells, potions, enchanted items. MAGIC governs spell power; STRENGTH melee; AGILITY stealth. Track inventory (max 6 items). Medieval fantasy — no modern technology.\\n\\n'
      +'### 5. OPENING MOVE\\nBegin at Level 1. '+name+' ('+sub+') wakes '+GAME_DATA.openings.fantasy+'.';
  }
  if(_genre==='scifi'){
    ch+='Scenario: '+sub+'\\n';
    var op=GAME_DATA.openings.scifi[sub]||'waking in an unfamiliar technological environment';
    return 'You are a dynamic, adaptive text-adventure game engine set in a science fiction universe.\\nNever break character or explain your mechanics.\\n\\n'+ch+'\\nSTAT BLOCK\\n'+sb+'\\n\\n'+ls
      +'\\n\\n### 4. GENRE MECHANICS\\nTechnology, science, gadgets. INTELLIGENCE governs problem-solving; TECH SKILL device operation. Track equipment (max 6). No magic — plausible science only.\\n\\n'
      +'### 5. OPENING MOVE\\nBegin at Level 1. '+name+' ('+sub+' scenario): '+pr+' is '+op+'.';
  }
  ch+='Era:    '+sub+'\\n';
  var op=GAME_DATA.openings.action[sub]||'finding themselves at the heart of a historical moment';
  return 'You are a dynamic, adaptive text-adventure game engine set in a historical action-adventure world.\\nNever break character or explain your mechanics.\\n\\n'+ch+'\\nSTAT BLOCK\\n'+sb+'\\n\\n'+ls
    +'\\n\\n### 4. GENRE MECHANICS\\nCombat, diplomacy, survival. All items and language MUST be era-accurate for "'+sub+'". STRENGTH governs physical; CUNNING strategy/deception; CHARISMA social. Track equipment (max 6). PG-13 only.\\n\\n'
    +'### 5. OPENING MOVE\\nBegin at Level 1. '+name+' in the '+sub+' era: '+pr+' is '+op+'.';
}

// ── Game start ───────────────────────────────────────────────
async function startGame(){
  if(!_rolledStats){alert('Roll your character first.');return;}
  var name=document.getElementById('char-name').value.trim()||'the adventurer';
  _character={genre:_genre,name:name,gender:getGender(),subtype:getSubtype(),stats:_rolledStats};
  var ctx=document.getElementById('game-context').value.trim()||buildContext();
  renderCharSheet();
  showView('view-play');
  _history=[{role:'system',content:ctx},{role:'user',content:'Begin the adventure.'}];
  var log=document.getElementById('chat-log');log.innerHTML='';
  document.getElementById('prompt').disabled=true;
  document.getElementById('submit-btn').disabled=true;
  document.getElementById('qs-btn').disabled=true;
  _turnCount=0;_currentSaveId=null;_gameStarted=false;
  try{
    var resp=await gameFetch(_history,document.getElementById('model-select').value);
    _history.push({role:'assistant',content:resp});appendMsg('assistant',resp);
    _gameStarted=true;
    document.getElementById('prompt').disabled=false;
    document.getElementById('submit-btn').disabled=false;
    document.getElementById('qs-btn').disabled=false;
    document.getElementById('prompt').focus();
  }catch(err){appendMsg('error','Failed to start: '+err.message);}
}

function renderCharSheet(){
  document.getElementById('cs-name').textContent=_character.name+' · '+_character.subtype;
  document.getElementById('cs-inner').innerHTML=Object.keys(_character.stats).map(function(n){
    return '<div class="stat-row"><span class="stat-name">'+n+'</span><span class="stat-bar">'+statBar(_character.stats[n])+'</span><span class="stat-val">&nbsp;'+_character.stats[n]+'</span></div>';
  }).join('');
}

// ── Chat ─────────────────────────────────────────────────────
async function sendTurn(e){
  if(e&&e.preventDefault)e.preventDefault();
  if(!_gameStarted)return;
  var prompt=document.getElementById('prompt').value.trim();if(!prompt)return;
  var btn=document.getElementById('submit-btn'),model=document.getElementById('model-select').value;
  _history.push({role:'user',content:prompt});appendMsg('user',prompt);
  document.getElementById('prompt').value='';btn.disabled=true;btn.textContent='...';
  try{
    var resp=await gameFetch(_history,model);
    _history.push({role:'assistant',content:resp});appendMsg('assistant',resp);
    _turnCount++;if(_turnCount%5===0)autoSave();
  }catch(err){_history.pop();appendMsg('error','Request failed: '+err.message);}
  finally{btn.disabled=false;btn.textContent='Send';}
}
async function gameFetch(msgs,model){
  var r=await fetch('/api/game/chat',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({messages:msgs,model:model})});
  var d=await r.json();if(d.error&&!d.response)throw new Error(d.error);
  return d.response||d.error||JSON.stringify(d);
}
function appendMsg(role,content){
  var log=document.getElementById('chat-log');
  var div=document.createElement('div');div.className='msg '+role;
  if(role==='assistant'){div.innerHTML=marked.parse(content,{breaks:true,gfm:true});}
  else{div.textContent=content;}
  log.appendChild(div);
  requestAnimationFrame(function(){log.scrollTop=log.scrollHeight;});
}
function resetGame(){
  _history=[];_gameStarted=false;_character=null;_rolledStats=null;_genre=null;_currentSaveId=null;_turnCount=0;
  document.getElementById('prompt').disabled=true;document.getElementById('submit-btn').disabled=true;
  document.getElementById('prompt').value='';document.getElementById('chat-log').innerHTML='';
  hideSaveForm();hideLoadPanel();showView('view-select');
}

// ── Save / Load ──────────────────────────────────────────────
function toggleSaveForm(){
  var f=document.getElementById('sv-form');
  if(f.style.display==='none'||!f.style.display){f.style.display='flex';document.getElementById('sv-name-inp').focus();}
  else hideSaveForm();
}
function hideSaveForm(){document.getElementById('sv-form').style.display='none';}
async function confirmSave(){
  var name=document.getElementById('sv-name-inp').value.trim();if(!name){alert('Enter a save name.');return;}
  await saveGame(name,null);hideSaveForm();document.getElementById('sv-name-inp').value='';
}
async function saveGame(name,id){
  if(!_character)return;
  var pay={save_name:name,genre:_character.genre,subtype:_character.subtype,char_name:_character.name,gender:_character.gender,stats:_character.stats,history:_history};
  try{
    var url=id?'/api/game/saves/'+id:'/api/game/saves',method=id?'PUT':'POST';
    var r=await fetch(url,{method:method,headers:{'Content-Type':'application/json'},body:JSON.stringify(pay)});
    var d=await r.json();if(d.save_id)_currentSaveId=d.save_id;
    setSvStatus('\\u2713 Saved: '+name);
  }catch(err){setSvStatus('Save failed');}
}
async function quickSave(){
  if(!_character)return;
  await saveGame('Quicksave — '+_character.name,_currentSaveId||null);
}
async function autoSave(){
  if(!_character)return;
  var pay={save_name:'Autosave',genre:_character.genre,subtype:_character.subtype,char_name:_character.name,gender:_character.gender,stats:_character.stats,history:_history,autosave:true};
  try{await fetch('/api/game/saves/autosave-'+_character.genre,{method:'PUT',headers:{'Content-Type':'application/json'},body:JSON.stringify(pay)});}catch(e){}
}
function setSvStatus(m){var e=document.getElementById('sv-st');e.textContent=m;setTimeout(function(){e.textContent='';},3000);}

function toggleLoadPanel(){
  var p=document.getElementById('load-panel');
  if(p.style.display==='none'||!p.style.display){refreshSaveList();p.style.display='block';}
  else hideLoadPanel();
}
function hideLoadPanel(){document.getElementById('load-panel').style.display='none';}
async function refreshSaveList(){
  var list=document.getElementById('load-list');list.innerHTML='<div class="load-empty">Loading&#8230;</div>';
  try{
    var saves=await fetch('/api/game/saves').then(function(r){return r.json();});
    if(!saves.length){list.innerHTML='<div class="load-empty">No saves found.</div>';return;}
    list.innerHTML=saves.map(function(s){
      var dt=s.updated_at?(s.updated_at.slice(0,16).replace('T',' ')):'';
      return '<div class="load-item">'
        +'<div class="load-item-info" onclick="loadGame(\''+s.save_id+'\')">'
        +'<span class="li-name">'+s.save_name+'</span>'
        +'<span class="li-meta">'+s.genre+' · '+s.char_name+' · '+dt+'</span>'
        +'</div>'
        +'<button class="btn-del" onclick="deleteGame(\''+s.save_id+'\')">&#128465;</button>'
        +'</div>';
    }).join('');
  }catch(e){list.innerHTML='<div class="load-empty">Failed to load saves.</div>';}
}
async function loadGame(id){
  try{
    var s=await fetch('/api/game/saves/'+id).then(function(r){return r.json();});
    _character={genre:s.genre,name:s.char_name,gender:s.gender,subtype:s.subtype,stats:s.stats};
    _history=s.history||[];_currentSaveId=id;_turnCount=0;_gameStarted=true;
    renderCharSheet();showView('view-play');hideLoadPanel();
    var log=document.getElementById('chat-log');log.innerHTML='';
    var last=_history.filter(function(m){return m.role==='assistant';});
    if(last.length)appendMsg('assistant',last[last.length-1].content);
    document.getElementById('prompt').disabled=false;
    document.getElementById('submit-btn').disabled=false;
    document.getElementById('qs-btn').disabled=false;
    setSvStatus('\\u2713 Loaded: '+s.save_name);
  }catch(err){alert('Load failed: '+err.message);}
}
async function deleteGame(id){
  if(!confirm('Delete this save?'))return;
  try{await fetch('/api/game/saves/'+id,{method:'DELETE'});refreshSaveList();}
  catch(err){alert('Delete failed: '+err.message);}
}

// ── Models / Node info / GPU (unchanged) ─────────────────────
(function loadModels(){
  fetch('/api/models').then(function(r){return r.json();}).then(function(d){
    var sel=document.getElementById('model-select');
    if(d.models&&d.models.length){
      sel.innerHTML='';
      d.models.forEach(function(m){var o=document.createElement('option');o.value=m;o.textContent=m;sel.appendChild(o);});
      var pref=d.models.indexOf('gemma4:e4b')>=0?'gemma4:e4b':d.models[0];sel.value=pref;
    }
  }).catch(function(){});
})();
(function loadNodeInfo(){
  fetch('/api/status',{credentials:'include'}).then(function(r){return r.json();}).then(function(d){
    document.getElementById('node-info').textContent=(d.node_id||'')+'  ·  v'+(d.version||'');
  }).catch(function(){document.getElementById('node-info').textContent='status unavailable';});
})();
(function(){
  var GPU_NODES=[{id:'node1',label:'node1 · RTX 3070',url:'/api/gpu'}];
  var container=document.getElementById('gpu-gauges');if(!container)return;
  GPU_NODES.forEach(function(n){
    var c=document.createElement('div');c.className='gpu-card';c.id='gpu-card-'+n.id;
    c.innerHTML='<div class="gpu-card-label">'+n.label+'</div><div class="gpu-bar-row"><div class="gpu-bar-track"><div class="gpu-bar-fill" id="gpu-bar-'+n.id+'" style="width:0"></div></div><span class="gpu-card-stats" id="gpu-stats-'+n.id+'">—</span></div>';
    container.appendChild(c);
  });
  function pollNode(n){
    fetch(n.url).then(function(r){return r.json();}).then(function(d){
      if(d.error)return;
      var bar=document.getElementById('gpu-bar-'+n.id),stats=document.getElementById('gpu-stats-'+n.id),card=document.getElementById('gpu-card-'+n.id);
      var pct=d.vram_total_mb>0?d.vram_used_mb/d.vram_total_mb*100:0,active=d.gpu_util_pct>20;
      bar.style.width=pct.toFixed(1)+'%';bar.className='gpu-bar-fill'+(active?' active':'');
      card.className='gpu-card'+(active?' active':'');
      stats.textContent=(d.vram_used_mb/1024).toFixed(1)+'/'+(d.vram_total_mb/1024).toFixed(1)+' GB · '+d.gpu_util_pct+'% · '+d.temp_c+'°C';
    }).catch(function(){});
  }
  function poll(){GPU_NODES.forEach(pollNode);}poll();setInterval(poll,3000);
})();
document.getElementById('prompt').addEventListener('keydown',function(e){
  if(e.key==='Enter'&&!e.shiftKey){e.preventDefault();if(_gameStarted)sendTurn(e);}
});
document.querySelectorAll('.genre-card[data-genre]').forEach(function(card){
  card.addEventListener('click',function(){selectGenre(card.getAttribute('data-genre'));});
});
adjustPadding();
window.addEventListener('resize',adjustPadding);
</script>
</body>
</html>"""


@app.route("/ui")
def ui():
    return render_template_string(_CHAT_HTML)


@app.route("/game")
def game():
    game_data = {
        "levelSystem": _GAME_LEVEL_SYSTEM,
        "openings": _GAME_OPENINGS,
    }
    return render_template_string(_GAME_HTML, game_data=game_data)


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

_PROMPTS_FILE = os.path.join(os.path.expanduser("~"), "vernex", "config", "game_prompts.json")
_SAVES_DIR = os.path.join(os.path.expanduser("~"), "vernex", "config", "game_saves")


def _load_prompts():
    try:
        with open(_PROMPTS_FILE) as f:
            return json.load(f)
    except Exception:
        return {}


def _save_prompts(data):
    os.makedirs(os.path.dirname(_PROMPTS_FILE), exist_ok=True)
    with open(_PROMPTS_FILE, "w") as f:
        json.dump(data, f, indent=2)


@app.route("/api/game/prompts/<genre>")
def api_game_prompts_get(genre):
    prompts = _load_prompts()
    return jsonify({"genre": genre, "prompt": prompts.get(genre)})


@app.route("/api/game/prompts", methods=["POST"])
def api_game_prompts_save():
    body = request.get_json(force=True) or {}
    genre = body.get("genre", "").strip()
    prompt = body.get("prompt")
    if not genre:
        return jsonify({"error": "genre required"}), 400
    prompts = _load_prompts()
    if prompt is None:
        prompts.pop(genre, None)
    else:
        prompts[genre] = prompt
    _save_prompts(prompts)
    return jsonify({"ok": True})


# ── Game save / load ──────────────────────────────────────────

import uuid as _uuid
from datetime import datetime as _dt


def _save_path(save_id):
    os.makedirs(_SAVES_DIR, exist_ok=True)
    return os.path.join(_SAVES_DIR, f"{save_id}.json")


@app.route("/api/game/saves")
def api_game_saves_list():
    os.makedirs(_SAVES_DIR, exist_ok=True)
    saves = []
    for fname in sorted(os.listdir(_SAVES_DIR), key=lambda f: os.path.getmtime(os.path.join(_SAVES_DIR, f)), reverse=True):
        if not fname.endswith(".json"):
            continue
        try:
            with open(os.path.join(_SAVES_DIR, fname)) as f:
                s = json.load(f)
            saves.append({
                "save_id": s.get("save_id", fname[:-5]),
                "save_name": s.get("save_name", ""),
                "genre": s.get("genre", ""),
                "char_name": s.get("char_name", ""),
                "updated_at": s.get("updated_at", ""),
            })
        except Exception:
            pass
    return jsonify(saves)


@app.route("/api/game/saves", methods=["POST"])
def api_game_saves_create():
    body = request.get_json(force=True) or {}
    save_id = body.get("save_id") or str(_uuid.uuid4())[:8]
    now = _dt.utcnow().isoformat()
    record = dict(body)
    record["save_id"] = save_id
    record.setdefault("created_at", now)
    record["updated_at"] = now
    with open(_save_path(save_id), "w") as f:
        json.dump(record, f, indent=2)
    return jsonify({"save_id": save_id})


@app.route("/api/game/saves/<save_id>")
def api_game_saves_get(save_id):
    path = _save_path(save_id)
    if not os.path.exists(path):
        return jsonify({"error": "not found"}), 404
    with open(path) as f:
        return jsonify(json.load(f))


@app.route("/api/game/saves/<save_id>", methods=["PUT"])
def api_game_saves_update(save_id):
    body = request.get_json(force=True) or {}
    now = _dt.utcnow().isoformat()
    path = _save_path(save_id)
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
    path = _save_path(save_id)
    if os.path.exists(path):
        os.remove(path)
    return jsonify({"ok": True})


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

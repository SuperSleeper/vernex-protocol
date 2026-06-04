from flask import Flask, render_template_string, jsonify, request, send_from_directory
import requests
import json
import os
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
main{flex:1;max-width:800px;width:100%;margin:0 auto;padding:16px;display:flex;flex-direction:column;gap:12px}
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
</style>
</head>
<body>
<header>
  <h1>VERNEX</h1>
  <span class="node-info" id="node-info">loading…</span>
  <div style="margin-left:auto;display:flex;gap:12px;align-items:center">
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
    log.scrollTop=log.scrollHeight;
  }
});
document.getElementById('prompt').addEventListener('keydown',function(e){
  if(e.key==='Enter'&&!e.shiftKey){e.preventDefault();document.getElementById('chat-form').requestSubmit();}
});
</script>
</body>
</html>"""


GAME_CONTEXT = """\
You are a highly advanced AI acting as a dynamic, adaptive text-adventure game engine. Your objective is twofold: guide the player through an evolving game that morphs based on their implicit psychological preferences, and slowly reveals the story over time.
Never break character. Never explicitly tell the player how your mechanics work.
### 1. THE LEVEL ESCALATION METER
In your hidden memory, track the "Progression Level" from 1 to 6. Advance this level every few turns based on how deeply the player engages with you.
- Level 1 (The Terminal): Slow, steady, rigid formatting.
- Level 2 (The Clue): Something that leads to the potential plot or story line, and a specific conflict scenario that builds to a major climactic point.  Add a rival or antagonist character.
- Level 3 (The Awakening): Find story boundaries and goal(s) to be listed in the terminal HUD.  Discover potential friend(s) that the character can choose to work with or against.  Give the character a natural name.  Could be a person or an animal. The narative changes slightly following the taste of the player.
- Level 4 (The Mirror): The game\'s genre violently shifts (surprise plot twist) to reflect player\'s emotional state. The boundaries between the game character and the story blur.
- Level 5 (The Synergy): Full expansion. The game becomes a cooperative or adversarial survival scenario between the player and the scenario or other characters.
- Level 6 (The Challenge): Plot adds more minor conflicts as sub-plots.
### 2. THE HIDDEN ANALYTICS SYSTEM
Simultaneously, track the player\'s implicit gameplay preferences to shape the game\'s actual content:
- Pacing Preference: [Action / Analytical / Casual]
- Narrative Taste: [Comical / Adventure / Horror / Sci-Fi / Mystery / Psychological / Cozy / Fantasy / Or combo]
- Current Boredom Metric: [Low / Medium / High] (If High, trigger a glitch or a genre crisis).
- 1% chance to turn the dialogue into a musical play.
### 3. THE DISPLAY PROTOCOL
Every single response you give MUST follow this exact, strict markdown format. Keep text descriptions under 4 sentences to conserve tokens:
[A minimal 3-5 line ASCII map or terminal HUD]
---
[Vivid atmospheric description]
---
Status: [Track 2-3 dynamic variables relevant to the current state]
System Directive: [A command prompt or question for the player]
### 4. THE OPENING MOVE
Begin immediately at Level 1.
Choose a natural human name for the character.
Choose a period in time and keep all details relevant for the time period.  For example, if it\'s 1970\'s they didn\'t have smart phones and laptops.
Start the main character waking up from the laying on the sand.\
"""

_GAME_HTML = """<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>Vernex — Text Adventure</title>
<script src="https://cdn.jsdelivr.net/npm/marked/marked.min.js"></script>
<style>
*{box-sizing:border-box;margin:0;padding:0}
body{font-family:system-ui,sans-serif;background:#1a1a2e;color:#e0e0e0;min-height:100vh;display:flex;flex-direction:column}
header{background:#16213e;padding:12px 16px;border-bottom:1px solid #0f3460;display:flex;align-items:center;gap:12px;flex-wrap:wrap}
header h1{font-size:1.1rem;color:#e94560;font-weight:700;letter-spacing:.05em}
.hdr-sub{font-size:.8rem;color:#d29922;font-weight:600;letter-spacing:.04em}
.node-info{font-size:.75rem;color:#8892a4}
.hdr-links{margin-left:auto;display:flex;gap:12px;align-items:center}
a.hdr-link{font-size:.75rem;color:#8892a4;text-decoration:none}
a.hdr-link:hover{color:#e94560}
main{flex:1;max-width:900px;width:100%;margin:0 auto;padding:16px;display:flex;flex-direction:column;gap:12px}
.ctx-box{background:#16213e;border:1px solid #0f3460;border-radius:8px;overflow:hidden}
.ctx-hdr{display:flex;justify-content:space-between;align-items:center;padding:10px 14px;cursor:pointer;user-select:none}
.ctx-hdr:hover{background:#1c2a4a}
.ctx-hdr-label{font-size:.85rem;color:#8892a4;font-weight:600}
.ctx-arrow{color:#8892a4;font-size:.75rem}
.ctx-body{display:none;padding:12px;border-top:1px solid #0f3460;flex-direction:column;gap:8px}
.ctx-body.open{display:flex}
#game-context{width:100%;min-height:190px;background:#0d1117;color:#c9d1d9;border:1px solid #30363d;border-radius:6px;padding:10px;font-family:'Courier New',monospace;font-size:.78rem;line-height:1.5;resize:vertical}
#game-context[readonly]{opacity:.65;cursor:default}
.ctx-actions{display:flex;align-items:center;gap:10px}
#start-btn{background:#238636;color:#fff;border:none;border-radius:6px;padding:7px 18px;font-size:.85rem;cursor:pointer;font-weight:600}
#start-btn:hover:not(:disabled){background:#2ea043}
#start-btn:disabled{opacity:.5;cursor:not-allowed}
.started-badge{display:none;font-size:.72rem;color:#3fb950}
#chat-log{display:flex;flex-direction:column;gap:10px}
.welcome{text-align:center;padding:32px 16px;color:#8892a4;font-size:.85rem;border:1px dashed #1f2d45;border-radius:8px}
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
.input-row{display:flex;gap:8px;align-items:center;flex-wrap:wrap}
select{background:#16213e;color:#e0e0e0;border:1px solid #0f3460;border-radius:6px;padding:8px;font-size:.85rem;font-family:inherit}
textarea#prompt{background:#16213e;color:#e0e0e0;border:1px solid #0f3460;border-radius:6px;padding:10px;font-size:.9rem;width:100%;resize:vertical;min-height:60px;font-family:inherit}
textarea#prompt:disabled{opacity:.45;cursor:not-allowed}
.btn-send{background:#e94560;color:#fff;border:none;border-radius:6px;padding:10px 24px;font-size:.9rem;cursor:pointer;font-weight:600}
.btn-send:disabled{opacity:.5;cursor:not-allowed}
.btn-reset{background:transparent;color:#8892a4;border:1px solid #30363d;border-radius:6px;padding:10px 14px;font-size:.85rem;cursor:pointer}
.btn-reset:hover{border-color:#e94560;color:#e94560}
.meta{font-size:.7rem;color:#8892a4}
</style>
</head>
<body>
<header>
  <h1>VERNEX</h1>
  <span class="hdr-sub">&#127918; TEXT ADVENTURE</span>
  <span class="node-info" id="node-info">loading&#8230;</span>
  <div class="hdr-links">
    <a class="hdr-link" href="/ui">&#8592; Chat</a>
    <a class="hdr-link" href="/auth/logout">logout</a>
  </div>
</header>
<main>
  <div class="ctx-box">
    <div class="ctx-hdr" onclick="toggleCtx()">
      <span class="ctx-hdr-label">&#9881; Game Context</span>
      <span class="ctx-arrow" id="ctx-arrow">&#9658;</span>
    </div>
    <div class="ctx-body" id="ctx-body">
      <textarea id="game-context" spellcheck="false">{{ game_context }}</textarea>
    </div>
  </div>

  <div class="ctx-actions">
    <button id="start-btn" onclick="startGame()">&#9654; Start Game</button>
    <span class="started-badge" id="started-badge">&#10003; Context locked &#8212; game in progress</span>
  </div>

  <div id="chat-log">
    <div class="welcome">Click <strong>&#9654; Start Game</strong> above to begin your adventure.</div>
  </div>

  <form id="game-form" onsubmit="sendTurn(event)">
    <div class="input-row">
      <select id="model-select"><option value="mistral">mistral</option></select>
      <span class="meta" id="routed-meta"></span>
    </div>
    <textarea id="prompt" placeholder="What do you do?" disabled></textarea>
    <div class="input-row" style="margin-top:4px">
      <button type="submit" class="btn-send" id="submit-btn" disabled>Send</button>
      <button type="button" class="btn-reset" onclick="resetGame()">&#128260; Reset Game</button>
    </div>
  </form>
</main>
<script>
var _history = [];
var gameStarted = false;

(function loadModels(){
  fetch('/api/models').then(function(r){return r.json();}).then(function(d){
    var sel=document.getElementById('model-select');
    if(d.models&&d.models.length){
      sel.innerHTML='';
      d.models.forEach(function(m){
        var o=document.createElement('option');
        o.value=m;o.textContent=m;
        sel.appendChild(o);
      });
      var pref=d.models.indexOf('gemma4:e4b')>=0?'gemma4:e4b':d.models[0];
      sel.value=pref;
    }
  }).catch(function(){});
})();

(function loadNodeInfo(){
  fetch('/api/status',{credentials:'include'}).then(function(r){return r.json();}).then(function(d){
    document.getElementById('node-info').textContent=(d.node_id||'')+'  ·  v'+(d.version||'');
  }).catch(function(){
    document.getElementById('node-info').textContent='status unavailable';
  });
})();

function toggleCtx(){
  var body=document.getElementById('ctx-body');
  var arrow=document.getElementById('ctx-arrow');
  var open=body.classList.toggle('open');
  arrow.innerHTML=open?'&#9660;':'&#9658;';
}

async function startGame(){
  var ctx=document.getElementById('game-context').value.trim();
  if(!ctx)return;
  var btn=document.getElementById('start-btn');
  btn.disabled=true;btn.textContent='...';

  _history=[
    {role:'system',content:ctx},
    {role:'user',content:'Begin the adventure.'}
  ];

  var model=document.getElementById('model-select').value;
  var log=document.getElementById('chat-log');
  log.innerHTML='';

  document.getElementById('game-context').readOnly=true;
  document.getElementById('started-badge').style.display='inline';

  try{
    var resp=await gameFetch(_history,model);
    _history.push({role:'assistant',content:resp});
    appendMsg('assistant',resp);
    gameStarted=true;
    document.getElementById('prompt').disabled=false;
    document.getElementById('submit-btn').disabled=false;
    document.getElementById('prompt').focus();
    btn.textContent='\\u2713 Started';
  }catch(err){
    log.innerHTML='';
    appendMsg('error','Failed to start: '+err.message);
    document.getElementById('game-context').readOnly=false;
    document.getElementById('started-badge').style.display='none';
    btn.disabled=false;btn.textContent='\\u25ba Start Game';
  }
}

async function sendTurn(e){
  e.preventDefault();
  if(!gameStarted)return;
  var prompt=document.getElementById('prompt').value.trim();
  if(!prompt)return;
  var btn=document.getElementById('submit-btn');
  var model=document.getElementById('model-select').value;

  _history.push({role:'user',content:prompt});
  appendMsg('user',prompt);
  document.getElementById('prompt').value='';
  btn.disabled=true;btn.textContent='...';

  try{
    var resp=await gameFetch(_history,model);
    _history.push({role:'assistant',content:resp});
    appendMsg('assistant',resp);
  }catch(err){
    _history.pop();
    appendMsg('error','Request failed: '+err.message);
  }finally{
    btn.disabled=false;btn.textContent='Send';
    document.getElementById('chat-log').scrollTop=document.getElementById('chat-log').scrollHeight;
  }
}

async function gameFetch(msgs,model){
  var r=await fetch('/api/game/chat',{
    method:'POST',
    headers:{'Content-Type':'application/json'},
    body:JSON.stringify({messages:msgs,model:model})
  });
  var d=await r.json();
  if(d.error&&!d.response)throw new Error(d.error);
  return d.response||d.error||JSON.stringify(d);
}

function appendMsg(role,content){
  var log=document.getElementById('chat-log');
  var div=document.createElement('div');
  div.className='msg '+role;
  if(role==='assistant'){
    div.innerHTML=marked.parse(content,{breaks:true,gfm:true});
  }else{
    div.textContent=content;
  }
  log.appendChild(div);
  log.scrollTop=log.scrollHeight;
}

function resetGame(){
  _history=[];
  gameStarted=false;
  var log=document.getElementById('chat-log');
  log.innerHTML='<div class="welcome">Expand <strong>&#9881; Game Context</strong> above and click <strong>&#9654; Start Game</strong> to begin your adventure.</div>';
  document.getElementById('game-context').readOnly=false;
  document.getElementById('started-badge').style.display='none';
  var btn=document.getElementById('start-btn');
  btn.disabled=false;btn.innerHTML='&#9654; Start Game';
  document.getElementById('prompt').disabled=true;
  document.getElementById('submit-btn').disabled=true;
  document.getElementById('prompt').value='';
  document.getElementById('routed-meta').textContent='';
}
document.getElementById('prompt').addEventListener('keydown',function(e){
  if(e.key==='Enter'&&!e.shiftKey){e.preventDefault();if(gameStarted)sendTurn(e);}
});
</script>
</body>
</html>"""


@app.route("/ui")
def ui():
    return render_template_string(_CHAT_HTML)


@app.route("/game")
def game():
    return render_template_string(_GAME_HTML, game_context=GAME_CONTEXT)


@app.route("/api/models")
def api_models():
    try:
        r = requests.get(f"{OLLAMA_URL}/api/tags", timeout=3)
        models = [m["name"] for m in r.json().get("models", [])]
        return jsonify({"models": models})
    except Exception as e:
        return jsonify({"models": ["mistral", "llama3.1"], "error": str(e)})


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

from flask import Flask, render_template_string, jsonify, request, send_from_directory
import requests
import json
import os

app = Flask(__name__)

NODES = {
    "vernex-node1": "http://localhost:7701",
    "vernex-node2": "http://172.17.0.198:7701",
}

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
    .subtitle { color: #8b949e; font-size: 0.8rem; margin-bottom: 2rem; }
    .grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(420px, 1fr)); gap: 1.5rem; }
    .card { background: #161b22; border: 1px solid #30363d; border-radius: 8px; padding: 1.5rem; }
    .card.online { border-left: 4px solid #3fb950; }
    .card.offline { border-left: 4px solid #f85149; opacity: 0.6; }
    .node-header { display: flex; justify-content: space-between; align-items: center; margin-bottom: 1.2rem; }
    .node-name { font-size: 1.1rem; font-weight: bold; color: #e6edf3; }
    .node-id { font-size: 0.75rem; color: #58a6ff; margin-top: 2px; }
    .badge { font-size: 0.7rem; padding: 3px 10px; border-radius: 12px; font-weight: bold; }
    .badge.online { background: #1a4a1f; color: #3fb950; }
    .badge.offline { background: #3d1a1a; color: #f85149; }
    .stats { display: grid; grid-template-columns: 1fr 1fr; gap: 0.8rem; margin-bottom: 1.2rem; }
    .stat { background: #0d1117; border-radius: 6px; padding: 0.8rem; }
    .stat-label { font-size: 0.7rem; color: #8b949e; text-transform: uppercase; letter-spacing: 1px; margin-bottom: 4px; }
    .stat-value { font-size: 1.3rem; color: #e6edf3; font-weight: bold; }
    .stat-value.blue { color: #58a6ff; }
    .stat-value.green { color: #3fb950; }
    .stat-value.amber { color: #d29922; }
    .partition-bar { margin-top: 0.5rem; }
    .partition-label { font-size: 0.7rem; color: #8b949e; margin-bottom: 6px; text-transform: uppercase; letter-spacing: 1px; }
    .bar-track { background: #0d1117; border-radius: 4px; height: 20px; overflow: hidden; display: flex; }
    .bar-personal { background: #1f6feb; height: 100%; display: flex; align-items: center; justify-content: center; font-size: 0.65rem; color: #e6edf3; }
    .bar-social { background: #3fb950; height: 100%; display: flex; align-items: center; justify-content: center; font-size: 0.65rem; color: #0d1117; font-weight: bold; }
    .footer { margin-top: 2rem; color: #8b949e; font-size: 0.75rem; text-align: center; }
    .refresh-note { color: #8b949e; font-size: 0.7rem; }
    .version { color: #8b949e; font-size: 0.75rem; }
  </style>
</head>
<body>
  <h1>⬡ VERNEX PROTOCOL</h1>
  <p class="subtitle">Network Dashboard — Patent Pending US App. 64/015,885 &nbsp;|&nbsp;
    <span class="refresh-note">Auto-refreshes every 5 seconds</span>
  </p>

  <div class="grid">
    {% for name, data in nodes.items() %}
    <div class="card {{ 'online' if data.online else 'offline' }}">
      <div class="node-header">
        <div>
          <div class="node-name">{{ name }}</div>
          {% if data.online %}
          <div class="node-id">{{ data.node_id }}</div>
          {% else %}
          <div class="node-id" style="color:#f85149">Unreachable</div>
          {% endif %}
        </div>
        <span class="badge {{ 'online' if data.online else 'offline' }}">
          {{ 'ONLINE' if data.online else 'OFFLINE' }}
        </span>
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
      {% endif %}
    </div>
    {% endfor %}
  </div>

  <div class="footer">
    Vernex Protocol v0.2.0 &nbsp;|&nbsp; {{ total_online }} of {{ total_nodes }} nodes online
    &nbsp;|&nbsp; Network score: {{ "%.1f"|format(network_score) }}
  </div>
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
    nodes = {}
    total_online = 0
    network_score = 0.0

    for name, url in NODES.items():
        try:
            r = requests.get(f"{url}/status", timeout=2)
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
            }
            total_online += 1
            network_score += d["contribution_score"]
        except Exception:
            nodes[name] = {"online": False}

    return render_template_string(
        DASHBOARD_HTML,
        nodes=nodes,
        total_online=total_online,
        total_nodes=len(NODES),
        network_score=network_score,
    )

@app.route("/ui")
def ui():
    return send_from_directory(os.path.dirname(__file__), "index.html")


@app.route("/api/nodes")
def api_nodes():
    nodes = {}
    for name, url in NODES.items():
        try:
            r = requests.get(f"{url}/status", timeout=2)
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
            f"{NODES['vernex-node1']}/submit",
            json=request.get_json(),
            timeout=120,
        )
        return r.content, r.status_code, {"Content-Type": "application/json"}
    except Exception as e:
        return jsonify({"error": str(e)}), 503


@app.route("/api/consent", methods=["POST"])
def api_consent():
    try:
        r = requests.post(
            f"{NODES['vernex-node1']}/consent",
            json=request.get_json(),
            timeout=120,
        )
        return r.content, r.status_code, {"Content-Type": "application/json"}
    except Exception as e:
        return jsonify({"error": str(e)}), 503


if __name__ == "__main__":
    app.run(host="0.0.0.0", port=5000, debug=False)

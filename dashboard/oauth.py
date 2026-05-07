"""
Vernex OAuth handler — delegates Google login to the central vernex.net relay.
Runs as a Flask app on 127.0.0.1:5002 (started as daemon thread from app.py).

No Google credentials are stored on individual nodes. The relay at
https://vernex.net:5443 handles the OAuth flow and issues a short-lived RS256
JWT; this handler verifies the JWT and issues a local HMAC session cookie.

nginx auth_request uses:
  GET /auth/verify?role=admin  → 200 OK | 401 not logged in | 403 wrong role
  GET /auth/verify?role=user   → same

Routes proxied through nginx at port 5080:
  GET  /auth/login              — redirect to relay /login
  GET  /auth/complete?token=... — verify JWT, set session cookie, redirect
  GET  /auth/logout             — clear cookie
  GET  /api/me                  — return {email, role} or 401

Config files (auto-created if missing):
  ~/vernex/config/oauth.json   — session_secret, relay_url, redirect_base
  ~/vernex/config/users.json   — {email: {role, enabled}}
"""

import base64
import hashlib
import hmac
import json
import logging
import os
import secrets
import socket
import ssl
import time
import urllib.parse
import urllib.request

from cryptography.hazmat.primitives import hashes, serialization
from cryptography.hazmat.primitives.asymmetric import padding
from flask import Flask, jsonify, make_response, redirect, request

app = Flask("vernex_oauth")

_CONFIG_DIR = os.path.join(os.path.expanduser("~"), "vernex", "config")
_OAUTH_PATH = os.path.join(_CONFIG_DIR, "oauth.json")
_USERS_PATH = os.path.join(_CONFIG_DIR, "users.json")
_COOKIE_NAME = "_vsession"
_SESSION_DAYS = 7


# ── Config ────────────────────────────────────────────────────────────────────

def _detect_lan_ip() -> str:
    """Return the node's LAN IP, preferring the daemon's reported value."""
    try:
        ctx = ssl.create_default_context()
        ctx.check_hostname = False
        ctx.verify_mode = ssl.CERT_NONE
        with urllib.request.urlopen(
            "https://localhost:7701/status", context=ctx, timeout=2
        ) as r:
            data = json.loads(r.read())
        ip = data.get("ip_address", "")
        if ip and ip != "127.0.0.1":
            return ip
    except Exception:
        pass
    try:
        s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
        s.connect(("8.8.8.8", 80))
        ip = s.getsockname()[0]
        s.close()
        if ip and ip != "127.0.0.1":
            return ip
    except Exception:
        pass
    return "127.0.0.1"


def _resolve_redirect_base() -> str:
    """Return redirect_base from oauth.json; auto-detect and persist if missing."""
    try:
        rb = _load_cfg().get("redirect_base", "")
        if rb:
            return rb
    except Exception:
        pass
    ip = _detect_lan_ip()
    rb = f"http://{ip}:5080"
    try:
        cfg = _load_cfg()
        cfg["redirect_base"] = rb
        with open(_OAUTH_PATH, "w") as f:
            json.dump(cfg, f, indent=2)
            f.write("\n")
        os.chmod(_OAUTH_PATH, 0o600)
        logging.info("[oauth] auto-detected redirect_base=%s", rb)
    except Exception:
        pass
    return rb


def _ensure_configs():
    os.makedirs(_CONFIG_DIR, exist_ok=True)
    if not os.path.exists(_OAUTH_PATH):
        default = {
            "session_secret": secrets.token_hex(32),
            "relay_url": "https://vernex.net:5443",
        }
        with open(_OAUTH_PATH, "w") as f:
            json.dump(default, f, indent=2)
            f.write("\n")
        os.chmod(_OAUTH_PATH, 0o600)
        logging.info("[oauth] created %s", _OAUTH_PATH)
    if not os.path.exists(_USERS_PATH):
        with open(_USERS_PATH, "w") as f:
            json.dump({}, f, indent=2)
            f.write("\n")
        os.chmod(_USERS_PATH, 0o600)


def _load_cfg() -> dict:
    with open(_OAUTH_PATH) as f:
        return json.load(f)


def _load_users() -> dict:
    try:
        with open(_USERS_PATH) as f:
            return json.load(f)
    except (FileNotFoundError, json.JSONDecodeError):
        return {}


def _save_users(users: dict):
    with open(_USERS_PATH, "w") as f:
        json.dump(users, f, indent=2)
        f.write("\n")


# ── Session cookie (HMAC-SHA256) ──────────────────────────────────────────────

def _make_cookie(email: str, role: str, secret: str) -> str:
    exp = int(time.time()) + 86400 * _SESSION_DAYS
    payload = f"{email}|{role}|{exp}"
    payload_b64 = base64.urlsafe_b64encode(payload.encode()).decode()
    sig = hmac.new(secret.encode(), payload.encode(), hashlib.sha256).hexdigest()
    return f"{payload_b64}.{sig}"


def _verify_cookie(cookie_val: str, secret: str):
    """Return (email, role) or (None, None)."""
    try:
        dot = cookie_val.rfind(".")
        if dot < 0:
            return None, None
        payload_b64, sig = cookie_val[:dot], cookie_val[dot + 1:]
        payload = base64.urlsafe_b64decode(payload_b64.encode()).decode()
        expected = hmac.new(secret.encode(), payload.encode(), hashlib.sha256).hexdigest()
        if not hmac.compare_digest(sig, expected):
            return None, None
        parts = payload.split("|", 2)
        if len(parts) != 3:
            return None, None
        email, role, exp = parts
        if int(exp) < int(time.time()):
            return None, None
        return email, role
    except Exception:
        return None, None


# ── Relay JWT verification (RS256) ────────────────────────────────────────────

_pubkey_cache: tuple = ()  # (pem: str, fetched_at: float)
_PUBKEY_TTL = 3600


def _fetch_relay_pubkey(relay_url: str) -> str:
    """Fetch relay public key, caching for _PUBKEY_TTL seconds."""
    global _pubkey_cache
    now = time.time()
    if _pubkey_cache and now - _pubkey_cache[1] < _PUBKEY_TTL:
        return _pubkey_cache[0]
    ctx = ssl.create_default_context()
    ctx.check_hostname = False  # relay may use self-signed cert; JWT signature provides auth
    ctx.verify_mode = ssl.CERT_NONE
    req = urllib.request.Request(f"{relay_url}/pubkey")
    with urllib.request.urlopen(req, context=ctx, timeout=5) as r:
        data = json.loads(r.read())
    pem = data["pubkey"]
    _pubkey_cache = (pem, now)
    return pem


def _b64url_decode(s: str) -> bytes:
    return base64.urlsafe_b64decode(s + "==")


def _verify_jwt(token: str, relay_url: str) -> dict:
    """Verify RS256 JWT from the relay. Returns payload dict or raises."""
    parts = token.split(".")
    if len(parts) != 3:
        raise ValueError("invalid JWT format")
    header_b64, payload_b64, sig_b64 = parts
    pem = _fetch_relay_pubkey(relay_url)
    pub_key = serialization.load_pem_public_key(pem.encode())
    msg = f"{header_b64}.{payload_b64}".encode()
    sig = _b64url_decode(sig_b64)
    pub_key.verify(sig, msg, padding.PKCS1v15(), hashes.SHA256())
    payload = json.loads(_b64url_decode(payload_b64))
    if payload.get("exp", 0) < time.time():
        raise ValueError("JWT expired")
    return payload


# ── Route helpers ─────────────────────────────────────────────────────────────

_ROLE_RANK = {"user": 1, "admin": 2}


def _current_user():
    cfg = _load_cfg()
    val = request.cookies.get(_COOKIE_NAME, "")
    if not val:
        return None, None
    return _verify_cookie(val, cfg.get("session_secret", ""))


# ── Routes ────────────────────────────────────────────────────────────────────

@app.route("/auth/verify")
def auth_verify():
    """nginx auth_request — returns 200, 401, or 403."""
    required = request.args.get("role", "user")
    email, role = _current_user()
    if not email:
        return "", 401
    users = _load_users()
    if not users.get(email, {}).get("enabled", False):
        return "", 403
    if _ROLE_RANK.get(role, 0) < _ROLE_RANK.get(required, 0):
        return "", 403
    return "", 200


@app.route("/auth/login")
def auth_login():
    cfg = _load_cfg()
    relay_url = cfg.get("relay_url", "")
    if not relay_url:
        return "<h2>oauth.json missing relay_url</h2>", 503
    redirect_base = cfg.get("redirect_base", "") or _resolve_redirect_base()
    return_url = urllib.parse.quote(f"{redirect_base}/auth/complete", safe="")
    return redirect(f"{relay_url}/login?return={return_url}")


@app.route("/auth/complete")
def auth_complete():
    """Receives JWT from relay, verifies it, sets local session cookie."""
    cfg = _load_cfg()
    relay_url = cfg.get("relay_url", "")
    token = request.args.get("token", "")
    if not token:
        return "Missing token", 400

    try:
        payload = _verify_jwt(token, relay_url)
    except Exception as exc:
        logging.warning("[oauth] JWT verification failed: %s", exc)
        return f"<h2>Login failed</h2><p>Could not verify relay token: {exc}</p>", 401

    email = payload.get("email", "")
    if not email:
        return "No email in JWT", 400

    # Provision user — first login gets admin, subsequent get user
    users = _load_users()
    if email not in users:
        role = "admin" if not users else "user"
        users[email] = {"role": role, "enabled": True}
        _save_users(users)
        logging.info("[oauth] new user %s → %s", email, role)

    user = users[email]
    if not user.get("enabled", True):
        return "Account disabled. Contact the node operator.", 403

    role = user.get("role", "user")
    cookie_val = _make_cookie(email, role, cfg.get("session_secret", ""))
    next_url = "/" if role == "admin" else "/ui"
    resp = make_response(redirect(next_url))
    resp.set_cookie(
        _COOKIE_NAME, cookie_val,
        httponly=True, samesite="Lax",
        max_age=86400 * _SESSION_DAYS,
    )
    return resp


@app.route("/auth/logout")
def auth_logout():
    resp = make_response(redirect("/auth/login"))
    resp.delete_cookie(_COOKIE_NAME)
    return resp


@app.route("/api/me")
def api_me():
    email, role = _current_user()
    if not email:
        return jsonify({"error": "not authenticated"}), 401
    return jsonify({"email": email, "role": role})


# ── Server entry point ────────────────────────────────────────────────────────

def run_oauth_server():
    _ensure_configs()
    logging.getLogger("werkzeug").setLevel(logging.WARNING)
    app.run(host="127.0.0.1", port=5002, threaded=True, debug=False)


if __name__ == "__main__":
    logging.basicConfig(level=logging.INFO)
    run_oauth_server()

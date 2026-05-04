"""
Vernex OAuth handler — Google OAuth 2.0 with role-based signed session cookies.
Runs as a Flask app on 127.0.0.1:5002 (started as daemon thread from app.py).

nginx auth_request uses:
  GET /auth/verify?role=admin  → 200 OK | 401 not logged in | 403 wrong role
  GET /auth/verify?role=user   → same

Routes proxied through nginx at port 5080:
  GET  /auth/login    — redirect to Google
  GET  /auth/callback — exchange code, set cookie, redirect to / or /ui
  GET  /auth/logout   — clear cookie, redirect to /auth/login
  GET  /api/me        — return {email, role} or 401

Config files (auto-created if missing):
  ~/vernex/config/oauth.json   — credentials + session secret (mode 0600)
  ~/vernex/config/users.json   — {email: {role, enabled}} (mode 0600)
"""

import base64
import hashlib
import hmac
import json
import logging
import os
import secrets
import time
import urllib.parse
import urllib.request

from flask import Flask, make_response, redirect, request, jsonify

app = Flask("vernex_oauth")

_CONFIG_DIR = os.path.join(os.path.expanduser("~"), "vernex", "config")
_OAUTH_PATH = os.path.join(_CONFIG_DIR, "oauth.json")
_USERS_PATH = os.path.join(_CONFIG_DIR, "users.json")
_COOKIE_NAME = "_vsession"
_STATE_COOKIE = "_voauth_state"
_SESSION_DAYS = 7

_GOOGLE_AUTH_URL = "https://accounts.google.com/o/oauth2/v2/auth"
_GOOGLE_TOKEN_URL = "https://oauth2.googleapis.com/token"
_GOOGLE_USERINFO_URL = "https://www.googleapis.com/oauth2/v3/userinfo"


# ── Config helpers ────────────────────────────────────────────────────────────

def _ensure_configs():
    os.makedirs(_CONFIG_DIR, exist_ok=True)
    if not os.path.exists(_OAUTH_PATH):
        default = {
            "google_client_id": "",
            "google_client_secret": "",
            "facebook_client_id": "",
            "facebook_client_secret": "",
            "session_secret": secrets.token_hex(32),
            "redirect_base": "http://172.17.0.132:5080",
        }
        with open(_OAUTH_PATH, "w") as f:
            json.dump(default, f, indent=2)
            f.write("\n")
        os.chmod(_OAUTH_PATH, 0o600)
        logging.info("[oauth] created %s — fill in google_client_id/secret", _OAUTH_PATH)
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


# ── Cookie signing ────────────────────────────────────────────────────────────

def _make_cookie(email: str, role: str, secret: str) -> str:
    """Return a signed cookie: base64url(email|role|exp) + '.' + hmac-sha256."""
    exp = int(time.time()) + 86400 * _SESSION_DAYS
    payload = f"{email}|{role}|{exp}"
    payload_b64 = base64.urlsafe_b64encode(payload.encode()).decode()
    sig = hmac.new(secret.encode(), payload.encode(), hashlib.sha256).hexdigest()
    return f"{payload_b64}.{sig}"


def _verify_cookie(cookie_val: str, secret: str):
    """Return (email, role) or (None, None) if invalid/expired/tampered."""
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


# ── Route helpers ─────────────────────────────────────────────────────────────

_ROLE_RANK = {"user": 1, "admin": 2}


def _current_user():
    """Return (email, role) from session cookie, or (None, None)."""
    cfg = _load_cfg()
    secret = cfg.get("session_secret", "")
    cookie_val = request.cookies.get(_COOKIE_NAME, "")
    if not cookie_val:
        return None, None
    return _verify_cookie(cookie_val, secret)


# ── Routes ────────────────────────────────────────────────────────────────────

@app.route("/auth/verify")
def auth_verify():
    """nginx auth_request endpoint. Returns 200, 401, or 403."""
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
    client_id = cfg.get("google_client_id", "")
    redirect_base = cfg.get("redirect_base", "")
    if not client_id:
        return (
            "<h2>OAuth not configured</h2>"
            "<p>Set <code>google_client_id</code> and <code>google_client_secret</code> "
            f"in <code>{_OAUTH_PATH}</code>, then restart the dashboard.</p>"
        ), 503
    state = secrets.token_hex(16)
    params = urllib.parse.urlencode({
        "client_id": client_id,
        "redirect_uri": f"{redirect_base}/auth/callback",
        "response_type": "code",
        "scope": "openid email profile",
        "state": state,
        "access_type": "online",
    })
    resp = make_response(redirect(f"{_GOOGLE_AUTH_URL}?{params}"))
    resp.set_cookie(_STATE_COOKIE, state, httponly=True, samesite="Lax", max_age=600)
    return resp


@app.route("/auth/callback")
def auth_callback():
    cfg = _load_cfg()
    client_id = cfg.get("google_client_id", "")
    client_secret = cfg.get("google_client_secret", "")
    redirect_base = cfg.get("redirect_base", "")
    secret = cfg.get("session_secret", "")

    code = request.args.get("code", "")
    state = request.args.get("state", "")
    stored_state = request.cookies.get(_STATE_COOKIE, "")
    if not state or state != stored_state:
        return "State mismatch — possible CSRF. Try logging in again.", 400
    if not code:
        return "Missing authorization code.", 400

    # Exchange code for access token
    try:
        token_body = urllib.parse.urlencode({
            "code": code,
            "client_id": client_id,
            "client_secret": client_secret,
            "redirect_uri": f"{redirect_base}/auth/callback",
            "grant_type": "authorization_code",
        }).encode()
        req = urllib.request.Request(_GOOGLE_TOKEN_URL, data=token_body, method="POST")
        with urllib.request.urlopen(req, timeout=10) as r:
            token_resp = json.loads(r.read())
        access_token = token_resp.get("access_token", "")
        if not access_token:
            return f"Token exchange failed: {token_resp.get('error_description', 'unknown')}", 502
    except Exception as exc:
        return f"Token exchange error: {exc}", 502

    # Get user info from Google
    try:
        ui_req = urllib.request.Request(
            _GOOGLE_USERINFO_URL,
            headers={"Authorization": f"Bearer {access_token}"},
        )
        with urllib.request.urlopen(ui_req, timeout=10) as r:
            userinfo = json.loads(r.read())
        email = userinfo.get("email", "")
        if not email:
            return "Could not retrieve email from Google.", 502
    except Exception as exc:
        return f"User info error: {exc}", 502

    # Provision user — first login gets admin; subsequent logins get user role
    users = _load_users()
    if email not in users:
        role = "admin" if not users else "user"
        users[email] = {"role": role, "enabled": True}
        _save_users(users)
        logging.info("[oauth] new user %s provisioned as %s", email, role)

    user = users[email]
    if not user.get("enabled", True):
        return "Account disabled. Contact the node operator.", 403

    role = user.get("role", "user")
    cookie_val = _make_cookie(email, role, secret)
    next_url = "/" if role == "admin" else "/ui"
    resp = make_response(redirect(next_url))
    resp.set_cookie(
        _COOKIE_NAME, cookie_val,
        httponly=True, samesite="Lax",
        max_age=86400 * _SESSION_DAYS,
    )
    resp.delete_cookie(_STATE_COOKIE)
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
    log = logging.getLogger("werkzeug")
    log.setLevel(logging.WARNING)
    app.run(host="127.0.0.1", port=5002, threaded=True, debug=False)


if __name__ == "__main__":
    logging.basicConfig(level=logging.INFO)
    run_oauth_server()

"""
Vernex OAuth Relay — single Google OAuth app registration for ALL Vernex nodes.
Runs behind nginx (TLS terminated at port 5443); Flask binds 127.0.0.1:5003.

Stateless design: the OAuth `state` parameter encodes the return URL, signed
with HMAC so no server-side session store is required.

Routes:
  GET /login?return=<node_url>  — validate return URL, redirect to Google
  GET /callback                 — exchange code, issue RS256 JWT, redirect to node
  GET /pubkey                   — return relay RS256 public key (cached by nodes)

Config: /etc/vernex-relay/config.json
Keys:   /etc/vernex-relay/relay.key  (RSA-2048 private, mode 0600)
        /etc/vernex-relay/relay.pub  (RSA public, mode 0644)
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

from cryptography.hazmat.primitives import hashes, serialization
from cryptography.hazmat.primitives.asymmetric import padding
from flask import Flask, jsonify, redirect, request

app = Flask("vernex_relay")

_CONF_DIR = "/etc/vernex-relay"
_CONF_FILE = os.path.join(_CONF_DIR, "config.json")
_KEY_FILE = os.path.join(_CONF_DIR, "relay.key")

_GOOGLE_AUTH = "https://accounts.google.com/o/oauth2/v2/auth"
_GOOGLE_TOKEN = "https://oauth2.googleapis.com/token"
_GOOGLE_USERINFO = "https://www.googleapis.com/oauth2/v3/userinfo"


# ── Key loading (once at startup) ─────────────────────────────────────────────

_priv_key = None
_pub_key_pem = ""


def _load_keys():
    global _priv_key, _pub_key_pem
    with open(_KEY_FILE, "rb") as f:
        _priv_key = serialization.load_pem_private_key(f.read(), password=None)
    _pub_key_pem = (
        _priv_key.public_key()
        .public_bytes(
            encoding=serialization.Encoding.PEM,
            format=serialization.PublicFormat.SubjectPublicKeyInfo,
        )
        .decode()
    )


# ── Config ────────────────────────────────────────────────────────────────────

def _cfg() -> dict:
    with open(_CONF_FILE) as f:
        return json.load(f)


# ── JWT (RS256) ───────────────────────────────────────────────────────────────

def _b64url(data: bytes) -> str:
    return base64.urlsafe_b64encode(data).rstrip(b"=").decode()


def _make_jwt(payload: dict) -> str:
    header = _b64url(json.dumps({"alg": "RS256", "typ": "JWT"}, separators=(",", ":")).encode())
    body = _b64url(json.dumps(payload, separators=(",", ":")).encode())
    msg = f"{header}.{body}".encode()
    sig = _priv_key.sign(msg, padding.PKCS1v15(), hashes.SHA256())
    return f"{header}.{body}.{_b64url(sig)}"


# ── Stateless state parameter (encodes return_url + timestamp, HMAC signed) ──

def _make_state(return_url: str, secret: str) -> str:
    ts = str(int(time.time()))
    payload = f"{ts}|{return_url}"
    sig = hmac.new(secret.encode(), payload.encode(), hashlib.sha256).hexdigest()[:24]
    return _b64url(payload.encode()) + "." + sig


def _decode_state(state: str, secret: str) -> str:
    """Return return_url or raise ValueError."""
    dot = state.rfind(".")
    if dot < 0:
        raise ValueError("malformed state")
    b64_part, sig = state[:dot], state[dot + 1:]
    payload = base64.urlsafe_b64decode(b64_part + "==").decode()
    expected = hmac.new(secret.encode(), payload.encode(), hashlib.sha256).hexdigest()[:24]
    if not hmac.compare_digest(sig, expected):
        raise ValueError("invalid state signature")
    ts, return_url = payload.split("|", 1)
    if int(time.time()) - int(ts) > 600:
        raise ValueError("state expired")
    return return_url


# ── Routes ────────────────────────────────────────────────────────────────────

@app.route("/login")
def login():
    cfg = _cfg()
    return_url = request.args.get("return", "").strip()
    if not return_url.endswith("/auth/complete"):
        return "Invalid return URL — must end in /auth/complete", 400
    parsed = urllib.parse.urlparse(return_url)
    if parsed.scheme not in ("http", "https"):
        return "Invalid return URL scheme", 400

    state = _make_state(return_url, cfg["state_secret"])
    params = urllib.parse.urlencode({
        "client_id": cfg["google_client_id"],
        "redirect_uri": cfg["relay_callback_url"],
        "response_type": "code",
        "scope": "openid email profile",
        "state": state,
        "access_type": "online",
    })
    return redirect(f"{_GOOGLE_AUTH}?{params}")


@app.route("/callback")
def callback():
    cfg = _cfg()
    code = request.args.get("code", "")
    state = request.args.get("state", "")
    if not code or not state:
        return "Missing code or state", 400

    try:
        return_url = _decode_state(state, cfg["state_secret"])
    except ValueError as exc:
        return f"State error: {exc}", 400

    # Exchange code for access token
    try:
        body = urllib.parse.urlencode({
            "code": code,
            "client_id": cfg["google_client_id"],
            "client_secret": cfg["google_client_secret"],
            "redirect_uri": cfg["relay_callback_url"],
            "grant_type": "authorization_code",
        }).encode()
        req = urllib.request.Request(_GOOGLE_TOKEN, data=body, method="POST")
        with urllib.request.urlopen(req, timeout=10) as r:
            token_resp = json.loads(r.read())
        access_token = token_resp.get("access_token", "")
        if not access_token:
            return f"Token exchange failed: {token_resp.get('error_description', 'unknown')}", 502
    except Exception as exc:
        return f"Token exchange error: {exc}", 502

    # Fetch user info
    try:
        ui_req = urllib.request.Request(
            _GOOGLE_USERINFO,
            headers={"Authorization": f"Bearer {access_token}"},
        )
        with urllib.request.urlopen(ui_req, timeout=10) as r:
            userinfo = json.loads(r.read())
        email = userinfo.get("email", "")
        if not email:
            return "No email in Google response", 502
    except Exception as exc:
        return f"User info error: {exc}", 502

    now = int(time.time())
    token = _make_jwt({
        "email": email,
        "name": userinfo.get("name", ""),
        "picture": userinfo.get("picture", ""),
        "iat": now,
        "exp": now + 86400,
    })

    dest = f"{return_url}?token={urllib.parse.quote(token, safe='')}"
    return redirect(dest)


@app.route("/pubkey")
def pubkey():
    return jsonify({"pubkey": _pub_key_pem, "alg": "RS256"})


@app.route("/health")
def health():
    return "ok"


# ── Entry point ───────────────────────────────────────────────────────────────

if __name__ == "__main__":
    logging.basicConfig(level=logging.INFO)
    _load_keys()
    app.run(host="127.0.0.1", port=5003, debug=False, threaded=True)

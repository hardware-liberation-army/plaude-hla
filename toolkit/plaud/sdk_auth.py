import base64
import json
import os
import time as _time
import requests
from .config import SDK_APP_KEY, SDK_APP_SECRET

API_BASE = "https://platform.plaud.cn"
CACHE_PATH = os.path.join(os.path.dirname(__file__), ".sdk_token_cache.json")


def _load_cache() -> dict:
    try:
        with open(CACHE_PATH, "r") as f:
            return json.load(f)
    except (FileNotFoundError, json.JSONDecodeError):
        return {}


def _save_cache(data: dict):
    with open(CACHE_PATH, "w") as f:
        json.dump(data, f)


def get_sdk_token() -> str:
    credentials = f"{SDK_APP_KEY}:{SDK_APP_SECRET}"
    b64 = base64.b64encode(credentials.encode("utf-8")).decode("utf-8")
    resp = requests.post(
        f"{API_BASE}/api/oauth/sdk-token",
        headers={"Authorization": f"Bearer {b64}"},
        timeout=10,
    )
    resp.raise_for_status()
    data = resp.json()
    print(f"[SDK] Got sdk_token (expires in {data.get('expires_in', '?')}s)")
    return data["sdk_token"]


def get_api_token(sdk_token: str) -> str:
    resp = requests.post(
        f"{API_BASE}/api/sdk/api-token",
        headers={"Authorization": f"Bearer {sdk_token}"},
        timeout=10,
    )
    resp.raise_for_status()
    data = resp.json()
    print(f"[SDK] Got api_token (expires in {data.get('expires_in', '?')}s)")
    return data["api_token"]


def get_sdk_config(api_token: str) -> dict:
    resp = requests.get(
        f"{API_BASE}/api/sdk/config",
        headers={"Authorization": api_token},
        timeout=10,
    )
    resp.raise_for_status()
    return resp.json()


def bind_device(api_token: str, owner_id: str, sn: str, sn_type: str = "notepin") -> dict:
    resp = requests.post(
        f"{API_BASE}/api/devices/bind",
        headers={"Authorization": api_token},
        json={"owner_id": owner_id, "sn_type": sn_type, "sn": sn},
        timeout=10,
    )
    resp.raise_for_status()
    return resp.json()


def full_sdk_auth() -> str:
    cache = _load_cache()
    now = _time.time()

    api_token = cache.get("api_token")
    expires_at = cache.get("expires_at", 0)

    if api_token and now < expires_at:
        remaining = int(expires_at - now)
        print(f"[SDK] Using cached api_token (expires in {remaining}s)")
        return api_token

    print("[SDK] Starting SDK token exchange...")
    sdk_token = get_sdk_token()
    api_token = get_api_token(sdk_token)
    try:
        config = get_sdk_config(api_token)
        print(f"[SDK] Config: {config}")
    except Exception as e:
        print(f"[SDK] Config fetch failed (non-critical): {e}")

    _save_cache({"api_token": api_token, "expires_at": now + 3600 * 24 * 30})

    return api_token

import json
import os
import hashlib
import time as _time
from pathlib import Path
from datetime import datetime, timezone, timedelta

import requests
from .config import API_TOKEN, DEVICE_SN

API_BASE = "https://api-apse1.plaud.ai"
DOWNLOAD_DIR = Path("./plaud_cloud_downloads")


def _headers():
    return {"Authorization": f"Bearer {API_TOKEN}", "Content-Type": "application/json"}


def list_file_versions() -> list[dict]:
    resp = requests.get(f"{API_BASE}/file/version", headers=_headers(), timeout=15)
    resp.raise_for_status()
    data = resp.json()
    return data.get("data_version_list", [])


def get_file_details(file_ids: list[str]) -> list[dict]:
    resp = requests.post(
        f"{API_BASE}/file/list",
        headers=_headers(),
        json=file_ids,
        timeout=15,
    )
    resp.raise_for_status()
    data = resp.json()
    return data.get("data_file_list", [])


def get_download_url(file_id: str) -> str:
    resp = requests.post(
        f"{API_BASE}/file/get_download_presigned_url",
        headers=_headers(),
        json={"file_id": file_id},
        timeout=15,
    )
    resp.raise_for_status()
    data = resp.json()
    return data.get("data", "")


def download_file(url: str, output_path: str) -> str:
    resp = requests.get(url, timeout=60)
    resp.raise_for_status()
    Path(output_path).parent.mkdir(parents=True, exist_ok=True)
    with open(output_path, "wb") as f:
        f.write(resp.content)
    return output_path


def _ts_to_str(ts_ms: int, tz_offset: int = 0) -> str:
    dt = datetime.fromtimestamp(ts_ms / 1000, tz=timezone(timedelta(hours=tz_offset)))
    return dt.strftime("%Y-%m-%d_%H%M%S")


def sync_all(output_dir: str | None = None, sn: str | None = None) -> list[str]:
    out = Path(output_dir) if output_dir else DOWNLOAD_DIR
    out.mkdir(parents=True, exist_ok=True)
    sn = sn or DEVICE_SN

    print(f"[CloudSync] Fetching file versions...")
    versions = list_file_versions()
    file_ids = [v["file_id"] for v in versions]
    print(f"[CloudSync] Found {len(file_ids)} files")

    print(f"[CloudSync] Fetching file details...")
    all_files = []
    batch_size = 20
    for i in range(0, len(file_ids), batch_size):
        batch = file_ids[i : i + batch_size]
        details = get_file_details(batch)
        all_files.extend(details)
        print(f"  Got {len(details)} details (batch {i // batch_size + 1})")

    if sn:
        all_files = [f for f in all_files if f.get("serial_number") == sn]
        print(f"[CloudSync] {len(all_files)} files from SN {sn}")

    saved = []
    for i, f in enumerate(all_files):
        fid = f["id"]
        ts = f.get("start_time") or f.get("version_ms", 0)
        tz = f.get("timezone", 0)
        duration = f.get("duration", 0)
        fname = f"{_ts_to_str(ts, tz)}_{fid[:8]}_{duration}s.ogg"
        fpath = str(out / fname)

        if Path(fpath).exists():
            existing_md5 = hashlib.md5(open(fpath, "rb").read()).hexdigest()
            if existing_md5 == f.get("file_md5", ""):
                print(f"  [{i+1}/{len(all_files)}] SKIP (exists): {fname}")
                saved.append(fpath)
                continue

        print(f"  [{i+1}/{len(all_files)}] Downloading: {fname}")
        try:
            url = get_download_url(fid)
            if not url:
                print(f"    No download URL")
                continue
            download_file(url, fpath)
            saved.append(fpath)
            print(f"    OK ({Path(fpath).stat().st_size} bytes)")
        except Exception as e:
            print(f"    FAILED: {e}")

    print(f"\n[CloudSync] Downloaded {len(saved)}/{len(all_files)} files to {out}")
    return saved


def match_ble_download(ble_session_id: int, output_dir: str | None = None) -> str | None:
    out = Path(output_dir) if output_dir else DOWNLOAD_DIR
    out.mkdir(parents=True, exist_ok=True)

    versions = list_file_versions()
    file_ids = [v["file_id"] for v in versions]

    all_files = []
    for i in range(0, len(file_ids), 20):
        batch = file_ids[i : i + 20]
        all_files.extend(get_file_details(batch))

    for f in all_files:
        session = f.get("session_id", 0)
        start = f.get("start_time", 0) // 1000
        if session == ble_session_id or start == ble_session_id or abs(start - ble_session_id) < 5:
            fid = f["id"]
            url = get_download_url(fid)
            if url:
                tz = f.get("timezone", 0)
                duration = f.get("duration", 0)
                fname = f"recording_{ble_session_id}_cloud.ogg"
                fpath = str(out / fname)
                download_file(url, fpath)
                print(f"[CloudSync] Matched session {ble_session_id} -> {fid}")
                print(f"  Saved to {fpath} ({Path(fpath).stat().st_size} bytes)")
                return fpath

    print(f"[CloudSync] No cloud match for session {ble_session_id}")
    return None


if __name__ == "__main__":
    import sys

    if len(sys.argv) > 1 and sys.argv[1] == "match":
        sid = int(sys.argv[2])
        match_ble_download(sid)
    else:
        sync_all()

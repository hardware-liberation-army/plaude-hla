"""Runtime configuration for the Plaud toolkit.

All credentials are loaded from environment variables (or an optional
git-ignored ``.env`` / config file) so that no device secrets are ever
committed to the repository.

Environment variables
---------------------
``PLAUD_API_TOKEN``            A Plaud cloud API JWT.
``PLAUD_SDK_APP_KEY``          SDK application key.
``PLAUD_SDK_APP_SECRET``       SDK application secret.
``PLAUD_DEVICE_SN``            Device serial number (e.g. ``<your-device-sn>``).
``PLAUD_SN_SIGNATURE_B64``     Base64 SN signature blob (from ``/device/sn-sign``).
``PLAUD_RSA_PRIVATE_KEY_PEM``  RSA private key (PEM) used for BLE key exchange.
``PLAUD_RSA_PUBLIC_KEY_PEM``   RSA public key (PEM) sent to the device per session.
``PLAUD_FIRMWARE_PRIVATE_KEY_PEM`` Firmware signing private key (PEM), if used.

Tip: export these in your shell, or drop a ``.env`` file in ``PLAUD_CONFIG``
(named ``.env`` is already git-ignored).
"""

import os
from pathlib import Path


def _load_dotenv(path):
    """Minimal .env loader (KEY=VALUE lines)."""
    try:
        lines = Path(path).read_text().splitlines()
    except OSError:
        return
    for line in lines:
        line = line.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        key, _, value = line.partition("=")
        key = key.strip()
        value = value.strip().strip('"').strip("'")
        # Do not clobber values already set in the environment.
        if key and key not in os.environ:
            os.environ[key] = value


# Load an optional config file (path via PLAUD_CONFIG, else a local .env).
_config_path = os.environ.get("PLAUD_CONFIG", "")
if not _config_path:
    for candidate in (".env", "plaud.env"):
        if Path(candidate).exists():
            _config_path = candidate
            break
if _config_path:
    _load_dotenv(_config_path)


def _get(name, default=""):
    return os.environ.get(name, default)


API_TOKEN = _get("PLAUD_API_TOKEN")
SDK_APP_KEY = _get("PLAUD_SDK_APP_KEY")
SDK_APP_SECRET = _get("PLAUD_SDK_APP_SECRET")
DEVICE_SN = _get("PLAUD_DEVICE_SN")
SN_SIGNATURE_B64 = _get("PLAUD_SN_SIGNATURE_B64")
RSA_PRIVATE_KEY_PEM = _get("PLAUD_RSA_PRIVATE_KEY_PEM")
RSA_PUBLIC_KEY_PEM = _get("PLAUD_RSA_PUBLIC_KEY_PEM")
FIRMWARE_PRIVATE_KEY_PEM = _get("PLAUD_FIRMWARE_PRIVATE_KEY_PEM")

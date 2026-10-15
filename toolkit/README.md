# plaud-toolkit

The **canonical implementation** of the reverse-engineered Plaud Note Pro BLE
protocol. A Python package that scans for, connects to, and downloads
recordings from a Plaud Note Pro device over BLE, then decrypts the
`PLAUD.AI` containers to playable OGG audio.

## Installation

```bash
cd toolkit
python3 -m pip install -r requirements.txt
# or: python3 -m pip install .
```

Requires Python 3.10+.

## Configuration

Credentials are read from environment variables (or an optional `.env` /
`PLAUD_CONFIG` file). **No secrets are committed to the repository.**

| Variable | Purpose |
|---|---|
| `PLAUD_API_TOKEN` | Plaud cloud API JWT |
| `PLAUD_SDK_APP_KEY` | SDK application key |
| `PLAUD_SDK_APP_SECRET` | SDK application secret |
| `PLAUD_DEVICE_SN` | Device serial number |
| `PLAUD_SN_SIGNATURE_B64` | Base64 SN signature blob |
| `PLAUD_RSA_PRIVATE_KEY_PEM` | RSA private key for BLE key exchange |
| `PLAUD_RSA_PUBLIC_KEY_PEM` | RSA public key sent to the device |
| `PLAUD_FIRMWARE_PRIVATE_KEY_PEM` | Firmware signing key (if used) |

See [`plaud/config.py`](plaud/config.py) for the full list. See
[`docs/key-acquisition.md`](../docs/key-acquisition.md) for how to obtain
these values.

## Usage

```bash
# Scan for Plaud devices
python -m plaud scan

# Full SDK auth + connect flow
python -m plaud sdk-connect

# List recordings
python -m plaud list-files

# Download all recordings
python -m plaud download --all

# Download a single recording
python -m plaud download -s <session_id>

# Decrypt a PLAUD.AI container to OGG (see plaud/plaud_decrypt.py)
python -m plaud.plaud_decrypt recording.bin

# Standalone decode of an AVC/OPUS recording to WAV (see plaud/avc_to_ogg.py)
python -m plaud.avc_to_ogg recording.bin -o recording.wav
```

## Package layout

| Module | Purpose |
|---|---|
| `plaud/cli.py` | CLI entry point (`plaud` command / `python -m plaud`) |
| `plaud/ble_transport.py` | BLE scan / connect / GATT read-write / notifications |
| `plaud/protocol.py` | Packet encode/decode, CRC-16, command builders, parsers |
| `plaud/handshake.py` | SN signature chunking + RSA key exchange + handshake |
| `plaud/crypto.py` | ChaCha20-Poly1305 session encryption (`BleCrypto`) |
| `plaud/file_sync.py` | File listing / download / delete orchestration |
| `plaud/sdk_auth.py` | Plaud Cloud SDK OAuth token exchange |
| `plaud/cloud_sync.py` | Cloud-side sync helpers |
| `plaud/plaud_decrypt.py` | `PLAUD.AI` container → OGG decryption |
| `plaud/avc_to_ogg.py` | `PLAUD.AI` container → WAV (Opus decode) |
| `plaud/config.py` | Environment-based configuration |

## Dependencies

- `bleak` — BLE transport (BlueZ on Linux)
- `cryptography` — ChaCha20-Poly1305 AEAD and RSA
- `requests` — Cloud API
- `opuslib`, `numpy` — Opus decoding

## License

See [`LICENSE`](../LICENSE).

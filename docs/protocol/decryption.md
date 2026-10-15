# PLAUD Note Pro — Encryption & Key Management

This document explains the two encryption layers used by the PLAUD Note Pro, what keys are required, where they come from, whether they ever need regeneration, and how the full decryption pipeline works.

---

## Table of Contents

1. [Overview](#overview)
2. [Layer 1: BLE Transport Encryption (Session Keys)](#layer-1-ble-transport-encryption)
3. [Layer 2: File Encryption (Per-File Keys)](#layer-2-file-encryption)
4. [All Keys at a Glance](#all-keys-at-a-glance)
5. [Key Origins & Lifecycle](#key-origins--lifecycle)
6. [Do Keys Need Regeneration?](#do-keys-need-regeneration)
7. [Full Pipeline: From Device to Decrypted Audio](#full-pipeline-from-device-to-decrypted-audio)
8. [Usage](#usage)

---

## Overview

The PLAUD system uses **two independent encryption layers**:

| Layer | Purpose | Algorithm | Key Scope |
|-------|---------|-----------|-----------|
| BLE Transport | Encrypt BLE communication between phone/PC and device | ChaCha20-Poly1305 AEAD | Per-session (ephemeral) |
| File Encryption | Encrypt audio files stored on the device | ChaCha20 (no Poly1305) | Per-file (unique per recording) |

Both layers rely on **RSA-2048** for key exchange, but use different RSA key pairs:
- BLE transport uses a **user RSA key pair** (obtained from the PLAUD API)
- File encryption uses a **separate user RSA key pair** (also obtained from the PLAUD API)

There is also a **cloud API token** (JWT) used to authenticate with PLAUD servers, and **SDK credentials** used to obtain the API token.

---

## Layer 1: BLE Transport Encryption

### Purpose

Encrypts all BLE traffic between the client (phone app or our Python tool) and the PLAUD device. This prevents sniffing or injection of BLE commands.

### Algorithm

**ChaCha20-Poly1305 AEAD** with:
- 32-byte key (E)
- 12-byte nonce (F) — static per session
- 12-byte AAD (G) — static per session
- 4-byte LE counter prepended to plaintext before encryption, incremented per message

### How the Session Keys Are Established

#### 1. SN Signature Exchange (unencrypted)

The client sends a **SN signature** to the device via BLE command `0xFE10`. This is a 256-byte RSA-encrypted blob specific to the device serial number, obtained from the PLAUD cloud API. It proves the client is authorized.

The device responds with command `0xFE12` containing 256 bytes of RSA-encrypted session material.

#### 2. RSA Decryption of Session Material

The client decrypts the 256-byte `0xFE12` response using the **user's RSA private key** (PKCS1v15 padding):

```
RSA-2048 decrypt(secret_data_256_bytes, user_rsa_private_key) -> 80 bytes
```

The 80-byte plaintext contains:

| Offset | Length | Field | Description |
|--------|--------|-------|-------------|
| 0 | 32 | Key (E) | ChaCha20-Poly1305 session key |
| 32 | 12 | Nonce (F) | ChaCha20-Poly1305 nonce |
| 44 | 12 | AAD (G) | ChaCha20-Poly1305 additional authenticated data |
| 56 | 24 | Verification | Encrypted `"PLAUD.AI"` (8 bytes plaintext + 16-byte Poly1305 tag) |

#### 3. Verification

The client decrypts the 24-byte verification ciphertext with the derived key. If the result equals `b"PLAUD.AI"`, the session is established.

#### 4. All Subsequent Communication

Every BLE write/notification after this point uses ChaCha20-Poly1305 with the session keys. The counter provides replay protection and per-message uniqueness despite the nonce being static.

### Key Properties

- **Ephemeral**: New keys are generated every connection. Old session keys cannot decrypt new sessions.
- **Derived from**: RSA key exchange between the user's private key and the device's response.
- **Required to establish**: User RSA private key + SN signature (from cloud API) + SDK token (to call the API).

---

## Layer 2: File Encryption

### Purpose

Encrypts audio recordings stored on the PLAUD device's flash storage. Each recording is a `PLAUD.AI` container file containing ChaCha20-encrypted OGG audio.

### Container Format

```
Offset 0x000:  8 bytes  Magic: "PLAUD.AI"
Offset 0x00A:  2 bytes  Header size (little-endian uint16), typically 512 (0x0200)
Offset 0x084: 12 bytes  UUID (ChaCha20 nonce suffix)
Offset 0x100: 256 bytes RSA-encrypted ChaCha20 file key (PKCS1v15)
Offset 0x200: ...       ChaCha20-encrypted OGG audio payload
```

### Decryption Algorithm

**ChaCha20** (NOT ChaCha20-Poly1305 — no authentication tag on the audio payload) with:
- 32-byte file key (unique per recording, RSA-wrapped in the header)
- 16-byte nonce = `\x00\x00\x00\x00` (4 zero bytes) + 12-byte UUID from header

### Decryption Steps

1. Read the 512-byte PLAUD.AI header from the file
2. Extract the 12-byte UUID at offset `0x084`
3. Extract the 256-byte encrypted key block at offset `0x100`
4. RSA-decrypt the key block using the **E2EE RSA private key** (PKCS1v15):
   ```
   file_key = RSA_decrypt(encrypted_key_block, e2ee_rsa_private_key)  # -> 32 bytes
   ```
5. Construct the ChaCha20 nonce: `\x00\x00\x00\x00` + UUID (16 bytes total)
6. Decrypt the audio payload (everything after the 512-byte header):
   ```python
   cipher = ChaCha20(key=file_key, nonce=nonce)
   audio = cipher.decrypt(file_data[header_size:])
   ```
7. Verify: first 4 bytes of decrypted audio should be `OggS`

### Key Properties

- **Per-file**: Every recording has a unique 32-byte ChaCha20 key, encrypted with the user's E2EE RSA public key.
- **Persistent**: The RSA key pair used for file encryption is long-lived (tied to the user account). As long as you have the private key, you can decrypt any recording ever made while that key was active.
- **No integrity check**: The audio payload uses plain ChaCha20 without Poly1305, so there is no authentication tag to verify.

---

## All Keys at a Glance

| Key / Token | Type | Scope | Where Stored | Source |
|-------------|------|-------|-------------|--------|
| **API Token** (JWT) | HS256 JWT | User account session | `config.py` → `API_TOKEN` | PLAUD API login (`api-apse1.plaud.ai`) |
| **SDK App Key** | String | App identity | `config.py` → `SDK_APP_KEY` | Hardcoded in PLAUD SDK |
| **SDK App Secret** | String | App identity | `config.py` → `SDK_APP_SECRET` | Hardcoded in PLAUD SDK |
| **SN Signature** | 256-byte RSA blob | Per device serial number | `config.py` → `SN_SIGNATURE_B64` | PLAUD cloud API (`platform.plaud.cn`) |
| **User RSA Private Key** (BLE) | RSA-2048 PEM | User account | `config.py` → `RSA_PRIVATE_KEY_PEM` | PLAUD cloud API (observed during app setup) |
| **User RSA Public Key** (BLE) | RSA-2048 PEM | User account | `config.py` → `RSA_PUBLIC_KEY_PEM` | PLAUD cloud API (observed during app setup) |
| **E2EE RSA Private Key** (file decryption) | RSA-2048 PEM | User account | `.plaud_rsa_key` (cached) | PLAUD API `POST /e2ee/gen_key` |
| **BLE Session Key (E)** | 32 bytes | Per BLE connection | Ephemeral (in memory) | Derived from RSA key exchange with device |
| **BLE Session Nonce (F)** | 12 bytes | Per BLE connection | Ephemeral (in memory) | Derived from RSA key exchange with device |
| **BLE Session AAD (G)** | 12 bytes | Per BLE connection | Ephemeral (in memory) | Derived from RSA key exchange with device |
| **Per-File ChaCha20 Key** | 32 bytes | Per recording | Embedded in PLAUD.AI container header | Generated by device firmware, RSA-wrapped |

---

## Key Origins & Lifecycle

### API Token (JWT)

- **Origin**: Obtained when the user logs into their PLAUD account (via the mobile app or web). The token we have was extracted from the app's shared preferences.
- **Format**: JWT with `sub` (user ID), `exp` (expiry), `client_id: "web"`, `region: "aws:ap-southeast-1"`.
- **Expiry**: The current token expires at epoch 1803958225 (~May 2027).
- **Regeneration**: When it expires, obtain a new one by logging into the PLAUD web app and extracting the token from browser local storage or network requests. Alternatively, use the SDK token exchange flow with valid SDK credentials.

### SDK Credentials (App Key + Secret)

- **Origin**: Hardcoded in the PLAUD SDK. Found by decompiling the SDK showcase app (`com.plaud.nicebuild.debug`).
- **Lifespan**: These appear to be long-lived developer credentials. The China platform credentials (`plaud-<china-app-key>`) work on `platform.plaud.cn`. The US credentials (`plaud-<us-app-key>`) require a different API domain.
- **Regeneration**: If revoked, new ones would need to be extracted from an updated SDK or app.

### SN Signature

- **Origin**: Obtained from the PLAUD cloud API (`platform.plaud.cn/api/devices/...`) for a specific device serial number. It is a 256-byte RSA-encrypted blob that the BLE device recognizes as proof of authorization.
- **Lifespan**: Unknown. Possibly long-lived, possibly tied to the device binding.
- **Regeneration**: Re-fetch from the cloud API using valid SDK credentials and the device serial number.

### User RSA Key Pair (BLE Transport)

- **Origin**: Generated during initial device setup. The private key is stored on the PLAUD servers and sent to the app. The public key is sent to the BLE device (either directly during pairing or via the cloud).
- **Location in our codebase**: Hardcoded in `config.py` as `RSA_PRIVATE_KEY_PEM` and `RSA_PUBLIC_KEY_PEM`.
- **Lifespan**: Tied to the user account. Does not change unless the user re-pairs the device or creates a new account.
- **Regeneration**: Not normally needed. If lost, re-pairing the device through the mobile app would generate a new key pair.

### E2EE RSA Private Key (File Decryption)

- **Origin**: Returned by `POST /e2ee/gen_key` on the PLAUD API (`api-apse1.plaud.ai`). The endpoint returns a base64-encoded PEM private key.
- **How it works**: The PLAUD device encrypts each recording's ChaCha20 key with the corresponding RSA public key. The private key returned by this endpoint can decrypt all file keys.
- **Location in our codebase**: Cached in `.plaud_rsa_key` in the project root. Also stored in `config.py` as `RSA_PRIVATE_KEY_PEM` (same key).
- **Lifespan**: **Critical concern** — the endpoint is named `gen_key`, suggesting it may generate a new key pair each time it is called. If a new key is generated, recordings encrypted with the old key become undecryptable unless the old private key was saved.
- **Regeneration**: Run `python3 plaud_decrypt.py --fetch-key` to fetch the current key from the API. **Always save the old key before doing this**, in case the API rotates it.
- **Important**: The same key appears to be returned consistently for the same user account, but this is not guaranteed. The `.plaud_rsa_key` cache file preserves the last-fetched key.

### BLE Session Keys (E, F, G)

- **Origin**: Derived per-connection during the RSA key exchange (Phase 2 of BLE connection).
- **Lifespan**: Ephemeral — only valid for the current BLE session. Discarded on disconnect.
- **Regeneration**: Automatic on every new BLE connection.

### Per-File ChaCha20 Key

- **Origin**: Generated by the PLAUD device firmware when a recording is finalized. Unique per recording.
- **Storage**: Embedded in the PLAUD.AI container header (offset 0x100), RSA-encrypted with the E2EE RSA public key.
- **Lifespan**: Permanent — tied to the recording file. The key does not change.
- **Regeneration**: Not applicable. Each file has a fixed key. If the E2EE RSA private key is lost, the file key cannot be recovered.

---

## Do Keys Need Regeneration?

| Key | Needs Regeneration? | When? | How? |
|-----|---------------------|-------|------|
| API Token | Eventually | When JWT expires (~2027) | Re-login to PLAUD account |
| SDK Credentials | Unlikely | If revoked by PLAUD | Extract from updated SDK/app |
| SN Signature | Possibly | If device is re-paired or signature expires | Re-fetch from cloud API |
| User RSA Keys (BLE) | Rarely | If device is re-paired | Re-pair device via mobile app |
| **E2EE RSA Key** | **Possibly** | **If the API rotates it** | **`python3 plaud_decrypt.py --fetch-key`** |
| BLE Session Keys | Always | Every new connection | Automatic during BLE handshake |
| Per-File Keys | Never | N/A | N/A (unique per file, stored in header) |

### The One Key That Matters Most: E2EE RSA Private Key

The **E2EE RSA private key** (stored in `.plaud_rsa_key`) is the single most important secret. With it, you can decrypt any recording downloaded from the device. Without it, recordings are permanently unreadable.

**Recommendations:**
1. **Back up `.plaud_rsa_key`** to a safe location.
2. **Do not call `--fetch-key` unnecessarily** — if the API generates a new key, old recordings become undecryptable with the new key.
3. If you need to fetch the key (e.g., first setup on a new machine), save the existing key first.

---

## Full Pipeline: From Device to Decrypted Audio

### Method 1: BLE Download + Decrypt

```
1. Authenticate with PLAUD cloud → SDK token → API token
2. BLE scan → find device (MAC address, changes on reboot)
3. BLE connect → send SN signature (0xFE10)
4. Device responds with RSA-encrypted session material (0xFE12)
5. RSA decrypt with user private key → extract ChaCha20-Poly1305 session keys (E, F, G)
6. Verify: decrypt verification ciphertext, check == "PLAUD.AI"
7. Send encrypted handshake (cmd 1) with bind token (JWT sub)
8. List files on device (cmd 26) → get session IDs and sizes
9. Download file via BLE (cmd 28 → type-2 data chunks → cmd 29 tail with CRC16)
   → Result: PLAUD.AI container file (512-byte header + encrypted payload)
10. Decrypt: RSA unwrap per-file key → ChaCha20 decrypt payload → OGG audio
```

### Method 2: Cloud Download (No Decrypt Needed)

```
1. Authenticate with PLAUD API (api-apse1.plaud.ai)
2. GET /file/version → list all file IDs
3. POST /file/list → get file details (name, duration, MD5)
4. POST /file/get_download_presigned_url → get S3 URL
5. Download → already-decrypted OGG file
```

Cloud-stored files are **already decrypted**. The decryption only applies to files downloaded directly from the device via BLE.

### Method 3: Just Decrypt an Existing File

```
python3 plaud_decrypt.py recording_1778072664.ogg
```

This reads the PLAUD.AI container, RSA-unwraps the file key, ChaCha20-decrypts the payload, and writes the OGG output.

---

## Usage

### BLE Download & Decrypt (All-in-One)

```bash
# Download all recordings from device
python3 plaud_tool.py download -a

# Download specific session
python3 plaud_tool.py download -s 1778072664

# Then decrypt the downloaded files
python3 plaud_decrypt.py plaud_downloads/recording_1778072664.ogg
```

### Decrypt an Existing File

```bash
python3 plaud_decrypt.py <input.bin_or_oggpa> [output.ogg]

# With explicit RSA key
python3 plaud_decrypt.py --key /path/to/private.pem recording.bin
```

### Fetch / Refresh the E2EE RSA Key

```bash
# Fetches from API and caches to .plaud_rsa_key
python3 plaud_decrypt.py --fetch-key
```

### Cloud Sync (Download Unencrypted OGG)

```bash
python3 cloud_sync.py            # Download all files
python3 cloud_sync.py match 1778072664  # Download specific session
```

### BLE Commands

```bash
python3 plaud_tool.py scan                      # Scan for devices
python3 plaud_tool.py list-files                # List recordings on device
python3 plaud_tool.py download -a               # Download all
python3 plaud_tool.py download -s <session_id>  # Download specific
python3 plaud_tool.py delete-file -s <id>       # Delete specific
python3 plaud_tool.py delete-file --clear-all   # Delete all
```

---

## File Layout

```
plaud_re/
├── config.py               # All credentials: API token, SDK keys, RSA keys, device SN
├── plaud_decrypt.py         # Standalone file decryptor (ChaCha20 + RSA)
├── plaud_tool.py            # CLI: BLE scan, connect, download, record, delete
├── cloud_sync.py            # Cloud API: download unencrypted OGG files
├── ble_transport.py         # BLE transport layer (Bleak-based)
├── protocol.py              # Wire protocol: packet encode/decode, CRC-16
├── handshake.py             # SN signature chunking, RSA key exchange parsing
├── crypto.py                # ChaCha20-Poly1305 BLE session encryption
├── file_sync.py             # BLE file download manager
├── sdk_auth.py              # SDK OAuth token exchange
├── .plaud_rsa_key           # Cached E2EE RSA private key (for file decryption)
├── .sdk_token_cache.json    # Cached SDK API token
├── plaud_downloads/         # BLE-downloaded recordings (encrypted PLAUD.AI containers)
└── plaud_cloud_downloads/   # Cloud-downloaded recordings (unencrypted OGG)
```

---

## Security Notes

- The E2EE RSA private key in `.plaud_rsa_key` is the critical secret — **back it up**.
- BLE session keys are ephemeral and pose no persistent risk if leaked.
- The API token grants full account access — treat it like a password.
- The SN signature authorizes BLE connections — anyone with it can connect to the device.
- Cloud-stored files are unencrypted OGG — the server can read your recordings. The E2EE only protects files on the physical device.

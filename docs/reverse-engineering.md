# Plaud Note Pro BLE Protocol Reverse Engineering

## Summary

Successfully reverse-engineered the Plaud Note Pro BLE communication protocol and built a Python CLI tool that connects to the device from a Linux desktop, completing the full encrypted handshake and retrieving device state (battery, status, storage, sync time).

## Device Info

| Field | Value |
|---|---|
| Device | Plaud Note Pro |
| Model | 881 |
| Serial | \<your-device-sn\> |
| BLE Address | Random/resolvable (changes each connection) |
| portVersion | 20 (enables encrypted BLE channel) |
| Firmware | v66048 |
| Features | WiFi, OGG audio, stereo microphone |

## BLE GATT Services

| Service UUID | Characteristic UUID | Properties | Purpose |
|---|---|---|---|
| `00001910-0000-1000-8000-00805f9b34fb` | `00002bb0-...` | Notify | Receive encrypted data from device |
| `00001910-0000-1000-8000-00805f9b34fb` | `00002bb1-...` | Write | Send encrypted commands to device |
| `0000180f-0000-1000-8000-00805f9b34fb` | `00002a19-...` | Read | Battery level (standard service) |
| `0000b004-...` | (indicate) | Indicate | Secondary notify channel |

MTU negotiated to 247 (requested 255).

## Protocol Architecture

### Packet Format (unencrypted)

For standard commands (cmd_id <= 255):

```
[0x01] [cmd_id LE uint16] [payload...]
```

For extended commands (cmd_id > 255, e.g. SN signature 0xFE10):

```
[cmd_id LE uint16] [total_chunks uint8] [chunk_index uint8] [payload...]
```

All multi-byte integers are little-endian (confirmed via ARM64 disassembly of `libtnt_ble_utils.so` -- only 6KB, uses `strh` for 16-bit, `strb` for 8-bit stores).

### Encryption Layer (portVersion >= 20)

When the device reports `portVersion >= 20`, all BLE communication after the initial SN signature exchange is encrypted with **ChaCha20-Poly1305** AEAD.

**Encryption parameters:**
- **Algorithm**: ChaCha20-Poly1305
- **Key (E)**: 32 bytes, derived per-session from RSA key exchange
- **Nonce (F)**: 12 bytes, static per session
- **AAD (G)**: 12 bytes, static per session
- **Counter**: 4-byte LE integer prepended to plaintext before encryption, incremented per message (starts at 1)

**Encryption process** (for writes):
1. Increment send counter
2. Prepend 4-byte LE counter to plaintext payload
3. Encrypt with `ChaCha20-Poly1305(key=E, nonce=F, aad=G)`
4. Write ciphertext (including 16-byte auth tag) to BLE characteristic 0x2BB1

**Decryption process** (for reads):
1. Decrypt notification data on 0x2BB0 with `ChaCha20-Poly1305(key=E, nonce=F, aad=G)`
2. Strip 4-byte counter prefix from decrypted plaintext

The encryption is implemented in the Android app's `li.m.e()` method (jadx-decompiled from `classes3.dex`), which uses Conscrypt's ChaCha20-Poly1305 Cipher. The write path is in `v4.G0()` and `v4.p0()`.

### Connection Flow

The complete connection sequence has 4 phases:

#### Phase 1: SN Signature Exchange (unencrypted)

The app sends the device's SN signature (obtained from the Plaud cloud API) in 100-byte chunks via command `0xFE10` (65040):

```
Send: [0xFE10 LE16] [total_chunks uint8] [chunk_idx uint8] [up to 100 bytes]
```

The SN signature is an RSA-encrypted blob (256 bytes) specific to each device serial number, proving the connecting client is authorized via the cloud.

The device responds with command `0xFE12` (65042) in chunks, containing RSA-encrypted session material:

```
Recv: [0xFE12 LE16] [total_chunks uint8] [chunk_idx uint8] [encrypted chunk data]
```

Total response size is 256 bytes (RSA-2048 ciphertext), sent in 3 chunks of 96-100 bytes each.

This phase is unencrypted because the session keys (E, F, G) have not yet been established. The app's write path falls back to raw writes when `E == null || F == null || G == null`.

#### Phase 2: Key Derivation

The app reassembles the 3 chunks from the `0xFE12` response (stripping the 4-byte headers: `12FE03XX`) and RSA-decrypts the 256-byte ciphertext using the user's RSA private key (PKCS1 v1.5 padding):

```python
from cryptography.hazmat.primitives.asymmetric import padding
from cryptography.hazmat.primitives.serialization import load_pem_private_key

private_key = load_pem_private_key(rsa_private_key_pem, password=None)
decrypted = private_key.decrypt(secret_data, padding.PKCS1v15())  # -> 80 bytes
```

The 80-byte plaintext is structured as:

| Offset | Length | Field | Description |
|---|---|---|---|
| 0 | 32 | E (key) | ChaCha20-Poly1305 256-bit key |
| 32 | 12 | F (nonce) | ChaCha20-Poly1305 96-bit nonce |
| 44 | 12 | G (AAD) | ChaCha20-Poly1305 additional authenticated data |
| 56 | 24 | Verification | ChaCha20-Poly1305 ciphertext of `"PLAUD.AI"` |

The app verifies the key exchange by decrypting the 24-byte verification ciphertext:

```python
from cryptography.hazmat.primitives.ciphers.aead import ChaCha20Poly1305

cipher = ChaCha20Poly1305(key=E)
plaintext = cipher.decrypt(nonce=F, data=verification_ciphertext, associated_data=G)
assert plaintext == b"PLAUD.AI"  # 8 bytes plaintext -> 24 bytes ciphertext (8 + 16 tag)
```

If verification passes, the session is established and all subsequent communication uses these keys.

#### Phase 3: Encrypted Handshake

The app sends command 1 (handshake) encrypted with the derived session keys.

**Packet structure** (portVersion >= 20 format):

```
[0x01] [0x01 0x00] [0x02] [0x00] [0x00] [bind_token_hex_ASCII_32_bytes]
```

Breakdown:
- `0x01` -- packet type
- `0x01 0x00` -- cmd_id 1 (LE uint16)
- `0x02` -- handshake sub-type marker
- `0x00` -- sub-type value (0 = old protocol)
- `0x00` -- extra byte (present when portVersion >= 3)
- 32 bytes -- hex-encoded bind token (the user's JWT `sub` field, e.g. `\<your-jwt-sub\>`)

The full plaintext (with 4-byte counter prefix) is then encrypted before writing to BLE. Example:
```
Counter: 02 00 00 00
Payload: 01 01 00 02 00 00 65 62 35 63 ... (38 bytes)
Total plaintext: 42 bytes -> encrypted to 58 bytes (42 + 16 Poly1305 tag)
```

The device responds (encrypted) with its capabilities:

```
[0x01] [0x01 0x00] [status] [portVersion LE16] [timezone LE16] [timezoneMin LE16]
      [audioChannel LE16] [supportWifi LE16] [noNsAgc LE16] [isOggAudio LE16]
```

Example parsed response from our device:
```
status=0, portVersion=20, timezone=8, timezoneMin=0,
audioChannel=257, supportWifi=true, noNsAgc=true, isOggAudio=true
```

#### Phase 4: Post-Handshake Commands

After successful handshake, all subsequent commands are encrypted using the same ChaCha20-Poly1305 session keys with incrementing counters:

| Command | ID | Payload | Description |
|---|---|---|---|
| Sync Time | 4 | `[timestamp LE32] [tz_offset uint8] [tz_min uint8]` | Sync device clock |
| Battery | 9 | (none) | Returns battery percentage |
| Device Status | 3 | (none) | Returns recording/storage state |
| Storage Info | 33 | (none) | Returns storage capacity and usage |
| Recording Control | 8 | `[sub_cmd LE16] [value LE16]` | Start/stop recording |
| Format Storage | 34 | `[0x01]` | Factory reset storage |
| Device Name | 108 (0x6C) | (none) | Get device display name |
| WiFi Switch | 103 | `[on/off]` | Toggle WiFi |
| Disconnect | 5 | `[0x00]` | Graceful disconnect |

Example battery response: `0109000064` -> battery = 100% (0x64)

## Cloud API

### SDK Token Exchange

The Plaud cloud API at `https://platform.plaud.cn` uses a two-step OAuth flow:

**Step 1 -- Get SDK Token:**
```
POST /api/oauth/sdk-token
Authorization: Basic base64(APP_KEY:APP_SECRET)
```

**Step 2 -- Get API Token:**
```
POST /api/sdk/api-token
Authorization: Bearer <sdk_token>
```

Working credentials (CHINA platform):
- `SDK_APP_KEY = "plaud-<your-app-key>"`
- `SDK_APP_SECRET = "aksk_<your-app-secret>"`

US_PROD credentials exist (format `plaud-<...>` / `aksk_<...>`) but fail with
SECRET_KEY_INVALID on `platform.plaud.cn` -- they likely require a different
US-specific domain.

### SN Signature & RSA Keys

The SN signature blob is obtained from the Plaud cloud API (specific to each device serial number). The RSA key pair (2048-bit) is per-user. The private key is used to decrypt the device's session key material; the public key is **sent to the device fresh during each BLE session** (not pre-embedded or pre-loaded). This means any RSA-2048 key pair can be used — it does not need to be the one originally generated during phone-app pairing. See `protocol/key-exchange.md` for details.

## Source Code Structure

The canonical implementation lives in [`toolkit/plaud/`](../toolkit/plaud/):

```
toolkit/plaud/
  config.py          # Env-based config (API token, device SN, RSA keys, SDK creds)
  ble_transport.py   # Bleak-based BLE transport (scan, connect, GATT ops)
  protocol.py        # Wire protocol: packet encode/decode, CRC-16, builders
  handshake.py       # SN signature chunking, RSA key exchange parsing
  crypto.py          # ChaCha20-Poly1305 session encryption/decryption
  sdk_auth.py        # Cloud SDK OAuth flow (token exchange)
  file_sync.py       # File listing / download / delete orchestration
  cli.py             # CLI entry point with subcommands
```

### CLI Usage

```bash
# Scan for devices
python -m plaud scan

# Full encrypted connection flow
python -m plaud sdk-connect

# Raw commands
python -m plaud raw <address> 0x09    # battery
python -m plaud raw <address> 0x03    # device status

# Recording
python -m plaud record <address> start
python -m plaud record <address> stop
```

## Reverse Engineering Method

### Sources Analyzed

1. **Consumer Plaud APK** (`ai.plaud.android.plaud` v3.17.0) -- Flutter-based app with embedded PenBleSDK. Decompiled with jadx. Key classes:
   - `ji/v4.java` -- BLE transport with encryption (G0 = write with encryption check)
   - `ji/p3.java` -- BleAgentImpl (handshake flow, SN signature)
   - `li/m.java` -- Crypto utilities (ChaCha20-Poly1305, RSA PKCS1)
   - `fi/c0.java` -- Handshake packet builder
   - `fi/e0.java` -- SN signature packet (cmd 0xFE10/0xFE12)
   - `li/l.java` -- Byte utilities (trim trailing zero pairs)

2. **SDK Showcase APK** (`com.plaud.nicebuild.debug` from github.com/RobbieInOz/plaud-sdk) -- Reference implementation. Key classes:
   - `BleAgentImpl.java` -- BLE agent with portVersion checks
   - `HandShakeReq.java` -- Handshake packet builder
   - `TntBleCommUtils.java` -- Native packInt wrappers
   - `RetrofitClient.java` -- Default base URL `https://platform.plaud.cn`
   - `AppKeyPairEnv.java` -- Per-environment credentials

3. **Native library** (`lib/arm64-v8a/libtnt_ble_utils.so`) -- Only 6KB. Disassembled ARM64 code confirms little-endian byte order (`strh` for 16-bit, `strb` for 8-bit).

4. **Logcat capture** (`adb logcat` with PenBleSDK tags) -- The breakthrough. Captured a full live connection from the consumer app, which revealed:
   - The key derivation flow (RSA decrypt -> extract ChaCha20 key/nonce/AAD)
   - The "PLAUD.AI" verification step
   - The encrypted sendData format (counter prefix + ChaCha20-Poly1305)
   - The exact handshake payload structure
   - The bind token is the JWT `sub` field, not the device SN

### Key Discoveries

1. **Two-layer encryption**: RSA-2048 for session key exchange, then ChaCha20-Poly1305 for all subsequent communication.

2. **Session keys are ephemeral**: Each connection generates new ChaCha20 keys via the RSA key exchange, so captured traffic from one session cannot decrypt another session's traffic.

3. **The SN signature is per-device**: It must be obtained from the cloud API for each device serial number, creating a server-side authorization gate.

4. **portVersion gating**: The encryption layer is only active when `portVersion >= 20`. The SDK showcase app creates BleDevice objects with portVersion=0 (bypassing encryption), but the consumer app uses the real portVersion from scan data.

5. **Counter-based replay protection**: The 4-byte LE counter prepended to each message provides ordering and replay protection. The ChaCha20 nonce is reused within a session (the counter in the plaintext provides per-message uniqueness).

6. **Key recovery from logcat**: The critical discovery was that the Android app logs the intermediate cryptographic values at `INFO` level (`RSA-chacha20Key`, `RSA-chacha20Nonce`, `RSA-chacha20AD`, `RSA-string: PLAUD.AI`), which allowed us to verify our crypto implementation byte-for-byte against the real app's output.

## Verification Results

Successfully tested against a real Plaud Note Pro device:

```
=== Phase 2: Key Derivation ===
  RSA decrypted: ebf35296a3ff7682... (80 bytes)
  Verification PASSED (expected 'PLAUD.AI')

=== Phase 3: Encrypted Handshake ===
  Handshake response: status=0, portVersion=20, timezone=8, audioChannel=257,
                      supportWifi=true, noNsAgc=true, isOggAudio=true

=== Phase 4: Post-Handshake ===
  Battery: 100%
  Sync time: acknowledged (timestamp matched)
  Device status: OK (recording=false, storage=normal)
```

Crypto implementation verified against captured logcat traffic: encrypted handshake packet matches byte-for-byte with the real Android app's output when using the same session keys.

## Dependencies

- **Python 3.12+**
- **bleak** -- Cross-platform BLE (uses BlueZ on Linux)
- **cryptography** -- ChaCha20-Poly1305 AEAD, RSA PKCS1v15
- **requests** -- Cloud API HTTP calls

## Limitations & Future Work

- **SN signature expiry**: The SN signature may expire and need re-fetching from the cloud API.
- **File transfer**: The protocol supports file sync (cmd 26/28/29/30) for downloading recordings, but this is not yet implemented.
- **WiFi transfer**: The device supports WiFi for bulk data transfer, which would be faster than BLE for audio files.
- **OTA updates**: Command 143/144 for firmware updates is defined but not implemented.
- **US platform**: US_PROD SDK credentials don't work on `platform.plaud.cn` -- need to find the US-specific API domain.
- **Recording control**: Need to implement the sub-command structure for cmd 8 (recording control with various settings).
- **Notification handling**: The `onCharacteristicChanged` callback in the Android app has complex multi-type parsing (type 1=command, 2=file sync, 4=rate test, 5=log) that our tool doesn't fully replicate.

# Firmware Analysis: MFOTA_V1.5.4

Analysis of the Plaud Note Pro firmware binary obtained from `ota.plaud.ai`.

## Firmware Details

| Field | Value |
|-------|-------|
| **File** | `MFOTA_V1.5.4_20260320063826.bin` |
| **URL** | `https://ota.plaud.ai/bins/MFOTA_V1.5.4_20260320063826.bin` |
| **Size** | 7,446,848 bytes (7.1 MB) |
| **Auth** | None required (public CDN) |
| **Last modified** | 2026-03-20 |
| **Magic header** | `PLAUD.AI` (first 8 bytes) |

## Version Check API

```
GET https://api-apse1.plaud.ai/device/version/881?version_code=V66048
```

Returns firmware metadata including download URL. Requires valid API token in the correct region.

## SoC Identification

The device uses a **Realtek RTL8773D** BLE/WiFi SoC. Confirmed by:

- USB device `0bda:8773` (Realtek)
- Firmware strings: `RTL8773DO`, `RTL8763C`, `RTL8763BO`
- Functions: `mmi_findmy_token_rtl8773d_*`

The USB-C port is **charge-only** — the device does not enumerate as a USB device when plugged in. No serial, mass storage, or debug interface exposed over USB.

## Embedded Cryptographic Keys

### Key 1: PLAUD Server Public Key (SN Signature Verification)

```
-----BEGIN PUBLIC KEY-----
MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEAxgcEHxApMLlv3MS3M9Ji
36hexqwLA7H64xYR0JLGNVhQzU6e0JpFTrxC8j8P8tTN74i/g6V7lFRn20FyYWdB
is/lK/e3JWVaj7osoBbRhTvIVT5qu0BV5PImR/wf8aWZl8+vAoo1u1ieVp8pJGoT
hv1rpwCzC/fKpGTSV6Iumkl5yJt1D2UnqrjTlNWtE2hc93m52dOG4cEjB6RHUa11
UTxO7ZrehB6xMOz3fRrW5hLDfSm+db/wT6W1g9F6GAyevcPjmN0QWR29xVGerZJc
FU9ummqei7ARocx9nAGNquwc6XijMl7kPQE9BkX46uLcv2ry3EmFtULfm+nVQCfW
9QIDAQAB
-----END PUBLIC KEY-----
```

- **Purpose**: Verifies SN signatures during BLE pre-handshake
- **Evidence**: Firmware contains strings `verify sn signature failed` / `verify sn signature success` and `signature verify by ras key[plaud]`
- **SN signature format**: PKCS1v15 with SHA-256, verified against this public key
- **Key size**: RSA-2048

### Key 2: Device Embedded RSA Key Pair

```
-----BEGIN RSA PRIVATE KEY-----
<redacted: device-embedded private key removed from public release>
-----END RSA PRIVATE KEY-----
```

- **Purpose**: Likely used for WiFi/TLS or firmware OTA verification
- **Evidence**: Firmware string `signature verify by ras key[default]` suggests a fallback verification key
- **Key size**: RSA-2048
- **Security concern**: This is a **private key** embedded in publicly downloadable firmware. Anyone can extract it. If it's used for anything security-sensitive (TLS, firmware signing), it provides no real security.
- **NOT used for SN signature verification**: Raw RSA recovery of the SN signature with this key yields garbage (no PKCS1v15 padding)

## SN Signature Verification (Confirmed)

The SN signature verification process:

1. Device receives 256-byte SN signature via BLE (cmd 0xFE10)
2. Performs RSA public-key recovery using **Key 1** (PLAUD's embedded public key)
3. Checks for valid PKCS1v15 padding (`0x00 0x01 0xFF...0x00`)
4. Extracts DigestInfo — confirmed to be **SHA-256** (OID 2.16.840.1.101.3.4.2.1)
5. Compares hash against expected value (likely derived from device SN + user info)

Firmware strings confirm the verification flow:
- `verify sn signature failed`
- `verify sn signature success`
- `signature verify by ras key[plaud] failed: %d`
- `signature verify by ras key[default] failed: %d`

**NOTE**: The `[plaud]` and `[default]` labels in log strings suggest a fallback was planned, but **decompilation confirms the `[default]` path is dead code** — it has zero cross-references and is never executed. Only Key 1 is used for verification.

## Firmware OTA Verification

Firmware updates are also signature-verified using Key 1 only:
- `[step2]: verify firmware signature`
- `verify firmware signature failed!!!`
- `firmware signature verify success!`
- `plaud_fota_verify_split`
- `plaud_verify_signature_data`
- `plaud_verify_signature_hash`

The firmware file has a 0x200-byte tail block containing the hash and RSA signature:
```
Offset 0x00: "PLAUD.AI" magic (8 bytes)
Offset 0x08: version/flags (4 bytes)
Offset 0x0C: CRC/timestamp (4 bytes)
Offset 0x10-0xDF: padding (zeros)
Offset 0xE0: SHA-256 hash (32 bytes) of firmware content (excluding tail)
Offset 0x100: RSA-2048 signature (256 bytes) signed by Key 1's private key
```

## Firmware Structure

The firmware starts with a `PLAUD.AI` magic header followed by a structured binary format containing multiple code/data sections. The firmware includes:

- Full mbedTLS/crypto library (RSA, ECDSA, SHA-256/512, ChaCha20-Poly1305)
- BLE stack (GATT, L2CAP)
- WiFi stack (WebSocket, TLS)
- Audio codec (Opus)
- SPI flash management
- DFU (Device Firmware Update) support
- Charger management (YHM2810)

## Ghidra Decompilation Analysis (MFOTA_V1.5.4)

Decompiled with Ghidra 12.0.4 headless mode (ARM Cortex-M LE 32-bit).

### Key Memory Map

| Item | File Offset | Device Address | Description |
|------|-------------|----------------|-------------|
| PLAUD.AI header | 0x000000 | 0x000000 (stripped) | 18KB header before vector table |
| Vector table | 0x00499C | 0x000000 | SP=0x20000004, Reset=0x000AF8AC |
| Key 1 PEM | 0x1C1D30 | 0x004190D8 | PLAUD server public key |
| Key 2 PEM | 0x1C1EF3 | 0x0041929B | Default/embedded key pair |
| Key 2 copy | 0x50E5E8 | (partition 2) | Key 1 in second partition |
| Key 2 copy | 0x50E7AB | (partition 2) | Key 2 in second partition |

Memory mapping: `device_address = file_offset + 0x002573A8` (data section offset)

### SN Signature Verification (FUN_000716d8 at 0x000716D8)

Complete flow confirmed via decompilation:

1. Device receives BLE command `0xFE10` with 256-byte RSA signature (sent in segments)
2. Reassembles segments into buffer at `*(iVar4 + 0x90) + 4`
3. Calls `FUN_000c89da(local_38)` to compute 16-byte hash of device SN
4. Calls `FUN_000a39de(local_38, 0x10, signature_buf, 0x100, DAT_00071c34, key_len)`
5. Inside `FUN_000a39de`:
   - Parses Key 1 PEM via `FUN_00087e1a` (mbedTLS PEM parser, supports RSA and PKCS#8 formats)
   - Hashes 16-byte input with SHA-256 via `FUN_00124b3e` → 32 bytes
   - Verifies `RSA_PKCS1v15(sha256_hash, 256_byte_sig, Key1)` via `FUN_00094cd4`
6. On success: sets auth flag, calls `FUN_00071478` to generate session keys
7. On failure: calls `FUN_00072cd4` (sends error response)

**Key used**: `DAT_00071c34 = 0x004190D8` → **Key 1 only** (PLAUD server public key)

### Firmware OTA Verification (FUN_0008B0FA at 0x0008B0FA)

Multi-step firmware update process:
1. Step 2: Verify firmware signature (`FUN_000a3b92` with Key 1)
2. Step 3: Split firmware into `FOTA.BIN` (CPU) and `WFOTA.BIN` (WiFi modem)
3. Step 4: Start upgrade
4. Step 5: WiFi modem DFU via `FUN_0002da7c`
5. Step 6: CPU DFU with retry logic

The firmware verify function `FUN_000a3b92`:
- Opens firmware file, checks `PLAUD.AI` magic
- Computes SHA-256 hash of entire firmware content
- Verifies RSA signature using Key 1 (DAT_000a44a4 = 0x004190D8)
- No [default] fallback path found in this function

### RSA Key File Storage

- Flash file: `0:/rsa.key` (device stores per-session RSA key here)
- `FUN_000ca584`: Load RSA key from flash into RAM at `uRam000ca8d0` (0x200 bytes)
- `FUN_000c942c`: Alternative load with `plaud_config_load_user_rsa_key`
- `FUN_000ca64a`: Clear RAM and delete flash file (called on `0xFE10` reset)
- `FUN_000ca66c`: Check if key exists (`return *0x000ca8d0 != 0`)

### Key Findings from Decompilation

1. **SN verification uses Key 1 exclusively** — no fallback to Key 2
2. **Key 1 is a public key only** — the corresponding private key exists only on PLAUD's servers
3. **Key 2 is a public/private key pair** — both are in the firmware, but only used for firmware OTA `[default]` fallback
4. **The SN signature format**: `RSA_PKCS1v15_SIGN(SHA-256(SN_hash_16bytes), PLAUD_private_key)`
5. **Firmware also uses Key 1** for OTA signature verification
6. **mbedTLS** is the crypto library (confirmed by function patterns)

### Self-Signed SN Signature Test (FAILED)

Attempted to sign `SHA-256(device_sn)` with Key 2's private key and send it to the device. **The device rejected the signature and disconnected immediately.**

This is confirmed by decompilation: SN verification (`FUN_000a39de`) uses `DAT_00071c34 = Key 1`, with no Key 2 fallback path.

**SN signatures require PLAUD's cloud private key.** The only ways to avoid this are:
1. Cache/reuse the existing SN signature (if it doesn't expire)
2. Use the PLAUD API (`/device/sn-sign`) to fetch fresh signatures
3. Modify the device firmware to skip SN verification (against project constraints)

## How Firmware Was Obtained

1. Connected Android phone via `adb`
2. Ran `adb logcat` while connecting to device via Plaud app
3. App logged: `[REQ] GET https://api-apse1.plaud.ai/device/version/881?version_code=V66048`
4. Response included firmware download URL on `ota.plaud.ai`
5. Downloaded firmware directly from CDN (no auth required)

## Implications

1. **SN signature cannot be forged**: Requires PLAUD's server-side private key (corresponding to Key 1). Confirmed via decompilation — no fallback exists.
2. **Firmware is publicly downloadable**: Anyone can analyze it, extract keys, and study the verification logic
3. **Embedded private key (Key 2)**: Provides no security for firmware OTA. Anyone can sign firmware with it.
4. **Firmware OTA with Key 2 is NOT viable**: The `[default]` key path is **dead code** with zero cross-references. Firmware verification (`FUN_000a3ad0`) only accepts Key 1 signatures. We cannot push modified firmware even though we have Key 2's private key.
5. **Practical path to cloud independence**: Cache the SN signature from `/device/sn-sign` and reuse it. The API token expires ~June 2027, and the SN signature expiry is unknown but likely long-lived.
6. **Ghidra project**: Available at `/tmp/ghidra_project/PlaudFW` for further analysis
7. **Full decompilation output**: See `/tmp/plaud_decompiled_sn_verify.c`, `/tmp/plaud_deep_decompiled.c`, `/tmp/plaud_key_load.c`, `/tmp/plaud_ota_analysis.c`

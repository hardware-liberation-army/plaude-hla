# Ghidra Decompilation Findings: MFOTA_V1.5.4

Decompiled with Ghidra 12.0.4 headless mode (ARM:LE:32:Cortex, raw binary).

## Firmware Memory Layout

The firmware is a raw binary loaded at address 0x0 in Ghidra. On the device, code and data are at different base addresses.

| Item | File Offset | Device Address | Notes |
|------|-------------|----------------|-------|
| PLAUD.AI header | 0x000000 | — | 18KB header, contains partition metadata |
| ARM vector table | 0x00499C | 0x000000 (after strip) | SP=0x20000004, Reset=0x000AF8AC |
| Code section | 0x00499C+ | 0x000000+ | ARM Thumb-2 code |
| Key 1 PEM (PLAUD server public) | 0x1C1D30 | 0x004190D8 | RSA-2048 public key |
| Key 2 PEM (default key pair) | 0x1C1EF3 | 0x0041929B | RSA-2048 public key (private also embedded) |
| Partition 2 starts | 0x34D000 | — | Identical copy of firmware |
| Key 1 PEM copy | 0x50E5E8 | — | In partition 2 |
| Key 2 PEM copy | 0x50E7AB | — | In partition 2 |
| Firmware tail (sig block) | 0x719E00 | — | PLAUD.AI magic + SHA-256 + RSA sig |

Data section mapping: `device_address = file_offset + 0x002573A8`

## SN Signature Verification

### Function: `FUN_000716d8` at 0x000716D8

Entry point for BLE command `0xFE10` (SN signature). The phone sends a 256-byte RSA signature in segmented BLE packets.

**Flow:**
1. Check if already authenticated (`auth_already_done` if `*(state + 0x80) != 0`)
2. Accept only commands `0xFE10` (signature) or `0xFE20` (reset)
3. Allocate 0x204-byte buffer, reassemble segments
4. When all segments received (total = 0x100 = 256 bytes):
   - `FUN_000c89da(local_38)` — compute 16-byte hash from SN
   - `thunk_FUN_0004d53a(DAT_00071c34)` — get length of verification key (strlen)
   - `FUN_000a39de(local_38, 0x10, buffer+4, 0x100, DAT_00071c34, keylen+1)` — verify signature
5. On success (`iVar11 == 0`):
   - Log `"verify sn signature success"`
   - `FUN_000ca66c()` — check if RSA key exists in flash
   - If key exists: set auth flag `*(state + 0x8c) = 1`, call `FUN_00071478()` to generate session keys
   - If no key: log `"rsa key not exist"`, call `FUN_00072cd4()`
6. On failure:
   - Log `"verify sn signature failed"`
   - Clean up buffer, return

### Function: `FUN_000a39de` at 0x000A39DE (RSA Signature Verify)

Core signature verification using mbedTLS.

```c
FUN_000a39de(hash_16bytes, 0x10, signature_256bytes, 0x100, key_pem_ptr, key_pem_len)
```

1. `FUN_00094bd0(ctx)` — init mbedTLS PK context
2. `FUN_00087e1a(ctx, key_pem, key_len)` — parse PEM key (supports `RSA PUBLIC KEY` and `PUBLIC KEY` formats)
3. `FUN_00094bc2(ctx)` — verify it's a public key (returns 1)
4. `FUN_00124b3e(hash_16, 0x10, output_32, 0)` — hash the 16-byte input to 32 bytes (SHA-256)
5. `FUN_00094cd4(ctx, 9, hash_32, 0x20, sig, sig_len)` — `mbedtls_pk_verify` with MD_SHA256 (algo=9)
6. On any failure: log and halt

**Key pointer**: `DAT_00071c34 = 0x004190D8` → **Key 1 only**. No fallback.

### Function: `FUN_00087e1a` at 0x00087E1A (PEM Key Parser)

Parses PEM-encoded public keys in multiple formats:
1. Try `-----BEGIN RSA PUBLIC KEY-----` / `-----END RSA PUBLIC KEY-----`
2. Try `-----BEGIN PUBLIC KEY-----` / `-----END PUBLIC KEY-----`
3. If both fail, try DER format

### Function: `FUN_00071478` at 0x00071478 (Session Key Generation)

Called after successful SN verification:
1. Generate 32 random bytes (encryption key material)
2. Derive two 12-byte values (nonce/IV)
3. Store key material at `state + 0x98`
4. Send encrypted session response via BLE (command 0x03, characteristic 3)
5. Sends in 100-byte chunks up to 256 bytes

### Function: `FUN_00072cd4` at 0x00072CD4 (SN Verify Fail — No Key)

Sends BLE response 0xFE11 with the RSA key existence flag. If no key exists in flash, tells the phone to send one.

## RSA Key Management

### Flash File: `0:/rsa.key`

The device stores the per-session RSA key (sent by phone during handshake) in flash.

| Function | Address | Purpose |
|----------|---------|---------|
| `FUN_000ca584` | 0x000CA584 | Load RSA key from flash into RAM (`uRam000ca8d0`, 0x200 bytes) |
| `FUN_000c942c` | 0x000C942C | Alternative load (`plaud_config_load_user_rsa_key`) |
| `FUN_000ca64a` | 0x000CA64A | Clear RAM buffer + delete `0:/rsa.key` from flash |
| `FUN_000ca66c` | 0x000CA66C | Check if key exists: `return *0x000ca8d0 != 0` |

## Firmware OTA Verification

### Function: `FUN_0008B0FA` at 0x0008B0FA (OTA Upgrade Orchestrator)

Multi-step firmware update:

1. **Condition check**: `FUN_0002166a()` (WiFi available?), `FUN_00023ca4()` (not in USB mode?), file size checks
2. **Step 2**: `FUN_000a3b92("0:/MFOTA.BIN", 0x8A35B)` — verify firmware signature
3. **Step 3**: `FUN_0008a4b6()` — split `MFOTA.BIN` into `FOTA.BIN` (CPU) + `WFOTA.BIN` (WiFi modem)
4. **Step 4**: Start upgrade, send progress
5. **Step 5**: WiFi modem DFU (`FUN_0002da7c`, timeout ~120s)
6. **Step 6**: CPU DFU with retry logic (up to 3 retries)

### Function: `FUN_000a3b92` at 0x000A3B92 (Firmware Signature Verify)

```c
FUN_000a3b92("0:/MFOTA.BIN", 0x8A35B)
```

1. Open firmware file, get size
2. Seek to `size - 0x200`, read last 0x200 bytes
3. Check for `"PLAUD.AI"` magic in tail block
4. Seek to offset 0, compute SHA-256 of `size - 0x200` bytes
5. Compare computed hash with stored hash in tail block (0x20 bytes at tail+0xE0)
6. `FUN_000a3ad0(hash, 0x20, sig_in_tail, 0x100, DAT_000a44a4, strlen+1)` — verify RSA signature
7. **Uses Key 1 only** (`DAT_000a44a4 = 0x004190D8`)

### Function: `FUN_000a3ad0` at 0x000A3AD0 (Firmware RSA Verify)

Same structure as `FUN_000a39de` but for firmware:
1. Parse key PEM from `DAT_000a44a4` (Key 1 only)
2. `mbedtls_pk_verify(ctx, SHA256, hash, 0x20, signature, 0x100)`
3. **No Key 2 fallback** — single key, single verify

### Firmware Tail Block Format

Last 0x200 bytes of the firmware file:

```
0x00-0x07: "PLAUD.AI" (8 bytes, magic)
0x08-0x0B: version/flags (4 bytes: 00 01 00 01)
0x0C-0x0F: CRC/timestamp (4 bytes)
0x10-0xDF: zero padding
0xE0-0xFF: SHA-256 hash (32 bytes) of firmware content
0x100-0x1FF: RSA-2048 signature (256 bytes) of the hash, signed by Key 1's private key
```

## BLE FOTA Mechanism

The device supports firmware updates over BLE with full protocol handling:

| Function | Purpose |
|----------|---------|
| `mmi_ble_handle_fota_push_ind` | Receives FOTA push indication (uid, version, size, CRC) |
| `mmi_ble_fota_data_handle` | Handles incoming FOTA data packets |
| `mmi_ble_handle_fota_segment_data` | Handles FOTA segment data |
| `mmi_ble_handle_fota_package_data` | Handles FOTA package data |
| `plaud_fota_write_cache` | Writes received data to cache |
| `plaud_fota_check_done` | Checks if download is complete |
| `mmi_ble_enter_fota_download_process` | Enters FOTA download mode |
| `mmi_ble_exit_fota_download_process` | Exits FOTA download mode |
| `mmi_ble_send_fota_result_ind` | Sends FOTA result indication |
| `fota_upgrade_thread_entry` | OTA upgrade thread (verify → split → flash) |

**FOTA Protocol Flow:**
1. Phone sends FOTA PUSH IND with metadata (uid, version, file_size, CRC16)
2. Device enters download mode, sends package requests
3. Phone sends firmware data in segments
4. Device writes to `0:/MFOTA.BIN`
5. After download complete: verify signature → split → flash CPU + WiFi modem
6. Device sends result indication (success/failure)

**CRC verification**: The FOTA protocol uses CRC16 for data integrity during transfer. This is separate from the cryptographic signature verification.

## Key 2 `[default]` Path Analysis

### Status: **DEAD CODE**

Evidence:
- String `"signature verify by ras key[default] failed: %d"` at 0x000A452C has **zero cross-references**
- Key 2 PEM at device address 0x0041929B has **zero cross-references**
- The pointer to Key 2 at file offset 0x000A4524 has **zero cross-references**
- No function in the 0x000A3800-0x000A5000 range implements a Key 2 fallback

The `[default]` key path appears to be either:
- Planned but never implemented
- From an older firmware version, removed but strings remain
- A future feature that's not yet active

**Practical impact**: Even though Key 2's private key is embedded in the firmware, it cannot be used to sign modified firmware for OTA updates. The device will reject any signature not made with Key 1's private key.

## Embedded Keys Summary

| Key | Type | File Offset | Device Addr | Used For | Private Key Available |
|-----|------|-------------|-------------|----------|-----------------------|
| Key 1 | RSA-2048 public | 0x1C1D30 | 0x004190D8 | SN verify + firmware OTA | No (server-side only) |
| Key 2 | RSA-2048 pub+priv | 0x1C1EF3 | 0x0041929B | Dead code / unused | Yes (in firmware) |

## Crypto Library

The firmware uses **mbedTLS** (PolarSSL). Confirmed function patterns:
- `FUN_00094bd0` = `mbedtls_pk_init`
- `FUN_00087e1a` = `mbedtls_pk_parse_key` (PEM parser wrapper)
- `FUN_00094bc2` = `mbedtls_pk_can_do` (check key type)
- `FUN_00094cd4` = `mbedtls_pk_verify` (RSA PKCS#1 v1.5)
- `FUN_00094bd8` = `mbedtls_pk_free`
- `FUN_001240b8` / `FUN_001240d0` = `mbedtls_sha256_init` / `mbedtls_sha256_starts`
- `FUN_0012498c` = `mbedtls_sha256_update`
- `FUN_00124a2a` = `mbedtls_sha256_finish`

## Conclusions

1. **SN signature bypass is not possible** without PLAUD's server private key (Key 1)
2. **Firmware OTA bypass is not possible** — verification uses Key 1 only, Key 2 fallback is dead code
3. **SN signature replay works** — the device accepts the same SN signature repeatedly (no nonce/challenge)
4. **BLE FOTA mechanism works** — but requires a valid Key 1 signature on the firmware
5. **Only path to cloud independence**: Cache SN signature from `/device/sn-sign` API, replay on each connection

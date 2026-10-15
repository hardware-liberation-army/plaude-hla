# Plaud BLE File Sync - Progress Log

## Status: WORKING (listing + full download)

As of 2026-05-06, we can **list and fully download** recordings from the Plaud Note Pro via BLE.

## What Works
- Full encrypted BLE handshake (SN signature → RSA key exchange → encrypted handshake)
- File listing via cmd 26 (getRecSessions)
- File download via cmd 28 (syncFileStart) — full file received, exact size match
- Downloaded file has `PLAUD.AI` custom container format with metadata header

## Key Breakthrough
The missing piece was **cmd 6 (sync statistics)** sent immediately before cmd 26 in the init sequence. Without cmd 6, getRecSessions always returned `totals=0`.

## Init Sequence (Required Before File Operations)
```
1. SN Signature exchange (0xFE10 / 0xFE12) — unencrypted
2. RSA key derivation + ChaCha20-Poly1305 crypto setup
3. Encrypted handshake (cmd 1) with JWT sub as bind token
4. Post-handshake init (all encrypted):
   - cmd 4  (sync time)
   - cmd 9  (battery)
   - cmd 3  (device status)
   - cmd 8 sub 0x0F
   - cmd 8 sub 0x11
   - cmd 108 (device name)
   - cmd 141
   - cmd 108 (device name again)
   - cmd 25 (file sync init, payload 0x00)
   - cmd 3  (device status)
   - cmd 8 sub 0x17
   - cmd 24
   - cmd 8 sub 0x14, 0x13, 0x18, 0x19, 0x1F, 0x1B, 0x20
   - cmd 6  (sync statistics) ← CRITICAL, must come before cmd 26
5. cmd 26 (getRecSessions) — lists files
6. cmd 28 (syncFileStart) — downloads file
```

## Current Issues

### PLAUD.AI container format
- Downloaded files use a custom `PLAUD.AI` container, not raw OGG
- Container has: magic header (`PLAUD.AI`), userid, recording metadata, then audio data
- Need to parse/strip the container to extract playable OGG audio
- The container likely includes timestamp index data for the audio

### No CRC verification
- Device returns `0xFFFF` for all file CRCs — need to determine if universal
- CRC check should be skipped when `0xFFFF` is returned

## Files
| File | Purpose |
|------|---------|
| `plaud_tool.py` | CLI entry point, init sequence, file sync subcommands |
| `file_sync.py` | FileSyncManager — list/download/delete orchestration |
| `protocol.py` | Command builders, parsers, data models |
| `crypto.py` | BleCrypto — ChaCha20-Poly1305 encrypt/decrypt |
| `handshake.py` | SN signature + RSA public key chunked packet builders |
| `ble_transport.py` | BleTransport — bleak-based BLE scan/connect/write/notify |
| `sdk_auth.py` | Plaud cloud SDK token exchange (with local cache) |
| `config.py` | Device SN, RSA keys, API tokens |

## Logcat Captures
- `/tmp/plaud_full_log.txt` — original capture (app downloads 1 file)
- `/tmp/plaud_fresh_log.txt` — fresh capture (app downloads 4 files, including 2 BLE-initiated)

## Official App Sequence (from fresh logcat)
```
handshake → sync time → battery → device status
→ cmd 8 sub 0x0F, 0x11 → device name (108) → cmd 141 → device name (108)
→ cmd 25 (sync init) → device status
→ cmd 8 sub 0x17 → cmd 24 → cmd 8 sub 0x14, 0x13, 0x18, 0x19, 0x1F, 0x1B, 0x20
→ cmd 6 (sync statistics)
→ cmd 26 (getRecSessions) → gets 4 files
→ cmd 28 (download file 1) → type-2 data → cmd 29 (tail) → cmd 30 (delete file 1)
→ cmd 28 (download file 2) → ... → cmd 30 (delete file 2)
→ cmd 28 (download file 3) → ... → cmd 30 (delete file 3)
→ cmd 28 (download file 4) → ... → cmd 30 (delete file 4)
→ cmd 109 (setting change)
```

## cmd 26 (getRecSessions) Parameters
- `sessionId`: timestamp-based value (e.g. `0x69FB3A7D`). Used as a sync marker, echoed in response.
- `uid`: `0x00000000` in all observed captures
- `isFullSync`: `0x00` (false) — works even though named "incremental"

## Notable Observations
- Device status byte 3 is `01` regardless of whether files exist (was `03` in original logcat — difference unexplained but irrelevant)
- The official app **deletes files from device after downloading** via cmd 30
- Small BLE-initiated recordings (52-88 bytes) are stubs/metadata, not full audio
- Physical button recordings are larger (52KB) and contain actual audio data
- The `EMPTY_PACKAGE,code: 1` warning appears in the app for every download — seems normal
- The `sessionId miss match` warning in the app is also normal during downloads

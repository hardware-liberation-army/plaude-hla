# aplaud — Plaud BLE to Transcription Pipeline

A Go CLI that scans for a Plaud Note Pro device, downloads all recordings via BLE,
decrypts them, transcribes them with whisper.cpp, and deletes them from the device.

## CLI Design

```
aplaud scan                    # Scan for Plaud BLE devices
aplaud download                # Connect, download all files, decrypt to OGG
aplaud decrypt <file>          # Decrypt a PLAUD.AI container to OGG
aplaud transcribe <file.ogg>   # Transcribe an OGG file via whisper.cpp
aplaud oneshot                 # Full pipeline: scan → download → decrypt → transcribe → delete
```

`oneshot` is the primary workflow. The other subcommands exist for debugging or partial runs.

## `oneshot` Flow

```
1. Load config from YAML (~/.config/aplaud/config.yaml)
2. SDK auth — get/refresh API token (cached locally)
3. BLE scan → find Plaud device (filter by service UUID 0x1910)
4. Phase 1: Send SN signature in 100-byte chunks (cmd 0xFE10, unencrypted)
5. Phase 2: Collect 0xFE12 response chunks, RSA decrypt 256→80 bytes
            Extract: key[0:32], nonce[32:44], AAD[44:56], verify[56:80]
            Verify "PLAUD.AI" with ChaCha20-Poly1305
6. Phase 3: Encrypted handshake (cmd 1) with JWT sub as bind token
7. Phase 4: Post-handshake init sequence (all encrypted):
   - cmd 4  (sync time)
   - cmd 9  (battery)
   - cmd 3  (device status)
   - cmd 8 sub 0x0F, 0x11
   - cmd 108 (device name)
   - cmd 141
   - cmd 108 (device name again)
   - cmd 25 (file sync init, payload 0x00)
   - cmd 3  (device status)
   - cmd 8 sub 0x17
   - cmd 24
   - cmd 8 sub 0x14, 0x13, 0x18, 0x19, 0x1F, 0x1B, 0x20
   - cmd 6  (sync statistics) ← CRITICAL, must come before cmd 26
8. List files (cmd 26) → get session IDs and sizes
9. For each file:
   a. Download (cmd 28 → type-2 data chunks → cmd 29 tail with CRC)
   b. Decrypt PLAUD.AI container (RSA unwrap per-file key → ChaCha20 decrypt → OGG)
   c. Transcribe OGG via whisper.cpp CGo bindings
   d. Delete from device (cmd 30)
   e. Save: recording_<session_id>.ogg + recording_<session_id>.txt
10. Disconnect
```

## Project Structure

```
aplaud/
├── main.go                     # CLI entry point (cobra), subcommand routing
├── internal/
│   ├── config/
│   │   └── config.go           # YAML config loading, defaults, validation
│   ├── ble/
│   │   ├── transport.go        # BlueZ D-Bus BLE via go-bluetooth
│   │   └── uuid.go             # Service/characteristic UUID constants
│   ├── protocol/
│   │   ├── packet.go           # LE pack/unpack, CRC-16 (modbus 0xA001)
│   │   ├── commands.go         # All command builders (handshake, file sync, etc.)
│   │   └── responses.go        # Response parsers (file list, sync head/tail, data)
│   ├── crypto/
│   │   ├── session.go          # ChaCha20-Poly1305 AEAD for BLE session encryption
│   │   ├── rsa.go              # RSA PKCS1v15 decrypt, PEM key loading
│   │   └── decrypt.go          # PLAUD.AI container → OGG (ChaCha20 file decrypt)
│   ├── handshake/
│   │   └── handshake.go        # SN signature chunking, 4-phase connect flow
│   ├── filesync/
│   │   └── filesync.go         # File listing, downloading, deleting over BLE
│   ├── auth/
│   │   └── sdk.go              # SDK OAuth token exchange + JSON cache
│   └── whisper/
│       ├── whisper.go          # CGo bindings to whisper.cpp (libwhisper)
│       └── convert.go          # OGG → 16kHz PCM WAV conversion (for whisper input)
├── go.mod
├── go.sum
├── config.example.yaml         # Example config with all fields documented
├── PLAN.md                     # This file
└── Dockerfile                  # Build container with bluez + whisper.cpp + opus tools
```

## Dependencies

### Go Libraries
| Package | Purpose |
|---------|---------|
| `github.com/muka/go-bluetooth` | BlueZ D-Bus BLE (scan, connect, GATT, notify) |
| `github.com/spf13/cobra` | CLI subcommands |
| `golang.org/x/crypto/chacha20poly1305` | BLE session encryption (AEAD) |
| `golang.org/x/crypto/chacha20` | File decryption (plain ChaCha20, no Poly1305) |
| `gopkg.in/yaml.v3` | Config file parsing |
| CGo to `libwhisper` | whisper.cpp transcription |

### System Dependencies
| Package | Purpose |
|---------|---------|
| `bluez` | Linux BLE stack (D-Bus API) |
| `libwhisper.so` | whisper.cpp shared library (from existing build) |
| `libggml.so` | ggml backend for whisper |
| `opusdec` or `ffmpeg` | OGG → WAV conversion for whisper input |
| `whisper model` | ggml model file (user must download, e.g. ggml-base.en.bin) |

### Existing whisper.cpp Build
The existing whisper.cpp build at `/var/home/lvturner/Documents/Code/3rdParty/whisper.cpp`:
- Binary: `build/bin/whisper-cli`
- Libraries: `build/src/libwhisper.so`, `build/ggml/src/libggml.so`
- CGo bindings will link against these libraries

## Configuration

Config file: `~/.config/aplaud/config.yaml`

```yaml
# Plaud cloud API credentials
api_token: "<your_jwt_token>"
sdk_app_key: "plaud-<your-app-key>"
sdk_app_secret: "aksk_<your-app-secret>"

# Device identity
device_sn: "<your-device-sn>"
sn_signature_b64: "<base64 sn signature from /device/sn-sign>"

# RSA keys (BLE transport + file decryption)
rsa_private_key_pem: |
  -----BEGIN PRIVATE KEY-----
  ...
  -----END PRIVATE KEY-----
rsa_public_key_pem: |
  -----BEGIN PUBLIC KEY-----
  ...
  -----END PUBLIC KEY-----

# E2EE key for file decryption (separate RSA key fetched from /e2ee/gen_key)
e2ee_rsa_key_path: ~/.config/aplaud/plaud_rsa_key

# SDK token cache
sdk_token_cache_path: ~/.config/aplaud/sdk_token_cache.json

# Output
download_dir: ~/Recordings/Plaud

# Whisper transcription
whisper:
  model_path: ~/.config/aplaud/ggml-base.en.bin
  language: en
  # CGo library paths (if not in system lib path)
  lib_path: /var/home/lvturner/Documents/Code/3rdParty/whisper.cpp/build/src
  ggml_lib_path: /var/home/lvturner/Documents/Code/3rdParty/whisper.cpp/build/ggml/src
```

## Encryption Details

### Layer 1: BLE Transport (ChaCha20-Poly1305 AEAD)
- Session keys derived from RSA-2048 key exchange during handshake
- 32-byte key, 12-byte nonce (static per session), 12-byte AAD (static per session)
- 4-byte LE counter prepended to plaintext before encryption, incremented per message
- Counter starts at 1, incremented before use
- Ephemeral — new keys every connection

### Layer 2: File Encryption (ChaCha20, no Poly1305)
- Per-file 32-byte ChaCha20 key, RSA-wrapped in PLAUD.AI container header
- PLAUD.AI container format:
  ```
  0x000: 8 bytes   Magic "PLAUD.AI"
  0x00A: 2 bytes   Header size (LE uint16), typically 512 (0x0200)
  0x084: 12 bytes  UUID (ChaCha20 nonce suffix)
  0x100: 256 bytes RSA-encrypted ChaCha20 file key (PKCS1v15)
  0x200: ...       ChaCha20-encrypted OGG audio payload
  ```
- ChaCha20 nonce = `\x00\x00\x00\x00` (4 zero bytes) + 12-byte UUID from header
- Decrypted audio starts with `OggS` magic

## BLE GATT Services

| UUID | Role |
|------|------|
| `00001910-0000-1000-8000-00805f9b34fb` | Plaud main service |
| `00002bb0-0000-1000-8000-00805f9b34fb` | Notify (device → client) |
| `00002bb1-0000-1000-8000-00805f9b34fb` | Write (client → device) |
| `0000180f-0000-1000-8000-00805f9b34fb` | Battery service |
| `00002a19-0000-1000-8000-00805f9b34fb` | Battery level |

## BLE Protocol Details

### Packet Header
All commands use 3-byte header (protocol type 1):
```
[0] uint8  bleProtocolType = 0x01
[1] uint16 bleRequestType  (little-endian) — command ID
[3...]     payload
```

### Command IDs
| ID | Name | Description |
|----|------|-------------|
| 1 | HANDSHAKE | Encrypted handshake |
| 3 | DEVICE_STATUS | Query device status |
| 4 | SYNC_TIME | Sync device clock |
| 6 | SYNC_STATISTICS | Sync statistics (MUST send before cmd 26) |
| 8 | CMD_ROUTE | Multi-purpose dispatch (sub-commands) |
| 9 | GET_BATTERY | Battery level |
| 26 | GET_REC_SESSIONS | List recordings on device |
| 28 | FILE_SYNC_START | Start file download |
| 29 | FILE_SYNC_STOP | Stop file download |
| 30 | FILE_SYNC_DEL | Delete recording from device |
| 104 | CLEAR_REC_FILE | Delete all recordings |
| 0xFE10 | SN_SIGNATURE | SN signature exchange (unencrypted) |
| 0xFE12 | RSA_KEY_SEND | RSA key exchange response |

### File Download Protocol
```
Client                                    Device
  |-- cmd 28 (syncFileStart) ----------->|
  |<-- cmd 28 (head response, status) ---|
  |<-- [type-2 data chunk 1] -----------|  (session_id, offset, payload)
  |<-- [type-2 data chunk 2] -----------|
  |<-- [type-2 data chunk N] -----------|
  |<-- cmd 29 (tail response, CRC) ------|
```

Type-2 data packet format:
```
[0] uint8   protocolType = 0x02
[1] uint32  session_id (LE)
[5] uint32  file_offset (LE) — 0xFFFFFFFF = end marker
[9] uint8   data_len
[10...]     payload bytes
```

### CRC-16
Modbus CRC-16 (polynomial 0xA001, init 0x0000).
Device returns 0xFFFF for all files — skip CRC check when 0xFFFF.

## Whisper.cpp CGo Integration

### Approach
- CGo wrapper around `whisper.h` C API
- Initialize whisper context with model file
- Convert OGG to 16kHz mono WAV first (via `opusdec` subprocess or inline PCM conversion)
- Feed PCM floats to `whisper_full()` API
- Extract text segments from result

### Build Requirements
- `CGO_ENABLED=1`
- `LD_LIBRARY_PATH` must include whisper.cpp lib directories at build and run time
- Or copy/symlink `.so` files to a system lib path

### Fallback
If CGo bindings are problematic, fall back to shelling out to `whisper-cli`:
```
LD_LIBRARY_PATH=... whisper-cli -m model.bin -f input.wav -otxt
```

## Dockerfile

Container with all dependencies:
- Base: `fedora` or `ubuntu` with `bluez`, `dbus`
- Install: `opus-tools` (for `opusdec`), `gcc` (for CGo)
- Copy: whisper.cpp libraries
- Build: Go binary with CGo
- Run with: `--net host --privileged` (for BLE D-Bus access)

## Implementation Order

1. **config** — YAML loading, validation
2. **protocol** — packet building, CRC-16, LE encoding
3. **crypto** — ChaCha20-Poly1305, RSA decrypt, PLAUD.AI decryption
4. **auth** — SDK OAuth token exchange
5. **ble/transport** — BlueZ D-Bus scan/connect/notify/write
6. **handshake** — 4-phase encrypted connection flow
7. **filesync** — file listing, downloading, deleting
8. **whisper** — CGo bindings, OGG→WAV conversion
9. **main.go** — CLI subcommands, orchestration
10. **Dockerfile** — containerization

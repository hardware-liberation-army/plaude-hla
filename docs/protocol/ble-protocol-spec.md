# PLAUD BLE Protocol Specification
## Reverse-Engineered Technical Reference for Interoperability Research

**Version:** 1.0
**Date:** 2026-03-23
**Source:** Decompiled from `PLAUD_release.apk` (TinnoTech PenBLE SDK)
**Purpose:** EU interoperability research — local-storage privacy alternative

---

## Table of Contents

1. [Overview](#1-overview)
2. [BLE GATT Service & Characteristic UUIDs](#2-ble-gatt-service--characteristic-uuids)
3. [Packet Structure](#3-packet-structure)
4. [Command ID Registry](#4-command-id-registry)
5. [Device Onboarding & QR Code Pairing](#5-device-onboarding--qr-code-pairing)
6. [Handshake & Authentication Sequence](#6-handshake--authentication-sequence)
7. [File Transfer Protocol](#7-file-transfer-protocol)
8. [Encryption Layer (v20+ Devices)](#8-encryption-layer-v20-devices)
9. [State Machine Pseudocode](#9-state-machine-pseudocode)
10. [Appendix: Device Identification](#10-appendix-device-identification)

---

## 1. Overview

The PLAUD device communicates over Bluetooth Low Energy (BLE) using a custom GATT-based protocol built on the **TinnoTech PenBLE SDK** (`com.tinnotech.penblesdk`). The architecture consists of:

- A **single custom GATT service** (`0x1910`) with two characteristics: one for **commands** (write) and one for **notifications** (notify/read).
- A **standard Battery Service** (`0x180F`) for battery level.
- A **command-response model**: the client writes serialized command packets to the Write characteristic, and receives responses/data via notifications on the Notify characteristic.
- **Two protocol generations**: devices with `portVersion < 20` use plaintext commands; devices with `portVersion >= 20` add ChaCha20-Poly1305 encryption with RSA key exchange.
- **Little-endian** byte order throughout.

---

## 2. BLE GATT Service & Characteristic UUIDs

All UUIDs are defined in the obfuscated class `wi.q4`:

| Name | UUID | Role |
|------|------|------|
| **PLAUD Service** | `00001910-0000-1000-8000-00805f9b34fb` | Primary custom service |
| **Command/Notify Characteristic** | `00002BB0-0000-1000-8000-00805f9b34fb` | Notifications FROM device (subscribe here) |
| **Write Characteristic** | `00002BB1-0000-1000-8000-00805f9b34fb` | Commands TO device (write here) |
| **Battery Service** | `0000180F-0000-1000-8000-00805f9b34fb` | Standard BLE battery service |
| **Battery Level Char** | `00002A19-0000-1000-8000-00805f9b34fb` | Battery level (0–100) |
| **CCCD** | `00002902-0000-1000-8000-00805f9b34fb` | Client Characteristic Config Descriptor (enable notifications) |

---

## 3. Packet Structure

### 3.1 Command Packet (Client -> Device)

#### Standard Command (type = 1):
```
Byte 0:      Packet Type       (uint8)  — always 0x01 for commands
Bytes 1–2:   Command ID        (uint16, little-endian)
Bytes 3–N:   Payload           (variable, command-specific)
```

#### File Sync Command (type = 2/3/5):
```
Byte 0:      Packet Type       (uint8)  — 0x02, 0x03, or 0x05
Bytes 1–4:   Reserved          (uint32, little-endian) — set to 0xFFFFFFFF (-1)
Bytes 5–N:   Payload           (variable)
```

### 3.2 Response Packet (Device -> Client)

#### Type 0x01 — Command Response:
```
Byte 0:      Response Type     (uint8)  — 0x01
Bytes 1–2:   Command ID        (uint16, LE) — echoes the command this responds to
Bytes 3–N:   Response Payload  (variable, command-specific)
```

#### Type 0x02 — File Data Packet (portVersion >= 7):
```
Byte 0:      Response Type     (uint8)  — 0x02
Bytes 1–4:   Session ID        (uint32, LE) — matches the requested session
Bytes 5–8:   File Offset       (uint32, LE) — byte offset in file
Byte 9:      Chunk Length       (uint8)
Bytes 10–N:  Data Payload       (variable, up to Chunk Length bytes)
```

**Special sentinel:** If File Offset = `0x0000FFFF`, this is an **EMPTY_PACKAGE** signal indicating end-of-data or error.

---

## 4. Command ID Registry

### Core Protocol Commands

| ID (hex) | Description |
|----------|-------------|
| 0x01 | **HandshakeReq** — First handshake (authentication) |
| 0x02 | **GetSsnRsp** — SSN response (within handshake) |
| 0x03 | **GetDeviceStatus** |
| 0x04 | **SyncTime** — Synchronize clock |
| 0x05 | **SetRecordSwitch** — Start/stop recording |
| 0x09 | **GetDeviceInfo** |
| 0x0A | **GetFileList** |
| 0x0D | **GetStorageInfo** |

### File Transfer Commands

| ID (hex) | Description |
|----------|-------------|
| 0x14 | **FileSyncPrepare** — Prepare file download |
| 0x15 | **FileSyncStart** — Start file transfer |
| 0x16 | **FileSyncStop** — Stop file transfer |
| 0x17 | **FileSyncConfig** |
| 0x18 | **DeleteFile** |
| 0x1C | **SyncFileHead** — Request file header |
| 0x1D | **SyncFileTail** — File transfer complete signal |

### Encryption Commands (portVersion >= 20)

| ID (hex) | Description |
|----------|-------------|
| 0xFE10 | **PreHandshakeReq** — Send SN signature chunks |
| 0xFE11 | **PreHandshakeCnf** — Device confirms pre-handshake |
| 0xFE12 | **SendRSAPublicKey** |
| 0xFE20 | **PreHandshakeReq** (alternate for Note Pro) |

---

## 7. File Transfer Protocol

### 7.1 Listing Files: CMD 0x0A
### 7.2 Preparing Download: CMD 0x14
### 7.3 Starting Transfer: CMD 0x1C
```
Bytes 3–6:   Session ID        (uint32, LE)
Bytes 7–10:  Start Position    (uint32, LE) — byte offset (0 for new)
Bytes 11–14: File Size          (uint32, LE)
```

### 7.4 Receiving File Data (Type 0x02 packets)
### 7.5 Stopping Transfer: CMD 0x16

### 7.6 Audio Data Format
- **Codec:** Opus (via `OpusUtils` native library)
- **Raw audio packet size:** `audioChannel * 80` bytes (default: 80 bytes for mono)
- **Frame ratio:** Every 20 bytes of compressed data = 80 bytes of audio frames
- **Container:** May be OGG (`isOggAudio` flag from handshake)
- **Sample rate:** 16000 Hz

---

## 8. Encryption Layer (v20+ Devices)

**Algorithm:** ChaCha20-Poly1305 (AEAD cipher)

### Key Material (from RSA-decrypted device response):
| Component | Size | Offset |
|-----------|------|--------|
| Key | 32 bytes | 0–31 |
| Nonce | 12 bytes | 32–43 |
| Associated Data (AD) | 12 bytes | 44–55 |
| Verification ciphertext | 24+ bytes | 56+ |

Verification: decrypt last 24 bytes → must equal ASCII "PLAUD.AI"

### Encrypted Command Wrapping:
1. Increment sequence counter (uint32)
2. Prepend 4-byte LE counter to plaintext command
3. Encrypt with ChaCha20-Poly1305 using stored key/nonce/AD
4. Write ciphertext to 0x2BB1

---

## Key Decompiled Source References

- `wi/l3.java` — BLE agent, connection state machine, handshake flow
- `wi/r4.java` — GATT operations, write queue (30ms delay), encryption wrapper (line 1087-1093)
- `wi/q4.java` — BLE UUID definitions
- `si/a0.java` — Handshake command builder
- `yi/m.java` — ChaCha20-Poly1305 / AES-GCM encryption utilities
- `com/tinnotech/penblesdk/entity/BleFile.java` — File model (sessionId, fileSize)

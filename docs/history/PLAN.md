# Plaud Note Pro - Desktop BLE Tool

## Goal
Build a Python CLI tool to interact with the Plaud Note Pro recording device via BLE from a Linux desktop, eventually replacing core phone app functionality.

## Device Info
- **Device**: Plaud Note Pro (BLE, cod:0-1f-0)
- **BLE Address**: observed as `xx:xx:xx:xx:eb:2a` on phone
- **SDK**: Tinnotech PenBleSDK (`com.tinnotech.penblesdk`)
- **App**: Flutter-based v3.17.0 (`ai.plaud.android.plaud`)

## BLE GATT Services

| UUID | Name | Role |
|---|---|---|
| `0x1910` | Tinnotech Proprietary Service | Main communication channel |
| `0x2BB0` | Notify characteristic | Device -> Phone (notifications/indications) |
| `0x2BB1` | Write characteristic | Phone -> Device (commands) |
| `0x180F` | Standard Battery Service | Battery level |
| `0x2A19` | Battery Level | Battery percentage |
| `0x2902` | CCCD | Enable/disable notifications |

## Protocol

### Packet Format (little-endian)
```
Header (3 bytes for short, 5 for long):
  [1 byte]  packet_type (1=short cmd, 2/3/5=long cmd)
  [2 bytes] cmd_id (uint16 LE)
  [optional 2 bytes] extra length/data

Payload:
  [N bytes] command-specific data
```

### Command IDs
| CMD | Function |
|---|---|
| 1 | Handshake (old protocol) |
| 3 | Depair/unbind |
| 5 | Disconnect |
| 6 | Sync time |
| 8 | Multi-purpose dispatch / routing |
| 10 | Handshake continue |
| 20 | Record start |
| 23 | Record stop |
| 26 | File sync head |
| 28/29 | File sync data |
| 30 | File sync tail |
| 33 | Storage info query |
| 34 | Format storage |
| 38 | Device status query |
| 61 | Sync statistics |
| 103 | WiFi mode switch |
| 109 | Setting change |
| 140-142 | Advanced settings |
| 143/144 | OTA/Firmware update |
| 65041/65042 | RSA handshake phase 1 |
| 65056/65040 | RSA handshake data |

### Connection Flow
1. BLE scan for Plaud devices
2. Connect, negotiate MTU (target: 255)
3. Service discovery (0x1910, 0x180F)
4. Enable notifications on 0x2BB0 + battery char
5. RSA handshake (two-phase, sends SN signature)
6. Device responds with capabilities (audio channels, wifi, VAD, codec, firmware)
7. Sync time, read settings, then ready for commands

### Encryption
- Firmware >= v20: RSA-encrypted communication after handshake
- Params E, F, G derived during handshake
- Raw binary pack/unpack via TntBleCommUtils (packInt/readInt/tntGetCrc)
- CRC used for file integrity

## Implementation Plan

### Phase 1 - Foundation (scan, connect, explore)
- BLE scanning and device discovery (filter by name "Plaud")
- Connect/disconnect with GATT service enumeration
- Parse both GATT services
- Battery level reading
- Notification subscription on 0x2BB0

### Phase 2 - Protocol Layer
- Python TntBleCommUtils (pack/unpack LE ints, CRC)
- Packet builder pattern (header + payload)
- Command dispatcher for notification responses
- Raw command interface

### Phase 3 - Handshake
- Attempt old-protocol handshake (cmd=1) first
- If needed, extract RSA keys from phone app data
- Implement RSA handshake and session key derivation

### Phase 4 - Functional Commands
- Sync time (cmd=6)
- Read device status (cmd=38)
- Storage info (cmd=33)
- List recordings
- Start/stop recording (cmd=20/23)
- Read/write settings

### Phase 5 - File Sync
- Recording download protocol
- Handle packet retransmission
- Audio format handling (likely Opus/OGG)

## Project Structure
```
plaud_re/
  plaud_tool.py       - CLI entry point
  ble_transport.py    - BLE connection, GATT ops, notifications
  protocol.py         - Packet encode/decode, CRC, command defs
  handshake.py        - RSA handshake and key exchange
  commands.py         - High-level command implementations
  file_sync.py        - Recording download/sync
```

## Prerequisites
- `bluez` and `bluetoothctl` on the host
- `python3-bleak` (or pip install bleak)
- BT adapter available (hci0 confirmed on this host)
- Plaud device in BLE range of desktop (may need to disconnect from phone)

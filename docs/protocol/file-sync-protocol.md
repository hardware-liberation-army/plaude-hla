# Plaud BLE File Sync Protocol

Reverse-engineered from `plaud-sdk-showcase.apk` using androguard bytecode analysis.

## Overview

All file sync operations use **BLE notifications** on a specific characteristic.
The protocol uses a custom binary framing with little-endian multi-byte integers.

**Native library**: `libtnt_ble_utils.so` provides core `readInt(bitWidth, byte[], offset)` 
and `packInt(bitWidth, byte[], offset, value)` which are used by the Java `TntBleCommUtils` class.
The `readInt`/`packInt` core is a native method, but the wrapper methods in Java reveal the 
byte ordering through `packInt32Little` which explicitly constructs LE bytes:

```java
// packInt32Little(long value) -> byte[4]
byte[0] = (byte)(value & 0xFF)          // LSB
byte[1] = (byte)((value >> 8) & 0xFF)
byte[2] = (byte)((value >> 16) & 0xFF)
byte[3] = (byte)((value >> 24) & 0xFF)  // MSB
```

So all multi-byte integers are **Little-Endian**.

## Protocol Layer: Packet Header (packHead)

Every request packet starts with a **3-byte header** (for `BleProtocolType == 1`):

```
Offset  Size  Field
0       1     bleProtocolType (uint8)  = 0x01 for command packets
1       2     bleRequestType   (uint16 LE)  = command ID (e.g., 26, 28, 29, 30, 104)
```

There are also 5-byte header variants for other protocol types (type 2 = voice/OTA, type 3 = test),
but file sync uses type 1 exclusively.

**BaseReqPkgBean.packHead()** logic:
- If protocolType == 1: allocate `byte[3]`, write type as uint8 at [0], write requestType as uint16 LE at [1]
- If protocolType == 2 or 3: allocate `byte[5]`, write type as uint8 at [0], write -1 (0xFFFFFFFF) as uint32 LE at [1]
- enPkg() = packHead() + payload fields merged via byteMergeAll()

## Command IDs (BleRequest constants)

From `BleProtocol$BleRequest` class fields mapped to `getBleRequestType()` return values:

| Constant Name                       | Value | Description              |
|-------------------------------------|-------|--------------------------|
| HOST_GET_REC_SESSIONS_REQ           | 26    | Get file list            |
| HOST_FILE_SYNC_DATA                 | 28    | Sync file start/download |
| HOST_SYNC_REC_FILE_STOP_IND         | 29    | Sync file stop           |
| HOST_SYNC_REC_FILE_DEL_IND          | 30    | Delete file              |
| HOST_CLEAR_REC_FILE_REQ             | 104   | Clear all files          |
| HOST_FILE_INFO_SYNC_IND             | 112   | File info sync           |
| HOST_FILE_DATA_CHECKSUM_RESULT      | 114   | File data sync/checksum  |
| HOST_SYNC_STAT_FILE_REQ             | 61    | Sync stat file           |

## Response Command IDs (BleConfirmType)

| Class                    | ConfirmType | Description              |
|--------------------------|-------------|--------------------------|
| GetRecSessionsRsp        | 26          | File list response       |
| SyncFileHeadRsp          | 28          | File download head/ack   |
| SyncFileTailRsp          | 29          | File download tail/done  |
| SyncRecFileStopRsp       | 30          | Stop response            |
| SyncRecFileDelRsp        | 31          | Delete response          |
| ClearRecordFileRsp       | 104         | Clear all response       |

---

## Command 1: getRecSessions (Get File List)

### Request (GetRecSessionsReq)

**Command ID**: 26 (0x1A)  
**Protocol Type**: 1

**Constructor**: `GetRecSessionsReq(long sessionId, long uid, boolean isFullSync)`

**Packet Layout**:
```
Offset  Size  Field
0       1     bleProtocolType = 0x01
1       2     bleRequestType  = 0x001A (uint16 LE)
3       4     sessionId       (uint32 LE)   -- session ID / sequence counter
7       4     uid             (uint32 LE)   -- unique request ID (timestamp-based)
11      1     isFullSync      (uint8)       -- 0 = incremental, 1 = full sync
```

**Total**: 12 bytes

### Response (GetRecSessionsRsp)

**Confirm Type**: 26

The response may arrive in **multiple BLE notifications**. The `SessionsHelper.onDataReceive()`
method accumulates data across notifications.

**First notification / BaseRspBleBean header** (parsed in `<init>`):
```
Offset  Size  Field
0       1     bleProtocolType
1       2     bleRequestType  (validated == 26)
3       4     uid             (uint32 LE)   -- matches request uid
7       2     totals          (uint16 LE)   -- total number of files
```

**Subsequent data** (parsed in `findMoreFiles(data, protocolVersion)`):

The method reads additional file entries from `offset 11` onwards (after header).

Each file entry is **variable size** depending on protocol version:

**For protocolVersion >= 2 and < 7 (entrySize = 10)**:
```
Offset  Size  Field
0       4     sessionId    (uint32 LE)   -- BleFile.sessionId
4       4     fileSize     (uint32 LE)   -- BleFile.fileSize
8       1     attribute    (uint8)       -- BleFile.attribute (recording attribute)
9       1     scene        (uint8)       -- BleFile.scene (recording scene)
```

**For protocolVersion >= 7 (entrySize = 8)**:
```
Offset  Size  Field
0       4     sessionId    (uint32 LE)
4       4     fileSize     (uint32 LE)
```
(attribute and scene default to 0)

**For protocolVersion < 2 (entrySize = 9)**:
```
Offset  Size  Field
0       4     sessionId    (uint32 LE)
4       4     fileSize     (uint32 LE)
8       1     attribute    (uint8)
```
(scene defaults to 0)

**findMoreFiles** also checks:
- `offset 9`: reads `uint16` as current file count (must match fileList.size() for dedup)
- Data starts at `offset 11`
- Entry count = `(data.length - 11) / entrySize`

### BleFile Data Model

```
class BleFile implements Parcelable {
    long sessionId;      // process_item_data (J) - unique session/file ID
    long fileSize;       // retrieve_config_value (J) - file size in bytes
    int attribute;       // update_internal_state (I) - recording attribute
    int scene;           // get_display_metrics (I) - recording scene (4 = music)
    String summaryMarkdown;
}
```

**Note**: Field names are obfuscated in the APK. The real field-to-name mapping:
- `process_item_data` → `sessionId`
- `retrieve_config_value` → `fileSize`  
- `update_internal_state` → `attribute`
- `get_display_metrics` → `scene`

---

## Command 2: syncFileStart (Download File)

### Request (SyncRecFileStartReq)

**Command ID**: 28 (0x1C)  
**Protocol Type**: 1

**Constructor**: `SyncRecFileStartReq(long sessionId, long uid, long fileSize)`

**Packet Layout**:
```
Offset  Size  Field
0       1     bleProtocolType = 0x01
1       2     bleRequestType  = 0x001C (uint16 LE)
3       4     sessionId       (uint32 LE)
7       4     uid             (uint32 LE)
11      4     fileSize        (uint32 LE)
```

**Total**: 15 bytes

### Response: SyncFileHeadRsp (type 28)

This is the **head/ack** response confirming the device is ready to stream data.

**Parsed in `<init>`**:
```
Offset  Size  Field
0       1     bleProtocolType
1       2     bleRequestType (validated == 28)
3       4     sessionId      (uint32 LE)
7       1     status         (uint8)  -- 0 = success, >0 = error
```

After receiving the head response with status == 0, the device starts streaming 
file data via type 2 notifications (see below).

### Response: File Data Notifications (type 2)

After a successful SyncFileHeadRsp, the device sends continuous data packets.
These are handled by `BetaComponentHandler.onCallback(byte[])` for **protocolType == 2**.

**Data notification format**:
```
Offset  Size  Field
0       1     bleProtocolType = 0x02 (voice/data type)
1       4     sessionId       (uint32 LE)  -- must match the requested sessionId
5       1     dataFlag        (uint8)      -- 0 = more data, 1 = last packet
6       1     dataLen         (uint8)      -- length of payload
7       N     payload         (byte[N])    -- raw file data bytes
```

**BetaComponentHandler.onCallback() logic for type 2**:
1. Read sessionId at offset 1 (uint32)
2. Compare with expected sessionId (stored in `query_status_flags` field)
3. If mismatch and expected == -1 (0xFFFFFFFF): read at offset 1 instead (different path)
4. Read dataFlag at offset `1 + 4 + 1 = 6`
5. Read dataLen at offset `1 + 4 = 5` (wait, let me re-check)

Actually from the bytecode more carefully:
- `v4 = 1` (offset starting point for sessionId)
- Read sessionId as uint32 at offset 1
- Add 4: `v1 = 5` (next offset)
- If sessionId != 0xFFFFFFFF: add 1 to get `v5 = 6`, read dataFlag as uint8 at offset 6
- If dataFlag matches expected: this is a sync-file-stop trigger
- Otherwise: read dataLen as uint8 at offset v5, compute total = v1 + dataLen
- Extract payload from v1 to end (or dataLen bytes)
- Pass to `ISyncVoiceDataKeepOut.receiveVoiceData(payload, sessionId)`

**Accumulation**: The handler tracks `process_item_data` (accumulated byte count) and
calls `receiveVoiceData()` for each chunk.

### Response: SyncFileTailRsp (type 29)

This is the **tail/completion** response sent after all data is streamed.

**Parsed in `<init>`**:
```
Offset  Size  Field
0       1     bleProtocolType
1       2     bleRequestType (validated == 29)
3       4     sessionId      (uint32 LE)
7       2     crc            (uint16 LE)  -- CRC of the complete file data
```

**Note**: `SyncFileTailRsp` is created in `BetaComponentHandler.onCallback()` when it
receives a type-1 response with bleRequestType == 29. This is NOT a type-2 notification;
it's a separate BLE response on the response characteristic.

---

## Command 3: syncFileStop (Stop Download)

### Request (SyncRecFileStopReq)

**Command ID**: 29 (0x001D)  
**Protocol Type**: 1

**Constructor**: `SyncRecFileStopReq()` -- **no parameters**

**Packet Layout**:
```
Offset  Size  Field
0       1     bleProtocolType = 0x01
1       2     bleRequestType  = 0x001D (uint16 LE)
```

**Total**: 3 bytes (header only, no payload)

### Response (SyncRecFileStopRsp)

**Confirm Type**: 30

Empty response body - just validates the command type matches.

---

## Command 4: syncFileDel (Delete File)

### Request (SyncRecFileDelReq)

**Command ID**: 30 (0x1E)  
**Protocol Type**: 1

**Constructor**: `SyncRecFileDelReq(long sessionId)`

**Packet Layout**:
```
Offset  Size  Field
0       1     bleProtocolType = 0x01
1       2     bleRequestType  = 0x001E (uint16 LE)
3       4     sessionId       (uint32 LE)
```

**Total**: 7 bytes

### Response (SyncRecFileDelRsp)

**Confirm Type**: 31

**Constructor**: `SyncRecFileDelRsp(byte[] data, int protocolVersion)`

**Parsed fields**:
```
Offset  Size  Field
0       1     bleProtocolType
1       2     bleRequestType (validated == 31)
3       4     sessionId      (uint32 LE)
7       1     status         (uint8)  -- 0 = success
```

(Only parsed if protocolVersion >= 7 / data length >= 7)

---

## Command 5: clearRecordFile (Clear All Files)

### Request (ClearRecordFileReq)

**Command ID**: 104 (0x68)  
**Protocol Type**: 1

**Constructor**: `ClearRecordFileReq()` -- **no parameters**

**Packet Layout**:
```
Offset  Size  Field
0       1     bleProtocolType = 0x01
1       2     bleRequestType  = 0x0068 (uint16 LE)
```

**Total**: 3 bytes (header only, no payload)

### Response (ClearRecordFileRsp)

**Confirm Type**: 104

**Parsed fields**:
```
Offset  Size  Field
0       1     bleProtocolType
1       2     bleRequestType (validated == 104)
3       1     status         (uint8)  -- 0 = success
```

---

## Additional Commands

### FileInfoSyncReq (Command 112)

```
Offset  Size  Field
0       1     bleProtocolType = 0x01
1       2     bleRequestType  = 0x0070 (uint16 LE)
3       1     type            (uint8)
4       4     sessionId       (uint32 LE)
```

### FileDataSyncReq (Command 114)

```
Offset  Size  Field
0       1     bleProtocolType = 0x01
1       2     bleRequestType  = 0x0072 (uint16 LE)
3       1     type            (uint8)
4       4     sessionId       (uint32 LE)
8       2     dataLength      (uint16 LE)
10      N     data            (byte[N])
```

### SyncStatFileReq (Command 61)

```
Offset  Size  Field
0       1     bleProtocolType = 0x01
1       2     bleRequestType  = 0x003D (uint16 LE)
3       4     statType        (uint32 LE)
```

---

## Notification Handler: BetaComponentHandler.onCallback()

This is the central dispatcher for all BLE notifications related to file sync.

### Flow for type-1 responses (command responses):

```
1. Read bleRequestType as uint16 at offset 4 (PkgUtils.getDataType() reads at offset 4)

   Wait - there's a discrepancy. Let me re-analyze...
```

Actually, looking more carefully at the `BaseRspBleBean` constructor:
- It reads uint16 at offset 1 and compares to the expected `getBleConfirmType()`
- So offset 0 = protocolType (uint8), offset 1-2 = requestType (uint16 LE)

And `PkgUtils.getDataType()` reads uint16 at offset 4 (not offset 1).
This suggests the BLE notification characteristic may have a different framing
than the raw response bytes, with additional bytes prepended.

**BetaComponentHandler.onCallback(byte[]) complete flow**:

```
1. Read uint16 at offset 4 -> cmdType (this is in the BLE notification frame)

2. For cmdType == 28 (FILE_SYNC_HEAD):
   - Create SyncFileHeadRsp(fullNotificationData)
   - If status <= 0 (success):
     - Remove response bean for cmdType 29
     - Trigger process_item_data() which starts the file download loop
     - Set up timeout (5000ms)
     - Start async thread "syncFileStart"

3. For cmdType == 29 (FILE_SYNC_STOP / tail):  
   - Create SyncFileTailRsp(fullNotificationData)
   - Notify register_event_listener callback
   - Signal download complete
   - Dispatch "syncFileTailRsp" async

4. For type-2 data (voice/file content):
   - Read sessionId at offset 1 (uint32 LE)
   - Track expected sessionId vs received
   - Handle packet loss detection (compare accumulated bytes)
   - Extract payload bytes
   - Forward to ISyncVoiceDataKeepOut.receiveVoiceData()
```

---

## Response Frame Format

All responses on the BLE notification characteristic appear to have this structure:

```
Offset  Size  Field
0       ?     (possibly BLE ATT header or additional framing)
...
4       2     bleRequestType  (uint16 LE)  -- the actual command type
...     ...   response-specific fields
```

But the response *classes* (BaseRspBleBean) parse starting from offset 0:
```
Offset 0: protocolType (uint8) - only used for request building
Offset 1: requestType (uint16 LE) - used for response validation
```

This means the BetaComponentHandler receives the **raw BLE notification data** which
has some additional framing bytes at the start, and it reads the command type at offset 4.
But the response classes (SyncFileHeadRsp, etc.) are constructed with the **same byte array**
and parse from offset 0 for their fields.

This implies the notification data has 3 extra bytes at the start (perhaps an ATT handle
or notification header), making the effective layout:

```
Offset  Size  Field
0       3     (ATT notification header / handle)
3       1     bleProtocolType  
4       2     bleRequestType (uint16 LE)  <-- BetaComponentHandler reads here
5+      ...   response payload
```

And the response classes skip the first byte (protocolType at [0]) and start their
real parsing at offset 3 for the sessionId fields. This matches:
- `SyncFileHeadRsp`: reads sessionId at offset 3 (uint32), status at offset 7 (uint8)
- `SyncFileTailRsp`: reads sessionId at offset 3 (uint32), crc at offset 7 (uint16)
- `GetRecSessionsRsp`: reads totals at offset 7 (uint16), files from offset 11+

---

## File Download Sequence

```
Client                                    Device
  |                                         |
  |-- GetRecSessionsReq (cmd 26) ---------->|
  |<-- GetRecSessionsRsp (cmd 26, N files)--|
  |                                         |
  |  (for each file to download)            |
  |-- SyncRecFileStartReq (cmd 28) -------->|
  |<-- SyncFileHeadRsp (cmd 28, status) ----|
  |                                         |
  |<-- [type 2 data notification 1] --------|  (file data chunks)
  |<-- [type 2 data notification 2] --------|
  |<-- [type 2 data notification N] --------|
  |<-- SyncFileTailRsp (cmd 29, crc) -------|  (or sent as type-1 response)
  |                                         |
  |  (optional: stop early)                 |
  |-- SyncRecFileStopReq (cmd 29) --------->|
  |<-- SyncRecFileStopRsp (cmd 30) ---------|
  |                                         |
  |  (optional: delete file)                |
  |-- SyncRecFileDelReq (cmd 30, sessionId)->|
  |<-- SyncRecFileDelRsp (cmd 31) ----------|
  |                                         |
  |  (optional: clear all)                  |
  |-- ClearRecordFileReq (cmd 104) -------->|
  |<-- ClearRecordFileRsp (cmd 104) --------|
```

## Byte-Level Packet Examples

### getRecSessions request (sessionId=1, uid=0x12345678, isFullSync=true)
```
01          protocolType = 1
1A 00       requestType = 26 (LE)
01 00 00 00 sessionId = 1 (LE)
78 56 34 12 uid = 0x12345678 (LE)
01          isFullSync = true
```

### syncFileStart request (sessionId=5, uid=0xAABBCCDD, fileSize=65536)
```
01          protocolType = 1
1C 00       requestType = 28 (LE)
05 00 00 00 sessionId = 5 (LE)
DD CC BB AA uid = 0xAABBCCDD (LE)
00 00 01 00 fileSize = 65536 (LE)
```

### syncFileStop request
```
01          protocolType = 1
1D 00       requestType = 29 (LE)
```

### syncFileDel request (sessionId=5)
```
01          protocolType = 1
1E 00       requestType = 30 (LE)
05 00 00 00 sessionId = 5 (LE)
```

### clearRecordFile request
```
01          protocolType = 1
68 00       requestType = 104 (LE)
```

## Utility Methods (TntBleCommUtils)

| Method | Size | Description |
|--------|------|-------------|
| `readInt08(buf, off)` | 1 byte | Read uint8 |
| `readInt16(buf, off)` | 2 bytes | Read uint16 (big-endian via native) |
| `readInt24(buf, off)` | 3 bytes | Read uint24 (big-endian via native) |
| `readInt32(buf, off)` | 4 bytes | Read uint32 (big-endian via native) |
| `readInt64(buf, off)` | 8 bytes | Read uint64 (big-endian via native) |
| `packInt08(buf, off, val)` | 1 byte | Write uint8 |
| `packInt16(buf, off, val)` | 2 bytes | Write uint16 (big-endian via native) |
| `packInt32(buf, off, val)` | 4 bytes | Write uint32 (big-endian via native) |
| `packInt64(buf, off, val)` | 8 bytes | Write uint64 (big-endian via native) |
| `packInt8Little(val)` | 1 byte | Pack uint8 to byte[1] |
| `packInt16Little(val)` | 2 bytes | Pack uint16 LE to byte[2] (takes first 2 of packInt32Little) |
| `packInt24Little(val)` | 3 bytes | Pack uint24 LE to byte[3] (takes first 3 of packInt32Little) |
| `packInt32Little(val)` | 4 bytes | Pack uint32 LE to byte[4] (explicit LE construction) |

**IMPORTANT**: The `packInt*` methods (without "Little") use the native library and appear 
to write **big-endian**. The `packInt*Little` methods explicitly construct **little-endian**.
Request packets use `packInt*Little` for payload fields but the `packHead()` method uses 
`packInt08` (1 byte, no endian issue) and `packInt16` (big-endian via native) for the header.

**HOWEVER**: The response classes parse using `readInt32`, `readInt16`, `readInt08` which 
go through the native library. And `packInt32Little` is explicitly little-endian.

Looking at the `packHead()` method more carefully:
- For protocolType == 1: Uses `packInt08(buf, 0, protocolType)` then `packInt16(buf, 1, requestType)` 
- `packInt16` calls native `packInt(16, buf, offset, value)` - this is likely **big-endian** per the native code
- BUT the response classes use `readInt16(buf, 1)` to verify the command type, and it works

So the header command ID is written via native (big-endian) and read via native (big-endian),
meaning they're consistent. The **payload** fields use the explicit Little-Endian methods.

**Wait** - let me re-examine. `packInt16` delegates to native `packInt(bitWidth=16, ...)`.
And the response validates with `readInt16`. If both use the same native code, the endianness 
is consistent. But `packInt32Little` explicitly constructs LE, while `readInt32` uses native.

The key insight: **requests use `packInt32Little` (LE) for payload but responses use `readInt32` 
(native/big-endian)**. This would mean the byte order is SWAPPED between request and response!

**More likely explanation**: The native `readInt` with bitWidth parameter reads `bitWidth/8` bytes 
in the same order that `packInt` writes them. The "Little" variants exist because the protocol 
requires LE for certain fields. The responses are parsed with the native methods because the 
device firmware sends them in big-endian (matching the native code).

**Actually**, looking at `GetRecSessionsRsp` more carefully - it reads `readInt32(data, 3)` for 
the uid at offset 3. But `SessionsHelper.onDataReceive()` also reads `readInt32(data, 3)` to 
match the uid. Since the request uid was packed with `packInt32Little()`, this suggests the 
response uid bytes are in the same LE format - meaning the native `readInt32` might actually 
read in little-endian too, or the firmware echoes back in LE.

**Conclusion**: The safest assumption based on the code is:
- Request payloads use **Little-Endian** (explicit via `packInt32Little`)
- Response fields are parsed with `readInt32` which appears consistent
- The header command type is written/read consistently via native methods

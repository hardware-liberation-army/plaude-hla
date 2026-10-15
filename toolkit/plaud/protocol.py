import struct
from enum import IntEnum
from typing import Optional


class Cmd(IntEnum):
    HANDSHAKE = 1
    GET_SSN = 2
    DEVICE_STATUS = 3
    SYNC_TIME = 4
    DISCONNECT = 5
    CMD_ROUTE = 8
    GET_BATTERY = 9
    HANDSHAKE_CONTINUE = 10
    RECORD_START = 20
    RECORD_STOP = 23
    GET_REC_SESSIONS = 26
    FILE_SYNC_START = 28
    FILE_SYNC_STOP = 29
    FILE_SYNC_DEL = 30
    STORAGE_INFO = 33
    FORMAT_STORAGE = 34
    SYNC_STATISTICS = 61
    CLEAR_REC_FILE = 104
    FILE_INFO_SYNC = 112
    FILE_DATA_CHECKSUM = 114
    WIFI_SWITCH = 103
    SETTING_CHANGE = 109
    OTA_PUSH = 143
    OTA_DATA = 144
    SN_SIGNATURE = 65040
    RSA_KEY_SEND = 65042


CMD_NAMES = {c.value: c.name for c in Cmd}


def crc16(data: bytes, init: int = 0) -> int:
    crc = init
    for byte in data:
        crc ^= byte
        for _ in range(8):
            if crc & 0x0001:
                crc = (crc >> 1) ^ 0xA001
            else:
                crc >>= 1
    return crc & 0xFFFF


def pack_uint8(value: int) -> bytes:
    return struct.pack("<B", value & 0xFF)


def pack_uint16(value: int) -> bytes:
    return struct.pack("<H", value & 0xFFFF)


def pack_uint24(value: int) -> bytes:
    return struct.pack("<I", value & 0xFFFFFF)[:3]


def pack_uint32(value: int) -> bytes:
    return struct.pack("<I", value & 0xFFFFFFFF)


def pack_uint64(value: int) -> bytes:
    return struct.pack("<Q", value & 0xFFFFFFFFFFFFFFFF)


def read_uint8(data: bytes, offset: int = 0) -> int:
    return data[offset] & 0xFF


def read_uint16(data: bytes, offset: int = 0) -> int:
    return struct.unpack_from("<H", data, offset)[0]


def read_uint24(data: bytes, offset: int = 0) -> int:
    b = data[offset:offset + 3] + b'\x00'
    return struct.unpack("<I", b)[0]


def read_uint32(data: bytes, offset: int = 0) -> int:
    return struct.unpack_from("<I", data, offset)[0]


def read_uint64(data: bytes, offset: int = 0) -> int:
    return struct.unpack_from("<Q", data, offset)[0]


class Packet:
    def __init__(self, cmd_id: int, payload: bytes = b""):
        self.cmd_id = cmd_id
        self.payload = payload

    def to_bytes(self) -> bytes:
        if self.cmd_id > 255:
            header = pack_uint16(self.cmd_id) + pack_uint16(len(self.payload))
        else:
            header = pack_uint8(1) + pack_uint16(self.cmd_id)
        return header + self.payload

    @staticmethod
    def from_bytes(data: bytes) -> "Packet":
        if len(data) < 2:
            raise ValueError(f"Packet too short: {len(data)} bytes")

        cmd_id = read_uint16(data, 0)

        if cmd_id >= 256:
            if len(data) >= 4:
                total_chunks = read_uint8(data, 2)
                chunk_idx = read_uint8(data, 3)
                payload = data[4:]
            else:
                payload = data[2:]
        elif len(data) >= 3:
            pkt_type = read_uint8(data, 0)
            if pkt_type == 1:
                cmd_id = read_uint16(data, 1)
                payload = data[3:]
            elif pkt_type == 2:
                cmd_id = read_uint16(data, 1)
                payload = data[3:]
            elif pkt_type == 4:
                cmd_id = read_uint16(data, 1)
                payload = data[3:]
            elif pkt_type == 5:
                cmd_id = read_uint16(data, 1)
                extra = read_uint16(data, 3)
                payload = data[5:]
            elif pkt_type == 8:
                sub_cmd = read_uint16(data, 3)
                cmd_id = sub_cmd
                payload = data[5:]
            else:
                cmd_id = read_uint16(data, 1)
                payload = data[3:]
        else:
            payload = data[2:]

        return Packet(cmd_id, payload)

    @staticmethod
    def build_short(cmd_id: int, payload: bytes = b"") -> bytes:
        return pack_uint16(cmd_id) + payload

    @staticmethod
    def build_with_header(cmd_id: int, payload: bytes = b"") -> bytes:
        header = bytearray(3)
        struct.pack_into("<B", header, 0, 1)
        struct.pack_into("<H", header, 1, cmd_id)
        return bytes(header) + payload

    def __repr__(self):
        name = CMD_NAMES.get(self.cmd_id, f"UNKNOWN_0x{self.cmd_id:04X}")
        return f"Packet(cmd={name}, payload={self.payload.hex()})"


def _trim_trailing_zero_pairs(data: bytearray) -> bytes:
    length = len(data)
    while length > 1:
        if data[length - 1] == 0 and data[length - 2] == 0:
            length -= 1
        else:
            break
    return bytes(data[:length]) if length > 0 else b""


def build_handshake(bind_token: str = "", sub_type: int = 0, port_version: int = 0) -> bytes:
    buf = bytearray(255)
    off = 0
    buf[off] = 0x01; off += 1
    struct.pack_into("<H", buf, off, 0x0001); off += 2
    buf[off] = 0x02; off += 1
    buf[off] = sub_type & 0xFF; off += 1
    if port_version >= 3:
        buf[off] = 0; off += 1
    token_len = 32 if port_version >= 9 else 16
    padded_token = bind_token.ljust(token_len, '0')[:token_len]
    token_bytes = padded_token.encode('ascii')
    buf[off:off + len(token_bytes)] = token_bytes
    return _trim_trailing_zero_pairs(buf)


def build_two_handshake(bind_token: str = "", app_id: bytes = b'\x00' * 8, extra: str = "") -> bytes:
    payload = bytearray()
    payload += pack_uint8(0x02)
    payload += pack_uint8(0)
    payload += pack_uint8(1)
    padded_token = bind_token.ljust(16, '0')[:16]
    payload += padded_token.encode('ascii')
    payload += app_id[:8].ljust(8, b'\x00')
    if extra:
        extra_bytes = extra.encode('utf-8')
        payload += pack_uint8(len(extra_bytes))
        payload += extra_bytes
    else:
        payload += pack_uint8(0)
    return Packet.build_with_header(Cmd.HANDSHAKE, bytes(payload))


def build_sync_time() -> bytes:
    import time as _time
    ts = int(_time.time())
    payload = pack_uint32(ts) + pack_uint8(0) + pack_uint8(0)
    return Packet.build_with_header(Cmd.SYNC_TIME, payload)


def build_get_battery() -> bytes:
    return Packet.build_with_header(Cmd.GET_BATTERY)


def build_device_status() -> bytes:
    return Packet.build_with_header(Cmd.DEVICE_STATUS)


def build_storage_info() -> bytes:
    return Packet.build_with_header(Cmd.STORAGE_INFO)


def build_record_start() -> bytes:
    return Packet.build_with_header(Cmd.RECORD_START)


def build_record_stop() -> bytes:
    return Packet.build_with_header(Cmd.RECORD_STOP)


def build_handshake_old() -> bytes:
    return build_handshake("", 0, 0)


def build_disconnect() -> bytes:
    return Packet.build_with_header(Cmd.DISCONNECT, pack_uint8(0))


def build_depair() -> bytes:
    return Packet.build_with_header(Cmd.DEPAIR)


def build_format_storage() -> bytes:
    return Packet.build_with_header(Cmd.FORMAT_STORAGE, pack_uint8(1))


def build_raw(cmd_id: int, payload: bytes = b"") -> bytes:
    return Packet.build_with_header(cmd_id, payload)


def build_cmd8_sub(subcmd: int) -> bytes:
    return Packet.build_with_header(Cmd.CMD_ROUTE, pack_uint8(1) + pack_uint8(subcmd) + pack_uint8(0))


def parse_notification(data: bytes) -> Optional[Packet]:
    if not data or len(data) < 3:
        return None
    try:
        return Packet.from_bytes(data)
    except Exception as e:
        print(f"Parse error: {e}, raw: {data.hex()}")
        return None


def hex_dump(data: bytes, width: int = 16) -> str:
    lines = []
    for i in range(0, len(data), width):
        chunk = data[i:i + width]
        hex_part = " ".join(f"{b:02X}" for b in chunk)
        ascii_part = "".join(chr(b) if 32 <= b < 127 else "." for b in chunk)
        lines.append(f"  {i:04X}  {hex_part:<{width * 3 - 1}}  {ascii_part}")
    return "\n".join(lines)


def build_get_rec_sessions(session_id: int = 0, uid: int = 0, full_sync: bool = False) -> bytes:
    payload = pack_uint32(session_id) + pack_uint32(uid) + pack_uint8(1 if full_sync else 0)
    return Packet.build_with_header(Cmd.GET_REC_SESSIONS, payload)


def build_sync_file_start(session_id: int, uid: int = 0, file_size: int = 0) -> bytes:
    payload = pack_uint32(session_id) + pack_uint32(uid) + pack_uint32(file_size)
    return Packet.build_with_header(Cmd.FILE_SYNC_START, payload)


def build_sync_file_stop() -> bytes:
    return Packet.build_with_header(Cmd.FILE_SYNC_STOP)


def build_sync_file_del(session_id: int) -> bytes:
    payload = pack_uint32(session_id)
    return Packet.build_with_header(Cmd.FILE_SYNC_DEL, payload)


def build_clear_rec_file() -> bytes:
    return Packet.build_with_header(Cmd.CLEAR_REC_FILE)


def parse_file_list_response(data: bytes, protocol_version: int = 20) -> "FileListResponse":
    return FileListResponse(data, protocol_version)


def parse_sync_file_head(data: bytes) -> "SyncFileHeadResponse":
    return SyncFileHeadResponse(data)


def parse_sync_file_tail(data: bytes) -> "SyncFileTailResponse":
    return SyncFileTailResponse(data)


def parse_file_data_notification(data: bytes) -> "FileDataNotification":
    return FileDataNotification(data)


class BleFile:
    def __init__(self, session_id: int = 0, file_size: int = 0, attribute: int = 0, scene: int = 0):
        self.session_id = session_id
        self.file_size = file_size
        self.attribute = attribute
        self.scene = scene

    def __repr__(self):
        return f"BleFile(id={self.session_id}, size={self.file_size}, attr={self.attribute}, scene={self.scene})"


class FileListResponse:
    def __init__(self, data: bytes, protocol_version: int = 20):
        self.raw = data
        self.files: list[BleFile] = []
        self.total = 0
        self.uid = 0
        self._parse(data, protocol_version)

    def _parse(self, data: bytes, protocol_version: int):
        if len(data) < 7:
            return
        self.uid = read_uint32(data, 3)
        self.total = read_uint16(data, 7)
        if protocol_version >= 7:
            entry_size = 10
        elif protocol_version >= 2:
            entry_size = 10
        else:
            entry_size = 9
        offset = 9
        if len(data) > offset + 2:
            offset = 11
        while offset + entry_size <= len(data):
            sid = read_uint32(data, offset)
            fsz = read_uint32(data, offset + 4)
            attr = data[offset + 8] if entry_size >= 9 else 0
            scene = data[offset + 9] if entry_size >= 10 else 0
            self.files.append(BleFile(sid, fsz, attr, scene))
            offset += entry_size

    def __repr__(self):
        return f"FileListResponse(total={self.total}, files={len(self.files)})"


class SyncFileHeadResponse:
    def __init__(self, data: bytes):
        self.raw = data
        self.session_id = 0
        self.status = -1
        if len(data) >= 7:
            self.session_id = read_uint32(data, 3)
            self.status = read_uint8(data, 7)

    @property
    def ok(self) -> bool:
        return self.status == 0

    def __repr__(self):
        return f"SyncFileHeadResponse(session={self.session_id}, status={self.status})"


class SyncFileTailResponse:
    def __init__(self, data: bytes):
        self.raw = data
        self.session_id = 0
        self.crc = 0
        if len(data) >= 9:
            self.session_id = read_uint32(data, 3)
            self.crc = read_uint16(data, 7)

    def __repr__(self):
        return f"SyncFileTailResponse(session={self.session_id}, crc=0x{self.crc:04X})"


class FileDataNotification:
    def __init__(self, data: bytes):
        self.raw = data
        self.session_id = 0
        self.file_offset = 0
        self.payload = b""
        if len(data) < 10:
            return
        self.session_id = read_uint32(data, 1)
        self.file_offset = read_uint32(data, 5)
        if self.file_offset == 0xFFFFFFFF:
            return
        data_len = read_uint8(data, 9)
        self.payload = data[10:10 + data_len] if data_len > 0 else data[10:]

    @property
    def is_last(self) -> bool:
        return self.file_offset == 0xFFFFFFFF

    def __repr__(self):
        return f"FileDataNotification(session={self.session_id}, flag={self.data_flag}, {len(self.payload)}B)"

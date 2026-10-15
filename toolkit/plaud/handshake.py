import struct
import base64
import hashlib
from .config import SN_SIGNATURE_B64, RSA_PRIVATE_KEY_PEM, RSA_PUBLIC_KEY_PEM, DEVICE_SN, FIRMWARE_PRIVATE_KEY_PEM
from cryptography.hazmat.primitives.asymmetric import padding as asym_padding
from cryptography.hazmat.primitives import hashes
from cryptography.hazmat.primitives.serialization import load_pem_private_key


def get_sn_signature_bytes() -> bytes:
    return base64.b64decode(SN_SIGNATURE_B64)


def generate_self_signed_sn_signature(device_sn: str = DEVICE_SN) -> bytes:
    private_key = load_pem_private_key(FIRMWARE_PRIVATE_KEY_PEM.encode(), password=None)
    digest = hashlib.sha256(device_sn.encode()).digest()
    signature = private_key.sign(digest, asym_padding.PKCS1v15(), hashes.SHA256())
    return signature


def get_rsa_public_key_bytes() -> bytes:
    from cryptography.hazmat.primitives.serialization import load_pem_public_key
    key = load_pem_public_key(RSA_PUBLIC_KEY_PEM.encode())
    from cryptography.hazmat.primitives.serialization import Encoding, PublicFormat
    return key.public_bytes(Encoding.DER, PublicFormat.SubjectPublicKeyInfo)


def get_rsa_private_key_bytes() -> bytes:
    from cryptography.hazmat.primitives.serialization import load_pem_private_key, Encoding, PrivateFormat
    key = load_pem_private_key(RSA_PRIVATE_KEY_PEM.encode(), password=None)
    return key.private_bytes(Encoding.DER, PrivateFormat.PKCS8, NoEncryption())


CMD_SN_SIGN = 65040       # 0xFE10 - SN signature send
CMD_RSA_KEY = 65042       # 0xFE12 - RSA public key send
RESP_SEND_KEY = 65041     # 0xFE11 - device says "send RSA key"
RESP_PROCEED = 65042      # 0xFE12 - device says "proceed to handshake"
CHUNK_SIZE = 100


def _build_chunked_packets(cmd_id: int, data: bytes) -> list[bytes]:
    total_chunks = (len(data) + CHUNK_SIZE - 1) // CHUNK_SIZE
    packets = []
    for i in range(total_chunks):
        offset = i * CHUNK_SIZE
        chunk = data[offset:offset + CHUNK_SIZE]
        pkt = bytearray()
        pkt += struct.pack("<H", cmd_id)
        pkt += struct.pack("<B", total_chunks)
        pkt += struct.pack("<B", i)
        pkt += chunk
        packets.append(bytes(pkt))
    return packets


def build_sn_signature_packets() -> list[bytes]:
    sig_bytes = get_sn_signature_bytes()
    return _build_chunked_packets(CMD_SN_SIGN, sig_bytes)


def build_self_signed_sn_signature_packets(device_sn: str = DEVICE_SN) -> list[bytes]:
    sig_bytes = generate_self_signed_sn_signature(device_sn)
    return _build_chunked_packets(CMD_SN_SIGN, sig_bytes)


def build_rsa_public_key_packets() -> list[bytes]:
    pub_key_bytes = get_rsa_public_key_bytes()
    return _build_chunked_packets(CMD_RSA_KEY, pub_key_bytes)


def parse_response(data: bytes) -> dict:
    if len(data) < 2:
        return {"raw": data.hex(), "error": "too short"}
    cmd_id = struct.unpack_from("<H", data, 0)[0]
    result = {
        "cmd_id": cmd_id,
        "cmd_hex": f"0x{cmd_id:04X}",
    }
    if cmd_id == RESP_SEND_KEY:
        result["type"] = "send_rsa_key"
        result["status"] = data[2] if len(data) > 2 else None
    elif cmd_id == RESP_PROCEED:
        result["type"] = "proceed_handshake"
        total_chunks = data[2] if len(data) > 2 else None
        chunk_idx = data[3] if len(data) > 3 else None
        result["total_chunks"] = total_chunks
        result["chunk_idx"] = chunk_idx
        result["chunk_data"] = data[4:].hex() if len(data) > 4 else ""
    else:
        result["type"] = "unknown"
        result["payload"] = data[2:].hex() if len(data) > 2 else ""
    return result


class HandshakeResponse:
    def __init__(self, data: bytes):
        self.raw = data
        self.status = 0
        self.version = 0
        self.audio_channels = 0
        self.storage_total = 0
        self.storage_used = 0
        self.support_wifi = False
        self.no_ns_agc = False
        self.is_ogg_audio = False
        self.hardware_version = 0

        if len(data) >= 1 and data[0] == 0x01:
            self._parse_standard(data)
        elif len(data) >= 2:
            cmd = struct.unpack_from("<H", data, 0)[0]
            if cmd == RESP_SEND_KEY and len(data) > 2:
                self.status = data[2]
            elif cmd == RESP_PROCEED and len(data) > 2:
                self.status = data[2]

    def _parse_standard(self, data: bytes):
        import struct as st
        if len(data) < 3:
            return
        cmd = st.unpack_from("<H", data, 1)[0]
        if cmd == 1 and len(data) > 3:
            self._parse_old_handshake(data[3:])

    def _parse_old_handshake(self, payload: bytes):
        if len(payload) < 1:
            return
        self.status = payload[0]
        if self.status != 0:
            return
        if len(payload) >= 3:
            self.version = struct.unpack_from("<H", payload, 1)[0]
        if len(payload) >= 5:
            self.hardware_version = struct.unpack_from("<H", payload, 3)[0]
        if len(payload) >= 7:
            self.audio_channels = struct.unpack_from("<H", payload, 5)[0]
        if len(payload) >= 9:
            self.support_wifi = bool(struct.unpack_from("<H", payload, 7)[0])
        if len(payload) >= 11:
            self.no_ns_agc = bool(struct.unpack_from("<H", payload, 9)[0])
        if len(payload) >= 13:
            self.is_ogg_audio = bool(struct.unpack_from("<H", payload, 11)[0])

    def __repr__(self):
        return (f"HandshakeResponse(status={self.status}, version={self.version}, "
                f"hw={self.hardware_version}, audio_ch={self.audio_channels}, "
                f"wifi={self.support_wifi}, ogg={self.is_ogg_audio})")

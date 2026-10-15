import struct
from cryptography.hazmat.primitives.ciphers.aead import ChaCha20Poly1305
from cryptography.hazmat.primitives.asymmetric import padding
from cryptography.hazmat.primitives.serialization import load_pem_private_key
import base64

PLAUD_VERIFICATION = b"PLAUD.AI"


class BleCrypto:
    def __init__(self, key: bytes, nonce: bytes, aad: bytes):
        if len(key) != 32:
            raise ValueError(f"Key must be 32 bytes, got {len(key)}")
        if len(nonce) != 12:
            raise ValueError(f"Nonce must be 12 bytes, got {len(nonce)}")
        self.key = key
        self.nonce = nonce
        self.aad = aad if aad else None
        self._cipher = ChaCha20Poly1305(key)
        self._send_counter = 1

    @staticmethod
    def derive_from_rsa_response(decrypted_80bytes: bytes) -> "BleCrypto":
        if len(decrypted_80bytes) < 56:
            raise ValueError(f"Need at least 56 bytes, got {len(decrypted_80bytes)}")
        key = decrypted_80bytes[0:32]
        nonce = decrypted_80bytes[32:44]
        aad = decrypted_80bytes[44:56]
        return BleCrypto(key, nonce, aad)

    @staticmethod
    def rsa_decrypt_device_response(secret_data: bytes, rsa_private_key_pem: str) -> bytes:
        private_key = load_pem_private_key(rsa_private_key_pem.encode(), password=None)
        return private_key.decrypt(secret_data, padding.PKCS1v15())

    def verify(self, verification_ciphertext: bytes) -> bool:
        try:
            plaintext = self._cipher.decrypt(self.nonce, verification_ciphertext, self.aad)
            return plaintext == PLAUD_VERIFICATION
        except Exception:
            return False

    def encrypt_send(self, plaintext: bytes) -> bytes:
        self._send_counter += 1
        counter_bytes = struct.pack("<I", self._send_counter)
        data_with_counter = counter_bytes + plaintext
        return self._cipher.encrypt(self.nonce, data_with_counter, self.aad)

    def decrypt_recv(self, ciphertext: bytes) -> bytes:
        decrypted = self._cipher.decrypt(self.nonce, ciphertext, self.aad)
        if len(decrypted) < 4:
            return decrypted
        return decrypted[4:]

    @property
    def send_counter(self) -> int:
        return self._send_counter

    def set_send_counter(self, value: int):
        self._send_counter = value

    def __repr__(self):
        return (f"BleCrypto(key={self.key.hex()[:16]}..., "
                f"nonce={self.nonce.hex()}, aad={self.aad.hex() if self.aad else 'None'}, "
                f"counter={self._send_counter})")

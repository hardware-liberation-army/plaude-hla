#!/usr/bin/env python3
"""Decrypt PLAUD.AI container files (.bin / .oggpa) downloaded via BLE.

Usage:
    python plaud_decrypt.py <input.bin> [output.ogg]
    python plaud_decrypt.py --fetch-key   # fetch RSA key from API (backs up old key first)

The encryption scheme (reverse-engineered from the PLAUD Flutter app):
  1. PLAUD.AI container = 512-byte header + ChaCha20-encrypted OGG audio
  2. Header offset 0x100-0x1FF = RSA-encrypted 32-byte ChaCha20 file key
  3. Header offset 0x084-0x08F = 12-byte UUID (ChaCha20 nonce suffix)
  4. ChaCha20 nonce = 4-byte counter (0x00000000) + 12-byte UUID
  5. RSA private key obtained from PLAUD API endpoint /e2ee/gen_key

Key management:
  - The E2EE RSA private key is cached in .plaud_rsa_key (gitignored).
  - If the key is already cached, the cloud API is NOT called -- no network needed.
  - --fetch-key backs up the old key to .plaud_rsa_key.bak.{timestamp} before overwriting.
  - If decryption fails with the cached key, the tool automatically tries all backed-up keys.
"""

import argparse
import base64
import glob
import json
import struct
import sys
import os
from datetime import datetime

from cryptography.hazmat.primitives.serialization import load_pem_private_key
from cryptography.hazmat.primitives.asymmetric import padding as asym_padding
from cryptography.hazmat.primitives.ciphers import Cipher, algorithms, modes

HEADER_MAGIC = b'PLAUD.AI'
HEADER_SIZE_OFFSET = 0xA
UUID_OFFSET = 0x84
UUID_SIZE = 12
ENCRYPTED_KEY_OFFSET = 0x100
ENCRYPTED_KEY_SIZE = 256

PROJECT_DIR = os.path.dirname(os.path.abspath(__file__))
KEY_CACHE_PATH = os.path.join(PROJECT_DIR, '.plaud_rsa_key')
KEY_BACKUP_GLOB = os.path.join(PROJECT_DIR, '.plaud_rsa_key.bak.*')


def _backup_key():
    if not os.path.exists(KEY_CACHE_PATH):
        return None
    ts = datetime.now().strftime('%Y%m%d_%H%M%S')
    backup_path = os.path.join(PROJECT_DIR, f'.plaud_rsa_key.bak.{ts}')
    old_key = open(KEY_CACHE_PATH, 'r').read()
    with open(backup_path, 'w') as f:
        f.write(old_key)
    print(f"Backed up existing key to {os.path.basename(backup_path)}")
    return backup_path


def fetch_rsa_key(api_token, api_base='https://api-apse1.plaud.ai'):
    import requests
    headers = {
        'Authorization': f'Bearer {api_token}',
        'Content-Type': 'application/json',
    }
    r = requests.post(f'{api_base}/e2ee/gen_key', headers=headers, json={}, timeout=30)
    r.raise_for_status()
    data = r.json()
    pk_b64 = data['data']['private_key_base64']
    return base64.b64decode(pk_b64).decode()


def _load_all_cached_keys():
    keys = []
    if os.path.exists(KEY_CACHE_PATH):
        keys.append(('current', open(KEY_CACHE_PATH, 'r').read()))
    backup_paths = sorted(glob.glob(KEY_BACKUP_GLOB), reverse=True)
    for bp in backup_paths:
        label = os.path.basename(bp)
        keys.append((label, open(bp, 'r').read()))
    return keys


def load_rsa_key(key_pem=None):
    if key_pem is None:
        if os.path.exists(KEY_CACHE_PATH):
            key_pem = open(KEY_CACHE_PATH, 'r').read()
        else:
            from .config import API_TOKEN
            key_pem = _fetch_and_cache_key(API_TOKEN)
    return load_pem_private_key(key_pem.encode() if isinstance(key_pem, str) else key_pem, password=None)


def _fetch_and_cache_key(api_token):
    _backup_key()
    key_pem = fetch_rsa_key(api_token)
    with open(KEY_CACHE_PATH, 'w') as f:
        f.write(key_pem)
    print(f"RSA key cached to {os.path.basename(KEY_CACHE_PATH)}")
    return key_pem


def _extract_encrypted_key_block(enc_data):
    if enc_data[:8] != HEADER_MAGIC:
        raise ValueError(f"Not a PLAUD.AI container (magic: {enc_data[:8]!r})")
    return enc_data[ENCRYPTED_KEY_OFFSET:ENCRYPTED_KEY_OFFSET + ENCRYPTED_KEY_SIZE]


def _try_decrypt_with_pem(enc_data, key_pem):
    rsa_key = load_pem_private_key(key_pem.encode() if isinstance(key_pem, str) else key_pem, password=None)

    header_size = struct.unpack_from('<H', enc_data, HEADER_SIZE_OFFSET)[0]
    uuid_bytes = enc_data[UUID_OFFSET:UUID_OFFSET + UUID_SIZE]
    encrypted_key_block = enc_data[ENCRYPTED_KEY_OFFSET:ENCRYPTED_KEY_OFFSET + ENCRYPTED_KEY_SIZE]

    try:
        file_key = rsa_key.decrypt(encrypted_key_block, asym_padding.PKCS1v15())
    except Exception:
        return None

    if len(file_key) != 32:
        return None

    nonce = b'\x00\x00\x00\x00' + uuid_bytes
    cipher = Cipher(algorithms.ChaCha20(file_key, nonce), mode=None)
    decryptor = cipher.decryptor()
    audio_data = decryptor.update(enc_data[header_size:])

    if audio_data[:4] != b'OggS':
        return None

    return audio_data


def decrypt_plaud_file(enc_path, rsa_key, out_path=None):
    enc_data = open(enc_path, 'rb').read()
    return _decrypt_data(enc_data, rsa_key, out_path)


def _decrypt_data(enc_data, rsa_key, out_path=None):
    magic = enc_data[:8]
    if magic != HEADER_MAGIC:
        raise ValueError(f"Not a PLAUD.AI container (magic: {magic!r})")

    header_size = struct.unpack_from('<H', enc_data, HEADER_SIZE_OFFSET)[0]
    uuid_bytes = enc_data[UUID_OFFSET:UUID_OFFSET + UUID_SIZE]
    encrypted_key_block = enc_data[ENCRYPTED_KEY_OFFSET:ENCRYPTED_KEY_OFFSET + ENCRYPTED_KEY_SIZE]

    file_key = rsa_key.decrypt(encrypted_key_block, asym_padding.PKCS1v15())
    if len(file_key) != 32:
        raise ValueError(f"Expected 32-byte file key, got {len(file_key)}")

    nonce = b'\x00\x00\x00\x00' + uuid_bytes
    cipher = Cipher(algorithms.ChaCha20(file_key, nonce), mode=None)
    decryptor = cipher.decryptor()
    audio_data = decryptor.update(enc_data[header_size:])

    if audio_data[:4] != b'OggS':
        raise ValueError("Decryption failed: no OGG header found")

    if out_path:
        with open(out_path, 'wb') as f:
            f.write(audio_data)

    return audio_data


def decrypt_with_any_cached_key(enc_data, out_path=None):
    all_keys = _load_all_cached_keys()
    if not all_keys:
        raise FileNotFoundError(
            "No cached RSA key found. Run with --fetch-key first, or use --key <pem_file>."
        )

    for label, key_pem in all_keys:
        result = _try_decrypt_with_pem(enc_data, key_pem)
        if result is not None:
            print(f"Decrypted using key: {label}")
            if out_path:
                with open(out_path, 'wb') as f:
                    f.write(result)
            return result

    raise ValueError(
        "Decryption failed with all cached keys. The file may have been encrypted with a different RSA key.\n"
        "Try --fetch-key to get the current key from the API (old key will be backed up)."
    )


def main():
    parser = argparse.ArgumentParser(description='Decrypt PLAUD.AI container files')
    parser.add_argument('input', help='Input PLAUD.AI container file (.bin or .oggpa)')
    parser.add_argument('output', nargs='?', help='Output OGG file (default: <input>.ogg)')
    parser.add_argument('--key', help='RSA private key PEM file')
    parser.add_argument('--fetch-key', action='store_true', help='Fetch RSA key from API and cache it (backs up old key)')
    parser.add_argument('--try-all', action='store_true', help='Try all cached keys (current + backups)')
    args = parser.parse_args()

    if args.fetch_key:
        from .config import API_TOKEN
        _fetch_and_cache_key(API_TOKEN)
        return

    enc_data = open(args.input, 'rb').read()
    out_path = args.output
    if not out_path:
        base, _ = os.path.splitext(args.input)
        out_path = base + '.ogg'

    if args.key:
        key_pem = open(args.key, 'r').read()
        rsa_key = load_pem_private_key(key_pem.encode(), password=None)
        audio = decrypt_plaud_file(args.input, rsa_key, out_path)
        print(f"Decrypted {len(audio)} bytes -> {out_path}")
        return

    if args.try_all or not os.path.exists(KEY_CACHE_PATH):
        audio = decrypt_with_any_cached_key(enc_data, out_path)
        print(f"Decrypted {len(audio)} bytes -> {out_path}")
        return

    key_pem = open(KEY_CACHE_PATH, 'r').read()
    rsa_key = load_pem_private_key(key_pem.encode(), password=None)
    try:
        audio = _decrypt_data(enc_data, rsa_key, out_path)
        print(f"Decrypted {len(audio)} bytes -> {out_path}")
    except (ValueError, Exception) as e:
        print(f"Failed with current cached key: {e}")
        print("Trying all cached keys (current + backups)...")
        audio = decrypt_with_any_cached_key(enc_data, out_path)
        print(f"Decrypted {len(audio)} bytes -> {out_path}")


if __name__ == '__main__':
    main()

import argparse
import asyncio
import sys
import json
import time as _time
import struct
import base64
import shutil
from pathlib import Path
from .ble_transport import BleTransport
from .protocol import (
    Packet, Cmd, CMD_NAMES, hex_dump, parse_notification,
    build_sync_time, build_device_status, build_storage_info,
    build_record_start, build_record_stop, build_handshake_old,
    build_disconnect, build_depair, build_format_storage,
    build_raw, build_cmd8_sub,
    read_uint8, read_uint16, read_uint32, read_uint64,
    build_handshake,
)
from .handshake import (
    build_sn_signature_packets, build_rsa_public_key_packets,
    parse_response, HandshakeResponse,
    build_self_signed_sn_signature_packets,
)
from .sdk_auth import full_sdk_auth
from .config import DEVICE_SN, RSA_PRIVATE_KEY_PEM, API_TOKEN, FIRMWARE_PRIVATE_KEY_PEM
from .crypto import BleCrypto
from .file_sync import FileSyncManager

transport = BleTransport()
_notification_log: list[bytes] = []
_crypto: BleCrypto | None = None
_file_sync: FileSyncManager | None = None


def on_notify(data: bytes):
    _notification_log.append(data)
    if _file_sync:
        _file_sync.on_notification(data)
        return
    if _crypto:
        try:
            dec = _crypto.decrypt_recv(data)
            print(f"\n[NOTIFY] ({len(data)}B enc) -> ({len(dec)}B dec) {dec.hex()}")
        except Exception:
            print(f"\n[NOTIFY ENC RAW] ({len(data)}B) {data.hex()[:60]}...")
    else:
        pkt = parse_notification(data)
        if pkt:
            print(f"\n[NOTIFY] {pkt}")
        else:
            print(f"\n[NOTIFY RAW] {data.hex()}")


async def cmd_scan(args):
    print(f"Scanning for {args.duration}s...")
    if args.all:
        devices = await BleTransport.scan_all(args.duration)
        if not devices:
            print("No BLE devices found.")
            return
        print(f"\nFound {len(devices)} device(s):\n")
        print(f"{'Address':<20} {'RSSI':>5}  {'Name':<30}  {'Services'}")
        print("-" * 100)
        for dev, info in devices:
            name = info["name"] or "(unknown)"
            svcs = ", ".join(info["service_uuids"][:3])
            if len(info["service_uuids"]) > 3:
                svcs += "..."
            plaud_marker = " <<< PLAUD" if any(
                "plaud" in name.lower() for _ in [1]
            ) or any(
                u.lower().startswith("00001910") for u in info["service_uuids"]
            ) else ""
            print(f"{dev.address:<20} {info['rssi']:>5}  {name:<30}  {svcs}{plaud_marker}")
    else:
        devices = await BleTransport.scan(args.duration)
        if not devices:
            print("No Plaud devices found.")
            return
        print(f"\nFound {len(devices)} Plaud device(s):\n")
        for dev in devices:
            print(f"  {dev.address}  {dev.name or '(unknown)'}")


async def cmd_connect(args):
    print(f"Connecting to {args.address}...")
    connected = await transport.connect(args.address)
    if not connected:
        print("Failed to connect.")
        return

    print("Connected! Enumerating services...\n")
    services = await transport.discover_services()

    for svc_uuid, svc in services.items():
        print(f"Service: {svc_uuid}")
        if svc.get("description"):
            print(f"  Description: {svc['description']}")
        for char in svc["characteristics"]:
            props = ", ".join(char["properties"])
            print(f"  Characteristic: {char['uuid']}")
            print(f"    Properties: {props}")
            if char["descriptors"]:
                for desc in char["descriptors"]:
                    print(f"    Descriptor: {desc['uuid']} (handle: {desc['handle']})")

    if args.subscribe:
        print("\nSubscribing to notifications...")
        await transport.subscribe_notify(on_notify)
        print("Subscribed. Waiting for notifications (Ctrl+C to stop)...")
        try:
            while transport.is_connected:
                await asyncio.sleep(1)
        except KeyboardInterrupt:
            print("\nStopping...")


async def cmd_battery(args):
    async with _connect(args):
        level = await transport.read_battery()
        if level >= 0:
            print(f"Battery: {level}%")
        else:
            print("Could not read battery level.")


async def cmd_info(args):
    async with _connect(args):
        services = await transport.discover_services()
        print("=== Device Services ===")
        for svc_uuid, svc in services.items():
            print(f"\n  Service: {svc_uuid}")

        print("\n=== Battery ===")
        level = await transport.read_battery()
        print(f"  Level: {level}%")

        print("\nRequesting device status...")
        await transport.subscribe_notify(on_notify)
        await asyncio.sleep(0.5)
        await transport.write_command(build_device_status())
        await asyncio.sleep(2)

        print("\nRequesting storage info...")
        await transport.write_command(build_storage_info())
        await asyncio.sleep(2)


async def cmd_listen(args):
    async with _connect(args):
        print(f"Listening for notifications for {args.duration}s...")
        print("Subscribing...")
        await transport.subscribe_notify(on_notify)

        await asyncio.sleep(0.5)
        if args.sync_time:
            print("Sending sync time...")
            await transport.write_command(build_sync_time())
            await asyncio.sleep(0.5)

        try:
            await asyncio.sleep(args.duration)
        except KeyboardInterrupt:
            print("\nStopping...")
        print(f"\nReceived {len(_notification_log)} notification(s).")


async def cmd_raw(args):
    async with _connect(args):
        await transport.subscribe_notify(on_notify)
        await asyncio.sleep(0.5)

        cmd_id = int(args.cmd, 0)
        payload = bytes.fromhex(args.payload) if args.payload else b""
        data = build_raw(cmd_id, payload)

        print(f"Sending: cmd=0x{cmd_id:04X} ({cmd_id}) payload={payload.hex()}")
        print(f"Raw bytes: {data.hex()}")
        ok = await transport.write_command(data)
        print(f"Write result: {'OK' if ok else 'FAILED'}")

        if args.wait:
            print(f"Waiting {args.wait}s for response...")
            await asyncio.sleep(args.wait)


async def cmd_record(args):
    async with _connect(args):
        await transport.subscribe_notify(on_notify)
        await asyncio.sleep(0.5)

        if args.action == "start":
            print("Starting recording...")
            await transport.write_command(build_record_start())
        elif args.action == "stop":
            print("Stopping recording...")
            await transport.write_command(build_record_stop())
        else:
            print(f"Unknown action: {args.action}")
            return

        await asyncio.sleep(2)


async def cmd_handshake(args):
    async with _connect(args):
        await transport.subscribe_notify(on_notify)
        await asyncio.sleep(0.5)

        print("Attempting old-protocol handshake (cmd=1)...")
        data = build_handshake_old()
        print(f"Sending: {data.hex()}")
        ok = await transport.write_command(data)
        print(f"Write result: {'OK' if ok else 'FAILED'}")

        print("Waiting for response (10s)...")
        await asyncio.sleep(10)


async def cmd_disconnect_cmd(args):
    async with _connect(args):
        await transport.write_command(build_disconnect())
        print("Disconnect command sent.")


async def cmd_depair(args):
    async with _connect(args):
        await transport.subscribe_notify(on_notify)
        await asyncio.sleep(0.5)
        await transport.write_command(build_depair())
        print("Depair command sent.")
        await asyncio.sleep(2)


def _reassemble_fe12_chunks(notifications: list[bytes]) -> bytes | None:
    chunks: dict[int, bytes] = {}
    for n in notifications:
        if len(n) < 4:
            continue
        cmd_id = struct.unpack_from("<H", n, 0)[0]
        if cmd_id != 0xFE12:
            continue
        total = n[2]
        idx = n[3]
        chunks[idx] = n[4:]
    if not chunks:
        return None
    result = bytearray()
    for i in range(max(chunks.keys()) + 1):
        if i not in chunks:
            print(f"  WARNING: missing chunk {i}")
            continue
        result.extend(chunks[i])
    return bytes(result) if result else None


async def _encrypted_write(data: bytes):
    global _crypto
    if _crypto:
        encrypted = _crypto.encrypt_send(data)
        print(f"  >> encrypted ({len(encrypted)}B, counter={_crypto.send_counter}): {encrypted.hex()[:40]}...")
        await transport.write_command(encrypted)
    else:
        await transport.write_command(data)


def _try_decrypt_notification(data: bytes) -> bytes | None:
    global _crypto
    if not _crypto:
        return data
    try:
        return _crypto.decrypt_recv(data)
    except Exception as e:
        print(f"  decrypt failed: {e}")
        return None


async def cmd_sdk_connect(args):
    global _crypto
    api_token = full_sdk_auth()

    print(f"\nScanning for Plaud device ({args.duration}s)...")
    devices = await BleTransport.scan(args.duration)
    if not devices:
        print("No Plaud devices found. Make sure phone BT is off and device is awake.")
        return

    print(f"\nFound {len(devices)} device(s):")
    for i, dev in enumerate(devices):
        print(f"  [{i}] {dev.address}  {dev.name or '(unknown)'}")

    if args.address:
        target_addr = args.address
    elif len(devices) == 1:
        target_addr = devices[0].address
    else:
        try:
            idx = int(input("Select device [0]: ") or "0")
            target_addr = devices[idx].address
        except (ValueError, IndexError):
            print("Invalid selection.")
            return

    print(f"\nConnecting to {target_addr}...")
    connected = await transport.connect(target_addr)
    if not connected:
        print("Failed to connect.")
        return
    print("Connected.")

    await transport.subscribe_notify(on_notify)
    await asyncio.sleep(0.3)

    # Phase 1: SN Signature exchange (unencrypted)
    print("\n=== Phase 1: SN Signature (0xFE10) - unencrypted ===")
    sn_packets = build_sn_signature_packets()
    for i, pkt in enumerate(sn_packets):
        print(f"  Sending SN sig chunk {i+1}/{len(sn_packets)} ({len(pkt)}B): {pkt.hex()[:40]}...")
        await transport.write_command(pkt)
        await asyncio.sleep(0.05)

    print("  Waiting for 0xFE12 response...")
    _notification_log.clear()
    await asyncio.sleep(2)

    fe12_chunks_raw = [n for n in _notification_log if len(n) >= 2 and struct.unpack_from("<H", n, 0)[0] == 0xFE12]
    if not fe12_chunks_raw:
        print("  No 0xFE12 response. Dumping all notifications:")
        for n in _notification_log:
            print(f"    {n.hex()}")
        await transport.disconnect()
        return

    print(f"  Got {len(fe12_chunks_raw)} 0xFE12 chunk(s)")

    # Phase 2: Key derivation
    print("\n=== Phase 2: Key Derivation ===")
    secret_data = _reassemble_fe12_chunks(_notification_log)
    if not secret_data:
        print("  Failed to reassemble 0xFE12 chunks")
        await transport.disconnect()
        return
    print(f"  Reassembled secret data: {len(secret_data)}B")

    rsa_private_key_pem = RSA_PRIVATE_KEY_PEM
    try:
        decrypted = BleCrypto.rsa_decrypt_device_response(secret_data, rsa_private_key_pem)
    except Exception as e:
        print(f"  RSA decryption failed: {e}")
        print(f"  Secret data hex: {secret_data.hex()}")
        await transport.disconnect()
        return

    print(f"  RSA decrypted ({len(decrypted)}B): {decrypted.hex()}")
    print(f"    Key  (0:32, 32B): {decrypted[0:32].hex()}")
    print(f"    Nonce(32:44, 12B): {decrypted[32:44].hex()}")
    print(f"    AAD  (44:56, 12B): {decrypted[44:56].hex()}")
    if len(decrypted) > 56:
        extra = decrypted[56:]
        print(f"    Extra (56:{len(decrypted)}, {len(extra)}B): {extra.hex()}")

    # Save full RSA response to file for offline analysis
    Path("/tmp/plaud_rsa_response.bin").write_bytes(decrypted)
    Path("/tmp/plaud_rsa_response.hex").write_text(decrypted.hex())
    print(f"  [SAVED] Full RSA response -> /tmp/plaud_rsa_response.bin/.hex")

    if len(decrypted) >= 80:
        verification_ct = decrypted[56:80]
    elif len(decrypted) >= 56:
        verification_ct = decrypted[56:]
    else:
        verification_ct = b""

    _crypto = BleCrypto.derive_from_rsa_response(decrypted)
    print(f"  Crypto initialized: {_crypto}")

    if verification_ct:
        verified = _crypto.verify(verification_ct)
        print(f"  Verification {'PASSED' if verified else 'FAILED'} (expected 'PLAUD.AI')")
        if not verified:
            print(f"    Verification CT: {verification_ct.hex()}")
            await transport.disconnect()
            return

    # Phase 3: Encrypted handshake (cmd=1)
    print("\n=== Phase 3: Encrypted Handshake (cmd=1) ===")
    import base64 as _b64, json as _json
    jwt_payload = API_TOKEN.split('.')[1]
    jwt_payload += '=' * (4 - len(jwt_payload) % 4)
    jwt_sub = _json.loads(_b64.b64decode(jwt_payload))['sub']
    print(f"  Using JWT sub as bind token: {jwt_sub}")
    hs_data = build_handshake(bind_token=jwt_sub, sub_type=0, port_version=20)
    print(f"  Plaintext: {hs_data.hex()}")
    await _encrypted_write(hs_data)

    _notification_log.clear()
    print("  Waiting for encrypted handshake response...")
    await asyncio.sleep(3)

    if _notification_log:
        print(f"  Got {len(_notification_log)} notification(s):")
        for n in _notification_log:
            decrypted_n = _try_decrypt_notification(n)
            if decrypted_n:
                pkt = parse_notification(decrypted_n)
                if pkt:
                    print(f"    [DECRYPTED] {pkt}")
                else:
                    print(f"    [DECRYPTED RAW] {decrypted_n.hex()}")
            else:
                print(f"    [ENCRYPTED RAW] {n.hex()}")
    else:
        print("  No response to handshake. Trying raw handshake fallback...")
        _crypto = None
        hs_data2 = build_handshake(bind_token=jwt_sub, sub_type=0, port_version=20)
        await transport.write_command(hs_data2)
        _notification_log.clear()
        await asyncio.sleep(3)
        for n in _notification_log:
            pkt = parse_notification(n)
            if pkt:
                print(f"    {pkt}")
            else:
                print(f"    Raw: {n.hex()}")

    # Phase 4: Post-handshake commands
    if _crypto or _notification_log:
        print("\n=== Phase 4: Post-handshake ===")
        for label, builder in [
            ("Sync time", build_sync_time),
            ("Battery", lambda: build_raw(9, b"")),
            ("Device status", build_device_status),
            ("Storage info", build_storage_info),
            ("File sync init (cmd 25)", lambda: build_raw(25, b"\x00")),
            ("Storage info (after init)", build_storage_info),
        ]:
            print(f"  Sending: {label}")
            await _encrypted_write(builder())
            await asyncio.sleep(0.8)

        _notification_log.clear()
        await asyncio.sleep(3)
        print(f"\n  Received {len(_notification_log)} notification(s):")
        for n in _notification_log:
            decrypted_n = _try_decrypt_notification(n)
            if decrypted_n:
                pkt = parse_notification(decrypted_n)
                if pkt:
                    print(f"    [DEC] {pkt}")
                else:
                    print(f"    [DEC RAW] {decrypted_n.hex()}")

    print("\nListening for 10 more seconds (Ctrl+C to stop)...")
    try:
        await asyncio.sleep(10)
    except KeyboardInterrupt:
        pass

    await transport.disconnect()
    print("Done.")


async def _do_sdk_connect(address: str | None = None, scan_duration: float = 10.0) -> bool:
    global _crypto, _file_sync
    api_token = full_sdk_auth()

    print(f"\nScanning for Plaud device ({scan_duration}s)...")
    devices = await BleTransport.scan(scan_duration)
    if not devices:
        print("No Plaud devices found.")
        return False

    print(f"\nFound {len(devices)} device(s):")
    for i, dev in enumerate(devices):
        print(f"  [{i}] {dev.address}  {dev.name or '(unknown)'}")

    if address:
        target_addr = address
    elif len(devices) == 1:
        target_addr = devices[0].address
    else:
        try:
            idx = int(input("Select device [0]: ") or "0")
            target_addr = devices[idx].address
        except (ValueError, IndexError):
            print("Invalid selection.")
            return False

    print(f"\nConnecting to {target_addr}...")
    connected = await transport.connect(target_addr)
    if not connected:
        print("Failed to connect.")
        return False
    print("Connected.")

    await transport.subscribe_notify(on_notify)
    await asyncio.sleep(0.3)

    print("\n=== Phase 1: SN Signature (0xFE10) ===")
    sn_packets = build_sn_signature_packets()
    for i, pkt in enumerate(sn_packets):
        print(f"  Sending SN sig chunk {i+1}/{len(sn_packets)} ({len(pkt)}B)")
        await transport.write_command(pkt)
        await asyncio.sleep(0.05)

    print("  Waiting for 0xFE12 response...")
    _notification_log.clear()
    await asyncio.sleep(2)

    fe12_chunks = [n for n in _notification_log if len(n) >= 2 and struct.unpack_from("<H", n, 0)[0] == 0xFE12]
    if not fe12_chunks:
        print("  No 0xFE12 response.")
        await transport.disconnect()
        return False
    print(f"  Got {len(fe12_chunks)} 0xFE12 chunk(s)")

    print("\n=== Phase 2: Key Derivation ===")
    secret_data = _reassemble_fe12_chunks(_notification_log)
    if not secret_data:
        print("  Failed to reassemble 0xFE12 chunks")
        await transport.disconnect()
        return False

    try:
        decrypted = BleCrypto.rsa_decrypt_device_response(secret_data, RSA_PRIVATE_KEY_PEM)
    except Exception as e:
        print(f"  RSA decryption failed: {e}")
        await transport.disconnect()
        return False

    print(f"  RSA decrypted ({len(decrypted)}B): {decrypted.hex()}")
    print(f"    Key  (0:32):  {decrypted[0:32].hex()}")
    print(f"    Nonce(32:44): {decrypted[32:44].hex()}")
    print(f"    AAD  (44:56): {decrypted[44:56].hex()}")
    if len(decrypted) > 56:
        print(f"    Extra(56+):   {decrypted[56:].hex()}")

    Path("/tmp/plaud_rsa_response.bin").write_bytes(decrypted)
    Path("/tmp/plaud_rsa_response.hex").write_text(decrypted.hex())
    print(f"  [SAVED] Full RSA response -> /tmp/plaud_rsa_response.bin/.hex")

    _crypto = BleCrypto.derive_from_rsa_response(decrypted)
    print(f"  Crypto initialized")

    if len(decrypted) >= 80:
        verified = _crypto.verify(decrypted[56:80])
        print(f"  Verification {'PASSED' if verified else 'FAILED'}")
        if not verified:
            await transport.disconnect()
            return False

    print("\n=== Phase 3: Encrypted Handshake ===")
    jwt_payload = API_TOKEN.split('.')[1]
    jwt_payload += '=' * (4 - len(jwt_payload) % 4)
    jwt_sub = json.loads(base64.b64decode(jwt_payload))['sub']
    hs_data = build_handshake(bind_token=jwt_sub, sub_type=0, port_version=20)
    await _encrypted_write(hs_data)

    _notification_log.clear()
    await asyncio.sleep(3)

    if _notification_log:
        for n in _notification_log:
            dec = _try_decrypt_notification(n)
            if dec:
                pkt = parse_notification(dec)
                if pkt:
                    print(f"  [DEC] {pkt}")

    print("\n=== Phase 4: Post-handshake (matching official app sequence) ===")
    init_sequence = [
        ("Sync time", build_sync_time),
        ("Battery (cmd 9)", lambda: build_raw(9, b"")),
        ("Device status", build_device_status),
        ("cmd 8 sub 0x0F", lambda: build_cmd8_sub(0x0F)),
        ("cmd 8 sub 0x11", lambda: build_cmd8_sub(0x11)),
        ("Device name (cmd 108)", lambda: build_raw(108, b"")),
        ("cmd 141", lambda: build_raw(141, b"")),
        ("Device name (cmd 108) again", lambda: build_raw(108, b"")),
        ("File sync init (cmd 25)", lambda: build_raw(25, b"\x00")),
        ("Device status", build_device_status),
        ("cmd 8 sub 0x17", lambda: build_cmd8_sub(0x17)),
        ("cmd 24", lambda: build_raw(24, b"")),
        ("cmd 8 sub 0x14", lambda: build_cmd8_sub(0x14)),
        ("cmd 8 sub 0x13", lambda: build_cmd8_sub(0x13)),
        ("cmd 8 sub 0x18", lambda: build_cmd8_sub(0x18)),
        ("cmd 8 sub 0x19", lambda: build_cmd8_sub(0x19)),
        ("cmd 8 sub 0x1F", lambda: build_cmd8_sub(0x1F)),
        ("cmd 8 sub 0x1B", lambda: build_cmd8_sub(0x1B)),
        ("cmd 8 sub 0x20", lambda: build_cmd8_sub(0x20)),
        ("cmd 6 (sync statistics)", lambda: build_raw(6, b"")),
    ]
    for label, builder in init_sequence:
        print(f"  Sending: {label}")
        await _encrypted_write(builder())
        await asyncio.sleep(0.15)

    _notification_log.clear()
    await asyncio.sleep(1.0)
    for n in _notification_log:
        dec = _try_decrypt_notification(n)
        if dec:
            pkt = parse_notification(dec)
            if pkt:
                print(f"    [DEC] {pkt}")

    _file_sync = FileSyncManager(transport, _crypto, _encrypted_write)
    print("\n=== Ready for file operations ===")
    return True


async def cmd_record_test(args):
    global _crypto, _file_sync
    try:
        if not await _do_sdk_connect(args.address, args.duration):
            return
        assert _file_sync is not None

        print("\n=== Starting recording via BLE ===")
        await _encrypted_write(build_record_start())
        await asyncio.sleep(0.5)

        rec_duration = args.rec_duration
        print(f"  Recording for {rec_duration}s...")
        await asyncio.sleep(rec_duration)

        print("  Stopping recording...")
        _notification_log.clear()
        _file_sync._clear()
        await _encrypted_write(build_record_stop())
        await asyncio.sleep(5.0)

        print("  All notifications after stop:")
        for i, n in enumerate(_file_sync._notifications):
            print(f"    [{i}] ({len(n)}B) {n.hex()}")

        print("\n=== Full init sequence after recording ===")
        for label, builder in [
            ("Device status", build_device_status),
            ("cmd 8 sub 0x17", lambda: build_cmd8_sub(0x17)),
            ("cmd 8 sub 0x1A", lambda: build_cmd8_sub(0x1A)),
            ("Device status", build_device_status),
            ("Storage info", build_storage_info),
        ]:
            await _encrypted_write(builder())
            await asyncio.sleep(0.5)

        _file_sync._clear()
        await asyncio.sleep(3.0)
        print("  Responses:")
        for n in _file_sync._notifications:
            print(f"    ({len(n)}B) {n.hex()}")

        print("\n=== Listing files ===")
        file_list = await _file_sync.get_file_list()
        if not file_list.files:
            print("  No files found after recording.")
        else:
            print(f"  Found {len(file_list.files)} file(s):")
            for i, f in enumerate(file_list.files):
                from file_sync import _human_size
                print(f"    [{i}] session_id={f.session_id} size={_human_size(f.file_size)} attr={f.attribute} scene={f.scene}")
    finally:
        if transport.is_connected:
            await transport.disconnect()
        _crypto = None
        _file_sync = None


async def cmd_list_files(args):
    global _crypto, _file_sync
    try:
        if not await _do_sdk_connect(args.address, args.duration):
            return
        assert _file_sync is not None

        file_list = await _file_sync.get_file_list()
        if not file_list.files:
            print("\nNo files on device.")
            return

        print(f"\n{'#':<4} {'Session ID':<14} {'Size':<12} {'Attr':<6} {'Scene'}")
        print("-" * 50)
        for i, f in enumerate(file_list.files):
            from file_sync import _human_size
            print(f"{i:<4} {f.session_id:<14} {_human_size(f.file_size):<12} {f.attribute:<6} {f.scene}")
        print(f"\nTotal: {len(file_list.files)} file(s), {file_list.total} reported")
    finally:
        if transport.is_connected:
            await transport.disconnect()
        _crypto = None
        _file_sync = None


async def cmd_download_file(args):
    global _crypto, _file_sync
    try:
        if not await _do_sdk_connect(args.address, args.duration):
            return
        assert _file_sync is not None

        if args.all:
            paths = await _file_sync.download_all_files(output_dir=args.output_dir)
            print(f"\nDownloaded {len(paths)} file(s)")
        elif args.session_id is not None:
            file_list = await _file_sync.get_file_list()
            target = None
            for f in file_list.files:
                if f.session_id == args.session_id:
                    target = f
                    break
            if not target:
                print(f"Session ID {args.session_id} not found.")
                ids = [f.session_id for f in file_list.files]
                print(f"Available: {ids}")
                return

            from pathlib import Path
            out_dir = Path(args.output_dir)
            out_dir.mkdir(parents=True, exist_ok=True)
            filepath = str(out_dir / f"recording_{target.session_id}.ogg")
            await _file_sync.download_file(target.session_id, target.file_size, output_path=filepath)
        else:
            print("Specify --session-id <id> or --all")
    finally:
        if transport.is_connected:
            await transport.disconnect()
        _crypto = None
        _file_sync = None


async def cmd_capture_session(args):
    global _crypto, _file_sync

    import time as _time
    from datetime import datetime
    ts = datetime.now().strftime("%Y%m%d_%H%M%S")
    log_dir = Path(f"/tmp/plaud_capture_{ts}")
    log_dir.mkdir(parents=True, exist_ok=True)
    print(f"  Capture log dir: {log_dir}")

    log_file = open(log_dir / "session.log", "w")

    def log(msg):
        print(msg)
        log_file.write(msg + "\n")
        log_file.flush()

    try:
        if not await _do_sdk_connect(args.address, args.duration):
            return
        assert _file_sync is not None

        # The RSA response is already saved by _do_sdk_connect
        if Path("/tmp/plaud_rsa_response.bin").exists():
            import shutil
            shutil.copy("/tmp/plaud_rsa_response.bin", log_dir / "rsa_response.bin")
            shutil.copy("/tmp/plaud_rsa_response.hex", log_dir / "rsa_response.hex")

        # Save crypto state
        if _crypto:
            (log_dir / "session_key.hex").write_text(_crypto.key.hex())
            (log_dir / "session_nonce.hex").write_text(_crypto.nonce.hex())
            (log_dir / "session_aad.hex").write_text((_crypto.aad.hex() if _crypto.aad else "none"))
            log(f"  Session key: {_crypto.key.hex()}")
            log(f"  Session nonce: {_crypto.nonce.hex()}")
            log(f"  Session AAD: {_crypto.aad.hex() if _crypto.aad else 'none'}")

        # Request device status (cmd 3) to get keyState
        log("\n=== Querying device state for keyState ===")
        _notification_log.clear()
        await _encrypted_write(build_device_status())
        await asyncio.sleep(1.5)

        for n in _notification_log:
            dec = _try_decrypt_notification(n)
            if dec:
                (log_dir / "device_state_response.bin").write_bytes(dec)
                log(f"  Device state response ({len(dec)}B): {dec.hex()}")
                if len(dec) >= 4 and dec[0] == 0x01:
                    cmd = struct.unpack_from("<H", dec, 1)[0]
                    if cmd == 3 and len(dec) > 3:
                        state_payload = dec[3:]
                        log(f"  State payload ({len(state_payload)}B): {state_payload.hex()}")
                        # Try to parse keyState - look for it in the response
                        # The iOS SDK has: state, privacy, keyState, uDisk, findMyToken, hasSndpKey, deviceAccessToken
                        # These are likely sequential fields in the payload
                        if len(state_payload) >= 2:
                            log(f"  state={state_payload[0]}, next_bytes={state_payload[1:min(20,len(state_payload))].hex()}")

        # List files
        log("\n=== Listing files ===")
        file_list = await _file_sync.get_file_list()
        if not file_list.files:
            log("  No files on device. Make a recording first!")
            await transport.disconnect()
            return

        log(f"  Found {len(file_list.files)} file(s):")
        for i, f in enumerate(file_list.files):
            log(f"    [{i}] session_id={f.session_id} size={f.file_size} attr={f.attribute} scene={f.scene}")

        # Download first (or specified) file
        if args.session_id:
            target = None
            for f in file_list.files:
                if f.session_id == args.session_id:
                    target = f
                    break
            if not target:
                log(f"  Session {args.session_id} not found")
                return
        else:
            target = file_list.files[0]
            log(f"\n=== Downloading file: session_id={target.session_id}, size={target.file_size} ===")

        filepath = str(log_dir / f"recording_{target.session_id}.bin")
        raw_data = await _file_sync.download_file(target.session_id, target.file_size, output_path=filepath)

        # Also save with .ogg extension for comparison
        shutil.copy(filepath, str(log_dir / f"recording_{target.session_id}.ogg"))

        # Analyze the downloaded file
        log(f"\n=== Analyzing downloaded file ({len(raw_data)}B) ===")
        if len(raw_data) >= 8:
            log(f"  First 8 bytes: {raw_data[:8]}")
            if raw_data[:8] == b'PLAUD.AI':
                log(f"  Container: PLAUD.AI")
                log(f"  Header bytes 8-16: {raw_data[8:16].hex()}")
                log(f"  Bytes at 0x84-0x90: {raw_data[0x84:0x90].hex()}")
                log(f"  Payload starts at 0x100, size={len(raw_data) - 0x100}B")
            elif raw_data[:4] == b'OggS':
                log(f"  Container: OGG")

        log(f"\n=== All capture files saved to {log_dir} ===")
        log(f"  rsa_response.bin/hex - Full RSA handshake response")
        log(f"  session_key.hex      - ChaCha20 session key")
        log(f"  session_nonce.hex    - ChaCha20 nonce")
        log(f"  session_aad.hex      - ChaCha20 AAD")
        log(f"  device_state_response.bin - Device state (has keyState)")
        log(f"  recording_*.bin/ogg  - Downloaded recording")

    finally:
        if transport.is_connected:
            await transport.disconnect()
        _crypto = None
        _file_sync = None
        log_file.close()


async def cmd_delete_file(args):
    global _crypto, _file_sync
    try:
        if not await _do_sdk_connect(args.address, args.duration):
            return
        assert _file_sync is not None

        if args.clear_all:
            ok = await _file_sync.clear_all_files()
            print(f"Clear all: {'OK' if ok else 'FAILED'}")
        else:
            ok = await _file_sync.delete_file(args.session_id)
            print(f"Delete session {args.session_id}: {'OK' if ok else 'FAILED'}")
    finally:
        if transport.is_connected:
            await transport.disconnect()
        _crypto = None
        _file_sync = None


async def cmd_self_sign_connect(args):
    global _crypto

    print(f"\nScanning for Plaud device ({args.duration}s)...")
    devices = await BleTransport.scan(args.duration)
    if not devices:
        print("No Plaud devices found.")
        return

    print(f"\nFound {len(devices)} device(s):")
    for i, dev in enumerate(devices):
        print(f"  [{i}] {dev.address}  {dev.name or '(unknown)'}")

    if args.address:
        target_addr = args.address
    elif len(devices) == 1:
        target_addr = devices[0].address
    else:
        try:
            idx = int(input("Select device [0]: ") or "0")
            target_addr = devices[idx].address
        except (ValueError, IndexError):
            print("Invalid selection.")
            return

    print(f"\nConnecting to {target_addr}...")
    connected = await transport.connect(target_addr)
    if not connected:
        print("Failed to connect.")
        return
    print("Connected.")

    await transport.subscribe_notify(on_notify)
    await asyncio.sleep(0.3)

    # Generate fresh RSA key pair for this session
    from cryptography.hazmat.primitives.asymmetric import rsa
    from cryptography.hazmat.primitives import serialization
    fresh_key = rsa.generate_private_key(public_exponent=65537, key_size=2048)
    fresh_private_pem = fresh_key.private_bytes(
        serialization.Encoding.PEM,
        serialization.PrivateFormat.PKCS8,
        serialization.NoEncryption()
    ).decode()
    fresh_public_der = fresh_key.public_key().public_bytes(
        serialization.Encoding.DER,
        serialization.PublicFormat.SubjectPublicKeyInfo
    )

    # Phase 1: Self-signed SN Signature
    print("\n=== Phase 1: Self-Signed SN Signature (0xFE10) ===")
    sn_packets = build_self_signed_sn_signature_packets(DEVICE_SN)
    sig_bytes = sn_packets[0][4:] if sn_packets else b""
    print(f"  Self-signed SN signature: {len(sig_bytes)} bytes")
    print(f"  First 32 bytes: {sig_bytes[:32].hex()}...")
    for i, pkt in enumerate(sn_packets):
        print(f"  Sending chunk {i+1}/{len(sn_packets)} ({len(pkt)}B)")
        await transport.write_command(pkt)
        await asyncio.sleep(0.05)

    print("  Waiting for response...")
    _notification_log.clear()
    await asyncio.sleep(3)

    # Check for 0xFE11 (send your key) or 0xFE12 (key material)
    fe11_found = False
    fe12_found = False
    for n in _notification_log:
        if len(n) >= 2:
            cmd_id = struct.unpack_from("<H", n, 0)[0]
            if cmd_id == 0xFE11:
                fe11_found = True
            elif cmd_id == 0xFE12:
                fe12_found = True

    if fe11_found:
        print("  Got 0xFE11 (send RSA key) - SN SIGNATURE ACCEPTED!")
    elif fe12_found:
        print("  Got 0xFE12 (key material) - SN SIGNATURE ACCEPTED (device skipped key request)!")
    else:
        print("  No 0xFE11/0xFE12 response. SN signature likely REJECTED.")
        print(f"  Got {len(_notification_log)} notification(s):")
        for n in _notification_log:
            print(f"    {n.hex()}")
        await transport.disconnect()
        return

    # Send RSA public key if device asked (0xFE11)
    if fe11_found:
        print("\n  Sending fresh RSA public key (0xFE12)...")
        pub_key_packets = _build_rsa_pubkey_packets(fresh_public_der)
        for i, pkt in enumerate(pub_key_packets):
            print(f"  Sending pubkey chunk {i+1}/{len(pub_key_packets)} ({len(pkt)}B)")
            await transport.write_command(pkt)
            await asyncio.sleep(0.05)
        _notification_log.clear()
        await asyncio.sleep(2)

    # Phase 2: Key derivation
    secret_data = _reassemble_fe12_chunks(_notification_log)
    if not secret_data:
        print("\n  No key material received. Dumping notifications:")
        for n in _notification_log:
            print(f"    {n.hex()}")
        await transport.disconnect()
        return

    print(f"\n=== Phase 2: Key Derivation ===")
    print(f"  Reassembled secret data: {len(secret_data)}B")

    try:
        decrypted = BleCrypto.rsa_decrypt_device_response(secret_data, fresh_private_pem)
    except Exception as e:
        print(f"  RSA decryption failed (with fresh key): {e}")
        print("  Trying with config RSA key...")
        try:
            decrypted = BleCrypto.rsa_decrypt_device_response(secret_data, RSA_PRIVATE_KEY_PEM)
        except Exception as e2:
            print(f"  RSA decryption also failed with config key: {e2}")
            await transport.disconnect()
            return

    print(f"  RSA decrypted ({len(decrypted)}B): {decrypted.hex()}")
    print(f"    Key  (0:32):  {decrypted[0:32].hex()}")
    print(f"    Nonce(32:44): {decrypted[32:44].hex()}")
    print(f"    AAD  (44:56): {decrypted[44:56].hex()}")

    _crypto = BleCrypto.derive_from_rsa_response(decrypted)

    if len(decrypted) >= 80:
        verified = _crypto.verify(decrypted[56:80])
        print(f"  Verification {'PASSED' if verified else 'FAILED'}")
        if not verified:
            await transport.disconnect()
            return

    # Phase 3: Encrypted handshake
    print("\n=== Phase 3: Encrypted Handshake ===")
    jwt_payload = API_TOKEN.split('.')[1]
    jwt_payload += '=' * (4 - len(jwt_payload) % 4)
    jwt_sub = json.loads(base64.b64decode(jwt_payload))['sub']
    hs_data = build_handshake(bind_token=jwt_sub, sub_type=0, port_version=20)
    await _encrypted_write(hs_data)

    _notification_log.clear()
    await asyncio.sleep(3)

    if _notification_log:
        for n in _notification_log:
            dec = _try_decrypt_notification(n)
            if dec:
                pkt = parse_notification(dec)
                if pkt:
                    print(f"  [DEC] {pkt}")

    # Phase 4: Post-handshake
    print("\n=== Phase 4: Post-handshake ===")
    for label, builder in [
        ("Sync time", build_sync_time),
        ("Battery (cmd 9)", lambda: build_raw(9, b"")),
        ("Device status", build_device_status),
        ("Storage info", build_storage_info),
    ]:
        print(f"  Sending: {label}")
        await _encrypted_write(builder())
        await asyncio.sleep(0.8)

    _notification_log.clear()
    await asyncio.sleep(2)
    for n in _notification_log:
        dec = _try_decrypt_notification(n)
        if dec:
            pkt = parse_notification(dec)
            if pkt:
                print(f"    [DEC] {pkt}")

    print("\n=== Self-signed connection SUCCESSFUL ===")
    print("Listening for 10 more seconds (Ctrl+C to stop)...")
    try:
        await asyncio.sleep(10)
    except KeyboardInterrupt:
        pass

    await transport.disconnect()
    print("Done.")


def _build_rsa_pubkey_packets(pub_key_der: bytes) -> list[bytes]:
    CMD_RSA_KEY = 65042
    CHUNK_SIZE = 100
    total_chunks = (len(pub_key_der) + CHUNK_SIZE - 1) // CHUNK_SIZE
    packets = []
    for i in range(total_chunks):
        offset = i * CHUNK_SIZE
        chunk = pub_key_der[offset:offset + CHUNK_SIZE]
        pkt = bytearray()
        pkt += struct.pack("<H", CMD_RSA_KEY)
        pkt += struct.pack("<B", total_chunks)
        pkt += struct.pack("<B", i)
        pkt += chunk
        packets.append(bytes(pkt))
    return packets


class _connect:
    def __init__(self, args):
        self.args = args

    async def __aenter__(self):
        print(f"Connecting to {self.args.address}...")
        connected = await transport.connect(self.args.address)
        if not connected:
            print("Failed to connect.")
            sys.exit(1)
        print("Connected.")
        return transport

    async def __aexit__(self, *exc):
        await transport.disconnect()
        print("Disconnected.")


def main():
    parser = argparse.ArgumentParser(
        prog="plaud",
        description="Plaud Note Pro BLE Desktop Tool",
    )
    sub = parser.add_subparsers(dest="command", required=True)

    # scan
    p_scan = sub.add_parser("scan", help="Scan for Plaud devices")
    p_scan.add_argument("-d", "--duration", type=float, default=10.0, help="Scan duration (seconds)")
    p_scan.add_argument("--all", action="store_true", help="Show all BLE devices, not just Plaud")

    # connect
    p_conn = sub.add_parser("connect", help="Connect and enumerate GATT services")
    p_conn.add_argument("address", help="Device MAC address")
    p_conn.add_argument("-s", "--subscribe", action="store_true", help="Subscribe to notifications")

    # battery
    p_batt = sub.add_parser("battery", help="Read battery level")
    p_batt.add_argument("address", help="Device MAC address")

    # info
    p_info = sub.add_parser("info", help="Get device info")
    p_info.add_argument("address", help="Device MAC address")

    # listen
    p_listen = sub.add_parser("listen", help="Listen for notifications")
    p_listen.add_argument("address", help="Device MAC address")
    p_listen.add_argument("-d", "--duration", type=float, default=30.0, help="Listen duration (seconds)")
    p_listen.add_argument("--sync-time", action="store_true", help="Send sync time after connecting")

    # raw
    p_raw = sub.add_parser("raw", help="Send raw command")
    p_raw.add_argument("address", help="Device MAC address")
    p_raw.add_argument("cmd", help="Command ID (hex or decimal)")
    p_raw.add_argument("payload", nargs="?", default="", help="Payload hex bytes")
    p_raw.add_argument("-w", "--wait", type=float, default=5.0, help="Wait time for response (seconds)")

    # record
    p_rec = sub.add_parser("record", help="Control recording")
    p_rec.add_argument("address", help="Device MAC address")
    p_rec.add_argument("action", choices=["start", "stop"])

    # handshake
    p_hs = sub.add_parser("handshake", help="Attempt handshake")
    p_hs.add_argument("address", help="Device MAC address")

    # disconnect
    p_disc = sub.add_parser("send-disconnect", help="Send disconnect command")
    p_disc.add_argument("address", help="Device MAC address")

    # depair
    p_dep = sub.add_parser("depair", help="Depair device")
    p_dep.add_argument("address", help="Device MAC address")

    # sdk-connect
    p_sdk = sub.add_parser("sdk-connect", help="Full SDK auth + BLE connect flow")
    p_sdk.add_argument("address", nargs="?", default=None, help="Device MAC address (auto-select if omitted)")
    p_sdk.add_argument("-d", "--duration", type=float, default=10.0, help="Scan duration (seconds)")
    p_sdk.add_argument("--no-bind", action="store_true", help="Skip server-side device binding")

    # list-files
    p_ls = sub.add_parser("list-files", help="List recording files on device")
    p_ls.add_argument("address", nargs="?", default=None, help="Device MAC address (auto-select if omitted)")
    p_ls.add_argument("-d", "--duration", type=float, default=10.0, help="Scan duration (seconds)")

    # download-file
    p_dl = sub.add_parser("download", help="Download recording(s) from device")
    p_dl.add_argument("address", nargs="?", default=None, help="Device MAC address (auto-select if omitted)")
    p_dl.add_argument("-d", "--duration", type=float, default=10.0, help="Scan duration (seconds)")
    p_dl.add_argument("-s", "--session-id", type=int, default=None, help="Session ID to download")
    p_dl.add_argument("-a", "--all", action="store_true", help="Download all files")
    p_dl.add_argument("-o", "--output-dir", default="./plaud_downloads", help="Output directory")

    # delete-file
    p_del = sub.add_parser("delete-file", help="Delete recording file(s) from device")
    p_del.add_argument("address", nargs="?", default=None, help="Device MAC address (auto-select if omitted)")
    p_del.add_argument("-d", "--duration", type=float, default=10.0, help="Scan duration (seconds)")
    p_del.add_argument("-s", "--session-id", type=int, default=None, help="Session ID to delete")
    p_del.add_argument("--clear-all", action="store_true", help="Clear all files")

    # record-test
    p_rt = sub.add_parser("record-test", help="Record, stop, then list files")
    p_rt.add_argument("address", nargs="?", default=None, help="Device MAC address (auto-select if omitted)")
    p_rt.add_argument("-d", "--duration", type=float, default=10.0, help="Scan duration (seconds)")
    p_rt.add_argument("--rec-duration", type=float, default=3.0, help="Recording duration (seconds)")

    # capture
    p_cap = sub.add_parser("capture", help="Connect, save all key material, and download a recording")
    p_cap.add_argument("address", nargs="?", default=None, help="Device MAC address (auto-select if omitted)")
    p_cap.add_argument("-d", "--duration", type=float, default=10.0, help="Scan duration (seconds)")
    p_cap.add_argument("-s", "--session-id", type=int, default=None, help="Specific session ID to download")

    # self-sign-connect
    p_ss = sub.add_parser("self-sign-connect", help="Connect using self-signed SN signature (no cloud)")
    p_ss.add_argument("address", nargs="?", default=None, help="Device MAC address (auto-select if omitted)")
    p_ss.add_argument("-d", "--duration", type=float, default=10.0, help="Scan duration (seconds)")

    args = parser.parse_args()

    commands = {
        "scan": cmd_scan,
        "connect": cmd_connect,
        "battery": cmd_battery,
        "info": cmd_info,
        "listen": cmd_listen,
        "raw": cmd_raw,
        "record": cmd_record,
        "handshake": cmd_handshake,
        "send-disconnect": cmd_disconnect_cmd,
        "depair": cmd_depair,
        "sdk-connect": cmd_sdk_connect,
        "list-files": cmd_list_files,
        "download": cmd_download_file,
        "delete-file": cmd_delete_file,
        "record-test": cmd_record_test,
        "capture": cmd_capture_session,
        "self-sign-connect": cmd_self_sign_connect,
    }

    handler = commands.get(args.command)
    if handler:
        asyncio.run(handler(args))
    else:
        parser.print_help()


if __name__ == "__main__":
    main()

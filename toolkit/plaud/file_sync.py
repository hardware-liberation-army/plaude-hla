import asyncio
import struct
import time as _time
import os
from pathlib import Path
from .protocol import (
    build_get_rec_sessions, build_sync_file_start, build_sync_file_stop,
    build_sync_file_del, build_clear_rec_file, parse_file_list_response,
    parse_sync_file_head, parse_sync_file_tail, parse_file_data_notification,
    parse_notification, FileListResponse, BleFile, crc16,
)
from .crypto import BleCrypto


class FileSyncManager:
    def __init__(self, transport, crypto: BleCrypto | None = None, write_fn=None):
        self._transport = transport
        self._crypto = crypto
        self._write_fn = write_fn
        self._notifications: list[bytes] = []
        self._file_data_chunks: dict[int, list[bytes]] = {}
        self._file_data_event = asyncio.Event()
        self._file_list_event = asyncio.Event()
        self._current_download_session: int | None = None
        self._download_complete = False

    def on_notification(self, data: bytes):
        decrypted = data
        if self._crypto:
            try:
                decrypted = self._crypto.decrypt_recv(data)
            except Exception as e:
                print(f"  [FileSync] decrypt failed: {e}, raw: {data.hex()[:40]}...")
                self._notifications.append(data)
                return

        self._notifications.append(decrypted)

        if len(decrypted) < 1:
            return

        proto_type = decrypted[0]

        if proto_type == 0x02:
            print(f"  [FileSync] TYPE-2 data ({len(decrypted)}B): {decrypted.hex()}")
            self._handle_file_data(decrypted)
        elif proto_type == 0x01 and len(decrypted) >= 3:
            cmd_id = struct.unpack_from("<H", decrypted, 1)[0]
            payload_hex = decrypted[3:].hex() if len(decrypted) > 3 else ""
            print(f"  [FileSync] TYPE-1 cmd={cmd_id} payload={payload_hex}")
            if cmd_id == 26:
                self._file_list_event.set()
            elif cmd_id == 29:
                self._download_complete = True
                self._file_data_event.set()
        else:
            print(f"  [FileSync] UNKNOWN proto=0x{proto_type:02X} ({len(decrypted)}B): {decrypted.hex()}")

    def _handle_file_data(self, data: bytes):
        if self._current_download_session is None:
            return
        if len(data) < 7:
            return
        session_id = struct.unpack_from("<I", data, 1)[0]
        if session_id != self._current_download_session:
            return
        file_offset = struct.unpack_from("<I", data, 5)[0]
        if file_offset == 0xFFFFFFFF:
            self._download_complete = True
            self._file_data_event.set()
            return
        data_len = data[9]
        payload = data[10:10 + data_len] if data_len > 0 else data[10:]
        if self._current_download_session not in self._file_data_chunks:
            self._file_data_chunks[self._current_download_session] = []
        self._file_data_chunks[self._current_download_session].append((file_offset, payload))
        self._file_data_event.set()

    async def _write(self, data: bytes):
        if self._write_fn:
            await self._write_fn(data)
        else:
            await self._transport.write_command(data)

    def _clear(self):
        self._notifications.clear()
        self._file_list_event.clear()
        self._file_data_event.clear()

    async def get_file_list(self, session_id: int | None = None, timeout: float = 10.0) -> FileListResponse:
        self._clear()
        if session_id is None:
            session_id = int(_time.time()) & 0xFFFFFFFF
        uid = 0
        pkt = build_get_rec_sessions(session_id, uid, full_sync=False)
        print(f"  [FileSync] Requesting file list (session_id={session_id}, uid=0x{uid:08X})...")
        await self._write(pkt)

        try:
            await asyncio.wait_for(self._file_list_event.wait(), timeout=timeout)
        except asyncio.TimeoutError:
            print(f"  [FileSync] TIMEOUT waiting for file list response ({timeout}s)")
            print(f"  [FileSync] Raw notifications received: {len(self._notifications)}")
            for i, n in enumerate(self._notifications):
                print(f"    [{i}] ({len(n)}B) {n.hex()}")
            return FileListResponse(b"", 20)

        print(f"  [FileSync] Got response, {len(self._notifications)} notification(s) total:")
        for i, n in enumerate(self._notifications):
            proto = n[0] if n else -1
            cmd = struct.unpack_from("<H", n, 1)[0] if len(n) >= 3 else -1
            print(f"    [{i}] proto=0x{proto:02X} cmd={cmd} ({len(n)}B): {n.hex()}")

        relevant = []
        for n in self._notifications:
            if len(n) >= 3 and n[0] == 0x01:
                cmd_id = struct.unpack_from("<H", n, 1)[0]
                if cmd_id == 26:
                    relevant.append(n)

        if not relevant:
            print("  [FileSync] No cmd-26 responses found in notifications")
            return FileListResponse(b"", 20)

        all_data = b"".join(relevant)
        resp = parse_file_list_response(all_data, protocol_version=20)

        return resp

    async def download_file(self, session_id: int, file_size: int,
                            output_path: str | None = None,
                            timeout_per_chunk: float = 5.0) -> bytes:
        self._clear()
        self._current_download_session = session_id
        self._download_complete = False
        if session_id in self._file_data_chunks:
            del self._file_data_chunks[session_id]

        uid = 0
        pkt = build_sync_file_start(session_id, uid, file_size)
        print(f"  [FileSync] Starting download session_id={session_id}, size={file_size}, uid=0x{uid:08X}")
        await self._write(pkt)

        head_received = False
        deadline = _time.time() + 10.0
        while _time.time() < deadline and not head_received:
            await asyncio.sleep(0.1)
            for n in list(self._notifications):
                if len(n) >= 7 and n[0] == 0x01:
                    cmd_id = struct.unpack_from("<H", n, 1)[0]
                    if cmd_id == 28:
                        head = parse_sync_file_head(n)
                        print(f"  [FileSync] Head response: {head}")
                        if not head.ok:
                            raise RuntimeError(f"File sync head failed: status={head.status}")
                        head_received = True
                        break

        if not head_received:
            raise RuntimeError("No file sync head response received")

        print(f"  [FileSync] Receiving data...")
        last_progress = 0
        while not self._download_complete:
            try:
                await asyncio.wait_for(self._file_data_event.wait(), timeout=timeout_per_chunk)
            except asyncio.TimeoutError:
                print(f"  [FileSync] Timeout waiting for data chunk")
                break
            self._file_data_event.clear()

            chunks = self._file_data_chunks.get(session_id, [])
            received = sum(len(p) for _, p in chunks)
            if file_size > 0 and received - last_progress >= file_size // 10:
                pct = received * 100 // file_size
                print(f"  [FileSync] Progress: {received}/{file_size} bytes ({pct}%)")
                last_progress = received

            if self._download_complete:
                break

        offset_chunks = self._file_data_chunks.get(session_id, [])
        offset_chunks.sort(key=lambda x: x[0])
        file_data = b"".join(payload for _, payload in offset_chunks)
        received = len(file_data)
        chunk_count = len(offset_chunks)
        if chunk_count > 0:
            first_off = offset_chunks[0][0]
            last_off = offset_chunks[-1][0]
            print(f"  [FileSync] Assembled {chunk_count} chunks, offsets {first_off}-{last_off}")

        tail_crc = None
        for n in list(self._notifications):
            if len(n) >= 9 and n[0] == 0x01:
                cmd_id = struct.unpack_from("<H", n, 1)[0]
                if cmd_id == 29:
                    tail = parse_sync_file_tail(n)
                    tail_crc = tail.crc
                    print(f"  [FileSync] Tail response: {tail}")

        if file_size > 0 and received != file_size:
            print(f"  [FileSync] WARNING: size mismatch (expected {file_size}, got {received})")

        if tail_crc is not None:
            computed_crc = crc16(file_data)
            if computed_crc == tail_crc:
                print(f"  [FileSync] CRC OK (0x{computed_crc:04X})")
            else:
                print(f"  [FileSync] CRC MISMATCH (computed=0x{computed_crc:04X}, expected=0x{tail_crc:04X})")

        if output_path:
            out = Path(output_path)
            out.parent.mkdir(parents=True, exist_ok=True)
            out.write_bytes(file_data)
            print(f"  [FileSync] Saved to {output_path} ({len(file_data)} bytes)")

        self._current_download_session = None
        return file_data

    async def stop_download(self):
        self._clear()
        pkt = build_sync_file_stop()
        print("  [FileSync] Sending stop...")
        await self._write(pkt)
        await asyncio.sleep(1.0)

    async def delete_file(self, session_id: int, timeout: float = 5.0) -> bool:
        self._clear()
        pkt = build_sync_file_del(session_id)
        print(f"  [FileSync] Deleting session_id={session_id}")
        await self._write(pkt)
        await asyncio.sleep(timeout)

        for n in self._notifications:
            if len(n) >= 8 and n[0] == 0x01:
                cmd_id = struct.unpack_from("<H", n, 1)[0]
                if cmd_id == 31:
                    status = n[7]
                    print(f"  [FileSync] Delete response: status={status}")
                    return status == 0
        print("  [FileSync] No delete response received")
        return False

    async def clear_all_files(self, timeout: float = 5.0) -> bool:
        self._clear()
        pkt = build_clear_rec_file()
        print("  [FileSync] Clearing all files...")
        await self._write(pkt)
        await asyncio.sleep(timeout)

        for n in self._notifications:
            if len(n) >= 4 and n[0] == 0x01:
                cmd_id = struct.unpack_from("<H", n, 1)[0]
                if cmd_id == 104:
                    status = n[3]
                    print(f"  [FileSync] Clear response: status={status}")
                    return status == 0
        print("  [FileSync] No clear response received")
        return False

    async def download_all_files(self, output_dir: str = "./plaud_downloads",
                                 session_id_start: int = 0) -> list[str]:
        file_list = await self.get_file_list(session_id=session_id_start)
        if not file_list.files:
            print("  [FileSync] No files on device")
            return []

        print(f"  [FileSync] Found {len(file_list.files)} file(s):")
        for i, f in enumerate(file_list.files):
            size_str = _human_size(f.file_size)
            print(f"    [{i}] session_id={f.session_id}  size={size_str}  attr={f.attribute}  scene={f.scene}")

        saved_paths = []
        out = Path(output_dir)
        out.mkdir(parents=True, exist_ok=True)

        for i, f in enumerate(file_list.files):
            filename = f"recording_{f.session_id}.ogg"
            filepath = str(out / filename)
            print(f"\n  [FileSync] Downloading {i+1}/{len(file_list.files)}: {filename}")
            try:
                await self.download_file(f.session_id, f.file_size, output_path=filepath)
                saved_paths.append(filepath)
            except Exception as e:
                print(f"  [FileSync] FAILED: {e}")
            await asyncio.sleep(0.5)

        print(f"\n  [FileSync] Downloaded {len(saved_paths)}/{len(file_list.files)} files to {output_dir}")
        return saved_paths


def _human_size(n: int) -> str:
    for unit in ("B", "KB", "MB", "GB"):
        if n < 1024:
            return f"{n:.1f} {unit}" if unit != "B" else f"{n} {unit}"
        n /= 1024
    return f"{n:.1f} TB"

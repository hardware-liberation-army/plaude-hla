import struct
import sys
import argparse
from pathlib import Path

import opuslib
import numpy as np


HEADER_SIZE = 0x100
OPUS_SAMPLE_RATE = 16000
OPUS_FRAME_SIZE = 320
OPUS_FRAME_BYTES = 80


def parse_header(data: bytes) -> dict:
    if len(data) < HEADER_SIZE:
        raise ValueError(f"File too small: {len(data)} bytes (need at least {HEADER_SIZE})")
    magic = data[:8]
    if magic != b"PLAUD.AI":
        raise ValueError(f"Invalid magic: {magic!r}")
    return {
        "magic": magic,
        "version": data[8],
        "type": data[11],
        "duration_ms": struct.unpack_from("<I", data, 0x0C)[0],
        "userid": data[0x10:0x30].split(b"\x00")[0].decode("utf-8", errors="replace"),
        "uuid": data[0x84:0x90].hex(),
        "payload_size": len(data) - HEADER_SIZE,
    }


def decode_avc_to_pcm(data: bytes, channels: int = 1) -> bytes:
    header = parse_header(data)
    payload = data[HEADER_SIZE:]
    frame_bytes = channels * OPUS_FRAME_BYTES
    num_frames = len(payload) // frame_bytes
    remainder = len(payload) % frame_bytes

    if remainder != 0:
        print(f"  WARNING: payload ({len(payload)}B) not evenly divisible by frame size ({frame_bytes}B)")
        print(f"  Truncating to {num_frames} frames ({num_frames * frame_bytes}B)")

    decoder = opuslib.Decoder(OPUS_SAMPLE_RATE, channels)
    pcm_parts = []
    ok = 0
    fail = 0

    for i in range(num_frames):
        offset = i * frame_bytes
        frame = payload[offset : offset + frame_bytes]
        try:
            pcm = decoder.decode(frame, frame_size=OPUS_FRAME_SIZE * channels)
            pcm_parts.append(pcm)
            ok += 1
        except Exception:
            pcm_parts.append(b"\x00\x00" * OPUS_FRAME_SIZE * channels)
            fail += 1

    print(f"  Decoded {ok}/{num_frames} frames successfully ({fail} failed)")
    return b"".join(pcm_parts)


def write_wav(path: str, pcm: bytes, sample_rate: int, channels: int):
    num_samples = len(pcm) // (channels * 2)
    data_size = len(pcm)
    fmt_chunk = struct.pack("<4sIHHIIHH", b"fmt ", 16, 1, channels, sample_rate,
                            sample_rate * channels * 2, channels * 2, 16)
    data_chunk = struct.pack("<4sI", b"data", data_size)
    riff_header = struct.pack("<4sI", b"RIFF", 4 + len(fmt_chunk) + 8 + data_size)
    with open(path, "wb") as f:
        f.write(riff_header + b"WAVE" + fmt_chunk + data_chunk + pcm)


def main():
    parser = argparse.ArgumentParser(description="Decode PLAUD.AI AVC recording to WAV")
    parser.add_argument("input", help="Input .bin/.ogg PLAUD.AI file")
    parser.add_argument("-o", "--output", help="Output WAV file (default: same name with .wav)")
    parser.add_argument("-c", "--channels", type=int, default=1, help="Number of channels (default: 1)")
    parser.add_argument("--info", action="store_true", help="Just show file info")
    args = parser.parse_args()

    data = Path(args.input).read_bytes()
    header = parse_header(data)
    print(f"File: {args.input}")
    print(f"  Version: {header['version']}")
    print(f"  Type: {header['type']}")
    print(f"  Duration: {header['duration_ms']}ms ({header['duration_ms']/1000:.1f}s)")
    print(f"  UserID: {header['userid']}")
    print(f"  UUID: {header['uuid']}")
    print(f"  Payload: {header['payload_size']}B")

    frame_bytes = args.channels * OPUS_FRAME_BYTES
    num_frames = header["payload_size"] // frame_bytes
    print(f"  Frames: {num_frames} ({frame_bytes}B each)")

    if args.info:
        return

    output = args.output or str(Path(args.input).with_suffix(".wav"))
    print(f"\nDecoding to {output}...")
    pcm = decode_avc_to_pcm(data, args.channels)
    write_wav(output, pcm, OPUS_SAMPLE_RATE, args.channels)
    duration_s = len(pcm) / (OPUS_SAMPLE_RATE * args.channels * 2)
    print(f"  Wrote {len(pcm)}B PCM, {duration_s:.1f}s")


if __name__ == "__main__":
    main()

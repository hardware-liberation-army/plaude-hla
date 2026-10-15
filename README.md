# Plaud Note Pro — Open Interoperability Project

Reverse-engineered, open documentation and tooling for the **Plaud Note Pro**
BLE recording device. This repository lets you talk to the device from a
Linux desktop over Bluetooth Low Energy — scan, connect, complete the
encrypted handshake, list and download recordings, and decrypt the result to
playable audio — without the vendor's app, subscriptions, or cloud lock-in.

> Part of the **[Hardware Liberation Army](https://github.com/hardware-liberation-army)** effort to
> own our hardware again. No subscriptions. No e-waste. Forward, comrades.

---

## What's in this repo

| Path | What it is |
|---|---|
| [`docs/`](docs/) | The canonical, organized protocol documentation (handshake, encryption, file sync, firmware analysis, key acquisition, and a consolidated protocol spec). |
| [`toolkit/`](toolkit/) | **The canonical implementation.** A Python package (`plaud`) that talks to the device over BLE and decrypts recordings. |
| [`demos/aplaud/`](demos/aplaud/) | **Demo app.** A Go CLI/TUI (`aplaud`) that shows the full download → decrypt → transcribe pipeline. Useful as a worked example of the protocol. |
| [`research/`](research/) | Decompiled source fragments (`opendict_source`) used to derive the protocol spec. |
| [`third_party/`](third_party/) | Vendored/proprietary material (official Plaud SDK, firmware images) kept for reference and interoperability research. |
| [`archive/plaud-web/`](archive/plaud-web/) | An older browser (WebBluetooth) implementation of the protocol, kept for reference. |

## Quick start

### The toolkit (Python)

```bash
cd toolkit
python -m pip install -r requirements.txt

# Configure credentials via environment variables (see toolkit/plaud/config.py)
export PLAUD_API_TOKEN="..."
export PLAUD_DEVICE_SN="..."

# Scan for your device
python -m plaud scan

# Full authenticated connect + file list
python -m plaud list-files

# Download a recording and decrypt it to OGG
python -m plaud download --all
```

### The demo app (Go)

See [`demos/aplaud/README.md`](demos/aplaud/README.md) for build instructions.
It's a reference demo showing a complete end-to-end pipeline.

## License

Licensed under the **Hardware Liberation License** — see [`LICENSE`](LICENSE).
The spirit: open tools, user ownership, no further lock-downs.

Third-party material under `third_party/` carries its own licenses; see
[`third_party/NOTICE.md`](third_party/NOTICE.md).

## Contributing

The protocol documentation in `docs/protocol/` is the source of truth. If you
find correct behaviour that differs from the docs, please file an issue or a
pull request.

## Legal / ethical note

This is **interoperability research** for hardware you own. It does not bypass
authentication on devices you do not own. Please use it responsibly, and don't
use it to circumvent locks on hardware belonging to others.

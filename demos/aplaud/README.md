# aplaud — Demo App (Go)

> **This is a demo / reference application.** It is *not* the canonical
> implementation of the Plaud protocol in this repository. The canonical
> implementation is the Python package in [`../../toolkit/`](../../toolkit/).
> This app exists as a worked, end-to-end example: it shows the full
> **scan → connect → handshake → download → decrypt → transcribe → delete**
> pipeline as a CLI *and* an interactive TUI.

## What it demonstrates

- BLE connection and the 4-phase encrypted handshake (Go port of the same
  protocol documented in [`../../docs/`](../../docs/)).
- File listing and downloading from the device.
- Decryption of `PLAUD.AI` containers to OGG.
- Transcription via `whisper.cpp`, optional LLM summaries, and a
  memory/entity store for daily logs.
- An interactive terminal UI (TUI) for browsing recordings and building
  daily logs.

## Build & run

```bash
cd demos/aplaud

# Copy the example config and fill in your credentials.
cp config.example.yaml ~/.config/aplaud/config.yaml

go build -o aplaud .
./aplaud oneshot            # full pipeline
./aplaud scan               # scan for devices
./aplaud download           # download + decrypt only
./aplaud tui                # interactive terminal UI
```

## Notes

- This is intentionally a demo: it duplicates protocol/crypto logic that the
  canonical `toolkit/` package owns. Treat the Go source as a reference for
  how the protocol is applied, not as the authoritative implementation.
- The Go module is `aplaud`; moving this directory does not affect the module
  path (it is directory-independent).

See [`PLAN.md`](PLAN.md) and [`docs/`](../../docs/) for background.

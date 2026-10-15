# Documentation

The canonical documentation for the reverse-engineered **Plaud Note Pro** BLE
protocol. Read these in order for the full picture.

## Index

### Protocol reference
- [`protocol/ble-protocol-spec.md`](protocol/ble-protocol-spec.md) — **Start here.** Consolidated protocol specification (GATT services, packet formats, command registry, handshake, encryption, file transfer).
- [`protocol/encryption.md`](protocol/encryption.md) — ChaCha20-Poly1305 transport + RSA key exchange.
- [`protocol/decryption.md`](protocol/decryption.md) — Decrypting the `PLAUD.AI` recording container to OGG.
- [`protocol/file-sync-protocol.md`](protocol/file-sync-protocol.md) — Listing and downloading recordings over BLE.
- [`protocol/key-exchange.md`](protocol/key-exchange.md) — Detailed findings on the BLE key exchange.
- [`protocol/firmware-analysis.md`](protocol/firmware-analysis.md) — Firmware structure analysis.
- [`protocol/ghidra-decompilation.md`](protocol/ghidra-decompilation.md) — Ghidra decompilation findings.

### Guides & overview
- [`reverse-engineering.md`](reverse-engineering.md) — Overview of the reverse-engineering effort and how it was done.
- [`key-acquisition.md`](key-acquisition.md) — How to obtain the API token, SN signature, and RSA keys.

### Project history
- [`history/PLAN.md`](history/PLAN.md) — Original implementation plan.
- [`history/PROGRESS.md`](history/PROGRESS.md) — Progress log / current status.

## Relationship to the code

- The **canonical implementation** of this spec lives in [`toolkit/`](../toolkit/).
- A **demo** implementation lives in [`demos/aplaud/`](../demos/aplaud/).

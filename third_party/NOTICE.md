# Third-party material notice

The files and directories under this directory are **third-party** material
that is **not** covered by the repository's `LICENSE`. They are included solely
for interoperability research and reference.

## `plaud-sdk-public/`

The **official Plaud SDK** (iOS frameworks + Android AAR) and its template
apps, vendored from the vendor's public SDK repository.

- It carries its **own repository** (nested `.git`) and **its own license**
  (Apache License 2.0 for the template apps, with the SDK binaries under a
  separate proprietary license).
- See `plaud-sdk-public/LICENSE` and `plaud-sdk-public/README.md`.
- This is **not** our code and is **not** released under the Hardware
  Liberation License.

## `firmware/`

Device firmware images (e.g. `firmware_MFOTA_V1.5.4.bin`) are **proprietary**
vendor binaries. They are included only as the subject of the analysis in
[`docs/protocol/firmware-analysis.md`](../docs/protocol/firmware-analysis.md)
and are **not** redistributed or licensed by this project. Do not assume any
rights to redistribute them.

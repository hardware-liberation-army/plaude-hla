# Research

Decompiled / reference source fragments that were used to derive the
protocol documentation in [`../docs/`](../docs/).

- `opendict_source/` — TypeScript fragments decompiled from the Plaud app's
  PenBLE SDK (`PlaudCrypto.ts`, `PlaudFileSync.ts`, `PlaudAPI.ts`,
  `OpusDecoder.ts`, `constants.ts`). These are **reference material only**;
  the canonical implementation is the Python package in
  [`../toolkit/`](../toolkit/).

> These fragments and the canonical implementation overlap in purpose. The
> `toolkit/` package is the maintained implementation; this directory is kept
> purely as provenance for the protocol documentation.

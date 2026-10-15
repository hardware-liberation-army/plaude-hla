# Transcription Alternatives

## The Problem

Whisper (all variants) uses a generative decoder that autoregressively predicts tokens. This makes it prone to **fluent hallucinations** on non-speech, quiet, or noisy audio — producing plausible-sounding sentences that were never spoken. Observed with `ggml-large-v3-turbo` on a 10.4s recording that contained no matching speech.

## Current Solution: Silero VAD Pre-filtering (Implemented)

**Silero VAD** (Voice Activity Detection) runs before whisper and determines whether audio contains actual speech. If no speech is detected, transcription is skipped entirely. Speech segments are also isolated so whisper only processes relevant audio.

- **Status**: Implemented in `internal/whisper/vad.go`
- **Model**: Silero VAD v4 (ONNX runtime)
- **Threshold**: Configurable via `whisper.vad_threshold` (default 0.5)
- **Behavior**: Recordings with no detected speech produce an empty `.txt` file instead of hallucinated text

## Future Alternatives

### Non-Generative Models (No Hallucination by Design)

| Engine | Architecture | Language | Go Integration | Notes |
|--------|-------------|----------|----------------|-------|
| **Vosk** | Kaldi (CTC/hybrid) | C/C++ | Go bindings exist (`github.com/vsergeev/vosk-go`) | Very lightweight, no hallucination, lower accuracy on noisy audio |
| **Sherpa-ONNX** | Transducer/CTC | C/C++ | C API via CGo | Supports many model types (Transducer, CTC, Paraformer), ONNX runtime |
| **Moonshine** (Useful Sensors) | Encoder-decoder (tiny) | Python/ONNX | Would need ONNX wrapper | Designed for edge/real-time, very fast, small model |
| **Coqui STT** (formerly Mozilla DeepSpeech) | CTC | Python/C++ | C API available | Project largely abandoned, but stable |

### Improved Whisper-Based Approaches

| Approach | How it Helps | Effort |
|----------|-------------|--------|
| **Smaller whisper model** (base/small/medium) | Less prone to hallucination than large-v3, but lower accuracy | Low (just swap model file) |
| **Faster-whisper** (CTranslate2) | Same models, ~4x faster inference | Medium (Python dependency or CTranslate2 C API) |
| **WhisperX** | Word-level timestamps + speaker diarization via faster-whisper | Medium (Python dependency) |
| **Whisper + VAD segment isolation** | Transcribe only speech segments, not silence between | **Done** (part of Silero VAD integration) |

### Cloud Options (for reference, not recommended for privacy)

| Service | Notes |
|---------|-------|
| Deepgram | On-prem available, Nova-2 model, excellent accuracy |
| AssemblyAI | Good accuracy, speaker diarization |
| OpenAI Whisper API | Same hallucination risk as local whisper |

## Decision Log

- **2026-05-14**: Added Silero VAD pre-filtering as first mitigation. Simplest change, addresses hallucination at the source by skipping non-speech audio.
- Future: Consider Vosk as cross-check validator, or switch to Moonshine/Sherpa-ONNX if hallucinations persist.

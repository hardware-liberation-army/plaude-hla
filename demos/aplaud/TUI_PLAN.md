# aplaud tui — Full TUI Application Plan

## Overview

Add a `tui` subcommand to the existing `aplaud` Go CLI providing a rich terminal interface with:
- **Persistent BLE connection** with auto-sync polling (configurable, default 5 min)
- **Auto download + transcribe** new recordings as they appear
- **TUI modal prompt** when new transcription lands
- **Per-day transcription browser** with markdown rendering
- **Daily log builder** with checkbox selection of recordings to include
- All existing CLI functionality accessible from the TUI

## New Dependencies

```
charm.land/bubbletea/v2      — TUI framework (Elm architecture)
charm.land/lipgloss/v2       — Styling/layout
charm.land/bubbles/v2        — Pre-built components (list, viewport, table, spinner)
charm.land/glamour/v2        — Markdown rendering
```

## New Files

```
aplaud/internal/tui/
├── app.go              — Root bubbletea Model, window management, view routing
├── styles.go           — lipgloss style definitions (shared across views)
├── keymap.go           — Global and view-specific key bindings
├── views/
│   ├── dashboard.go    — Main screen: connection status, battery, last sync
│   ├── recordings.go   — Per-day list of recordings with excerpts
│   ├── detail.go       — Full transcription view with markdown rendering
│   ├── daily_log.go    — Daily log builder: checkbox list, preview, save
│   ├── sync.go         — Sync management: auto-sync toggle, progress
│   └── settings.go     — Config viewer, poll interval, download dir
├── ble_bridge.go       — BLE connection lifecycle manager
├── store.go            — Local transcription store: scan disk, index by date
└── messages.go         — Custom bubbletea messages
```

## Architecture

```
┌─────────────────────────────────────────────────┐
│  bubbletea Program                              │
│                                                 │
│  ┌───────────┐  ┌───────────┐  ┌────────────┐  │
│  │ Dashboard  │  │Recordings │  │ Daily Log  │  │
│  │ (status)   │  │ (per-day) │  │ (builder)  │  │
│  └───────────┘  └───────────┘  └────────────┘  │
│         ▲              ▲               ▲        │
│         │              │               │        │
│  ┌──────┴──────────────┴───────────────┴─────┐  │
│  │              Store (disk index)            │  │
│  │  - Scans ~/Recordings/Plaud/ for .txt/.ogg│  │
│  │  - Groups recordings by date               │  │
│  │  - Reads/writes daily/ YYYY-MM-DD.md      │  │
│  └──────────────────┬────────────────────────┘  │
│                     ▲                           │
│  ┌──────────────────┴────────────────────────┐  │
│  │           BLE Bridge (goroutine)          │  │
│  │  - Manages connection lifecycle           │  │
│  │  - Periodic file list poll (5 min default)│  │
│  │  - Auto download + transcribe new files   │  │
│  │  - Sends tea.Msg on new transcription     │  │
│  │  - Sends tea.Msg on connection changes    │  │
│  └───────────────────────────────────────────┘  │
└─────────────────────────────────────────────────┘
```

## Component Details

### Store (store.go)

Disk-based store that indexes existing recordings:
- Scans `download_dir` for `recording_*.txt` and `recording_*.ogg` pairs
- Extracts timestamp from session_id (Unix timestamp) → groups by date (local timezone)
- Each `Recording` struct: `{ SessionID, Timestamp, Date, Text, OggPath, TxtPath, Size }`
- Daily logs stored in `download_dir/daily/YYYY-MM-DD.md`
- Daily log metadata (included/excluded) stored in `download_dir/daily/YYYY-MM-DD.json`
- Markdown daily log regenerated from JSON metadata + transcription text on save

### BLE Bridge (ble_bridge.go)

Goroutine managing full BLE lifecycle:
- States: `disconnected` → `scanning` → `connecting` → `handshaking` → `connected` → `monitoring`
- When `monitoring`: polls `GetFileList()` every N seconds (configurable, default 5 min)
- On new files detected: runs download → decrypt → transcribe pipeline automatically
- Sends `NewTranscriptionMsg` to TUI when a new transcription lands
- Sends `ConnectionStatusMsg` on state changes
- Reconnects automatically on disconnect
- Reuses all existing `handshake`, `filesync`, `ble`, `crypto` packages

### Views

#### Dashboard (dashboard.go)
- Connection status indicator (connected/disconnected/scanning)
- Device name, address, battery level
- Auto-sync status + countdown to next poll
- Number of recordings today, total recordings
- Last sync timestamp
- Key hints for navigation

#### Recordings (recordings.go)
- Left panel: list of dates (days that have recordings)
- Right panel: recordings for selected day, showing:
  - Time (HH:MM from session_id timestamp)
  - Duration/size
  - First 80 chars of transcription as excerpt
- Enter → opens detail view
- `d` → opens daily log builder for that day

#### Detail (detail.go)
- Full transcription text rendered with glamour (markdown)
- Scrollable viewport
- File metadata (timestamp, size, session_id)
- Options: add to daily log, open in editor

#### Daily Log Builder (daily_log.go)
- Chronological list of all recordings for selected day
- Checkbox `[x]` / `[ ]` per recording (all checked by default for new logs)
- Toggle with spacebar
- Preview pane showing the rendered markdown
- Save generates/overwrites `daily/YYYY-MM-DD.md`:
  ```markdown
  # Daily Log — 2026-05-07

  ## 01:34 — recording_1778126481
  Full transcription text here...

  ## 01:05 — recording_1778122596
  Another transcription...
  ```

#### Sync (sync.go)
- Manual "Sync Now" button
- Auto-sync toggle + interval setting
- Progress indicator during download/transcribe
- Queue of pending files

#### Settings (settings.go)
- Config viewer
- Poll interval setting
- Download directory display
- Key binding reference

### New Config Fields

```yaml
tui:
  poll_interval: 300          # seconds between auto-sync checks (default 5 min)
  auto_sync: true             # enable auto-sync on connect
  auto_reconnect: true        # reconnect on disconnect
  editor: ""                  # external editor command (default: $EDITOR or vi)
```

### New Transcription Prompt

When auto-sync detects and transcribes a new recording:
- A **TUI modal** appears asking "Add recording from HH:MM to today's daily log? [Y/n]"
- Stays within TUI context, no external prompting
- If user is in the middle of something, it queues and shows when current view allows

## Implementation Order

1. `store.go` — Disk scanning, recording indexing, daily log read/write
2. `styles.go` + `keymap.go` — Shared styles and key bindings
3. `messages.go` — All custom message types
4. `ble_bridge.go` — BLE connection manager goroutine
5. `app.go` — Root model, view routing, program entry
6. `views/dashboard.go` — First view
7. `views/recordings.go` — Per-day recording list
8. `views/detail.go` — Markdown transcription viewer
9. `views/daily_log.go` — Checkbox builder + markdown generation
10. `views/sync.go` — Sync management view
11. `views/settings.go` — Settings viewer
12. Wire up `tui` subcommand in `main.go`
13. Update `config.go` with `TUIConfig`
14. Build and verify

# Memory System Implementation Plan

## Overview

Add a persistent entity memory store (SQLite + FTS5) to the aplaud summary system. The LLM learns proper nouns, project names, technical terms, and their correct spellings over time. Memory is injected into summary prompts and updated automatically after each summary generation.

**No user-visible changes.** The user presses `s`, `g`, `enter`, or runs `aplaud summarize` exactly as before.

## Architecture

```
User presses s / g / enter  (unchanged)
        |
        v
generateSummary() [summary.go]
        |
        |-- 1. Read daily log (existing)
        |-- 2. Open memory DB, FTS search daily log text  (NEW)
        |-- 3. Generate summary with memory context       (MODIFIED)
        |-- 4. Save summary (existing)
        |-- 5. Extract entities via LLM, upsert into DB   (NEW)
        |-- 6. Return SummaryGeneratedMsg (existing)
```

Step 5 runs in the same background goroutine, after the summary is saved. If it fails, it's non-fatal.

## New Dependency

- `modernc.org/sqlite` — pure Go SQLite, no CGo

## New Files

### `internal/memory/memory.go`

Store type wrapping `*sql.DB` with:

- `Open(path) (*Store, error)` — open/create DB, run schema migrations
- `Close() error`
- `SearchByText(text string) ([]Entity, error)` — extract keywords, FTS5 search
- `Recent(since time.Time) ([]Entity, error)` — recently seen entities
- `AllTerms() ([]string, error)` — all canonical terms + aliases for dedup
- `UpsertBatch(entities []Entity, today time.Time) error` — insert/update with alias merge
- `RenderForPrompt(entities []Entity) string` — format for LLM prompt

Schema:
```sql
CREATE TABLE IF NOT EXISTS entities (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    term TEXT NOT NULL UNIQUE,
    category TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    first_seen TEXT NOT NULL,
    last_seen TEXT NOT NULL,
    aliases_text TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS aliases (
    alias TEXT NOT NULL UNIQUE,
    entity_id INTEGER NOT NULL REFERENCES entities(id) ON DELETE CASCADE
);

CREATE VIRTUAL TABLE IF NOT EXISTS entities_fts USING fts5(
    term, description, aliases_text,
    content='entities', content_rowid='id'
);

-- FTS sync triggers on INSERT/UPDATE/DELETE
```

### `internal/memory/keywords.go`

- `ExtractKeywords(text string) []string` — regex heuristics:
  - Sequences of 2+ capitalized words
  - ALL CAPS words (3+ chars) — acronyms
  - Capitalized words appearing 2+ times
  - Deduplicated, filtered of common English words

### `internal/memory/extract.go`

- `ExtractEntities(ctx, llmClient, dailyLog, existingTerms) ([]Entity, error)`
- Single LLM call with extraction prompt
- Parses JSON array response into `[]Entity`
- Returns empty slice if no new entities / on parse error (non-fatal)

## Modified Files

### `internal/config/config.go`

Add `MemoryDBPath string` with YAML tag `memory_db_path`, default `~/.config/aplaud/memory.db`.

### `internal/llm/summary.go`

Change `GenerateSummary` to accept `memoryContext string`. If non-empty, append known terms to system prompt.

### `internal/tui/app.go`

- Add `memStore *memory.Store` to `AppModel`
- Open in `NewApp()`, close on shutdown
- Pass to `NewSummaryModel`

### `internal/tui/summary.go`

- Add `memStore *memory.Store` to `SummaryModel`
- In `generateSummary`: memory lookup before LLM call, entity extraction after save

### `main.go`

CLI `summarize` command: same memory lookup + entity extraction flow.

## Costs

- **Extra LLM call**: 1 entity extraction per summary (small, cheap)
- **Token overhead**: matched entities in prompt (~200-500 tokens)
- **Binary size**: ~10MB for pure Go SQLite

## Pruning (v1)

- `memory_max_age_days` config (default 90)
- Lazy prune on `Open()` if not pruned in 7 days

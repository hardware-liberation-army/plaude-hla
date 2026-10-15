package memory

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Entity struct {
	ID          int64
	Term        string
	Category    string
	Description string
	Aliases     []string
	FirstSeen   time.Time
	LastSeen    time.Time
}

type Store struct {
	db *sql.DB
}

const schema = `
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

CREATE TABLE IF NOT EXISTS metadata (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

CREATE VIRTUAL TABLE IF NOT EXISTS entities_fts USING fts5(
    term, description, aliases_text,
    content='entities', content_rowid='id'
);

CREATE TRIGGER IF NOT EXISTS entities_ai AFTER INSERT ON entities BEGIN
    INSERT INTO entities_fts(rowid, term, description, aliases_text)
    VALUES (new.id, new.term, new.description, new.aliases_text);
END;

CREATE TRIGGER IF NOT EXISTS entities_ad AFTER DELETE ON entities BEGIN
    INSERT INTO entities_fts(entities_fts, rowid, term, description, aliases_text)
    VALUES ('delete', old.id, old.term, old.description, old.aliases_text);
END;

CREATE TRIGGER IF NOT EXISTS entities_au AFTER UPDATE ON entities BEGIN
    INSERT INTO entities_fts(entities_fts, rowid, term, description, aliases_text)
    VALUES ('delete', old.id, old.term, old.description, old.aliases_text);
    INSERT INTO entities_fts(rowid, term, description, aliases_text)
    VALUES (new.id, new.term, new.description, new.aliases_text);
END;
`

func Open(path string) (*Store, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("creating memory db directory: %w", err)
	}

	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, fmt.Errorf("opening memory db: %w", err)
	}

	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("initializing memory schema: %w", err)
	}

	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

func (s *Store) SearchByText(text string) ([]Entity, error) {
	keywords := ExtractKeywords(text)
	if len(keywords) == 0 {
		return nil, nil
	}
	return s.Search(keywords)
}

func (s *Store) Search(keywords []string) ([]Entity, error) {
	if len(keywords) == 0 {
		return nil, nil
	}

	quoted := make([]string, len(keywords))
	for i, kw := range keywords {
		escaped := strings.ReplaceAll(kw, `"`, `""`)
		quoted[i] = `"` + escaped + `"*`
	}
	query := strings.Join(quoted, " OR ")

	rows, err := s.db.Query(`
		SELECT e.id, e.term, e.category, e.description, e.first_seen, e.last_seen
		FROM entities e
		JOIN entities_fts fts ON fts.rowid = e.id
		WHERE entities_fts MATCH ?
		ORDER BY e.last_seen DESC
	`, query)
	if err != nil {
		return nil, fmt.Errorf("fts search: %w", err)
	}
	defer rows.Close()

	entities := scanEntityRows(rows)
	s.loadAliasesForAll(entities)
	return entities, nil
}

func (s *Store) Recent(since time.Time) ([]Entity, error) {
	rows, err := s.db.Query(`
		SELECT id, term, category, description, first_seen, last_seen
		FROM entities
		WHERE last_seen >= ?
		ORDER BY last_seen DESC
	`, since.Format("2006-01-02"))
	if err != nil {
		return nil, fmt.Errorf("querying recent: %w", err)
	}
	defer rows.Close()

	entities := scanEntityRows(rows)
	s.loadAliasesForAll(entities)
	return entities, nil
}

func (s *Store) AllTerms() ([]string, error) {
	rows, err := s.db.Query(`
		SELECT e.term, a.alias FROM entities e
		LEFT JOIN aliases a ON a.entity_id = e.id
		ORDER BY e.term
	`)
	if err != nil {
		return nil, fmt.Errorf("querying terms: %w", err)
	}
	defer rows.Close()

	seen := make(map[string]bool)
	var terms []string
	for rows.Next() {
		var term string
		var alias sql.NullString
		if err := rows.Scan(&term, &alias); err != nil {
			continue
		}
		if !seen[term] {
			terms = append(terms, term)
			seen[term] = true
		}
		if alias.Valid && alias.String != "" && !seen[alias.String] {
			terms = append(terms, alias.String)
			seen[alias.String] = true
		}
	}
	return terms, nil
}

func (s *Store) All() ([]Entity, error) {
	rows, err := s.db.Query(`
		SELECT id, term, category, description, first_seen, last_seen
		FROM entities
		ORDER BY last_seen DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("querying all: %w", err)
	}
	defer rows.Close()

	entities := scanEntityRows(rows)
	s.loadAliasesForAll(entities)
	return entities, nil
}

func (s *Store) UpsertBatch(entities []Entity, today time.Time) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	todayStr := today.Format("2006-01-02")

	for _, e := range entities {
		var existingID int64
		var existingDesc string
		err := tx.QueryRow(
			`SELECT id, description FROM entities WHERE term = ?`, e.Term,
		).Scan(&existingID, &existingDesc)

		if err == sql.ErrNoRows {
			aliasesText := strings.Join(e.Aliases, ", ")
			firstSeen := todayStr
			if !e.FirstSeen.IsZero() {
				firstSeen = e.FirstSeen.Format("2006-01-02")
			}

			result, err := tx.Exec(`
				INSERT INTO entities (term, category, description, first_seen, last_seen, aliases_text)
				VALUES (?, ?, ?, ?, ?, ?)
			`, e.Term, e.Category, e.Description, firstSeen, todayStr, aliasesText)
			if err != nil {
				continue
			}

			newID, _ := result.LastInsertId()
			for _, alias := range e.Aliases {
				tx.Exec(`INSERT OR IGNORE INTO aliases (alias, entity_id) VALUES (?, ?)`, alias, newID)
			}
		} else if err == nil {
			if e.Description != "" && existingDesc == "" {
				tx.Exec(`UPDATE entities SET description = ? WHERE id = ?`, e.Description, existingID)
			}
			tx.Exec(`UPDATE entities SET last_seen = ? WHERE id = ?`, todayStr, existingID)

			for _, alias := range e.Aliases {
				tx.Exec(`INSERT OR IGNORE INTO aliases (alias, entity_id) VALUES (?, ?)`, alias, existingID)
			}

			var currentAliases string
			if err := tx.QueryRow(`SELECT aliases_text FROM entities WHERE id = ?`, existingID).Scan(&currentAliases); err == nil {
				merged := mergeAliasStrings(currentAliases, e.Aliases)
				tx.Exec(`UPDATE entities SET aliases_text = ? WHERE id = ?`, merged, existingID)
			}
		}
	}

	return tx.Commit()
}

func (s *Store) Prune(before time.Time) (int, error) {
	result, err := s.db.Exec(`DELETE FROM entities WHERE last_seen < ?`, before.Format("2006-01-02"))
	if err != nil {
		return 0, fmt.Errorf("pruning entities: %w", err)
	}
	n, _ := result.RowsAffected()
	return int(n), nil
}

func (s *Store) MaybePrune(maxAgeDays int) error {
	if maxAgeDays <= 0 {
		return nil
	}

	var lastPrune sql.NullString
	s.db.QueryRow(`SELECT value FROM metadata WHERE key = 'last_prune'`).Scan(&lastPrune)
	if lastPrune.Valid {
		t, err := time.Parse("2006-01-02", lastPrune.String)
		if err == nil && time.Since(t) < 7*24*time.Hour {
			return nil
		}
	}

	cutoff := time.Now().AddDate(0, 0, -maxAgeDays)
	s.Prune(cutoff)

	s.db.Exec(`INSERT OR REPLACE INTO metadata (key, value) VALUES ('last_prune', ?)`, time.Now().Format("2006-01-02"))
	return nil
}

func (s *Store) loadAliasesForAll(entities []Entity) {
	if len(entities) == 0 {
		return
	}

	ids := make([]string, len(entities))
	idMap := make(map[int64]int)
	for i, e := range entities {
		ids[i] = fmt.Sprintf("%d", e.ID)
		idMap[e.ID] = i
	}

	query := fmt.Sprintf(
		`SELECT entity_id, alias FROM aliases WHERE entity_id IN (%s)`,
		strings.Join(ids, ","),
	)

	rows, err := s.db.Query(query)
	if err != nil {
		return
	}
	defer rows.Close()

	for rows.Next() {
		var entityID int64
		var alias string
		if err := rows.Scan(&entityID, &alias); err == nil {
			if idx, ok := idMap[entityID]; ok {
				entities[idx].Aliases = append(entities[idx].Aliases, alias)
			}
		}
	}
}

func scanEntityRows(rows *sql.Rows) []Entity {
	var entities []Entity
	for rows.Next() {
		var e Entity
		var firstSeen, lastSeen string
		if err := rows.Scan(&e.ID, &e.Term, &e.Category, &e.Description, &firstSeen, &lastSeen); err != nil {
			continue
		}
		e.FirstSeen, _ = time.Parse("2006-01-02", firstSeen)
		e.LastSeen, _ = time.Parse("2006-01-02", lastSeen)
		entities = append(entities, e)
	}
	return entities
}

func RenderForPrompt(entities []Entity) string {
	return renderEntities(entities, true)
}

func RenderForPromptCompact(entities []Entity) string {
	return renderEntities(entities, false)
}

func renderEntities(entities []Entity, includeDescriptions bool) string {
	if len(entities) == 0 {
		return ""
	}

	deduped := DedupEntities(entities)
	sort.Slice(deduped, func(i, j int) bool {
		return deduped[i].Term < deduped[j].Term
	})

	var sb strings.Builder
	sb.WriteString("Known terms with correct spellings:\n")
	for _, e := range deduped {
		line := fmt.Sprintf("- %s", e.Term)
		if len(e.Aliases) > 0 {
			line += fmt.Sprintf(" (also seen as: %s)", strings.Join(e.Aliases, ", "))
		}
		if includeDescriptions && e.Description != "" {
			line += ": " + e.Description
		}
		sb.WriteString(line + "\n")
	}
	return sb.String()
}

func DedupEntities(entities []Entity) []Entity {
	seen := make(map[string]bool)
	var result []Entity
	for _, e := range entities {
		if !seen[e.Term] {
			seen[e.Term] = true
			result = append(result, e)
		}
	}
	return result
}

func MergeMatchedAndRecent(matched, recent []Entity) []Entity {
	seen := make(map[string]bool)
	var result []Entity
	for _, e := range matched {
		if !seen[e.Term] {
			seen[e.Term] = true
			result = append(result, e)
		}
	}
	for _, e := range recent {
		if !seen[e.Term] {
			seen[e.Term] = true
			result = append(result, e)
		}
	}
	return result
}

func mergeAliasStrings(current string, newAliases []string) string {
	existing := make(map[string]bool)
	for _, a := range strings.Split(current, ",") {
		a = strings.TrimSpace(a)
		if a != "" {
			existing[strings.ToLower(a)] = true
		}
	}
	parts := strings.Split(current, ",")
	for _, a := range newAliases {
		if !existing[strings.ToLower(a)] {
			parts = append(parts, a)
			existing[strings.ToLower(a)] = true
		}
	}
	var clean []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			clean = append(clean, p)
		}
	}
	return strings.Join(clean, ", ")
}

package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Recording struct {
	SessionID uint32
	Timestamp time.Time
	Date      string
	Text      string
	OggPath   string
	TxtPath   string
	Size      int64
}

type DailyLogMeta struct {
	Date        string          `json:"date"`
	Recordings  []LogEntry      `json:"recordings"`
}

type LogEntry struct {
	SessionID uint32 `json:"session_id"`
	Included  bool   `json:"included"`
}

type Store struct {
	mu         sync.RWMutex
	dir        string
	recordings map[string][]*Recording
	dates      []string
}

func NewStore(dir string) *Store {
	return &Store{
		dir:        dir,
		recordings: make(map[string][]*Recording),
	}
}

func (s *Store) Scan() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.recordings = make(map[string][]*Recording)
	s.dates = nil

	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return fmt.Errorf("reading directory %s: %w", s.dir, err)
	}

	txtFiles := make(map[uint32]string)
	oggFiles := make(map[uint32]string)
	oggSizes := make(map[uint32]int64)

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, "recording_") {
			continue
		}

		ext := filepath.Ext(name)
		base := strings.TrimSuffix(name, ext)
		idStr := strings.TrimPrefix(base, "recording_")
		id, err := strconv.ParseUint(idStr, 10, 32)
		if err != nil {
			continue
		}

		fullPath := filepath.Join(s.dir, name)
		info, err := e.Info()
		if err != nil {
			continue
		}

		switch ext {
		case ".txt":
			txtFiles[uint32(id)] = fullPath
		case ".ogg":
			oggFiles[uint32(id)] = fullPath
			oggSizes[uint32(id)] = info.Size()
		}
	}

	for id, txtPath := range txtFiles {
		ts := time.Unix(int64(id), 0)
		date := ts.Format("2006-01-02")

		text, err := os.ReadFile(txtPath)
		if err != nil {
			text = []byte("")
		}

		rec := &Recording{
			SessionID: id,
			Timestamp: ts,
			Date:      date,
			Text:      strings.TrimSpace(string(text)),
			TxtPath:   txtPath,
			Size:      oggSizes[id],
		}
		if oggPath, ok := oggFiles[id]; ok {
			rec.OggPath = oggPath
		}

		s.recordings[date] = append(s.recordings[date], rec)
	}

	dateSet := make(map[string]bool)
	for d := range s.recordings {
		dateSet[d] = true
	}
	for d := range dateSet {
		s.dates = append(s.dates, d)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(s.dates)))

	for _, recs := range s.recordings {
		sort.Slice(recs, func(i, j int) bool {
			return recs[i].Timestamp.After(recs[j].Timestamp)
		})
	}

	return nil
}

func (s *Store) Dates() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, len(s.dates))
	copy(out, s.dates)
	return out
}

func (s *Store) RecordingsForDate(date string) []*Recording {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Recording, len(s.recordings[date]))
	copy(out, s.recordings[date])
	return out
}

func (s *Store) AllRecordings() []*Recording {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var all []*Recording
	for _, d := range s.dates {
		all = append(all, s.recordings[d]...)
	}
	return all
}

func (s *Store) GetRecording(sessionID uint32) *Recording {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, recs := range s.recordings {
		for _, r := range recs {
			if r.SessionID == sessionID {
				return r
			}
		}
	}
	return nil
}

func (s *Store) AddRecording(sessionID uint32) error {
	ts := time.Unix(int64(sessionID), 0)
	date := ts.Format("2006-01-02")
	txtPath := filepath.Join(s.dir, fmt.Sprintf("recording_%d.txt", sessionID))
	oggPath := filepath.Join(s.dir, fmt.Sprintf("recording_%d.ogg", sessionID))

	text, err := os.ReadFile(txtPath)
	if err != nil {
		return fmt.Errorf("reading transcription: %w", err)
	}

	var size int64
	if info, err := os.Stat(oggPath); err == nil {
		size = info.Size()
	}

	rec := &Recording{
		SessionID: sessionID,
		Timestamp: ts,
		Date:      date,
		Text:      strings.TrimSpace(string(text)),
		TxtPath:   txtPath,
		OggPath:   oggPath,
		Size:      size,
	}

	s.mu.Lock()
	s.recordings[date] = append(s.recordings[date], rec)
	sort.Slice(s.recordings[date], func(i, j int) bool {
		return s.recordings[date][i].Timestamp.After(s.recordings[date][j].Timestamp)
	})

	found := false
	for _, d := range s.dates {
		if d == date {
			found = true
			break
		}
	}
	if !found {
		s.dates = append(s.dates, date)
		sort.Sort(sort.Reverse(sort.StringSlice(s.dates)))
	}
	s.mu.Unlock()

	return nil
}

func (s *Store) DailyLogDir() string {
	return filepath.Join(s.dir, "daily")
}

func (s *Store) LoadDailyLogMeta(date string) (*DailyLogMeta, error) {
	metaPath := filepath.Join(s.DailyLogDir(), date+".json")
	data, err := os.ReadFile(metaPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var meta DailyLogMeta
	if err := parseJSON(data, &meta); err != nil {
		return nil, err
	}
	return &meta, nil
}

func (s *Store) SaveDailyLogMeta(meta *DailyLogMeta) error {
	dir := s.DailyLogDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	metaPath := filepath.Join(dir, meta.Date+".json")
	data, err := toJSON(meta)
	if err != nil {
		return err
	}
	return os.WriteFile(metaPath, data, 0644)
}

func (s *Store) GenerateDailyLog(date string, entries []LogEntry) error {
	s.mu.RLock()
	recs := s.recordings[date]
	s.mu.RUnlock()

	recMap := make(map[uint32]*Recording)
	for _, r := range recs {
		recMap[r.SessionID] = r
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Daily Log — %s\n\n", date))

	for _, entry := range entries {
		if !entry.Included {
			continue
		}
		rec, ok := recMap[entry.SessionID]
		if !ok {
			continue
		}
		timeStr := rec.Timestamp.Format("15:04")
		sb.WriteString(fmt.Sprintf("## %s — recording_%d\n\n", timeStr, rec.SessionID))
		sb.WriteString(rec.Text)
		sb.WriteString("\n\n")
	}

	dir := s.DailyLogDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	mdPath := filepath.Join(dir, date+".md")
	return os.WriteFile(mdPath, []byte(sb.String()), 0644)
}

func (s *Store) SummaryPath(date string) string {
	return filepath.Join(s.DailyLogDir(), date+"_summary.md")
}

func (s *Store) LoadSummary(date string) (string, error) {
	data, err := os.ReadFile(s.SummaryPath(date))
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(data), nil
}

func (s *Store) SaveSummary(date, content string) error {
	dir := s.DailyLogDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	return os.WriteFile(s.SummaryPath(date), []byte(content), 0644)
}

func (s *Store) ListSummaries() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	dir := s.DailyLogDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var dates []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, "_summary.md") {
			continue
		}
		date := strings.TrimSuffix(name, "_summary.md")
		if len(date) == 10 && date[4] == '-' && date[7] == '-' {
			dates = append(dates, date)
		}
	}

	sort.Sort(sort.Reverse(sort.StringSlice(dates)))
	return dates
}

func (s *Store) HasDailyLog(date string) bool {
	_, err := os.Stat(filepath.Join(s.DailyLogDir(), date+".md"))
	return err == nil
}

func (r *Recording) Excerpt(maxLen int) string {
	text := strings.ReplaceAll(r.Text, "\n", " ")
	text = strings.TrimSpace(text)
	if len(text) > maxLen {
		return text[:maxLen] + "..."
	}
	return text
}

func (r *Recording) TimeStr() string {
	return r.Timestamp.Format("15:04:05")
}

func (r *Recording) HumanSize() string {
	f := float64(r.Size)
	for _, u := range []string{"B", "KB", "MB", "GB"} {
		if f < 1024 {
			if u == "B" {
				return fmt.Sprintf("%.0f B", f)
			}
			return fmt.Sprintf("%.1f %s", f, u)
		}
		f /= 1024
	}
	return fmt.Sprintf("%.1f GB", f)
}

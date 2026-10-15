package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
)

type dailyLogMode int

const (
	dailyLogPicker dailyLogMode = iota
	dailyLogBuilder
	dailyLogViewer
)

type DailyLogModel struct {
	store      *Store
	date       string
	mode       dailyLogMode
	dates      []string
	dateCur    int
	entries    []dailyEntry
	cursor     int
	width      int
	height     int
	logContent string
	scrollY    int
}

type dailyEntry struct {
	rec      *Recording
	included bool
}

func NewDailyLogModel(store *Store, date string) DailyLogModel {
	m := DailyLogModel{
		store: store,
	}
	if date != "" {
		m.date = date
		m.mode = dailyLogBuilder
		m.loadEntries()
	} else {
		m.mode = dailyLogPicker
		m.loadDates()
	}
	return m
}

func (m *DailyLogModel) loadDates() {
	m.dates = m.store.Dates()
	if m.dateCur >= len(m.dates) {
		m.dateCur = 0
	}
}

func (m *DailyLogModel) loadEntries() {
	recs := m.store.RecordingsForDate(m.date)

	meta, _ := m.store.LoadDailyLogMeta(m.date)
	metaMap := make(map[uint32]bool)
	if meta != nil {
		for _, e := range meta.Recordings {
			metaMap[e.SessionID] = e.Included
		}
	}

	m.entries = nil
	for _, r := range recs {
		included := true
		if meta != nil {
			if v, ok := metaMap[r.SessionID]; ok {
				included = v
			}
		}
		m.entries = append(m.entries, dailyEntry{rec: r, included: included})
	}
}

func (m *DailyLogModel) loadLogContent() {
	path := filepath.Join(m.store.DailyLogDir(), m.date+".md")
	data, err := os.ReadFile(path)
	if err != nil {
		m.logContent = ""
		return
	}
	m.logContent = string(data)
}

func (m *DailyLogModel) SelectDate(date string) {
	m.date = date
	m.mode = dailyLogBuilder
	m.cursor = 0
	m.loadEntries()
}

func (m *DailyLogModel) BackToDatePicker() {
	if m.mode == dailyLogBuilder && m.date != "" && len(m.entries) > 0 {
		m.saveSync()
	}
	m.date = ""
	m.mode = dailyLogPicker
	m.cursor = 0
	m.scrollY = 0
	m.logContent = ""
	m.loadDates()
}

func (m *DailyLogModel) InBuilder() bool {
	return m.mode != dailyLogPicker
}

func (m *DailyLogModel) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	case StoreUpdatedMsg:
		if m.mode == dailyLogPicker {
			m.loadDates()
		} else if m.mode == dailyLogBuilder {
			m.loadEntries()
		}
	case tea.KeyPressMsg:
		switch m.mode {
		case dailyLogPicker:
			return m.updatePicker(msg)
		case dailyLogBuilder:
			return m.updateBuilder(msg)
		case dailyLogViewer:
			return m.updateViewer(msg)
		}
	}
	return nil
}

func (m *DailyLogModel) updatePicker(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "up", "k":
		if m.dateCur > 0 {
			m.dateCur--
		}
	case "down", "j":
		if m.dateCur < len(m.dates)-1 {
			m.dateCur++
		}
	case "enter":
		if m.dateCur < len(m.dates) {
			m.SelectDate(m.dates[m.dateCur])
		}
	case "v":
		if m.dateCur < len(m.dates) {
			date := m.dates[m.dateCur]
			m.date = date
			m.loadEntries()
			if len(m.entries) > 0 {
				for i := range m.entries {
					m.entries[i].included = true
				}
				m.saveSync()
				m.mode = dailyLogViewer
				m.scrollY = 0
				m.loadLogContent()
			}
		}
	case "s":
		if m.dateCur < len(m.dates) {
			return func() tea.Msg {
				return SummarizeRequestMsg{Date: m.dates[m.dateCur]}
			}
		}
	}
	return nil
}

func (m *DailyLogModel) updateBuilder(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.entries)-1 {
			m.cursor++
		}
	case "space":
		if m.cursor < len(m.entries) {
			m.entries[m.cursor].included = !m.entries[m.cursor].included
		}
	case "a":
		for i := range m.entries {
			m.entries[i].included = true
		}
	case "n":
		for i := range m.entries {
			m.entries[i].included = false
		}
	case "enter", "v":
		if len(m.entries) > 0 {
			m.saveSync()
			m.mode = dailyLogViewer
			m.scrollY = 0
			m.loadLogContent()
		}
	}
	return nil
}

func (m *DailyLogModel) updateViewer(msg tea.KeyPressMsg) tea.Cmd {
	lines := m.formattedLines()
	maxScroll := len(lines) - (m.height - 6)
	if maxScroll < 0 {
		maxScroll = 0
	}

	switch msg.String() {
	case "up", "k":
		if m.scrollY > 0 {
			m.scrollY--
		}
	case "down", "j":
		if m.scrollY < maxScroll {
			m.scrollY++
		}
	case "e":
		m.mode = dailyLogBuilder
		m.cursor = 0
		m.loadEntries()
	case "s":
		return func() tea.Msg {
			return SummarizeRequestMsg{Date: m.date}
		}
	}
	return nil
}

func (m *DailyLogModel) saveSync() error {
	var logEntries []LogEntry
	for _, e := range m.entries {
		logEntries = append(logEntries, LogEntry{
			SessionID: e.rec.SessionID,
			Included:  e.included,
		})
	}
	meta := &DailyLogMeta{
		Date:       m.date,
		Recordings: logEntries,
	}
	if err := m.store.SaveDailyLogMeta(meta); err != nil {
		return err
	}
	return m.store.GenerateDailyLog(m.date, logEntries)
}

func (m DailyLogModel) View() string {
	if m.width == 0 {
		m.width = 80
		m.height = 24
	}

	switch m.mode {
	case dailyLogPicker:
		return m.viewPicker()
	case dailyLogBuilder:
		return m.viewBuilder()
	case dailyLogViewer:
		return m.viewViewer()
	}
	return ""
}

func (m DailyLogModel) viewPicker() string {
	var b strings.Builder

	b.WriteString("\n")

	if len(m.dates) == 0 {
		b.WriteString(DimStyle.Render("  No recordings found. Sync from device first."))
		b.WriteString("\n")
		return b.String()
	}

	header := fmt.Sprintf("  %-14s %-10s %-12s", "Date", "Recs", "Daily Log")
	b.WriteString(HeaderStyle.Render(fmt.Sprintf("%-*s", m.width-4, header)))
	b.WriteString("\n")

	visHeight := m.height - 6
	if visHeight < 3 {
		visHeight = 3
	}

	start, end := visibleRange(m.dateCur, len(m.dates), visHeight)

	for i := start; i < end; i++ {
		d := m.dates[i]
		recs := m.store.RecordingsForDate(d)

		meta, _ := m.store.LoadDailyLogMeta(d)
		logStatus := DimStyle.Render("new")
		if meta != nil {
			included := 0
			for _, e := range meta.Recordings {
				if e.Included {
					included++
				}
			}
			logStatus = CheckedStyle.Render(fmt.Sprintf("%d/%d saved", included, len(meta.Recordings)))
		}

		line := fmt.Sprintf("  %-14s %-10d %s", d, len(recs), logStatus)

		if i == m.dateCur {
			line = SelectedStyle.Render("▸" + line[1:])
		}
		b.WriteString(line + "\n")
	}

	b.WriteString("\n")
	b.WriteString(HelpStyle.Render("  ↑↓ navigate  │  enter build  │  v view  │  s summarize  │  esc back"))

	return b.String()
}

func (m DailyLogModel) viewBuilder() string {
	var b strings.Builder

	b.WriteString("\n")

	if len(m.entries) == 0 {
		b.WriteString(DimStyle.Render("  No recordings for this date."))
		return b.String()
	}

	header := fmt.Sprintf("  %-4s  %-10s %-8s %s", "Inc", "Time", "Size", "Excerpt")
	b.WriteString(HeaderStyle.Render(fmt.Sprintf("%-*s", m.width-4, header)))
	b.WriteString("\n")

	visHeight := m.height - 10
	if visHeight < 3 {
		visHeight = 3
	}

	start, end := visibleRange(m.cursor, len(m.entries), visHeight)

	for i := start; i < end; i++ {
		e := m.entries[i]
		check := "[x]"
		style := CheckedStyle
		if !e.included {
			check = "[ ]"
			style = UncheckedStyle
		}

		excerpt := e.rec.Excerpt(m.width - 34)
		line := fmt.Sprintf("  %s  %-10s %-8s %s", check, e.rec.TimeStr(), e.rec.HumanSize(), excerpt)

		if i == m.cursor {
			line = SelectedStyle.Render("▸" + line[1:])
		} else {
			prefix := line[:4]
			rest := line[4:]
			line = style.Render(prefix) + rest
		}
		b.WriteString(line + "\n")
	}

	b.WriteString("\n")

	included := 0
	for _, e := range m.entries {
		if e.included {
			included++
		}
	}
	summary := fmt.Sprintf("  %d/%d recordings selected", included, len(m.entries))
	b.WriteString(summary)
	b.WriteString("\n\n")
	b.WriteString(HelpStyle.Render("  space toggle  │  a all  │  n none  │  enter view  │  esc back"))

	return b.String()
}

func (m DailyLogModel) viewViewer() string {
	var b strings.Builder

	absPath := filepath.Join(m.store.dir, "daily", m.date+".md")
	b.WriteString(DimStyle.Render(fmt.Sprintf("  %s", absPath)))
	b.WriteString("\n\n")

	lines := m.formattedLines()
	visHeight := m.height - 6
	if visHeight < 3 {
		visHeight = 3
	}

	maxScroll := len(lines) - visHeight
	if maxScroll < 0 {
		maxScroll = 0
	}
	if m.scrollY > maxScroll {
		m.scrollY = maxScroll
	}

	end := m.scrollY + visHeight
	if end > len(lines) {
		end = len(lines)
	}

	for i := m.scrollY; i < end; i++ {
		b.WriteString(lines[i])
		b.WriteString("\n")
	}

	if len(lines) == 0 {
		b.WriteString(DimStyle.Render("  No log file found. Save from the builder first."))
		b.WriteString("\n")
	}

	b.WriteString("\n")

	scrollIndicator := ""
	if len(lines) > visHeight {
		pct := float64(m.scrollY) / float64(maxScroll) * 100
		scrollIndicator = DimStyle.Render(fmt.Sprintf("  (%d%%)  ", int(pct)))
	}
	b.WriteString(HelpStyle.Render("  ↑↓ scroll  │  e edit  │  s summarize  │  esc back") + scrollIndicator)

	return b.String()
}

func wordWrap(s string, width int) string {
	if width <= 0 {
		return s
	}
	words := strings.Fields(s)
	if len(words) == 0 {
		return ""
	}
	var b strings.Builder
	lineLen := 0
	for i, word := range words {
		for len(word) > width {
			chunk := word[:width]
			word = word[width:]
			if lineLen > 0 {
				b.WriteString("\n")
			}
			b.WriteString(chunk)
			lineLen = width
		}
		if len(word) == 0 {
			continue
		}
		w := len(word)
		if i == 0 && lineLen == 0 {
			b.WriteString(word)
			lineLen = w
		} else if lineLen+1+w > width {
			b.WriteString("\n")
			b.WriteString(word)
			lineLen = w
		} else {
			b.WriteString(" ")
			b.WriteString(word)
			lineLen += 1 + w
		}
	}
	return b.String()
}

func (m DailyLogModel) formattedLines() []string {
	return renderedMarkdownLines(m.logContent, m.width)
}

func visibleRange(cursor, total, visHeight int) (int, int) {
	start := 0
	end := total
	if end-start > visHeight {
		if cursor >= visHeight {
			start = cursor - visHeight + 1
		}
		end = start + visHeight
		if end > total {
			end = total
		}
	}
	return start, end
}

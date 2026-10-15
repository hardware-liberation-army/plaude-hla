package tui

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"aplaud/internal/config"
	"aplaud/internal/llm"
	"aplaud/internal/memory"
	tea "charm.land/bubbletea/v2"
)

type summaryMode int

const (
	summaryList summaryMode = iota
	summaryViewer
	summaryGenerating
)

type SummaryModel struct {
	store          *Store
	cfg            *config.Config
	memStore       *memory.Store
	mode           summaryMode
	summaries      []string
	cursor         int
	date           string
	content        string
	preEditContent string
	scrollY        int
	width          int
	height         int
	err            error
	spinnerDots    int
}

func NewSummaryModel(store *Store, cfg *config.Config, memStore *memory.Store) SummaryModel {
	m := SummaryModel{
		store:    store,
		cfg:      cfg,
		memStore: memStore,
		mode:     summaryList,
	}
	m.loadSummaries()
	return m
}

func (m *SummaryModel) loadSummaries() {
	dates := m.store.Dates()
	var available []string
	for _, d := range dates {
		if m.store.HasDailyLog(d) {
			available = append(available, d)
		}
	}
	m.summaries = available
	if m.cursor >= len(m.summaries) {
		m.cursor = 0
	}
}

func (m *SummaryModel) loadContent() {
	if m.date == "" {
		m.content = ""
		return
	}
	summary, err := m.store.LoadSummary(m.date)
	if err != nil {
		m.content = ""
		return
	}
	m.content = summary
}

func (m *SummaryModel) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	case StoreUpdatedMsg:
		if m.mode == summaryList {
			m.loadSummaries()
		}
	case TickMsg:
		if m.mode == summaryGenerating {
			m.spinnerDots = (m.spinnerDots + 1) % 4
			return nil
		}
	case SummaryGeneratedMsg:
		return m.handleSummaryGenerated(msg)
	case EditorFinishedMsg:
		return m.handleEditorFinished(msg)
	case tea.KeyPressMsg:
		switch m.mode {
		case summaryList:
			return m.updateList(msg)
		case summaryViewer:
			return m.updateViewer(msg)
		case summaryGenerating:
			return nil
		}
	}
	return nil
}

func (m *SummaryModel) handleSummaryGenerated(msg SummaryGeneratedMsg) tea.Cmd {
	if msg.Err != nil {
		m.err = msg.Err
		m.mode = summaryList
		return nil
	}

	m.date = msg.Date
	m.content = msg.Content
	m.err = nil
	m.scrollY = 0
	m.mode = summaryViewer
	return nil
}

func (m *SummaryModel) updateList(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.summaries)-1 {
			m.cursor++
		}
	case "enter":
		if m.cursor < len(m.summaries) {
			date := m.summaries[m.cursor]
			summary, _ := m.store.LoadSummary(date)
			if summary == "" {
				return m.generateSummary(date)
			}
			m.date = date
			m.content = summary
			m.scrollY = 0
			m.mode = summaryViewer
		}
	case "g":
		if m.cursor < len(m.summaries) {
			return m.generateSummary(m.summaries[m.cursor])
		}
	}
	return nil
}

func (m *SummaryModel) updateViewer(msg tea.KeyPressMsg) tea.Cmd {
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
	case "g":
		return m.generateSummary(m.date)
	case "e":
		return m.openEditor()
	}
	return nil
}

func (m *SummaryModel) openEditor() tea.Cmd {
	m.preEditContent = m.content
	summaryPath := m.store.SummaryPath(m.date)
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}
	cmd := exec.Command(editor, summaryPath)
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		return EditorFinishedMsg{Err: err}
	})
}

func (m *SummaryModel) handleEditorFinished(msg EditorFinishedMsg) tea.Cmd {
	if msg.Err != nil {
		m.err = msg.Err
		m.preEditContent = ""
		return nil
	}
	original := m.preEditContent
	m.preEditContent = ""
	m.loadContent()

	if original == "" || m.memStore == nil || !m.cfg.LLM.Configured() || original == m.content {
		return nil
	}

	originalContent := original
	editedContent := m.content
	cfg := m.cfg
	memStore := m.memStore

	return func() tea.Msg {
		existingTerms, _ := memStore.AllTerms()
		client := llm.NewClient(cfg.LLM)
		entities, err := memory.ExtractFromEditDiff(context.Background(), client, originalContent, editedContent, existingTerms)
		if err != nil {
			log.Printf("memory edit diff extraction error: %v", err)
			return nil
		}
		if len(entities) > 0 {
			if err := memStore.UpsertBatch(entities, time.Now()); err != nil {
				log.Printf("memory upsert error: %v", err)
			}
		}
		return nil
	}
}

func (m *SummaryModel) generateSummary(date string) tea.Cmd {
	if m.cfg == nil || !m.cfg.LLM.Configured() {
		m.err = fmt.Errorf("LLM not configured (add llm.api_key to config)")
		return nil
	}

	if !m.store.HasDailyLog(date) {
		m.err = fmt.Errorf("no daily log for %s — build one from the daily log tab first", date)
		return nil
	}

	m.err = nil
	m.date = date
	m.mode = summaryGenerating
	m.spinnerDots = 0

	cfg := m.cfg
	store := m.store
	memStore := m.memStore

	return func() tea.Msg {
		logPath := filepath.Join(store.DailyLogDir(), date+".md")
		data, err := os.ReadFile(logPath)
		if err != nil {
			return SummaryGeneratedMsg{Date: date, Err: fmt.Errorf("reading daily log: %w", err)}
		}

		dailyLog := string(data)

		var memoryContext string
		if memStore != nil {
			matched, mErr := memStore.SearchByText(dailyLog)
			if mErr != nil {
				log.Printf("memory search error: %v", mErr)
			}
			memoryContext = memory.RenderForPromptCompact(matched)
		}

		client := llm.NewClient(cfg.LLM)
		summary, err := llm.GenerateSummary(context.Background(), client, dailyLog, memoryContext)
		if err != nil {
			return SummaryGeneratedMsg{Date: date, Err: err}
		}

		if err := store.SaveSummary(date, summary); err != nil {
			return SummaryGeneratedMsg{Date: date, Err: fmt.Errorf("saving summary: %w", err)}
		}

		if memStore != nil {
			existingTerms, _ := memStore.AllTerms()
			newEntities, extErr := memory.ExtractEntities(context.Background(), client, dailyLog, existingTerms)
			if extErr != nil {
				log.Printf("memory entity extraction error: %v", extErr)
			} else if len(newEntities) > 0 {
				if upsertErr := memStore.UpsertBatch(newEntities, time.Now()); upsertErr != nil {
					log.Printf("memory upsert error: %v", upsertErr)
				}
			}
		}

		return SummaryGeneratedMsg{Date: date, Content: summary}
	}
}

func (m *SummaryModel) BackToList() {
	m.mode = summaryList
	m.date = ""
	m.content = ""
	m.scrollY = 0
	m.loadSummaries()
}

func (m *SummaryModel) GenerateForDate(date string) tea.Cmd {
	m.loadSummaries()
	return m.generateSummary(date)
}

func (m SummaryModel) View() string {
	if m.width == 0 {
		m.width = 80
		m.height = 24
	}

	switch m.mode {
	case summaryList:
		return m.viewList()
	case summaryViewer:
		return m.viewViewer()
	case summaryGenerating:
		return m.viewGenerating()
	}
	return ""
}

func (m SummaryModel) viewList() string {
	var b strings.Builder

	b.WriteString("\n")

	if m.err != nil {
		b.WriteString(DisconnectedStyle.Render(fmt.Sprintf("  Error: %v", m.err)))
		b.WriteString("\n\n")
		m.err = nil
	}

	if len(m.summaries) == 0 {
		b.WriteString(DimStyle.Render("  No daily logs found. Build one from the daily log tab first."))
		b.WriteString("\n")
		return b.String()
	}

	header := fmt.Sprintf("  %-14s %-10s %s", "Date", "Status", "Preview")
	b.WriteString(HeaderStyle.Render(fmt.Sprintf("%-*s", m.width-4, header)))
	b.WriteString("\n")

	visHeight := m.height - 6
	if visHeight < 3 {
		visHeight = 3
	}

	start, end := visibleRange(m.cursor, len(m.summaries), visHeight)

	for i := start; i < end; i++ {
		d := m.summaries[i]
		summary, _ := m.store.LoadSummary(d)

		status := DimStyle.Render("new")
		if summary != "" {
			status = CheckedStyle.Render("summary")
		}

		preview := ""
		if summary != "" {
			for _, line := range strings.Split(summary, "\n") {
				line = strings.TrimSpace(line)
				if line != "" && !strings.HasPrefix(line, "#") {
					preview = line
					break
				}
			}
			maxPreview := m.width - 32
			if maxPreview > 0 && len(preview) > maxPreview {
				preview = preview[:maxPreview] + "..."
			}
		}

		line := fmt.Sprintf("  %-14s %-18s %s", d, status, preview)

		if i == m.cursor {
			line = SelectedStyle.Render("▸" + line[1:])
		}
		b.WriteString(line + "\n")
	}

	b.WriteString("\n")
	b.WriteString(HelpStyle.Render("  ↑↓ navigate  │  enter view/generate  │  g generate  │  esc back"))

	return b.String()
}

func (m SummaryModel) viewViewer() string {
	var b strings.Builder

	absPath := m.store.SummaryPath(m.date)
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
		b.WriteString(DimStyle.Render("  Summary is empty."))
		b.WriteString("\n")
	}

	b.WriteString("\n")

	scrollIndicator := ""
	if len(lines) > visHeight {
		pct := float64(m.scrollY) / float64(maxScroll) * 100
		scrollIndicator = DimStyle.Render(fmt.Sprintf("  (%d%%)  ", int(pct)))
	}
	b.WriteString(HelpStyle.Render("  ↑↓ scroll  │  e edit  │  g regenerate  │  esc back") + scrollIndicator)

	return b.String()
}

func (m SummaryModel) viewGenerating() string {
	var b strings.Builder

	b.WriteString("\n\n")

	dots := strings.Repeat(".", m.spinnerDots+1)
	b.WriteString(ScanningStyle.Render(fmt.Sprintf("  Generating summary%s", dots)))
	b.WriteString("\n\n")
	b.WriteString(DimStyle.Render(fmt.Sprintf("  Using %s (%s)", m.cfg.LLM.Provider, m.cfg.LLM.EffectiveModel())))
	b.WriteString("\n")

	return b.String()
}

func (m SummaryModel) formattedLines() []string {
	return renderedMarkdownLines(m.content, m.width)
}

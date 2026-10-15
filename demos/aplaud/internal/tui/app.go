package tui

import (
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"aplaud/internal/config"
	"aplaud/internal/memory"
	tea "charm.land/bubbletea/v2"
)

type viewID int

const (
	viewDashboard viewID = iota
	viewDailyLog
	viewSummaries
)

type AppModel struct {
	cfg      *config.Config
	bridge   *BLEBridge
	store    *Store
	memStore *memory.Store
	current  viewID
	width    int
	height   int
	err         error
	shuttingDown bool

	dashboard DashboardModel
	dailyLog  DailyLogModel
	summaries SummaryModel
}

func NewApp(cfg *config.Config) AppModel {
	store := NewStore(cfg.DownloadDir)
	store.Scan()

	bridge := NewBLEBridge(cfg, nil)

	var memStore *memory.Store
	if ms, err := memory.Open(cfg.MemoryDBPath); err != nil {
		log.Printf("warning: could not open memory store: %v", err)
	} else {
		memStore = ms
		memStore.MaybePrune(cfg.MemoryMaxAgeDays)
	}

	return AppModel{
		cfg:        cfg,
		bridge:     bridge,
		store:      store,
		memStore:   memStore,
		current:    viewDashboard,
		dashboard:  NewDashboardModel(bridge, store),
		dailyLog:   NewDailyLogModel(store, ""),
		summaries:  NewSummaryModel(store, cfg, memStore),
	}
}

func (m AppModel) Init() tea.Cmd {
	return tea.Batch(tickCmd(), m.connectCmd())
}

func (m AppModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.dashboard.Update(msg)
		m.dailyLog.Update(msg)
		m.summaries.Update(msg)

	case tea.KeyPressMsg:
		if handled, cmd := m.handleGlobalKey(msg); handled {
			return m, cmd
		}

	case ShutdownMsg:
		if m.memStore != nil {
			m.memStore.Close()
		}
		return m, tea.Quit

	case TickMsg:
		cmds = append(cmds, tickCmd())

	case ConnectionStatusMsg:
		m.err = nil

	case ErrorMsg:
		m.err = msg.Err

	case NewTranscriptionMsg:
		m.store.AddRecording(msg.SessionID)
		cmds = append(cmds, func() tea.Msg { return StoreUpdatedMsg{} })
		date := time.Now().Format("2006-01-02")
		meta, _ := m.store.LoadDailyLogMeta(date)
		if meta == nil {
			meta = &DailyLogMeta{Date: date}
		}
		found := false
		for i, e := range meta.Recordings {
			if e.SessionID == msg.SessionID {
				meta.Recordings[i].Included = true
				found = true
				break
			}
		}
		if !found {
			meta.Recordings = append(meta.Recordings, LogEntry{
				SessionID: msg.SessionID,
				Included:  true,
			})
		}
		m.store.SaveDailyLogMeta(meta)
		m.store.GenerateDailyLog(date, meta.Recordings)

	case StoreUpdatedMsg:
		m.summaries.Update(msg)

	case SummaryGeneratedMsg:
		cmd := m.summaries.Update(msg)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
		if m.current != viewSummaries {
			m.current = viewSummaries
		}

	case SummarizeRequestMsg:
		m.current = viewSummaries
		cmd := m.summaries.GenerateForDate(msg.Date)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	}

	cmd := m.updateCurrentView(msg)
	if cmd != nil {
		cmds = append(cmds, cmd)
	}

	return m, tea.Batch(cmds...)
}

func (m *AppModel) updateCurrentView(msg tea.Msg) tea.Cmd {
	switch m.current {
	case viewDashboard:
		return m.dashboard.Update(msg)
	case viewDailyLog:
		return m.dailyLog.Update(msg)
	case viewSummaries:
		return m.summaries.Update(msg)
	}
	return nil
}

func (m *AppModel) flushDailyLogBuilder() {
	if m.current == viewDailyLog && m.dailyLog.mode == dailyLogBuilder && m.dailyLog.date != "" && len(m.dailyLog.entries) > 0 {
		m.dailyLog.saveSync()
	}
}

func (m *AppModel) switchTab(offset int) (bool, tea.Cmd) {
	m.flushDailyLogBuilder()
	tabOrder := []viewID{viewDashboard, viewDailyLog, viewSummaries}
	for i, v := range tabOrder {
		if m.current == v {
			next := (i + offset + len(tabOrder)) % len(tabOrder)
			m.current = tabOrder[next]
			if m.current == viewDailyLog {
				m.dailyLog.BackToDatePicker()
			}
			if m.current == viewSummaries {
				m.summaries.BackToList()
			}
			return true, nil
		}
	}
	m.current = tabOrder[0]
	return true, nil
}

func (m *AppModel) handleGlobalKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	if m.shuttingDown {
		return true, nil
	}

	switch msg.String() {
	case "ctrl+c":
		m.flushDailyLogBuilder()
		m.shuttingDown = true
		if m.memStore != nil {
			m.memStore.Close()
		}
		go m.bridge.Stop()
		return true, tea.Tick(300*time.Millisecond, func(time.Time) tea.Msg { return ShutdownMsg{} })
	case "q":
		if m.current == viewDashboard {
			m.shuttingDown = true
			if m.memStore != nil {
				m.memStore.Close()
			}
			go m.bridge.Stop()
			return true, tea.Tick(300*time.Millisecond, func(time.Time) tea.Msg { return ShutdownMsg{} })
		}
		m.flushDailyLogBuilder()
		m.current = viewDashboard
		return true, nil
	case "esc":
		switch m.current {
		case viewDashboard:
			m.shuttingDown = true
			go m.bridge.Stop()
			return true, tea.Tick(300*time.Millisecond, func(time.Time) tea.Msg { return ShutdownMsg{} })
		case viewDailyLog:
			if m.dailyLog.mode == dailyLogPicker {
				m.current = viewDashboard
			} else if m.dailyLog.mode == dailyLogViewer {
				m.dailyLog.BackToDatePicker()
			} else {
				m.dailyLog.BackToDatePicker()
			}
			return true, nil
		case viewSummaries:
			if m.summaries.mode == summaryViewer || m.summaries.mode == summaryGenerating {
				m.summaries.BackToList()
			} else {
				m.current = viewDashboard
			}
			return true, nil
		}
	case "tab", "l":
		return m.switchTab(1)
	case "h":
		return m.switchTab(-1)
	case "1":
		m.flushDailyLogBuilder()
		m.current = viewDashboard
		return true, nil
	case "2":
		m.flushDailyLogBuilder()
		m.dailyLog.BackToDatePicker()
		m.current = viewDailyLog
		return true, nil
	case "3":
		m.flushDailyLogBuilder()
		m.summaries.BackToList()
		m.current = viewSummaries
		return true, nil
	}
	return false, nil
}

func (m AppModel) View() tea.View {
	if m.shuttingDown {
		view := tea.NewView(SubtitleStyle.Render("  Shutting down..."))
		view.AltScreen = true
		return view
	}

	tabBar := m.renderTabBar()

	var content string

	switch m.current {
	case viewDashboard:
		content = m.dashboard.View()
	case viewDailyLog:
		content = m.dailyLog.View()
	case viewSummaries:
		content = m.summaries.View()
	}

	if m.err != nil {
		content = fmt.Sprintf("%s\n\n%s\n", content,
			DisconnectedStyle.Render(fmt.Sprintf("Error: %v", m.err)))
	}

	full := tabBar + "\n" + DimStyle.Render(strings.Repeat("─", m.width)) + "\n" + content

	view := tea.NewView(full)
	view.AltScreen = true
	return view
}

func (m AppModel) renderTabBar() string {
	tabs := []struct {
		id    viewID
		label string
	}{
		{viewDashboard, "dashboard"},
		{viewDailyLog, "daily log"},
		{viewSummaries, "summaries"},
	}

	var parts []string
	for _, t := range tabs {
		label := fmt.Sprintf(" %s ", t.label)
		if m.current == t.id {
			parts = append(parts, SelectedStyle.Render(label))
		} else {
			parts = append(parts, DimStyle.Render(label))
		}
	}

	return strings.Join(parts, "│")
}

func tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return TickMsg(t)
	})
}

func (m AppModel) connectCmd() tea.Cmd {
	return func() tea.Msg {
		m.bridge.Connect()
		return ConnectionStatusMsg{}
	}
}

func RunTUI(cfg *config.Config) error {
	app := NewApp(cfg)

	p := tea.NewProgram(app)

	app.bridge.send = func(msg tea.Msg) {
		p.Send(msg)
	}

	r, w, _ := os.Pipe()
	oldStdout := os.Stdout
	os.Stdout = w

	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := r.Read(buf)
			if n > 0 {
				GlobalLogCapture.Write(buf[:n])
			}
			if err != nil {
				break
			}
		}
	}()

	defer func() {
		w.Close()
		os.Stdout = oldStdout
	}()

	_, err := p.Run()
	return err
}

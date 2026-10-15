package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

type DashboardModel struct {
	bridge     *BLEBridge
	store      *Store
	width      int
	height     int
	syncing    bool
	lastResult string
}

func NewDashboardModel(bridge *BLEBridge, store *Store) DashboardModel {
	return DashboardModel{
		bridge: bridge,
		store:  store,
	}
}

func (m *DashboardModel) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	case SyncStartMsg:
		m.syncing = true
	case SyncDoneMsg:
		m.syncing = false
		if len(msg.NewFiles) > 0 {
			m.lastResult = fmt.Sprintf("Downloaded %d new file(s)", len(msg.NewFiles))
		} else {
			m.lastResult = "Up to date"
		}
	case ErrorMsg:
		m.syncing = false
		m.lastResult = fmt.Sprintf("Error: %v", msg.Err)
	case ConnectionStatusMsg, BatteryMsg, StoreUpdatedMsg:
	case tea.KeyPressMsg:
		switch msg.String() {
		case "s":
			if !m.syncing {
				m.bridge.SyncNow()
			}
		case "a":
			m.bridge.SetAutoSync(!m.bridge.AutoSync())
		}
	}
	return nil
}

func (m DashboardModel) View() string {
	if m.width == 0 {
		m.width = 80
		m.height = 24
	}

	status, addr := m.bridge.Status()
	lastSync := m.bridge.LastSync()
	dates := m.store.Dates()
	allRecs := m.store.AllRecordings()

	todayStr := time.Now().Format("2006-01-02")
	var todayCount int
	var todayRecs []*Recording
	for _, d := range dates {
		if d == todayStr {
			recs := m.store.RecordingsForDate(d)
			todayRecs = recs
			todayCount = len(recs)
			break
		}
	}

	var b strings.Builder

	b.WriteString("\n")

	statusStr := m.renderStatus(status)
	b.WriteString(fmt.Sprintf("  Connection:  %s\n", statusStr))
	if addr != "" {
		b.WriteString(fmt.Sprintf("  Device:      %s\n", DimStyle.Render(addr)))
	}
	b.WriteString(fmt.Sprintf("  Auto-sync:   %s\n", m.renderAutoSync()))

	if status == StatusMonitoring || status == StatusConnected {
		pollSec := int(m.bridge.PollInterval().Seconds())
		b.WriteString(fmt.Sprintf("  Poll every:  %ds\n", pollSec))

		syncAgo := time.Since(lastSync)
		if lastSync.IsZero() {
			b.WriteString("  Last sync:   never\n")
		} else {
			b.WriteString(fmt.Sprintf("  Last sync:   %s ago\n",
				DimStyle.Render(formatDuration(syncAgo))))
		}
	}

	b.WriteString("\n")

	statsBox := fmt.Sprintf(
		"  Total recordings: %d\n  Days with recordings: %d\n  Today (%s): %d recordings",
		len(allRecs), len(dates), todayStr, todayCount,
	)
	b.WriteString(BoxStyle.Render(statsBox))
	b.WriteString("\n\n")

	phase, cur, total, sid := m.bridge.SyncProgress()
	if phase != "" {
		b.WriteString(ScanningStyle.Render("  Active sync: "))
		switch phase {
		case "listing":
			b.WriteString("Listing files on device...\n")
		case "downloading":
			b.WriteString(fmt.Sprintf("Downloading %d/%d (session %d)\n", cur, total, sid))
		case "transcribing":
			b.WriteString(fmt.Sprintf("Transcribing %d/%d (session %d)\n", cur, total, sid))
		}
		if total > 0 {
			barWidth := 30
			filled := barWidth * cur / total
			bar := strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled)
			b.WriteString(fmt.Sprintf("  [%s] %d/%d\n", bar, cur, total))
		}
		b.WriteString("\n")
	} else if m.lastResult != "" {
		b.WriteString(fmt.Sprintf("  Last sync result: %s\n\n", DimStyle.Render(m.lastResult)))
	}

	if todayCount > 0 {
		b.WriteString(HeaderStyle.Render("  Today's Recordings"))
		b.WriteString("\n")
		for _, r := range todayRecs {
			excerpt := r.Excerpt(m.width - 30)
			b.WriteString(fmt.Sprintf("    %s  %s  %s\n",
				TimeStyle.Render(r.TimeStr()),
				DimStyle.Render(r.HumanSize()),
				excerpt,
			))
		}
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(HelpStyle.Render("  s sync now  │  a auto-sync  │  q quit"))

	return b.String()
}

func (m DashboardModel) renderStatus(status string) string {
	switch status {
	case StatusConnected, StatusMonitoring:
		return ConnectedStyle.Render("● connected")
	case StatusDisconnected:
		return DisconnectedStyle.Render("○ disconnected")
	case StatusScanning:
		return ScanningStyle.Render("◎ scanning...")
	case StatusConnecting:
		return ScanningStyle.Render("◎ connecting...")
	case StatusHandshaking:
		return ScanningStyle.Render("◎ handshaking...")
	case StatusReconnecting:
		return ScanningStyle.Render("◎ reconnecting...")
	case StatusError:
		return DisconnectedStyle.Render("✕ error")
	default:
		return DimStyle.Render(status)
	}
}

func (m DashboardModel) renderAutoSync() string {
	if m.bridge.AutoSync() {
		return CheckedStyle.Render("on")
	}
	return UncheckedStyle.Render("off")
}

func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
}

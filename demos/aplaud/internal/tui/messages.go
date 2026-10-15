package tui

import (
	tea "charm.land/bubbletea/v2"
	"time"
)

type ConnectionStatusMsg struct {
	Status string
	Addr   string
}

type BatteryMsg struct {
	Level int
}

type SyncStartMsg struct{}

type SyncProgressMsg struct {
	Phase      string
	Current    int
	Total      int
	SessionID  uint32
}

type SyncDoneMsg struct {
	NewFiles []uint32
}

type NewTranscriptionMsg struct {
	SessionID uint32
	Text      string
	Time      time.Time
}

type StoreUpdatedMsg struct{}

type TickMsg time.Time

func (t TickMsg) String() string {
	return time.Time(t).Format("15:04:05")
}

type ReconnectMsg struct{}

type ShutdownMsg struct{}

type ErrorMsg struct {
	Err error
}

type SummaryGeneratedMsg struct {
	Date    string
	Content string
	Err     error
}

type SummarizeRequestMsg struct {
	Date string
}

type EditorFinishedMsg struct {
	Err error
}

func SendErrorMsg(err error) tea.Cmd {
	return func() tea.Msg {
		return ErrorMsg{Err: err}
	}
}

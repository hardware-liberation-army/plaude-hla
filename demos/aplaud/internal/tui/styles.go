package tui

import (
	"charm.land/lipgloss/v2"
)

var (
	normalFg = lipgloss.Color("252")
	dimFg    = lipgloss.Color("246")
	accent   = lipgloss.Color("69")
	green    = lipgloss.Color("76")
	yellow   = lipgloss.Color("178")
	red      = lipgloss.Color("167")
	cyan     = lipgloss.Color("81")
	bright   = lipgloss.Color("255")

	TitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(bright).
			Background(accent).
			Padding(0, 2)

	SubtitleStyle = lipgloss.NewStyle().
			Foreground(accent).
			Bold(true)

	StatusBarStyle = lipgloss.NewStyle().
			Foreground(bright).
			Background(lipgloss.Color("236"))

	HighlightStyle = lipgloss.NewStyle().
			Foreground(accent).
			Bold(true)

	DimStyle = lipgloss.NewStyle().
			Foreground(dimFg)

	SelectedStyle = lipgloss.NewStyle().
			Foreground(bright).
			Background(accent).
			Bold(true)

	CheckedStyle = lipgloss.NewStyle().
			Foreground(green)

	UncheckedStyle = lipgloss.NewStyle().
			Foreground(dimFg)

	DateStyle = lipgloss.NewStyle().
			Foreground(cyan).
			Bold(true)

	TimeStyle = lipgloss.NewStyle().
			Foreground(yellow)

	ExcerptStyle = lipgloss.NewStyle().
			Foreground(normalFg)

	ConnectedStyle = lipgloss.NewStyle().
			Foreground(green).
			Bold(true)

	DisconnectedStyle = lipgloss.NewStyle().
			Foreground(red)

	ScanningStyle = lipgloss.NewStyle().
			Foreground(yellow)

	HeaderStyle = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder(), false, false, true, false).
			BorderForeground(accent).
			Foreground(accent).
			Bold(true)

	BoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(accent).
			Padding(1, 2)

	ModalStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(yellow).
			Padding(1, 3)

	HelpStyle = lipgloss.NewStyle().
		Foreground(dimFg)

	PreviewStyle = lipgloss.NewStyle().
		Border(lipgloss.NormalBorder(), false, true, false, false).
		BorderForeground(lipgloss.Color("238")).
		Padding(0, 1)
)

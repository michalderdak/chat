package tui

import "github.com/charmbracelet/lipgloss"

var (
	UserStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("10")).
			Bold(true)

	AssistantStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("12"))

	StatusBarStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("236")).
			Foreground(lipgloss.Color("248")).
			Padding(0, 1)

	ErrorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("9")).
			Bold(true)

	InputPromptStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("205"))

	PanelBorderStyle = lipgloss.NewStyle().
				Border(lipgloss.NormalBorder()).
				BorderForeground(lipgloss.Color("240"))

	PanelTitleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("248")).
			Bold(true)

	TabActiveStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("15")).
			Background(lipgloss.Color("62")).
			Padding(0, 2)

	TabInactiveStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("245")).
			Background(lipgloss.Color("236")).
			Padding(0, 2)

	TabBarStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("236"))
)

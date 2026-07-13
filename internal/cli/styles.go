package cli

import "github.com/charmbracelet/lipgloss"

var (
	TitleStyle = lipgloss.NewStyle().Bold(true)
	MutedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	OkStyle    = lipgloss.NewStyle()
	BadStyle   = lipgloss.NewStyle()
	KeyStyle   = lipgloss.NewStyle()
	ValStyle   = lipgloss.NewStyle()
)

func StatusBadge(running bool) string {
	if running {
		return OkStyle.Render("running")
	}
	return BadStyle.Render("stopped")
}

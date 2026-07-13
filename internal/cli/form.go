package cli

import (
	"errors"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
)

// ErrCanceled is returned when the user aborts an interactive prompt (Esc / Ctrl+C).
var ErrCanceled = errors.New("canceled")

func themeMinimal() *huh.Theme {
	t := huh.ThemeBase()
	// Flatten the default left border / padding so prompts sit flush left.
	t.Focused.Base = lipgloss.NewStyle()
	t.Blurred.Base = lipgloss.NewStyle()
	t.Focused.Card = t.Focused.Base
	t.Blurred.Card = t.Blurred.Base
	t.FieldSeparator = lipgloss.NewStyle().SetString("\n")
	t.Focused.Title = lipgloss.NewStyle()
	t.Blurred.Title = lipgloss.NewStyle()
	t.Focused.Description = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	t.Blurred.Description = t.Focused.Description
	t.Focused.SelectSelector = lipgloss.NewStyle().SetString("> ")
	t.Focused.Option = lipgloss.NewStyle()
	t.Focused.SelectedOption = lipgloss.NewStyle().Bold(true)
	t.Blurred.SelectedOption = lipgloss.NewStyle()
	t.Focused.TextInput.Placeholder = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	t.Focused.TextInput.Prompt = lipgloss.NewStyle().SetString("")
	t.Focused.TextInput.Text = lipgloss.NewStyle()
	t.Focused.FocusedButton = lipgloss.NewStyle().Underline(true)
	t.Focused.BlurredButton = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	t.Blurred.FocusedButton = t.Focused.BlurredButton
	t.Blurred.BlurredButton = t.Focused.BlurredButton
	return t
}

func keyMap() *huh.KeyMap {
	km := huh.NewDefaultKeyMap()
	km.Quit = key.NewBinding(
		key.WithKeys("ctrl+c", "esc"),
		key.WithHelp("esc", "quit"),
	)
	return km
}

func runForm(groups ...*huh.Group) error {
	form := huh.NewForm(groups...).
		WithTheme(themeMinimal()).
		WithKeyMap(keyMap()).
		WithShowHelp(false)
	if err := form.Run(); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return ErrCanceled
		}
		return err
	}
	return nil
}

package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/huh"

	"github.com/folsomintel/kuma/internal/connect"
)

// SelectString prompts for one of options. title is shown as the select title.
func SelectString(title string, options []string) (string, error) {
	if len(options) == 0 {
		return "", fmt.Errorf("no options available")
	}
	if len(options) == 1 {
		return options[0], nil
	}
	var value string
	opts := make([]huh.Option[string], 0, len(options))
	for _, o := range options {
		opts = append(opts, huh.NewOption(o, o))
	}
	err := runForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title(title).
				Options(opts...).
				Value(&value),
		),
	)
	if err != nil {
		return "", err
	}
	return value, nil
}

// Confirm asks a yes/no question.
func Confirm(title string, def bool) (bool, error) {
	value := def
	err := runForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title(title).
				Value(&value),
		),
	)
	if err != nil {
		return false, err
	}
	return value, nil
}

// InputString prompts for a free-text value.
func InputString(title, placeholder, initial string, password bool) (string, error) {
	value := initial
	input := huh.NewInput().
		Title(title).
		Placeholder(placeholder).
		Value(&value)
	if password {
		input = input.EchoMode(huh.EchoModePassword)
	}
	if err := runForm(huh.NewGroup(input)); err != nil {
		return "", err
	}
	return strings.TrimSpace(value), nil
}

// SelectRemote picks a remote name from remotes.json.
func SelectRemote(configPath string) (string, error) {
	f, err := connect.LoadRemotesFile(configPath)
	if err != nil {
		return "", err
	}
	names := RemoteNames(f)
	if len(names) == 0 {
		return "", fmt.Errorf("no remotes configured; run: kuma remote add <name>")
	}
	if f.Default != "" {
		// Put default first for convenience.
		ordered := make([]string, 0, len(names))
		ordered = append(ordered, f.Default)
		for _, n := range names {
			if n != f.Default {
				ordered = append(ordered, n)
			}
		}
		names = ordered
	}
	return SelectString("Select remote", names)
}

// PromptRemoteAdd collects remote credentials interactively.
// If preferDefault is non-nil, that value is used without prompting.
func PromptRemoteAdd(name string, preferDefault *bool) (remote connect.Remote, outName string, asDefault bool, err error) {
	name = strings.TrimSpace(name)
	asDefault = true
	if preferDefault != nil {
		asDefault = *preferDefault
	}
	if name == "" {
		name, err = InputString("Remote name", "home", "", false)
		if err != nil {
			return connect.Remote{}, "", false, err
		}
	}
	machineID, err := InputString("Machine ID", "", FirstNonEmpty(os.Getenv("KUMA_MACHINE_ID")), false)
	if err != nil {
		return connect.Remote{}, "", false, err
	}
	key, err := InputString("E2E key", "", FirstNonEmpty(os.Getenv("KUMA_KEY")), true)
	if err != nil {
		return connect.Remote{}, "", false, err
	}
	relayURL, err := InputString("Relay URL", "ws://127.0.0.1:8080", FirstNonEmpty(os.Getenv("KUMA_RELAY_URL"), "ws://127.0.0.1:8080"), false)
	if err != nil {
		return connect.Remote{}, "", false, err
	}
	joinToken, err := InputString("Join token", "", FirstNonEmpty(os.Getenv("KUMA_JOIN_TOKEN")), true)
	if err != nil {
		return connect.Remote{}, "", false, err
	}
	if preferDefault == nil {
		asDefault, err = Confirm("Set as default remote?", true)
		if err != nil {
			return connect.Remote{}, "", false, err
		}
	}
	return connect.Remote{
		MachineID: machineID,
		Key:       key,
		RelayURL:  relayURL,
		JoinToken: joinToken,
	}, name, asDefault, nil
}

// PromptDaemonInit collects values for first-time daemon config.
func PromptDaemonInit(relayURL, joinToken, authSecret string) (relay, token, secret string, err error) {
	relay = FirstNonEmpty(relayURL, os.Getenv("KUMA_RELAY_URL"), "ws://127.0.0.1:8080")
	token = FirstNonEmpty(joinToken, os.Getenv("KUMA_JOIN_TOKEN"))
	secret = FirstNonEmpty(authSecret, os.Getenv("KUMA_RELAY_AUTH_SECRET"))

	relay, err = InputString("Relay URL", "ws://127.0.0.1:8080", relay, false)
	if err != nil {
		return "", "", "", err
	}
	if token == "" && secret == "" {
		mode, err := SelectString("Auth", []string{"auth secret (mint token)", "join token"})
		if err != nil {
			return "", "", "", err
		}
		if mode == "join token" {
			token, err = InputString("Join token", "", "", true)
			if err != nil {
				return "", "", "", err
			}
		} else {
			secret, err = InputString("Relay auth secret", "", "", true)
			if err != nil {
				return "", "", "", err
			}
		}
	}
	return relay, token, secret, nil
}

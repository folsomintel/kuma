package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/folsomintel/kuma/internal/connect"
	"github.com/folsomintel/kuma/internal/daemon"
	"github.com/folsomintel/kuma/internal/jointoken"
)

const LocalRemoteName = "local"

// AuthSecret resolves the relay auth secret from an explicit value or env.
func AuthSecret(explicit string) string {
	return FirstNonEmpty(explicit, os.Getenv("KUMA_RELAY_AUTH_SECRET"))
}

// EnsureLocalRemote upserts a "local" remote from daemon config + auth secret.
// The client join token is minted from authSecret. Returns whether the remote
// was written and the remotes file path.
func EnsureLocalRemote(cfg *daemon.Config, authSecret, remotesPath string) (path string, wrote bool, err error) {
	if cfg == nil {
		return "", false, fmt.Errorf("daemon config is required")
	}
	authSecret = strings.TrimSpace(authSecret)
	if authSecret == "" {
		return "", false, fmt.Errorf("auth secret is required to mint a client join token (pass -S / KUMA_RELAY_AUTH_SECRET)")
	}
	clientTok, err := jointoken.Mint(authSecret, cfg.MachineID, jointoken.RoleClient)
	if err != nil {
		return "", false, err
	}
	if remotesPath == "" {
		remotesPath, err = connect.DefaultRemotesPath()
		if err != nil {
			return "", false, err
		}
	}
	f, err := connect.LoadRemotesFile(remotesPath)
	if err != nil {
		return "", false, err
	}
	remote := connect.Remote{
		MachineID: cfg.MachineID,
		Key:       cfg.Key,
		RelayURL:  cfg.RelayURL,
		JoinToken: clientTok,
	}
	makeDefault := f.Default == "" || f.Default == LocalRemoteName
	if existing, ok := f.Remotes[LocalRemoteName]; ok &&
		existing.MachineID == remote.MachineID &&
		existing.Key == remote.Key &&
		existing.RelayURL == remote.RelayURL &&
		existing.JoinToken == remote.JoinToken {
		return remotesPath, false, nil
	}
	if err := connect.AddRemote(remotesPath, LocalRemoteName, remote, makeDefault); err != nil {
		return remotesPath, false, err
	}
	return remotesPath, true, nil
}

// PrintKeys writes machine identity fields (and optional client token) to stdout.
func PrintKeys(cfg *daemon.Config, clientToken string) {
	fmt.Printf("machine_id=%s\n", cfg.MachineID)
	fmt.Printf("relay_url=%s\n", cfg.RelayURL)
	fmt.Println("warning: treat key= / join_token= as secrets")
	fmt.Printf("key=%s\n", cfg.Key)
	if clientToken != "" {
		fmt.Printf("join_token=%s\n", clientToken)
	}
}

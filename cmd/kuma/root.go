package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
)

type rootFlags struct {
	configPath string
	machineID  string
	key        string
	relayURL   string
	joinToken  string
}

func newRootCmd() *cobra.Command {
	rf := &rootFlags{}
	cmd := &cobra.Command{
		Use:   "kuma",
		Short: "CLI for controlling kuma (daemon, remotes, agents, sessions)",
		Long: `kuma is the CLI for controlling all of kuma.

Manage the local daemon with up/down/status, configure remotes, list agents
and sessions, and run or resume agent sessions over the encrypted relay.

Run a parent command (remote, agent, session, run) with no args for an
interactive menu. Pass explicit args and flags to skip prompts.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	cmd.SetVersionTemplate("{{.Version}}\n")
	// Register version ourselves so the short flag is uppercase -V (cobra defaults to -v).
	cmd.Flags().BoolP("version", "V", false, "version for kuma")

	cmd.PersistentFlags().StringVarP(&rf.configPath, "config", "C", "", "path to remotes.json or daemon config.json")
	cmd.PersistentFlags().StringVarP(&rf.machineID, "machine-id", "M", "", "machine id (overrides remote/env/config)")
	cmd.PersistentFlags().StringVarP(&rf.key, "key", "K", "", "E2E AES-256 key, base64url")
	cmd.PersistentFlags().StringVarP(&rf.relayURL, "relay-url", "R", "", "relay WebSocket base URL")
	cmd.PersistentFlags().StringVarP(&rf.joinToken, "join-token", "T", "", "relay join token")

	cmd.AddCommand(
		newUpCmd(rf),
		newDownCmd(rf),
		newStatusCmd(rf),
		newDaemonCmd(rf),
		newKeysCmd(rf),
		newRemoteCmd(rf),
		newAgentCmd(rf),
		newSessionCmd(rf),
		newRunCmd(rf),
	)
	return cmd
}

func signalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

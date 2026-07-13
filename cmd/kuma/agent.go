package main

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/folsomintel/kuma/internal/cli"
)

func newAgentCmd(rf *rootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "List agents on a remote",
		Long:  "Interactively pick a remote and list agents, or use list.",
		RunE: func(cmd *cobra.Command, args []string) error {
			remote := ""
			if cli.IsTTY() {
				var err error
				remote, err = cli.SelectRemote(rf.configPath)
				if err != nil {
					return err
				}
			}
			return agentList(rf, remote)
		},
	}
	cmd.AddCommand(newAgentListCmd(rf))
	return cmd
}

func newAgentListCmd(rf *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "list [remote]",
		Short: "List agents on a remote kumad",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			remote := ""
			if len(args) > 0 {
				remote = args[0]
			}
			return agentList(rf, remote)
		},
	}
}

func agentList(rf *rootFlags, remote string) error {
	ctx, stop := signalContext()
	defer stop()

	name, err := cli.ResolveRemoteName(rf.configPath, remote)
	if err != nil {
		// Allow credential-only mode with no named remote.
		if remote == "" && (rf.machineID != "" || rf.key != "" || rf.relayURL != "" || rf.joinToken != "") {
			name = ""
		} else if remote == "" {
			return err
		} else {
			name = remote
		}
	}

	client, list, err := cli.DialReady(ctx, cli.CredFlags{
		ConfigPath: rf.configPath,
		Remote:     name,
		MachineID:  rf.machineID,
		Key:        rf.key,
		RelayURL:   rf.relayURL,
		JoinToken:  rf.joinToken,
	})
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()
	return cli.PrintAgents(os.Stdout, list.Agents)
}

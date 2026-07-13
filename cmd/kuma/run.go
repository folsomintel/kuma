package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/folsomintel/kuma/internal/cli"
	"github.com/folsomintel/kuma/internal/connect"
)

func newRunCmd(rf *rootFlags) *cobra.Command {
	var cwd string
	cmd := &cobra.Command{
		Use:   "run [remote] [agent]",
		Short: "Start an agent session on a remote",
		Long:  "Interactively pick remote and agent, or pass them as arguments.",
		Args:  cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			remote := ""
			agent := ""
			if len(args) > 0 {
				remote = args[0]
			}
			if len(args) > 1 {
				agent = args[1]
			}
			return runSession(rf, remote, agent, cwd)
		},
	}
	cmd.Flags().StringVarP(&cwd, "cwd", "W", "", "absolute working directory for the session")
	return cmd
}

func runSession(rf *rootFlags, remote, agent, cwd string) error {
	ctx, stop := signalContext()
	defer stop()

	name := strings.TrimSpace(remote)
	if name == "" {
		var err error
		if cli.IsTTY() {
			// Interactive entry: always let the user pick (even with a default).
			name, err = cli.SelectRemote(rf.configPath)
		} else {
			name, err = cli.ResolveRemoteName(rf.configPath, "")
		}
		if err != nil {
			return err
		}
	}

	fmt.Fprintf(os.Stderr, "connecting to %s…\n", name)
	client, agents, err := cli.DialReady(ctx, cli.CredFlags{
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

	agent = strings.TrimSpace(agent)
	if agent == "" {
		installed := cli.InstalledAgentNames(agents.Agents)
		if len(installed) == 0 {
			return fmt.Errorf("no installed agents on remote %q", name)
		}
		if len(installed) == 1 {
			agent = installed[0]
			fmt.Fprintf(os.Stderr, "using agent %s\n", agent)
		} else {
			if err := cli.RequireTTY("selecting an agent"); err != nil {
				return fmt.Errorf("usage: kuma run <remote> <agent>")
			}
			agent, err = cli.SelectString("Select agent", installed)
			if err != nil {
				return err
			}
		}
	}

	fmt.Fprintf(os.Stderr, "starting %s on %s…\n", agent, name)
	code, err := connect.RunSession(ctx, client, agent, strings.TrimSpace(cwd))
	if err != nil {
		return err
	}
	if code != 0 {
		os.Exit(code)
	}
	return nil
}

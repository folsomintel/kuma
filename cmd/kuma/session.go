package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/folsomintel/kuma/internal/cli"
	"github.com/folsomintel/kuma/internal/connect"
)

func newSessionCmd(rf *rootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "session",
		Short: "Manage remote sessions",
		Long:  "Interactively list, resume, or remove sessions.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSessionInteractive(rf)
		},
	}
	cmd.AddCommand(
		newSessionListCmd(rf),
		newSessionResumeCmd(rf),
		newSessionRemoveCmd(rf),
	)
	return cmd
}

func runSessionInteractive(rf *rootFlags) error {
	if err := cli.RequireTTY("kuma session"); err != nil {
		return err
	}
	remote, err := cli.ResolveRemoteName(rf.configPath, "")
	if err != nil {
		return err
	}
	action, err := cli.SelectString("Session", []string{"List", "Resume", "Remove"})
	if err != nil {
		return err
	}
	switch action {
	case "List":
		return sessionList(rf, remote)
	case "Resume":
		return sessionResume(rf, remote, "")
	case "Remove":
		return sessionRemove(rf, remote, "")
	default:
		return fmt.Errorf("unknown action %q", action)
	}
}

func newSessionListCmd(rf *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "list [remote]",
		Short: "List active sessions on a remote",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			remote := ""
			if len(args) > 0 {
				remote = args[0]
			}
			return sessionList(rf, remote)
		},
	}
}

func newSessionResumeCmd(rf *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "resume [id]",
		Short: "Resume (reattach) an existing session",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := ""
			if len(args) > 0 {
				id = args[0]
			}
			return sessionResume(rf, "", id)
		},
	}
}

func newSessionRemoveCmd(rf *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "remove [id]",
		Short: "Stop and remove a remote session",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := ""
			if len(args) > 0 {
				id = args[0]
			}
			return sessionRemove(rf, "", id)
		},
	}
}

func sessionList(rf *rootFlags, remote string) error {
	ctx, stop := signalContext()
	defer stop()

	name, err := cli.ResolveRemoteName(rf.configPath, remote)
	if err != nil {
		return err
	}
	client, _, err := cli.DialReady(ctx, cli.CredFlags{
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
	list, err := client.ListSessions(ctx)
	if err != nil {
		return err
	}
	return cli.PrintSessions(os.Stdout, list.Sessions)
}

func sessionResume(rf *rootFlags, remote, sessionID string) error {
	ctx, stop := signalContext()
	defer stop()

	name, err := cli.ResolveRemoteName(rf.configPath, remote)
	if err != nil {
		return err
	}
	client, _, err := cli.DialReady(ctx, cli.CredFlags{
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

	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		list, err := client.ListSessions(ctx)
		if err != nil {
			return err
		}
		if len(list.Sessions) == 0 {
			return fmt.Errorf("no sessions on remote")
		}
		if err := cli.RequireTTY("selecting a session"); err != nil {
			return fmt.Errorf("usage: kuma session resume <id>")
		}
		labels, byLabel := cli.SessionLabels(list.Sessions)
		label, err := cli.SelectString("Resume session", labels)
		if err != nil {
			return err
		}
		sessionID = byLabel[label]
	}

	code, err := connect.AttachSession(ctx, client, sessionID)
	if err != nil {
		return err
	}
	if code != 0 {
		os.Exit(code)
	}
	return nil
}

func sessionRemove(rf *rootFlags, remote, sessionID string) error {
	ctx, stop := signalContext()
	defer stop()

	name, err := cli.ResolveRemoteName(rf.configPath, remote)
	if err != nil {
		return err
	}
	client, _, err := cli.DialReady(ctx, cli.CredFlags{
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

	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		list, err := client.ListSessions(ctx)
		if err != nil {
			return err
		}
		if len(list.Sessions) == 0 {
			return fmt.Errorf("no sessions on remote")
		}
		if err := cli.RequireTTY("selecting a session"); err != nil {
			return fmt.Errorf("usage: kuma session remove <id>")
		}
		labels, byLabel := cli.SessionLabels(list.Sessions)
		label, err := cli.SelectString("Remove session", labels)
		if err != nil {
			return err
		}
		sessionID = byLabel[label]
	}

	if err := client.StopSession(ctx, sessionID); err != nil {
		return err
	}
	fmt.Printf("removed session %s\n", sessionID)
	return nil
}

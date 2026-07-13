package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/folsomintel/kuma/internal/cli"
	"github.com/folsomintel/kuma/internal/connect"
)

func newRemoteCmd(rf *rootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "remote",
		Short: "Manage saved remotes",
		Long:  "Manage remotes interactively, or use add/list/remove subcommands.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRemoteInteractive(rf)
		},
	}
	cmd.AddCommand(
		newRemoteListCmd(rf),
		newRemoteAddCmd(rf),
		newRemoteRemoveCmd(rf),
	)
	return cmd
}

func runRemoteInteractive(rf *rootFlags) error {
	if err := cli.RequireTTY("kuma remote"); err != nil {
		return err
	}
	action, err := cli.SelectString("Remote", []string{"List", "Add", "Remove"})
	if err != nil {
		return err
	}
	switch action {
	case "List":
		return remoteList(rf.configPath)
	case "Add":
		return remoteAddInteractive(rf, "", nil)
	case "Remove":
		name, err := cli.SelectRemote(rf.configPath)
		if err != nil {
			return err
		}
		ok, err := cli.Confirm(fmt.Sprintf("Remove remote %q?", name), false)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		return remoteRemove(rf.configPath, name)
	default:
		return fmt.Errorf("unknown action %q", action)
	}
}

func newRemoteListCmd(rf *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List saved remotes",
		RunE: func(cmd *cobra.Command, args []string) error {
			return remoteList(rf.configPath)
		},
	}
}

func newRemoteAddCmd(rf *rootFlags) *cobra.Command {
	var makeDefault bool
	cmd := &cobra.Command{
		Use:   "add [name]",
		Short: "Add or update a remote",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := ""
			if len(args) > 0 {
				name = args[0]
			}
			remote := connect.Remote{
				MachineID: cli.FirstNonEmpty(rf.machineID, os.Getenv("KUMA_MACHINE_ID")),
				Key:       cli.FirstNonEmpty(rf.key, os.Getenv("KUMA_KEY")),
				RelayURL:  cli.FirstNonEmpty(rf.relayURL, os.Getenv("KUMA_RELAY_URL")),
				JoinToken: cli.FirstNonEmpty(rf.joinToken, os.Getenv("KUMA_JOIN_TOKEN")),
			}
			if name == "" || remote.Validate() != nil {
				if !cli.IsTTY() {
					if name == "" {
						return fmt.Errorf("usage: kuma remote add <name> -M … -K … -R … -T …")
					}
					if err := remote.Validate(); err != nil {
						return err
					}
				}
				def := makeDefault
				return remoteAddInteractive(rf, name, &def)
			}
			if err := connect.AddRemote(rf.configPath, name, remote, makeDefault); err != nil {
				return err
			}
			path := rf.configPath
			if path == "" {
				var err error
				path, err = connect.DefaultRemotesPath()
				if err != nil {
					return err
				}
			}
			fmt.Printf("saved remote %q to %s\n", name, path)
			return nil
		},
	}
	cmd.Flags().BoolVarP(&makeDefault, "default", "D", true, "mark as default remote")
	return cmd
}

func remoteAddInteractive(rf *rootFlags, name string, makeDefault *bool) error {
	if err := cli.RequireTTY("adding a remote"); err != nil {
		return err
	}
	remote, name, def, err := cli.PromptRemoteAdd(name, makeDefault)
	if err != nil {
		return err
	}
	// Prefer flag overrides when set.
	remote.MachineID = cli.FirstNonEmpty(rf.machineID, remote.MachineID)
	remote.Key = cli.FirstNonEmpty(rf.key, remote.Key)
	remote.RelayURL = cli.FirstNonEmpty(rf.relayURL, remote.RelayURL)
	remote.JoinToken = cli.FirstNonEmpty(rf.joinToken, remote.JoinToken)
	if err := connect.AddRemote(rf.configPath, name, remote, def); err != nil {
		return err
	}
	path := rf.configPath
	if path == "" {
		path, err = connect.DefaultRemotesPath()
		if err != nil {
			return err
		}
	}
	fmt.Printf("saved remote %q to %s\n", name, path)
	return nil
}

func newRemoteRemoveCmd(rf *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "remove [name]",
		Short: "Remove a saved remote",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := ""
			if len(args) > 0 {
				name = args[0]
			}
			if strings.TrimSpace(name) == "" {
				if err := cli.RequireTTY("removing a remote"); err != nil {
					return fmt.Errorf("usage: kuma remote remove <name>")
				}
				var err error
				name, err = cli.SelectRemote(rf.configPath)
				if err != nil {
					return err
				}
			}
			return remoteRemove(rf.configPath, name)
		},
	}
}

func remoteList(configPath string) error {
	f, err := connect.LoadRemotesFile(configPath)
	if err != nil {
		return err
	}
	return cli.PrintRemotes(os.Stdout, f)
}

func remoteRemove(configPath, name string) error {
	if err := connect.RemoveRemote(configPath, name); err != nil {
		return err
	}
	fmt.Printf("removed remote %q\n", name)
	return nil
}

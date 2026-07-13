package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/folsomintel/kuma/internal/cli"
	"github.com/folsomintel/kuma/internal/connect"
	"github.com/folsomintel/kuma/internal/daemon"
)

func newUpCmd(rf *rootFlags) *cobra.Command {
	var (
		force      bool
		authSecret string
	)
	cmd := &cobra.Command{
		Use:   "up",
		Short: "Start the local kuma daemon in the background",
		RunE: func(cmd *cobra.Command, args []string) error {
			secret := cli.AuthSecret(authSecret)

			st, err := daemon.ReadStatus(rf.configPath)
			if err != nil {
				return err
			}

			configPath := rf.configPath
			if configPath == "" {
				configPath, err = daemon.DefaultConfigPath()
				if err != nil {
					return err
				}
			}
			needInit := false
			if _, err := os.Stat(configPath); err != nil {
				if os.IsNotExist(err) {
					needInit = true
				} else {
					return err
				}
			}

			relayURL := rf.relayURL
			joinToken := rf.joinToken
			if needInit || force {
				if !cli.IsTTY() && joinToken == "" && secret == "" {
					return fmt.Errorf("no daemon config at %s; pass -T/--join-token or -S/--auth-secret (or run interactively)", configPath)
				}
				if cli.IsTTY() && (needInit || force) {
					relayURL, joinToken, secret, err = cli.PromptDaemonInit(relayURL, joinToken, secret)
					if err != nil {
						return err
					}
				}
				cfg, path, created, err := daemon.InitConfig(configPath, relayURL, joinToken, secret, force)
				if err != nil {
					return err
				}
				if created {
					fmt.Printf("wrote %s\n", path)
					cli.PrintKeys(cfg, "")
				}
			}

			cfg, err := daemon.LoadConfig(configPath, rf.machineID, rf.key, rf.relayURL, rf.joinToken)
			if err != nil {
				return err
			}
			if err := ensureLocalRemoteHint(cfg, secret); err != nil {
				return err
			}

			if st.Running {
				cli.PrintDaemonStatus(os.Stdout, true, st.PID, st.MachineID, st.RelayURL, st.PIDFile, st.LogFile, st.Config)
				return nil
			}

			daemonArgs := []string{"daemon"}
			if rf.configPath != "" {
				daemonArgs = append(daemonArgs, "-C", rf.configPath)
			}
			if rf.machineID != "" {
				daemonArgs = append(daemonArgs, "-M", rf.machineID)
			}
			if rf.key != "" {
				daemonArgs = append(daemonArgs, "-K", rf.key)
			}
			if rf.relayURL != "" {
				daemonArgs = append(daemonArgs, "-R", rf.relayURL)
			}
			if rf.joinToken != "" {
				daemonArgs = append(daemonArgs, "-T", rf.joinToken)
			}

			st, err = daemon.StartDetached(daemon.StartOptions{
				ConfigPath: rf.configPath,
				ExtraArgs:  daemonArgs,
			})
			if err != nil {
				return err
			}
			fmt.Println(cli.OkStyle.Render("daemon started"))
			cli.PrintDaemonStatus(os.Stdout, st.Running, st.PID, st.MachineID, st.RelayURL, st.PIDFile, st.LogFile, st.Config)
			return nil
		},
	}
	cmd.Flags().BoolVarP(&force, "force", "F", false, "overwrite existing daemon config")
	cmd.Flags().StringVarP(&authSecret, "auth-secret", "S", "", "relay auth secret to mint join tokens / register remote \"local\"")
	return cmd
}

func ensureLocalRemoteHint(cfg *daemon.Config, authSecret string) error {
	if authSecret != "" {
		path, wrote, err := cli.EnsureLocalRemote(cfg, authSecret, "")
		if err != nil {
			return err
		}
		if wrote {
			fmt.Printf("saved remote %q to %s\n", cli.LocalRemoteName, path)
		}
		return nil
	}
	f, err := connect.LoadRemotesFile("")
	if err != nil {
		return nil
	}
	if _, ok := f.Remotes[cli.LocalRemoteName]; !ok {
		fmt.Println("hint: pass -S/--auth-secret (or KUMA_RELAY_AUTH_SECRET) to register remote \"local\"")
		fmt.Println("      or run: kuma keys -S <auth-secret>")
	}
	return nil
}

func newDownCmd(rf *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "down",
		Short: "Stop the local kuma daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := daemon.Stop(rf.configPath); err != nil {
				return err
			}
			fmt.Println(cli.OkStyle.Render("daemon stopped"))
			return nil
		},
	}
}

func newStatusCmd(rf *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show local daemon status",
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := daemon.ReadStatus(rf.configPath)
			if err != nil {
				return err
			}
			cli.PrintDaemonStatus(os.Stdout, st.Running, st.PID, st.MachineID, st.RelayURL, st.PIDFile, st.LogFile, st.Config)
			return nil
		},
	}
}

func newDaemonCmd(rf *rootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:    "daemon",
		Short:  "Run the daemon in the foreground (used by kuma up)",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDaemonForeground(rf)
		},
	}
	return cmd
}

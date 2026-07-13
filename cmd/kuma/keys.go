package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/folsomintel/kuma/internal/cli"
	"github.com/folsomintel/kuma/internal/daemon"
	"github.com/folsomintel/kuma/internal/jointoken"
)

func newKeysCmd(rf *rootFlags) *cobra.Command {
	var (
		authSecret string
		forceInit  bool
	)
	cmd := &cobra.Command{
		Use:   "keys",
		Short: "Show local machine id and E2E key",
		Long: `Print credentials from the local daemon config.

With -S/--auth-secret (or KUMA_RELAY_AUTH_SECRET), also mint a client join
token and upsert the "local" remote in remotes.json so kuma run / agent /
session can dial this machine.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			secret := cli.AuthSecret(authSecret)
			configPath := rf.configPath
			if configPath == "" {
				var err error
				configPath, err = daemon.DefaultConfigPath()
				if err != nil {
					return err
				}
			}

			if _, err := os.Stat(configPath); err != nil {
				if !os.IsNotExist(err) {
					return err
				}
				relayURL := rf.relayURL
				joinToken := rf.joinToken
				if !cli.IsTTY() && joinToken == "" && secret == "" {
					return fmt.Errorf("no daemon config at %s; run: kuma up  (or kuma keys -F -S <auth-secret>)", configPath)
				}
				if cli.IsTTY() && (secret == "" && joinToken == "") {
					var err error
					relayURL, joinToken, secret, err = cli.PromptDaemonInit(relayURL, joinToken, secret)
					if err != nil {
						return err
					}
				}
				cfg, path, created, err := daemon.InitConfig(configPath, relayURL, joinToken, secret, forceInit)
				if err != nil {
					return err
				}
				if created {
					fmt.Printf("wrote %s\n", path)
				}
				return printKeysAndMaybeLocal(cfg, secret)
			}

			cfg, err := daemon.LoadConfig(configPath, rf.machineID, rf.key, rf.relayURL, rf.joinToken)
			if err != nil {
				return err
			}
			return printKeysAndMaybeLocal(cfg, secret)
		},
	}
	cmd.Flags().StringVarP(&authSecret, "auth-secret", "S", "", "relay auth secret to mint a client join token and save remote \"local\"")
	cmd.Flags().BoolVarP(&forceInit, "force", "F", false, "create/overwrite daemon config if missing")
	return cmd
}

func printKeysAndMaybeLocal(cfg *daemon.Config, authSecret string) error {
	clientTok := ""
	if authSecret != "" {
		var err error
		clientTok, err = jointoken.Mint(authSecret, cfg.MachineID, jointoken.RoleClient)
		if err != nil {
			return err
		}
	}
	cli.PrintKeys(cfg, clientTok)
	if authSecret == "" {
		fmt.Println("hint: pass -S/--auth-secret to mint a client join token and save remote \"local\"")
		return nil
	}
	path, wrote, err := cli.EnsureLocalRemote(cfg, authSecret, "")
	if err != nil {
		return err
	}
	if wrote {
		fmt.Printf("saved remote %q to %s\n", cli.LocalRemoteName, path)
	} else {
		fmt.Printf("remote %q already up to date in %s\n", cli.LocalRemoteName, path)
	}
	return nil
}

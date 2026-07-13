package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/folsomintel/kuma/internal/connect"
	"github.com/folsomintel/kuma/internal/protocol"
)

func main() {
	var (
		configPath = flag.String("config", "", "path to remotes.json (default: user config dir /kuma/remotes.json)")
		machineID  = flag.String("machine-id", "", "machine id (overrides remote/env)")
		key        = flag.String("key", "", "E2E AES-256 key, base64url (overrides remote/env)")
		relayURL   = flag.String("relay-url", "", "relay WebSocket base URL (overrides remote/env)")
		joinToken  = flag.String("join-token", "", "client join token (overrides remote/env)")
		agent      = flag.String("agent", "", "agent to start (claude, codex, ...)")
		cwd        = flag.String("cwd", "", "absolute working directory for start_session")
		sessionID  = flag.String("session", "", "existing session id for attach")
	)
	flag.Usage = usage
	flag.Parse()

	args := flag.Args()
	cmd := ""
	if len(args) > 0 {
		cmd = args[0]
		args = args[1:]
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var err error
	switch cmd {
	case "remotes":
		err = runRemotes(*configPath, args)
	case "agents":
		err = runAgents(ctx, *configPath, firstArg(args), *machineID, *key, *relayURL, *joinToken)
	case "sessions":
		err = runSessions(ctx, *configPath, firstArg(args), *machineID, *key, *relayURL, *joinToken)
	case "attach":
		err = runAttach(ctx, *configPath, firstArg(args), *machineID, *key, *relayURL, *joinToken, *sessionID)
	case "help", "-h", "--help":
		usage()
		return
	case "":
		err = runConnect(ctx, *configPath, "", *machineID, *key, *relayURL, *joinToken, *agent, *cwd)
	default:
		err = runConnect(ctx, *configPath, cmd, *machineID, *key, *relayURL, *joinToken, *agent, *cwd)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `kuma-connect — attach to a remote kumad over the encrypted relay

Usage:
  kuma-connect remotes add <name> -machine-id ... -key ... -relay-url ... -join-token ...
  kuma-connect remotes list
  kuma-connect remotes remove <name>
  kuma-connect agents [remote]
  kuma-connect sessions [remote]
  kuma-connect attach [remote] -session <id>
  kuma-connect [remote] -agent <name> [-cwd <path>]

Credentials resolve from flags, then env (KUMA_MACHINE_ID, KUMA_KEY, KUMA_RELAY_URL,
KUMA_JOIN_TOKEN), then the named remote in remotes.json (default: KUMA_CONNECT_REMOTE
or the file default).

`)
	flag.PrintDefaults()
}

func firstArg(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}

func runRemotes(configPath string, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: kuma-connect remotes <add|list|remove>")
	}
	switch args[0] {
	case "list":
		f, err := connect.LoadRemotesFile(configPath)
		if err != nil {
			return err
		}
		if len(f.Remotes) == 0 {
			fmt.Println("(no remotes)")
			return nil
		}
		names := make([]string, 0, len(f.Remotes))
		for name := range f.Remotes {
			names = append(names, name)
		}
		sort.Strings(names)
		tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		_, _ = fmt.Fprintln(tw, "NAME\tMACHINE_ID\tRELAY_URL\tDEFAULT")
		for _, name := range names {
			r := f.Remotes[name]
			def := ""
			if name == f.Default {
				def = "*"
			}
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", name, r.MachineID, r.RelayURL, def)
		}
		return tw.Flush()

	case "add":
		if len(args) < 2 {
			return fmt.Errorf("usage: kuma-connect remotes add <name> with -machine-id -key -relay-url -join-token")
		}
		name := args[1]
		fs := flag.NewFlagSet("remotes add", flag.ContinueOnError)
		machineID := fs.String("machine-id", "", "machine id")
		key := fs.String("key", "", "E2E key")
		relayURL := fs.String("relay-url", "", "relay URL")
		joinToken := fs.String("join-token", "", "client join token")
		makeDefault := fs.Bool("default", true, "mark as default remote")
		if err := fs.Parse(args[2:]); err != nil {
			return err
		}
		remote := connect.Remote{
			MachineID: firstNonEmpty(*machineID, os.Getenv("KUMA_MACHINE_ID")),
			Key:       firstNonEmpty(*key, os.Getenv("KUMA_KEY")),
			RelayURL:  firstNonEmpty(*relayURL, os.Getenv("KUMA_RELAY_URL")),
			JoinToken: firstNonEmpty(*joinToken, os.Getenv("KUMA_JOIN_TOKEN")),
		}
		if err := connect.AddRemote(configPath, name, remote, *makeDefault); err != nil {
			return err
		}
		path := configPath
		if path == "" {
			var err error
			path, err = connect.DefaultRemotesPath()
			if err != nil {
				return err
			}
		}
		fmt.Printf("saved remote %q to %s\n", name, path)
		return nil

	case "remove":
		if len(args) < 2 {
			return fmt.Errorf("usage: kuma-connect remotes remove <name>")
		}
		if err := connect.RemoveRemote(configPath, args[1]); err != nil {
			return err
		}
		fmt.Printf("removed remote %q\n", args[1])
		return nil

	default:
		return fmt.Errorf("unknown remotes command %q", args[0])
	}
}

func runAgents(ctx context.Context, configPath, remote, machineID, key, relayURL, joinToken string) error {
	client, err := dialClient(ctx, configPath, remote, machineID, key, relayURL, joinToken)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	list, err := waitReady(ctx, client)
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "NAME\tINSTALLED\tPATH")
	for _, a := range list.Agents {
		_, _ = fmt.Fprintf(tw, "%s\t%v\t%s\n", a.Name, a.Installed, a.Path)
	}
	return tw.Flush()
}

func runSessions(ctx context.Context, configPath, remote, machineID, key, relayURL, joinToken string) error {
	client, err := dialClient(ctx, configPath, remote, machineID, key, relayURL, joinToken)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()
	if _, err := waitReady(ctx, client); err != nil {
		return err
	}
	list, err := client.ListSessions(ctx)
	if err != nil {
		return err
	}
	if len(list.Sessions) == 0 {
		fmt.Println("(no sessions)")
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "SESSION_ID\tAGENT")
	for _, s := range list.Sessions {
		_, _ = fmt.Fprintf(tw, "%s\t%s\n", s.SessionID, s.Agent)
	}
	return tw.Flush()
}

func runAttach(ctx context.Context, configPath, remote, machineID, key, relayURL, joinToken, sessionID string) error {
	client, err := dialClient(ctx, configPath, remote, machineID, key, relayURL, joinToken)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()
	code, err := connect.AttachSession(ctx, client, sessionID)
	if err != nil {
		return err
	}
	if code != 0 {
		os.Exit(code)
	}
	return nil
}

func runConnect(ctx context.Context, configPath, remote, machineID, key, relayURL, joinToken, agent, cwd string) error {
	if strings.TrimSpace(agent) == "" {
		return fmt.Errorf("agent is required (pass -agent); see kuma-connect -h")
	}
	client, err := dialClient(ctx, configPath, remote, machineID, key, relayURL, joinToken)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()
	code, err := connect.RunSession(ctx, client, agent, cwd)
	if err != nil {
		return err
	}
	if code != 0 {
		os.Exit(code)
	}
	return nil
}

func dialClient(ctx context.Context, configPath, remote, machineID, key, relayURL, joinToken string) (*connect.Client, error) {
	cred, err := connect.ResolveCredentials(configPath, remote, machineID, key, relayURL, joinToken)
	if err != nil {
		return nil, err
	}
	return connect.Dial(ctx, cred)
}

func waitReady(ctx context.Context, client *connect.Client) (protocol.ListAgentsResult, error) {
	readyCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return client.WaitReady(readyCtx)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

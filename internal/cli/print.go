package cli

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/folsomintel/kuma/internal/connect"
	"github.com/folsomintel/kuma/internal/protocol"
)

// PrintRemotes writes a styled remotes table.
func PrintRemotes(w io.Writer, f *connect.RemotesFile) error {
	if f == nil || len(f.Remotes) == 0 {
		fmt.Fprintln(w, MutedStyle.Render("(no remotes)"))
		return nil
	}
	names := make([]string, 0, len(f.Remotes))
	for name := range f.Remotes {
		names = append(names, name)
	}
	sort.Strings(names)
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, TitleStyle.Render("NAME")+"\t"+TitleStyle.Render("MACHINE_ID")+"\t"+TitleStyle.Render("RELAY_URL")+"\t"+TitleStyle.Render("DEFAULT"))
	for _, name := range names {
		r := f.Remotes[name]
		def := ""
		if name == f.Default {
			def = "*"
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", name, r.MachineID, r.RelayURL, def)
	}
	return tw.Flush()
}

// PrintAgents writes a styled agents table.
func PrintAgents(w io.Writer, agents []protocol.AgentInfo) error {
	if len(agents) == 0 {
		fmt.Fprintln(w, MutedStyle.Render("(no agents)"))
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, TitleStyle.Render("NAME")+"\t"+TitleStyle.Render("INSTALLED")+"\t"+TitleStyle.Render("PATH"))
	for _, a := range agents {
		_, _ = fmt.Fprintf(tw, "%s\t%v\t%s\n", a.Name, a.Installed, a.Path)
	}
	return tw.Flush()
}

// PrintSessions writes a styled sessions table.
func PrintSessions(w io.Writer, sessions []protocol.SessionInfo) error {
	if len(sessions) == 0 {
		fmt.Fprintln(w, MutedStyle.Render("(no sessions)"))
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, TitleStyle.Render("SESSION_ID")+"\t"+TitleStyle.Render("AGENT"))
	for _, s := range sessions {
		_, _ = fmt.Fprintf(tw, "%s\t%s\n", s.SessionID, s.Agent)
	}
	return tw.Flush()
}

// PrintDaemonStatus writes daemon status fields.
func PrintDaemonStatus(w io.Writer, running bool, pid int, machineID, relayURL, pidFile, logFile, config string) {
	fmt.Fprintf(w, "%s %s\n", KeyStyle.Render("status"), StatusBadge(running))
	if pid > 0 {
		fmt.Fprintf(w, "%s %s\n", KeyStyle.Render("pid"), ValStyle.Render(fmt.Sprintf("%d", pid)))
	}
	if machineID != "" {
		fmt.Fprintf(w, "%s %s\n", KeyStyle.Render("machine_id"), ValStyle.Render(machineID))
	}
	if relayURL != "" {
		fmt.Fprintf(w, "%s %s\n", KeyStyle.Render("relay_url"), ValStyle.Render(relayURL))
	}
	if config != "" {
		fmt.Fprintf(w, "%s %s\n", KeyStyle.Render("config"), MutedStyle.Render(config))
	}
	if pidFile != "" {
		fmt.Fprintf(w, "%s %s\n", KeyStyle.Render("pid_file"), MutedStyle.Render(pidFile))
	}
	if logFile != "" {
		fmt.Fprintf(w, "%s %s\n", KeyStyle.Render("log_file"), MutedStyle.Render(logFile))
	}
}

// RemoteNames returns sorted remote names.
func RemoteNames(f *connect.RemotesFile) []string {
	if f == nil {
		return nil
	}
	names := make([]string, 0, len(f.Remotes))
	for name := range f.Remotes {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// InstalledAgentNames returns names of installed agents.
func InstalledAgentNames(agents []protocol.AgentInfo) []string {
	var out []string
	for _, a := range agents {
		if a.Installed {
			out = append(out, a.Name)
		}
	}
	return out
}

// SessionLabels returns "id (agent)" labels and a map back to session id.
func SessionLabels(sessions []protocol.SessionInfo) (labels []string, byLabel map[string]string) {
	byLabel = make(map[string]string, len(sessions))
	for _, s := range sessions {
		label := s.SessionID
		if s.Agent != "" {
			label = fmt.Sprintf("%s (%s)", s.SessionID, s.Agent)
		}
		labels = append(labels, label)
		byLabel[label] = s.SessionID
	}
	return labels, byLabel
}

// FirstNonEmpty returns the first non-empty trimmed string.
func FirstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

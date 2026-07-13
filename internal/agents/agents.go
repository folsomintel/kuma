package agents

import (
	"os/exec"
	"path/filepath"
)

// Agent describes a known coding agent binary.
type Agent struct {
	Name      string `json:"name"`
	Installed bool   `json:"installed"`
	Path      string `json:"path,omitempty"`
}

var allowlist = []string{
	"claude",
	"codex",
	"opencode",
	"cursor-agent",
}

// Detect returns the allowlisted agents and whether each is on PATH.
func Detect() []Agent {
	out := make([]Agent, 0, len(allowlist))
	for _, name := range allowlist {
		path, err := lookPathVerified(name)
		out = append(out, Agent{
			Name:      name,
			Installed: err == nil,
			Path:      path,
		})
	}
	return out
}

// Lookup returns the installed executable path for an allowlisted agent.
func Lookup(name string) (string, bool) {
	if !Allowed(name) {
		return "", false
	}
	path, err := lookPathVerified(name)
	if err != nil {
		return "", false
	}
	return path, true
}

// Allowed reports whether name is in the fixed allowlist.
func Allowed(name string) bool {
	for _, n := range allowlist {
		if n == name {
			return true
		}
	}
	return false
}

func lookPathVerified(name string) (string, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return "", err
	}
	if filepath.Base(path) != name {
		return "", errPathMismatch
	}
	return path, nil
}

type pathMismatchError struct{}

func (pathMismatchError) Error() string { return "agent path mismatch" }

var errPathMismatch = pathMismatchError{}

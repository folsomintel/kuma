package daemon

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	defaultPIDFileName = "daemon.pid"
	defaultLogFileName = "daemon.log"
)

// ProcessPaths holds on-disk locations for the background daemon.
type ProcessPaths struct {
	PIDFile string
	LogFile string
}

// DefaultProcessPaths returns paths next to the default daemon config.
func DefaultProcessPaths() (ProcessPaths, error) {
	cfgPath, err := DefaultConfigPath()
	if err != nil {
		return ProcessPaths{}, err
	}
	return ProcessPathsForConfig(cfgPath), nil
}

// ProcessPathsForConfig places pid/log next to the daemon config file.
func ProcessPathsForConfig(configPath string) ProcessPaths {
	dir := filepath.Dir(configPath)
	return ProcessPaths{
		PIDFile: filepath.Join(dir, defaultPIDFileName),
		LogFile: filepath.Join(dir, defaultLogFileName),
	}
}

func resolveProcessPaths(configPath string) (ProcessPaths, string, error) {
	if configPath == "" {
		var err error
		configPath, err = DefaultConfigPath()
		if err != nil {
			return ProcessPaths{}, "", err
		}
	}
	return ProcessPathsForConfig(configPath), configPath, nil
}

// Status describes the background daemon process.
type Status struct {
	Running   bool
	PID       int
	MachineID string
	RelayURL  string
	PIDFile   string
	LogFile   string
	Config    string
}

// ReadStatus inspects the PID file and optional config.
func ReadStatus(configPath string) (Status, error) {
	paths, configPath, err := resolveProcessPaths(configPath)
	if err != nil {
		return Status{}, err
	}
	st := Status{
		PIDFile: paths.PIDFile,
		LogFile: paths.LogFile,
		Config:  configPath,
	}
	if cfg, err := LoadConfig(configPath, "", "", "", ""); err == nil {
		st.MachineID = cfg.MachineID
		st.RelayURL = cfg.RelayURL
	}
	pid, err := readPID(paths.PIDFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return st, nil
		}
		return st, err
	}
	st.PID = pid
	if alive(pid) {
		st.Running = true
	}
	return st, nil
}

// StartOptions configures a detached daemon child.
type StartOptions struct {
	ConfigPath string
	ExtraArgs  []string // e.g. ["daemon"] plus flags; first arg should be "daemon"
}

// StartDetached re-execs the current binary as a background daemon.
func StartDetached(opts StartOptions) (Status, error) {
	paths, configPath, err := resolveProcessPaths(opts.ConfigPath)
	if err != nil {
		return Status{}, err
	}
	if st, err := ReadStatus(opts.ConfigPath); err == nil && st.Running {
		return st, fmt.Errorf("daemon already running (pid %d)", st.PID)
	}

	exe, err := os.Executable()
	if err != nil {
		return Status{}, fmt.Errorf("resolve executable: %w", err)
	}
	args := opts.ExtraArgs
	if len(args) == 0 {
		args = []string{"daemon"}
	}
	cmd := exec.Command(exe, args...)
	if err := os.MkdirAll(filepath.Dir(paths.LogFile), 0o700); err != nil {
		return Status{}, err
	}
	logFile, err := os.OpenFile(paths.LogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return Status{}, fmt.Errorf("open log: %w", err)
	}
	defer func() { _ = logFile.Close() }()
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		return Status{}, fmt.Errorf("start daemon: %w", err)
	}
	pid := cmd.Process.Pid
	_ = cmd.Process.Release()

	if err := writePID(paths.PIDFile, pid); err != nil {
		_ = killPID(pid, syscall.SIGTERM)
		return Status{}, err
	}

	// Brief settle so a fast crash is visible.
	time.Sleep(150 * time.Millisecond)
	if !alive(pid) {
		_ = os.Remove(paths.PIDFile)
		return Status{}, fmt.Errorf("daemon exited immediately; check %s", paths.LogFile)
	}

	st, err := ReadStatus(opts.ConfigPath)
	if err != nil {
		return Status{Running: true, PID: pid, PIDFile: paths.PIDFile, LogFile: paths.LogFile, Config: configPath}, nil
	}
	return st, nil
}

// Stop sends SIGTERM to the daemon and removes the PID file.
func Stop(configPath string) error {
	paths, _, err := resolveProcessPaths(configPath)
	if err != nil {
		return err
	}
	pid, err := readPID(paths.PIDFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("daemon is not running")
		}
		return err
	}
	if !alive(pid) {
		_ = os.Remove(paths.PIDFile)
		return fmt.Errorf("daemon is not running (stale pid %d)", pid)
	}
	if err := killPID(pid, syscall.SIGTERM); err != nil {
		return err
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if !alive(pid) {
			_ = os.Remove(paths.PIDFile)
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = killPID(pid, syscall.SIGKILL)
	_ = os.Remove(paths.PIDFile)
	return nil
}

func readPID(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return 0, fmt.Errorf("invalid pid file %s", path)
	}
	return pid, nil
}

func writePID(path string, pid int) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(strconv.Itoa(pid)+"\n"), 0o600)
}

func alive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = p.Signal(syscall.Signal(0))
	return err == nil
}

func killPID(pid int, sig syscall.Signal) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Signal(sig)
}

package plist

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"howett.net/plist"
)

const (
	DaemonLabel = "com.skiff.daemon"
	MenuLabel   = "com.skiff.menu"
	plistDir    = "Library/LaunchAgents"
)

// Keep backward compat
const Label = DaemonLabel

// LaunchAgent represents a launchd plist for a skiff component.
type LaunchAgent struct {
	Label                string            `plist:"Label"`
	ProgramArguments     []string          `plist:"ProgramArguments"`
	WorkingDirectory     string            `plist:"WorkingDirectory,omitempty"`
	EnvironmentVariables map[string]string `plist:"EnvironmentVariables,omitempty"`
	StandardOutPath      string            `plist:"StandardOutPath"`
	StandardErrorPath    string            `plist:"StandardErrorPath"`
	KeepAlive            bool              `plist:"KeepAlive"`
	RunAtLoad            bool              `plist:"RunAtLoad"`
	ThrottleInterval     int               `plist:"ThrottleInterval,omitempty"`
}

// DaemonPlist is an alias for backward compatibility.
type DaemonPlist = LaunchAgent

// AgentPath returns the full path to a plist file for the given label.
func AgentPath(label string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("getting home dir: %w", err)
	}
	return filepath.Join(home, plistDir, label+".plist"), nil
}

// PlistPath returns the full path to the daemon plist file.
func PlistPath() (string, error) {
	return AgentPath(DaemonLabel)
}

// MenuPlistPath returns the full path to the menu bar plist file.
func MenuPlistPath() (string, error) {
	return AgentPath(MenuLabel)
}

// defaultPath returns a PATH that includes common binary locations.
func defaultPath() string {
	return "/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"
}

// Generate creates a launchd plist for the skiff daemon.
func Generate(binaryPath, configPath, logsDir string) (*LaunchAgent, error) {
	return &LaunchAgent{
		Label:            DaemonLabel,
		ProgramArguments: []string{binaryPath, "daemon", "--config", configPath},
		WorkingDirectory: filepath.Dir(configPath),
		EnvironmentVariables: map[string]string{
			"PATH": defaultPath(),
		},
		StandardOutPath:   filepath.Join(logsDir, "skiff-daemon.log"),
		StandardErrorPath: filepath.Join(logsDir, "skiff-daemon.err"),
		KeepAlive:         true,
		RunAtLoad:         true,
		ThrottleInterval:  10,
	}, nil
}

// GenerateMenu creates a launchd plist for the skiff menu bar app.
func GenerateMenu(menuBinaryPath, socketPath, logsDir string) (*LaunchAgent, error) {
	return &LaunchAgent{
		Label:            MenuLabel,
		ProgramArguments: []string{menuBinaryPath},
		EnvironmentVariables: map[string]string{
			"SKIFF_SOCKET": socketPath,
		},
		StandardOutPath:   filepath.Join(logsDir, "skiff-menu.log"),
		StandardErrorPath: filepath.Join(logsDir, "skiff-menu.err"),
		KeepAlive:         true,
		RunAtLoad:         true,
	}, nil
}

// InstallAgent writes a plist file and loads it with launchctl.
func InstallAgent(agent *LaunchAgent) error {
	path, err := AgentPath(agent.Label)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("creating launch agents dir: %w", err)
	}

	data, err := plist.MarshalIndent(agent, plist.XMLFormat, "\t")
	if err != nil {
		return fmt.Errorf("marshaling plist: %w", err)
	}

	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("writing plist: %w", err)
	}

	// Load with launchctl
	if out, err := exec.Command("launchctl", "load", path).CombinedOutput(); err != nil {
		return fmt.Errorf("launchctl load %s: %w: %s", path, err, string(out))
	}

	return nil
}

// UnloadAgent unloads and removes a plist by label.
func UnloadAgent(label string) error {
	path, err := AgentPath(label)
	if err != nil {
		return err
	}

	if _, statErr := os.Stat(path); statErr != nil {
		return nil // not installed
	}

	// Unload from launchctl (ignore errors if not loaded)
	exec.Command("launchctl", "unload", path).Run()

	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing plist %s: %w", path, err)
	}
	return nil
}

// Install writes the plist file to ~/Library/LaunchAgents/.
func Install(p *LaunchAgent) error {
	return InstallAgent(p)
}

// Uninstall removes the daemon plist file.
func Uninstall() error {
	return UnloadAgent(DaemonLabel)
}

// Exists checks if the daemon plist is installed.
func Exists() bool {
	path, err := AgentPath(DaemonLabel)
	if err != nil {
		return false
	}
	_, err = os.Stat(path)
	return err == nil
}

// MenuExists checks if the menu bar plist is installed.
func MenuExists() bool {
	path, err := AgentPath(MenuLabel)
	if err != nil {
		return false
	}
	_, err = os.Stat(path)
	return err == nil
}

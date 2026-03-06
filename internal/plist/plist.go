package plist

import (
	"fmt"
	"os"
	"path/filepath"

	"howett.net/plist"
)

const (
	Label    = "com.plane.daemon"
	plistDir = "Library/LaunchAgents"
)

// DaemonPlist represents the launchd plist for the plane daemon.
type DaemonPlist struct {
	Label                string            `plist:"Label"`
	ProgramArguments     []string          `plist:"ProgramArguments"`
	WorkingDirectory     string            `plist:"WorkingDirectory"`
	EnvironmentVariables map[string]string `plist:"EnvironmentVariables,omitempty"`
	StandardOutPath      string            `plist:"StandardOutPath"`
	StandardErrorPath    string            `plist:"StandardErrorPath"`
	KeepAlive            bool              `plist:"KeepAlive"`
	RunAtLoad            bool              `plist:"RunAtLoad"`
	ThrottleInterval     int               `plist:"ThrottleInterval"`
}

// PlistPath returns the full path to the daemon plist file.
func PlistPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("getting home dir: %w", err)
	}
	return filepath.Join(home, plistDir, Label+".plist"), nil
}

// Generate creates a launchd plist for the plane daemon.
func Generate(binaryPath, configPath, logsDir string) (*DaemonPlist, error) {
	return &DaemonPlist{
		Label:            Label,
		ProgramArguments: []string{binaryPath, "daemon", "--config", configPath},
		WorkingDirectory: filepath.Dir(configPath),
		StandardOutPath:  filepath.Join(logsDir, "plane-daemon.log"),
		StandardErrorPath: filepath.Join(logsDir, "plane-daemon.err"),
		KeepAlive:        true,
		RunAtLoad:        true,
		ThrottleInterval: 10,
	}, nil
}

// Install writes the plist file to ~/Library/LaunchAgents/.
func Install(p *DaemonPlist) error {
	path, err := PlistPath()
	if err != nil {
		return err
	}

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("creating launch agents dir: %w", err)
	}

	data, err := plist.MarshalIndent(p, plist.XMLFormat, "\t")
	if err != nil {
		return fmt.Errorf("marshaling plist: %w", err)
	}

	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("writing plist: %w", err)
	}

	return nil
}

// Uninstall removes the daemon plist file.
func Uninstall() error {
	path, err := PlistPath()
	if err != nil {
		return err
	}

	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing plist: %w", err)
	}
	return nil
}

// Exists checks if the daemon plist is installed.
func Exists() bool {
	path, err := PlistPath()
	if err != nil {
		return false
	}
	_, err = os.Stat(path)
	return err == nil
}

package systemd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	DaemonLabel = "skiff"
	unitDir     = ".config/systemd/user"
)

// ServiceUnit holds parameters for a systemd user service.
type ServiceUnit struct {
	Label       string
	Description string
	ExecStart   string
	WorkingDir  string
	LogFile     string
	ErrFile     string
	EnvVars     map[string]string
}

// unitPath returns the path to the unit file for the given label.
func unitPath(label string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("getting home dir: %w", err)
	}
	return filepath.Join(home, unitDir, label+".service"), nil
}

// UnitPath returns the path to the daemon unit file.
func UnitPath() (string, error) {
	return unitPath(DaemonLabel)
}

// Generate creates a ServiceUnit for the skiff daemon.
func Generate(binaryPath, configPath, logsDir string) (*ServiceUnit, error) {
	return &ServiceUnit{
		Label:       DaemonLabel,
		Description: "skiff container orchestration daemon",
		ExecStart:   fmt.Sprintf("%s daemon --config %s", binaryPath, configPath),
		WorkingDir:  filepath.Dir(configPath),
		LogFile:     filepath.Join(logsDir, "skiff-daemon.log"),
		ErrFile:     filepath.Join(logsDir, "skiff-daemon.err"),
	}, nil
}

func (u *ServiceUnit) unitContent() string {
	var b strings.Builder
	b.WriteString("[Unit]\n")
	b.WriteString(fmt.Sprintf("Description=%s\n", u.Description))
	b.WriteString("After=network.target\n\n")

	b.WriteString("[Service]\n")
	b.WriteString("Type=simple\n")
	b.WriteString(fmt.Sprintf("ExecStart=%s\n", u.ExecStart))
	if u.WorkingDir != "" {
		b.WriteString(fmt.Sprintf("WorkingDirectory=%s\n", u.WorkingDir))
	}
	for k, v := range u.EnvVars {
		b.WriteString(fmt.Sprintf("Environment=%s=%s\n", k, v))
	}
	b.WriteString("Restart=always\n")
	b.WriteString("RestartSec=10\n")
	if u.LogFile != "" {
		b.WriteString(fmt.Sprintf("StandardOutput=append:%s\n", u.LogFile))
	}
	if u.ErrFile != "" {
		b.WriteString(fmt.Sprintf("StandardError=append:%s\n", u.ErrFile))
	}
	b.WriteString("\n[Install]\n")
	b.WriteString("WantedBy=default.target\n")
	return b.String()
}

// InstallAgent writes the unit file and enables + starts it via systemctl.
func InstallAgent(u *ServiceUnit) error {
	path, err := unitPath(u.Label)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("creating systemd user dir: %w", err)
	}

	if err := os.WriteFile(path, []byte(u.unitContent()), 0600); err != nil {
		return fmt.Errorf("writing unit file: %w", err)
	}

	if out, err := exec.Command("systemctl", "--user", "daemon-reload").CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl daemon-reload: %w: %s", err, out)
	}

	if out, err := exec.Command("systemctl", "--user", "enable", "--now", u.Label).CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl enable %s: %w: %s", u.Label, err, out)
	}

	return nil
}

// UnloadAgent disables and removes the unit for the given label.
func UnloadAgent(label string) error {
	path, err := unitPath(label)
	if err != nil {
		return err
	}

	if _, statErr := os.Stat(path); statErr != nil {
		return nil // not installed
	}

	exec.Command("systemctl", "--user", "disable", "--now", label).Run()

	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing unit file %s: %w", path, err)
	}

	exec.Command("systemctl", "--user", "daemon-reload").Run()
	return nil
}

// Exists reports whether the daemon unit file is installed.
func Exists() bool {
	path, err := unitPath(DaemonLabel)
	if err != nil {
		return false
	}
	_, err = os.Stat(path)
	return err == nil
}

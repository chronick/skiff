package plist

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLabel(t *testing.T) {
	if Label != "com.skiff.daemon" {
		t.Errorf("expected label 'com.skiff.daemon', got %q", Label)
	}
}

func TestGenerate(t *testing.T) {
	p, err := Generate("/usr/local/bin/skiff", "/etc/skiff.yml", "/var/log/skiff")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if p.Label != Label {
		t.Errorf("expected label %q, got %q", Label, p.Label)
	}
	if len(p.ProgramArguments) != 4 {
		t.Errorf("expected 4 program arguments, got %d", len(p.ProgramArguments))
	}
	if p.ProgramArguments[0] != "/usr/local/bin/skiff" {
		t.Errorf("expected binary path, got %q", p.ProgramArguments[0])
	}
	if p.ProgramArguments[1] != "daemon" {
		t.Errorf("expected 'daemon' arg, got %q", p.ProgramArguments[1])
	}
	if p.ProgramArguments[2] != "--config" {
		t.Errorf("expected '--config' arg, got %q", p.ProgramArguments[2])
	}
	if p.ProgramArguments[3] != "/etc/skiff.yml" {
		t.Errorf("expected config path, got %q", p.ProgramArguments[3])
	}
	if !p.KeepAlive {
		t.Error("expected KeepAlive to be true")
	}
	if !p.RunAtLoad {
		t.Error("expected RunAtLoad to be true")
	}
	if p.ThrottleInterval != 10 {
		t.Errorf("expected ThrottleInterval 10, got %d", p.ThrottleInterval)
	}
}

func TestGenerate_LogPaths(t *testing.T) {
	p, _ := Generate("/bin/skiff", "/cfg/skiff.yml", "/var/log/skiff")

	if !strings.HasPrefix(p.StandardOutPath, "/var/log/skiff/") {
		t.Errorf("expected stdout path under logs dir, got %q", p.StandardOutPath)
	}
	if !strings.HasPrefix(p.StandardErrorPath, "/var/log/skiff/") {
		t.Errorf("expected stderr path under logs dir, got %q", p.StandardErrorPath)
	}
}

func TestGenerate_WorkingDirectory(t *testing.T) {
	p, _ := Generate("/bin/skiff", "/home/user/project/skiff.yml", "/var/log")

	if p.WorkingDirectory != "/home/user/project" {
		t.Errorf("expected working dir to be config parent, got %q", p.WorkingDirectory)
	}
}

func TestAgentPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	path, err := AgentPath("com.test.agent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := filepath.Join(home, "Library/LaunchAgents/com.test.agent.plist")
	if path != expected {
		t.Errorf("expected %q, got %q", expected, path)
	}
}

func TestPlistPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	path, err := PlistPath()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.HasSuffix(path, "com.skiff.daemon.plist") {
		t.Errorf("expected path ending in com.skiff.daemon.plist, got %q", path)
	}
}

func TestMenuPlistPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	path, err := MenuPlistPath()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.HasSuffix(path, "com.skiff.menu.plist") {
		t.Errorf("expected path ending in com.skiff.menu.plist, got %q", path)
	}
}

func TestExists_NotInstalled(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if Exists() {
		t.Error("expected Exists()=false when plist not installed")
	}
}

func TestExists_Installed(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Create the plist file manually
	plistDir := filepath.Join(home, "Library/LaunchAgents")
	os.MkdirAll(plistDir, 0755)
	os.WriteFile(filepath.Join(plistDir, "com.skiff.daemon.plist"), []byte("<plist/>"), 0600)

	if !Exists() {
		t.Error("expected Exists()=true when plist is installed")
	}
}

func TestMenuExists_NotInstalled(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if MenuExists() {
		t.Error("expected MenuExists()=false when menu plist not installed")
	}
}

func TestGenerateMenu(t *testing.T) {
	p, err := GenerateMenu("/usr/local/bin/skiff-menu", "/tmp/skiff.sock", "/var/log/skiff")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if p.Label != MenuLabel {
		t.Errorf("expected label %q, got %q", MenuLabel, p.Label)
	}
	if len(p.ProgramArguments) != 1 || p.ProgramArguments[0] != "/usr/local/bin/skiff-menu" {
		t.Errorf("unexpected program arguments: %v", p.ProgramArguments)
	}
	if p.EnvironmentVariables["SKIFF_SOCKET"] != "/tmp/skiff.sock" {
		t.Errorf("expected SKIFF_SOCKET set, got %v", p.EnvironmentVariables)
	}
	if !p.KeepAlive {
		t.Error("expected KeepAlive to be true")
	}
}

func TestUnloadAgent_NotInstalled(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Should be a no-op when plist doesn't exist
	err := UnloadAgent("com.test.nonexistent")
	if err != nil {
		t.Errorf("expected nil error for uninstalling non-existent plist, got %v", err)
	}
}

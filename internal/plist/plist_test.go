package plist

import (
	"strings"
	"testing"
)

func TestLabel(t *testing.T) {
	if Label != "com.plane.daemon" {
		t.Errorf("expected label 'com.plane.daemon', got %q", Label)
	}
}

func TestGenerate(t *testing.T) {
	p, err := Generate("/usr/local/bin/plane", "/etc/plane.yml", "/var/log/plane")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if p.Label != Label {
		t.Errorf("expected label %q, got %q", Label, p.Label)
	}
	if len(p.ProgramArguments) != 4 {
		t.Errorf("expected 4 program arguments, got %d", len(p.ProgramArguments))
	}
	if p.ProgramArguments[0] != "/usr/local/bin/plane" {
		t.Errorf("expected binary path, got %q", p.ProgramArguments[0])
	}
	if p.ProgramArguments[1] != "daemon" {
		t.Errorf("expected 'daemon' arg, got %q", p.ProgramArguments[1])
	}
	if p.ProgramArguments[2] != "--config" {
		t.Errorf("expected '--config' arg, got %q", p.ProgramArguments[2])
	}
	if p.ProgramArguments[3] != "/etc/plane.yml" {
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
	p, _ := Generate("/bin/plane", "/cfg/plane.yml", "/var/log/plane")

	if !strings.HasPrefix(p.StandardOutPath, "/var/log/plane/") {
		t.Errorf("expected stdout path under logs dir, got %q", p.StandardOutPath)
	}
	if !strings.HasPrefix(p.StandardErrorPath, "/var/log/plane/") {
		t.Errorf("expected stderr path under logs dir, got %q", p.StandardErrorPath)
	}
}

func TestGenerate_WorkingDirectory(t *testing.T) {
	p, _ := Generate("/bin/plane", "/home/user/project/plane.yml", "/var/log")

	if p.WorkingDirectory != "/home/user/project" {
		t.Errorf("expected working dir to be config parent, got %q", p.WorkingDirectory)
	}
}

package plist

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"howett.net/plist"
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

// --- helpers ---------------------------------------------------------------

type launchctlCall struct {
	args []string
}

// stubLaunchctl replaces the package launchctl seam for the duration of a test
// and returns a pointer to the recorded calls. launchctl only exists on macOS,
// so every test that exercises Install/Unload must stub it.
func stubLaunchctl(t *testing.T, err error) *[]launchctlCall {
	t.Helper()
	orig := launchctl
	calls := &[]launchctlCall{}
	launchctl = func(args ...string) ([]byte, error) {
		*calls = append(*calls, launchctlCall{args: args})
		return []byte("stub output"), err
	}
	t.Cleanup(func() { launchctl = orig })
	return calls
}

func testAgent() *LaunchAgent {
	p, _ := Generate("/usr/local/bin/skiff", "/cfg/skiff.yml", "/var/log/skiff")
	return p
}

// --- InstallAgent ----------------------------------------------------------

func TestInstallAgent_WritesPlistAndLoads(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	calls := stubLaunchctl(t, nil)

	agent := testAgent()
	if err := InstallAgent(agent); err != nil {
		t.Fatalf("InstallAgent: %v", err)
	}

	path := filepath.Join(home, "Library/LaunchAgents", DaemonLabel+".plist")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("expected plist at %s: %v", path, err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("expected plist mode 0600, got %o", perm)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading plist: %v", err)
	}
	var got LaunchAgent
	if _, err := plist.Unmarshal(data, &got); err != nil {
		t.Fatalf("plist is not valid XML plist: %v", err)
	}
	if got.Label != DaemonLabel {
		t.Errorf("expected label %q in written plist, got %q", DaemonLabel, got.Label)
	}
	if len(got.ProgramArguments) != 4 || got.ProgramArguments[0] != "/usr/local/bin/skiff" {
		t.Errorf("unexpected ProgramArguments round-trip: %v", got.ProgramArguments)
	}
	if !got.RunAtLoad || !got.KeepAlive {
		t.Errorf("expected RunAtLoad and KeepAlive true, got %+v", got)
	}

	if len(*calls) != 1 {
		t.Fatalf("expected 1 launchctl call, got %d: %v", len(*calls), *calls)
	}
	want := []string{"load", path}
	if strings.Join((*calls)[0].args, " ") != strings.Join(want, " ") {
		t.Errorf("expected launchctl args %v, got %v", want, (*calls)[0].args)
	}
}

func TestInstallAgent_LaunchctlError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	stubLaunchctl(t, errors.New("boom"))

	err := InstallAgent(testAgent())
	if err == nil {
		t.Fatal("expected error when launchctl load fails")
	}
	if !strings.Contains(err.Error(), "launchctl load") {
		t.Errorf("expected launchctl load error, got %v", err)
	}

	// The plist is still written before the load attempt.
	path := filepath.Join(home, "Library/LaunchAgents", DaemonLabel+".plist")
	if _, statErr := os.Stat(path); statErr != nil {
		t.Errorf("expected plist to be written even when load fails: %v", statErr)
	}
}

func TestInstallAgent_MkdirFails(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	stubLaunchctl(t, nil)

	// Occupy the Library path with a regular file so MkdirAll cannot succeed.
	if err := os.WriteFile(filepath.Join(home, "Library"), []byte("not a dir"), 0600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	err := InstallAgent(testAgent())
	if err == nil {
		t.Fatal("expected error when launch agents dir cannot be created")
	}
	if !strings.Contains(err.Error(), "creating launch agents dir") {
		t.Errorf("expected mkdir error, got %v", err)
	}
}

func TestInstallAgent_HomeUnset(t *testing.T) {
	t.Setenv("HOME", "")
	stubLaunchctl(t, nil)

	if err := InstallAgent(testAgent()); err == nil {
		t.Fatal("expected error when home dir cannot be resolved")
	}
}

// --- Install / Uninstall roundtrip ----------------------------------------

func TestInstall_Uninstall_Roundtrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	calls := stubLaunchctl(t, nil)

	if err := Install(testAgent()); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if !Exists() {
		t.Fatal("expected Exists()=true after Install")
	}

	if err := Uninstall(); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if Exists() {
		t.Error("expected Exists()=false after Uninstall")
	}

	if len(*calls) != 2 {
		t.Fatalf("expected load then unload, got %v", *calls)
	}
	if (*calls)[0].args[0] != "load" || (*calls)[1].args[0] != "unload" {
		t.Errorf("expected [load, unload] verbs, got %v", *calls)
	}
}

func TestInstallMenu_MenuExists(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	stubLaunchctl(t, nil)

	menu, err := GenerateMenu("/usr/local/bin/skiff-menu", "/tmp/skiff.sock", "/var/log/skiff")
	if err != nil {
		t.Fatalf("GenerateMenu: %v", err)
	}
	if err := InstallAgent(menu); err != nil {
		t.Fatalf("InstallAgent(menu): %v", err)
	}

	if !MenuExists() {
		t.Error("expected MenuExists()=true after installing the menu agent")
	}
	if Exists() {
		t.Error("installing the menu agent must not create the daemon plist")
	}
}

// --- UnloadAgent -----------------------------------------------------------

func TestUnloadAgent_Installed(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	calls := stubLaunchctl(t, nil)

	dir := filepath.Join(home, "Library/LaunchAgents")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	path := filepath.Join(dir, "com.test.agent.plist")
	if err := os.WriteFile(path, []byte("<plist/>"), 0600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	if err := UnloadAgent("com.test.agent"); err != nil {
		t.Fatalf("UnloadAgent: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("expected plist removed, stat err = %v", err)
	}
	if len(*calls) != 1 || (*calls)[0].args[0] != "unload" {
		t.Errorf("expected one unload call, got %v", *calls)
	}
}

func TestUnloadAgent_LaunchctlErrorIgnored(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	stubLaunchctl(t, errors.New("not loaded"))

	dir := filepath.Join(home, "Library/LaunchAgents")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	path := filepath.Join(dir, "com.test.agent.plist")
	if err := os.WriteFile(path, []byte("<plist/>"), 0600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	if err := UnloadAgent("com.test.agent"); err != nil {
		t.Errorf("expected launchctl unload failure to be ignored, got %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("expected plist removed despite unload failure, stat err = %v", err)
	}
}

func TestUnloadAgent_HomeUnset(t *testing.T) {
	t.Setenv("HOME", "")
	stubLaunchctl(t, nil)

	if err := UnloadAgent("com.test.agent"); err == nil {
		t.Fatal("expected error when home dir cannot be resolved")
	}
}

// --- path/existence error branches ----------------------------------------

func TestAgentPath_HomeUnset(t *testing.T) {
	t.Setenv("HOME", "")

	if _, err := AgentPath("com.test.agent"); err == nil {
		t.Fatal("expected error when home dir cannot be resolved")
	}
}

func TestExists_HomeUnset(t *testing.T) {
	t.Setenv("HOME", "")

	if Exists() {
		t.Error("expected Exists()=false when home dir cannot be resolved")
	}
	if MenuExists() {
		t.Error("expected MenuExists()=false when home dir cannot be resolved")
	}
}

func TestDefaultPath(t *testing.T) {
	p := defaultPath()
	for _, want := range []string{"/opt/homebrew/bin", "/usr/local/bin", "/usr/bin", "/bin"} {
		if !strings.Contains(p, want) {
			t.Errorf("expected default PATH to contain %q, got %q", want, p)
		}
	}
}

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"text/tabwriter"

	"github.com/fatih/color"
	"github.com/spf13/cobra"

	"github.com/chronick/skiff/internal/config"
	"github.com/chronick/skiff/internal/daemon"
	"github.com/chronick/skiff/internal/plist"
	"github.com/chronick/skiff/internal/tui"
)

var (
	configPath string
	bold       = color.New(color.Bold)
	green      = color.New(color.FgGreen)
	red        = color.New(color.FgRed)
	yellow     = color.New(color.FgYellow)
)

func main() {
	root := &cobra.Command{
		Use:   "skiff",
		Short: "Container orchestration for macOS",
		Long:  "skiff is a lightweight container orchestration layer for macOS with health-aware lifecycle management, scheduling, and service discovery.",
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			if configPath == "" {
				configPath = findConfig()
			}
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.PersistentFlags().StringVarP(&configPath, "config", "c", "", "path to config file (default: ./skiff.yml, ~/.config/skiff/config.yml)")

	root.AddCommand(
		daemonCmd(),
		upCmd(),
		downCmd(),
		stopCmd(),
		killCmd(),
		psCmd(),
		statusCmd(),
		statsCmd(),
		applyCmd(),
		restartCmd(),
		buildCmd(),
		runCmd(),
		execCmd(),
		logsCmd(),
		runNowCmd(),
		installCmd(),
		uninstallCmd(),
		configCmd(),
		initCmd(),
		tuiCmd(),
	)

	if err := root.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		os.Exit(1)
	}
}

func daemonCmd() *cobra.Command {
	var daemonize bool
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Start the skiff daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(configPath)
			if err != nil {
				return err
			}

			logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

			if daemonize {
				return daemonizeProcess(cfg)
			}

			d := daemon.New(cfg, logger)
			return d.Run(context.Background())
		},
	}
	cmd.Flags().BoolVarP(&daemonize, "daemonize", "d", false, "run in background")
	return cmd
}

func upCmd() *cobra.Command {
	var build bool
	cmd := &cobra.Command{
		Use:   "up [name...]",
		Short: "Start services and containers",
		RunE: func(cmd *cobra.Command, args []string) error {
			ensureDaemon()

			if build && len(args) > 0 {
				body := map[string]interface{}{"names": args}
				if _, err := apiCall("POST", "/v1/build", body); err != nil {
					fmt.Fprintf(os.Stderr, "Warning: build failed: %s\n", err)
				}
			} else if build {
				if _, err := apiCall("POST", "/v1/build", nil); err != nil {
					fmt.Fprintf(os.Stderr, "Warning: build failed: %s\n", err)
				}
			}

			body := map[string]interface{}{}
			if len(args) > 0 {
				body["names"] = args
			}

			resp, err := apiCall("POST", "/v1/up", body)
			if err != nil {
				return err
			}

			var result struct {
				Started []string          `json:"started"`
				Errors  map[string]string `json:"errors"`
			}
			if err := json.Unmarshal(resp, &result); err != nil {
				return err
			}

			for _, name := range result.Started {
				green.Printf("  Started %s\n", name)
			}
			for name, errMsg := range result.Errors {
				red.Printf("  Failed %s: %s\n", name, errMsg)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&build, "build", false, "build images before starting")
	return cmd
}

func downCmd() *cobra.Command {
	var volumes bool
	cmd := &cobra.Command{
		Use:   "down [name...]",
		Short: "Stop and remove containers, stop services",
		RunE: func(cmd *cobra.Command, args []string) error {
			body := map[string]interface{}{"volumes": volumes}
			if len(args) > 0 {
				body["names"] = args
			}
			resp, err := apiCall("POST", "/v1/down", body)
			if err != nil {
				return err
			}

			var result struct {
				Stopped []string          `json:"stopped"`
				Errors  map[string]string `json:"errors"`
			}
			if err := json.Unmarshal(resp, &result); err != nil {
				return err
			}
			for _, name := range result.Stopped {
				green.Printf("  Stopped %s\n", name)
			}
			for name, errMsg := range result.Errors {
				red.Printf("  Failed %s: %s\n", name, errMsg)
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&volumes, "volumes", "v", false, "also remove volumes")
	return cmd
}

func stopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop [name...]",
		Short: "Graceful stop (SIGTERM)",
		RunE: func(cmd *cobra.Command, args []string) error {
			body := map[string]interface{}{}
			if len(args) > 0 {
				body["names"] = args
			}
			resp, err := apiCall("POST", "/v1/down", body)
			if err != nil {
				return err
			}

			var result struct {
				Stopped []string `json:"stopped"`
			}
			_ = json.Unmarshal(resp, &result)
			for _, name := range result.Stopped {
				green.Printf("  Stopped %s\n", name)
			}
			return nil
		},
	}
}

func killCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "kill [name...]",
		Short: "Force stop (SIGKILL)",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Kill is handled the same as down for now
			body := map[string]interface{}{}
			if len(args) > 0 {
				body["names"] = args
			}
			_, err := apiCall("POST", "/v1/down", body)
			return err
		},
	}
}

func psCmd() *cobra.Command {
	var jsonOutput bool
	var showStats bool
	cmd := &cobra.Command{
		Use:   "ps",
		Short: "Show status of all resources",
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := apiCall("GET", "/v1/status", nil)
			if err != nil {
				return err
			}

			if jsonOutput {
				fmt.Println(string(resp))
				return nil
			}

			return printStatusTable(resp, showStats)
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "output as JSON")
	cmd.Flags().BoolVar(&showStats, "stats", false, "show CPU/memory stats for containers")
	return cmd
}

func statusCmd() *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show status (alias for ps)",
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := apiCall("GET", "/v1/status", nil)
			if err != nil {
				return err
			}
			if jsonOutput {
				fmt.Println(string(resp))
				return nil
			}
			return printStatusTable(resp, false)
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "output as JSON")
	return cmd
}

func statsCmd() *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "stats [name]",
		Short: "Show container CPU/memory stats",
		RunE: func(cmd *cobra.Command, args []string) error {
			var path string
			if len(args) > 0 {
				path = "/v1/stats/" + args[0]
			} else {
				path = "/v1/stats"
			}

			resp, err := apiCall("GET", path, nil)
			if err != nil {
				return err
			}

			if jsonOutput {
				fmt.Println(string(resp))
				return nil
			}

			if len(args) > 0 {
				// Single container stats
				var s struct {
					CPUPercent float64 `json:"cpu_percent"`
					MemUsageMB int64   `json:"mem_usage_mb"`
					MemLimitMB int64   `json:"mem_limit_mb"`
					PIDs       int     `json:"pids"`
					Status     string  `json:"status,omitempty"`
				}
				if err := json.Unmarshal(resp, &s); err != nil {
					fmt.Println(string(resp))
					return nil
				}
				if s.Status != "" {
					fmt.Println(s.Status)
					return nil
				}
				tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
				fmt.Fprintln(tw, "NAME\tCPU%\tMEM USAGE/LIMIT\tPIDS")
				fmt.Fprintf(tw, "%s\t%.1f%%\t%dMB/%dMB\t%d\n", args[0], s.CPUPercent, s.MemUsageMB, s.MemLimitMB, s.PIDs)
				tw.Flush()
				return nil
			}

			// All container stats
			var stats []struct {
				Name       string  `json:"name"`
				CPUPercent float64 `json:"cpu_percent"`
				MemUsageMB int64   `json:"mem_usage_mb"`
				MemLimitMB int64   `json:"mem_limit_mb"`
				PIDs       int     `json:"pids"`
			}
			if err := json.Unmarshal(resp, &stats); err != nil {
				fmt.Println(string(resp))
				return nil
			}
			if len(stats) == 0 {
				fmt.Println("No container stats available")
				return nil
			}

			tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "NAME\tCPU%\tMEM USAGE/LIMIT\tPIDS")
			for _, s := range stats {
				fmt.Fprintf(tw, "%s\t%.1f%%\t%dMB/%dMB\t%d\n", s.Name, s.CPUPercent, s.MemUsageMB, s.MemLimitMB, s.PIDs)
			}
			tw.Flush()
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "output as JSON")
	return cmd
}

func applyCmd() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Reconcile running state to config",
		RunE: func(cmd *cobra.Command, args []string) error {
			ensureDaemon()

			path := "/v1/apply"
			if dryRun {
				path += "?dry_run=true"
			}

			resp, err := apiCall("POST", path, nil)
			if err != nil {
				return err
			}

			var result struct {
				Actions []struct {
					Resource string `json:"resource"`
					Action   string `json:"action"`
					Reason   string `json:"reason"`
				} `json:"actions"`
			}
			if err := json.Unmarshal(resp, &result); err != nil {
				return err
			}

			if dryRun {
				bold.Println("Dry run — no changes applied:")
			}

			tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "RESOURCE\tACTION\tREASON")
			for _, a := range result.Actions {
				fmt.Fprintf(tw, "%s\t%s\t%s\n", a.Resource, a.Action, a.Reason)
			}
			tw.Flush()
			return nil
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show diff without executing")
	return cmd
}

func restartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restart <name>",
		Short: "Restart a service or container",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := apiCall("POST", "/v1/restart/"+args[0], nil)
			if err != nil {
				return err
			}
			green.Printf("  Restarted %s\n", args[0])
			return nil
		},
	}
}

func buildCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "build [name...]",
		Short: "Build container images",
		RunE: func(cmd *cobra.Command, args []string) error {
			ensureDaemon()
			body := map[string]interface{}{}
			if len(args) > 0 {
				body["names"] = args
			}
			resp, err := apiCall("POST", "/v1/build", body)
			if err != nil {
				return err
			}

			var result struct {
				Built  []string          `json:"built"`
				Errors map[string]string `json:"errors"`
			}
			_ = json.Unmarshal(resp, &result)
			for _, name := range result.Built {
				green.Printf("  Built %s\n", name)
			}
			for name, errMsg := range result.Errors {
				red.Printf("  Failed %s: %s\n", name, errMsg)
			}
			return nil
		},
	}
}

func runCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "run <name> [-- args...]",
		Short: "Run an ephemeral container",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ensureDaemon()
			body := map[string]interface{}{
				"name": args[0],
			}
			if len(args) > 1 {
				body["args"] = args[1:]
			}
			resp, err := apiCall("POST", "/v1/run", body)
			if err != nil {
				return err
			}
			fmt.Println(string(resp))
			return nil
		},
	}
}

func execCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "exec <name> -- <cmd>",
		Short: "Execute command in running container",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			var command []string
			for i, a := range args {
				if a == "--" {
					command = args[i+1:]
					break
				}
			}
			if len(command) == 0 && len(args) > 1 {
				command = args[1:]
			}

			body := map[string]interface{}{
				"command": command,
			}
			resp, err := apiCall("POST", "/v1/exec/"+name, body)
			if err != nil {
				return err
			}

			var result struct {
				Output string `json:"output"`
				Error  string `json:"error"`
			}
			_ = json.Unmarshal(resp, &result)
			if result.Output != "" {
				fmt.Print(result.Output)
			}
			if result.Error != "" {
				return fmt.Errorf("%s", result.Error)
			}
			return nil
		},
	}
}

func logsCmd() *cobra.Command {
	var follow bool
	var lines int
	cmd := &cobra.Command{
		Use:   "logs <name>",
		Short: "Tail logs for a resource",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			path := fmt.Sprintf("/v1/logs/%s?lines=%d", name, lines)

			if follow {
				for {
					resp, err := apiCall("GET", path, nil)
					if err != nil {
						return err
					}
					var entries []struct {
						Timestamp time.Time `json:"timestamp"`
						Source    string    `json:"source"`
						Level    string    `json:"level"`
						Message  string    `json:"message"`
					}
					_ = json.Unmarshal(resp, &entries)
					for _, e := range entries {
						printLogEntry(e.Timestamp, e.Level, e.Message)
					}
					time.Sleep(2 * time.Second)
				}
			}

			resp, err := apiCall("GET", path, nil)
			if err != nil {
				return err
			}
			var entries []struct {
				Timestamp time.Time `json:"timestamp"`
				Source    string    `json:"source"`
				Level    string    `json:"level"`
				Message  string    `json:"message"`
			}
			_ = json.Unmarshal(resp, &entries)
			for _, e := range entries {
				printLogEntry(e.Timestamp, e.Level, e.Message)
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "follow log output")
	cmd.Flags().IntVarP(&lines, "lines", "n", 100, "number of lines to show")
	return cmd
}

func runNowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "run-now <name>",
		Short: "Trigger a scheduled job immediately",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := apiCall("POST", "/v1/schedule/"+args[0]+"/run-now", nil)
			if err != nil {
				return err
			}
			green.Printf("  Triggered %s\n", args[0])
			return nil
		},
	}
}

func installCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Install skiff daemon and menu bar app as launchd agents",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(configPath)
			if err != nil {
				return err
			}

			binaryPath, err := os.Executable()
			if err != nil {
				return fmt.Errorf("finding binary: %w", err)
			}

			absConfig, err := filepath.Abs(configPath)
			if err != nil {
				return err
			}

			// Ensure logs directory exists
			if err := os.MkdirAll(cfg.Paths.Logs, 0755); err != nil {
				return fmt.Errorf("creating logs dir: %w", err)
			}

			// Install daemon agent
			daemonAgent, err := plist.Generate(binaryPath, absConfig, cfg.Paths.Logs)
			if err != nil {
				return err
			}
			if err := plist.InstallAgent(daemonAgent); err != nil {
				return err
			}
			daemonPath, _ := plist.PlistPath()
			green.Printf("  Installed daemon: %s\n", daemonPath)

			// Install menu bar agent
			menuBinary := filepath.Join(filepath.Dir(binaryPath), "skiff-menu")
			if _, err := os.Stat(menuBinary); err == nil {
				menuAgent, err := plist.GenerateMenu(menuBinary, cfg.Paths.Socket, cfg.Paths.Logs)
				if err != nil {
					return err
				}
				if err := plist.InstallAgent(menuAgent); err != nil {
					return fmt.Errorf("installing menu agent: %w", err)
				}
				menuPath, _ := plist.MenuPlistPath()
				green.Printf("  Installed menu:   %s\n", menuPath)
			} else {
				yellow.Println("  Skipped menu bar app (skiff-menu not found next to skiff binary)")
			}

			green.Println("  Loaded into launchd — skiff starts on login")
			return nil
		},
	}
}

func uninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Remove skiff daemon and menu bar app from launchd",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := plist.UnloadAgent(plist.MenuLabel); err != nil {
				yellow.Printf("  Warning: %v\n", err)
			} else if plist.MenuExists() {
				green.Println("  Uninstalled skiff menu bar app")
			}

			if err := plist.UnloadAgent(plist.DaemonLabel); err != nil {
				return err
			}
			green.Println("  Uninstalled skiff daemon")
			green.Println("  Removed from launchd")
			return nil
		},
	}
}

func configCmd() *cobra.Command {
	var validateOnly bool
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Validate and print resolved config",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(configPath)
			if err != nil {
				if validateOnly {
					fmt.Fprintf(os.Stderr, "%s\n", err)
					os.Exit(1)
				}
				return err
			}

			if validateOnly {
				return nil
			}

			data, _ := json.MarshalIndent(cfg, "", "  ")
			fmt.Println(string(data))
			return nil
		},
	}
	cmd.Flags().BoolVar(&validateOnly, "validate-only", false, "exit 0 if valid, 1 if invalid")
	return cmd
}

func initCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Generate a starter skiff.yml",
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := os.Stat("skiff.yml"); err == nil {
				return fmt.Errorf("skiff.yml already exists")
			}

			starter := `version: 1

paths:
  base: ~/platform
  socket: ~/platform/skiff.sock
  logs: ~/platform/logs
  state_file: ~/platform/skiff-state.json

daemon:
  status_poll_interval_secs: 5
  log_buffer_lines: 500
  config_watch: true
  shutdown_timeout_secs: 30

dns:
  enabled: true
  port: 15353
  domain: skiff.local
  ttl: 5

services: {}

containers: {}

schedules: {}
`
			if err := os.WriteFile("skiff.yml", []byte(starter), 0644); err != nil {
				return err
			}
			green.Println("  Created skiff.yml")
			return nil
		},
	}
}

func tuiCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "tui",
		Short: "Open the interactive terminal UI",
		RunE: func(cmd *cobra.Command, args []string) error {
			ensureDaemon()
			return tui.Run(socketPath())
		},
	}
}

// --- Helpers ---

func findConfig() string {
	candidates := []string{
		"skiff.yml",
		"config/skiff.yml",
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	// Check XDG-style config directory
	home, _ := os.UserHomeDir()
	if home != "" {
		xdg := filepath.Join(home, ".config", "skiff", "config.yml")
		if _, err := os.Stat(xdg); err == nil {
			return xdg
		}
		// Legacy location
		p := filepath.Join(home, "platform", "skiff.yml")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return "skiff.yml"
}

func socketPath() string {
	// Try to read config for socket path
	if configPath != "" {
		cfg, err := config.Load(configPath)
		if err == nil {
			return cfg.Paths.Socket
		}
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "platform", "skiff.sock")
}

func apiCall(method, path string, body interface{}) ([]byte, error) {
	sock := socketPath()

	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", sock)
			},
		},
		Timeout: 30 * time.Second,
	}

	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, "http://skiff"+path, bodyReader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cannot connect to skiff daemon (is it running?): %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode >= 400 {
		return data, fmt.Errorf("API error (%d): %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}

	return data, nil
}

func ensureDaemon() {
	sock := socketPath()
	conn, err := net.Dial("unix", sock)
	if err == nil {
		conn.Close()
		return
	}

	fmt.Fprintln(os.Stderr, "Starting skiff daemon...")

	exePath, _ := os.Executable()
	args := []string{"daemon", "-d"}
	if configPath != "" {
		args = append(args, "--config", configPath)
	}

	cmd := exec.Command(exePath, args...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	_ = cmd.Start()

	// Wait for socket to appear
	for i := 0; i < 50; i++ {
		time.Sleep(100 * time.Millisecond)
		if conn, err := net.Dial("unix", sock); err == nil {
			conn.Close()
			return
		}
	}
	fmt.Fprintln(os.Stderr, "Warning: daemon may not have started")
}

func daemonizeProcess(cfg *config.Config) error {
	exePath, err := os.Executable()
	if err != nil {
		return err
	}

	args := []string{"daemon"}
	if configPath != "" {
		args = append(args, "--config", configPath)
	}

	cmd := exec.Command(exePath, args...)
	cmd.Stdout = nil
	cmd.Stderr = nil

	// Write PID file
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("daemonizing: %w", err)
	}

	pidFile := filepath.Join(cfg.Paths.Base, "skiff.pid")
	os.MkdirAll(filepath.Dir(pidFile), 0755)
	os.WriteFile(pidFile, []byte(strconv.Itoa(cmd.Process.Pid)), 0600)

	fmt.Printf("skiff daemon started (pid %d)\n", cmd.Process.Pid)
	return nil
}

func printStatusTable(data []byte, showStats bool) error {
	var snapshot struct {
		Resources []struct {
			Name   string `json:"name"`
			Type   string `json:"type"`
			State  string `json:"state"`
			PID    int    `json:"pid"`
			Uptime int64  `json:"uptime_secs"`
			Ports  []string `json:"ports"`
			Health *struct {
				Status string `json:"status"`
			} `json:"health"`
			Stats *struct {
				CPUPercent float64 `json:"cpu_percent"`
				MemUsageMB int64   `json:"mem_usage_mb"`
				MemLimitMB int64   `json:"mem_limit_mb"`
				PIDs       int     `json:"pids"`
			} `json:"stats"`
		} `json:"resources"`
		Schedules []struct {
			Name       string `json:"name"`
			LastResult string `json:"last_result"`
			NextRun    string `json:"next_run"`
		} `json:"schedules"`
	}

	if err := json.Unmarshal(data, &snapshot); err != nil {
		// Just print raw JSON
		fmt.Println(string(data))
		return nil
	}

	if len(snapshot.Resources) > 0 {
		bold.Println("Resources:")
		tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		if showStats {
			fmt.Fprintln(tw, "NAME\tTYPE\tSTATE\tPID\tUPTIME\tHEALTH\tCPU%\tMEM\tPORTS")
		} else {
			fmt.Fprintln(tw, "NAME\tTYPE\tSTATE\tPID\tUPTIME\tHEALTH\tPORTS")
		}

		for _, r := range snapshot.Resources {
			healthStr := "-"
			if r.Health != nil {
				healthStr = r.Health.Status
			}
			uptimeStr := "-"
			if r.Uptime > 0 {
				uptimeStr = formatDuration(time.Duration(r.Uptime) * time.Second)
			}
			pidStr := "-"
			if r.PID > 0 {
				pidStr = strconv.Itoa(r.PID)
			}
			portsStr := strings.Join(r.Ports, ", ")
			if portsStr == "" {
				portsStr = "-"
			}
			if showStats {
				cpuStr := "-"
				memStr := "-"
				if r.Stats != nil {
					cpuStr = fmt.Sprintf("%.1f%%", r.Stats.CPUPercent)
					memStr = fmt.Sprintf("%dMB/%dMB", r.Stats.MemUsageMB, r.Stats.MemLimitMB)
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", r.Name, r.Type, r.State, pidStr, uptimeStr, healthStr, cpuStr, memStr, portsStr)
			} else {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", r.Name, r.Type, r.State, pidStr, uptimeStr, healthStr, portsStr)
			}
		}
		tw.Flush()
	}

	if len(snapshot.Schedules) > 0 {
		fmt.Println()
		bold.Println("Schedules:")
		tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "NAME\tLAST RESULT\tNEXT RUN")

		for _, s := range snapshot.Schedules {
			fmt.Fprintf(tw, "%s\t%s\t%s\n", s.Name, s.LastResult, s.NextRun)
		}
		tw.Flush()
	}

	return nil
}

func printLogEntry(ts time.Time, level, message string) {
	timeStr := ts.Format("15:04:05")
	switch level {
	case "error":
		fmt.Printf("%s %s %s\n", timeStr, red.Sprint("[ERROR]"), message)
	case "warn":
		fmt.Printf("%s %s %s\n", timeStr, yellow.Sprint("[WARN]"), message)
	default:
		fmt.Printf("%s [%s] %s\n", timeStr, strings.ToUpper(level), message)
	}
}

func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
}

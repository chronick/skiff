package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Client communicates with the plane daemon over a unix socket.
type Client struct {
	socketPath string
	http       *http.Client
}

type ResourceInfo struct {
	Name       string      `json:"name"`
	Type       string      `json:"type"`
	State      string      `json:"state"`
	PID        int         `json:"pid,omitempty"`
	UptimeSecs int64       `json:"uptime_secs,omitempty"`
	StartedAt  time.Time   `json:"started_at,omitempty"`
	ExitCode   int         `json:"exit_code,omitempty"`
	LastError  string      `json:"last_error,omitempty"`
	ConfigHash string      `json:"config_hash"`
	Health     *HealthInfo `json:"health,omitempty"`
	Ports      []string    `json:"ports,omitempty"`
	DependsOn  []string    `json:"depends_on,omitempty"`
	Stats      *StatsInfo  `json:"stats,omitempty"`
}

type StatsInfo struct {
	CPUPercent float64 `json:"cpu_percent"`
	MemUsageMB int64   `json:"mem_usage_mb"`
	MemLimitMB int64   `json:"mem_limit_mb"`
	PIDs       int     `json:"pids"`
}

type HealthInfo struct {
	Status           string    `json:"status"`
	ConsecutiveFails int       `json:"consecutive_fails"`
	LastCheck        time.Time `json:"last_check"`
	LastError        string    `json:"last_error,omitempty"`
}

type ScheduleInfo struct {
	Name       string     `json:"name"`
	LastRun    *time.Time `json:"last_run,omitempty"`
	NextRun    time.Time  `json:"next_run"`
	LastResult string     `json:"last_result"`
	LastError  string     `json:"last_error,omitempty"`
	Duration   int64      `json:"last_duration_ms,omitempty"`
}

type LogEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Source    string    `json:"source"`
	Level    string    `json:"level"`
	Message  string    `json:"message"`
}

type StatusSnapshot struct {
	Resources []ResourceInfo `json:"resources"`
	Schedules []ScheduleInfo `json:"schedules"`
	Timestamp time.Time      `json:"timestamp"`
}

type ActionResult struct {
	Started []string          `json:"started,omitempty"`
	Stopped []string          `json:"stopped,omitempty"`
	Errors  map[string]string `json:"errors,omitempty"`
}

func DefaultSocketPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "platform", "plane.sock")
}

func New(socketPath string) *Client {
	return &Client{
		socketPath: socketPath,
		http: &http.Client{
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return net.Dial("unix", socketPath)
				},
			},
			Timeout: 10 * time.Second,
		},
	}
}

func (c *Client) call(method, path string, body interface{}) ([]byte, error) {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, "http://plane"+path, bodyReader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cannot connect to daemon: %w", err)
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

func (c *Client) Status() (*StatusSnapshot, error) {
	data, err := c.call("GET", "/v1/status", nil)
	if err != nil {
		return nil, err
	}
	var snap StatusSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, err
	}
	return &snap, nil
}

func (c *Client) Health() error {
	_, err := c.call("GET", "/v1/health", nil)
	return err
}

func (c *Client) Logs(name string, lines int) ([]LogEntry, error) {
	path := fmt.Sprintf("/v1/logs/%s?lines=%d", name, lines)
	data, err := c.call("GET", path, nil)
	if err != nil {
		return nil, err
	}
	var entries []LogEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, err
	}
	return entries, nil
}

func (c *Client) Up(names []string) (*ActionResult, error) {
	body := map[string]interface{}{}
	if len(names) > 0 {
		body["names"] = names
	}
	data, err := c.call("POST", "/v1/up", body)
	if err != nil {
		return nil, err
	}
	var result ActionResult
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) Down(names []string) (*ActionResult, error) {
	body := map[string]interface{}{}
	if len(names) > 0 {
		body["names"] = names
	}
	data, err := c.call("POST", "/v1/down", body)
	if err != nil {
		return nil, err
	}
	var result ActionResult
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) Restart(name string) error {
	_, err := c.call("POST", "/v1/restart/"+name, nil)
	return err
}

func (c *Client) Stats() ([]StatsEntry, error) {
	data, err := c.call("GET", "/v1/stats", nil)
	if err != nil {
		return nil, err
	}
	var entries []StatsEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, err
	}
	return entries, nil
}

type StatsEntry struct {
	Name       string  `json:"name"`
	CPUPercent float64 `json:"cpu_percent"`
	MemUsageMB int64   `json:"mem_usage_mb"`
	MemLimitMB int64   `json:"mem_limit_mb"`
	PIDs       int     `json:"pids"`
}

func (c *Client) RunNow(name string) error {
	_, err := c.call("POST", "/v1/schedule/"+name+"/run-now", nil)
	return err
}

func (c *Client) Build(names []string) (*ActionResult, error) {
	body := map[string]interface{}{}
	if len(names) > 0 {
		body["names"] = names
	}
	data, err := c.call("POST", "/v1/build", body)
	if err != nil {
		return nil, err
	}
	var result ActionResult
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) Exec(name string, command []string) (string, error) {
	body := map[string]interface{}{"command": command}
	data, err := c.call("POST", "/v1/exec/"+name, body)
	if err != nil {
		return "", err
	}
	var result struct {
		Output string `json:"output"`
		Error  string `json:"error"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return "", err
	}
	if result.Error != "" {
		return result.Output, fmt.Errorf("%s", result.Error)
	}
	return result.Output, nil
}

func (c *Client) ContainerLogs(name string, lines int) (string, error) {
	path := fmt.Sprintf("/v1/logs/%s?lines=%d&source=container", name, lines)
	data, err := c.call("GET", path, nil)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

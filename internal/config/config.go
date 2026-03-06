package config

import (
	"bufio"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

var nameRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// Config is the top-level skiff.yml structure.
type Config struct {
	Version    int                        `yaml:"version"`
	Paths      PathsConfig                `yaml:"paths"`
	Daemon     DaemonConfig               `yaml:"daemon"`
	DNS        DNSConfig                  `yaml:"dns"`
	Networks   map[string]NetworkConfig   `yaml:"networks,omitempty"`
	Services   map[string]ServiceConfig   `yaml:"services"`
	Containers map[string]ContainerConfig `yaml:"containers"`
	Schedules  map[string]ScheduleConfig  `yaml:"schedules"`
	Proxy      *ProxyConfig               `yaml:"proxy,omitempty"`
}

// NetworkConfig defines a named container network.
type NetworkConfig struct {
	Subnet   string `yaml:"subnet,omitempty"`
	Internal bool   `yaml:"internal,omitempty"`
}

type PathsConfig struct {
	Base      string `yaml:"base"`
	Socket    string `yaml:"socket"`
	Logs      string `yaml:"logs"`
	StateFile string `yaml:"state_file"`
}

type DaemonConfig struct {
	StatusPollIntervalSecs int    `yaml:"status_poll_interval_secs"`
	LogBufferLines         int    `yaml:"log_buffer_lines"`
	ConfigWatch            bool   `yaml:"config_watch"`
	Listen                 string `yaml:"listen,omitempty"`
	AuthToken              string `yaml:"auth_token,omitempty"`
	AllowRemote            bool   `yaml:"allow_remote,omitempty"`
	ShutdownTimeoutSecs    int    `yaml:"shutdown_timeout_secs"`
}

type DNSConfig struct {
	Enabled bool   `yaml:"enabled"`
	Port    int    `yaml:"port"`
	Domain  string `yaml:"domain"`
	TTL     int    `yaml:"ttl"`
}

type ServiceConfig struct {
	Command            []string          `yaml:"command"`
	WorkingDir         string            `yaml:"working_dir"`
	Env                map[string]string `yaml:"env"`
	RestartPolicy      string            `yaml:"restart_policy"`
	MaxRestarts        int               `yaml:"max_restarts"`
	RestartBackoffSecs int               `yaml:"restart_backoff_secs"`
	LogFile            string            `yaml:"log_file"`
	HealthCheck        *HealthCheckConfig `yaml:"health_check,omitempty"`
	DependsOn          []string          `yaml:"depends_on,omitempty"`
}

type ContainerConfig struct {
	Image       string             `yaml:"image"`
	Dockerfile  string             `yaml:"dockerfile"`
	Context     string             `yaml:"context,omitempty"`
	Volumes     []string           `yaml:"volumes"`
	Env         map[string]string  `yaml:"env"`
	Ports       []string           `yaml:"ports"`
	CPUs        float64            `yaml:"cpus,omitempty"`
	Memory      string             `yaml:"memory,omitempty"`
	Labels      map[string]string  `yaml:"labels,omitempty"`
	Init        bool               `yaml:"init,omitempty"`
	ReadOnly    bool               `yaml:"read_only,omitempty"`
	Network     string             `yaml:"network,omitempty"`
	HealthCheck *HealthCheckConfig `yaml:"health_check,omitempty"`
	DependsOn   []string           `yaml:"depends_on,omitempty"`
}

type ScheduleConfig struct {
	Command         []string          `yaml:"command"`
	WorkingDir      string            `yaml:"working_dir"`
	IntervalSeconds int               `yaml:"interval_seconds,omitempty"`
	Calendar        *CalendarInterval `yaml:"calendar,omitempty"`
	LogFile         string            `yaml:"log_file"`
	Env             map[string]string `yaml:"env"`
	TimeoutSecs     int               `yaml:"timeout_secs,omitempty"`
}

type CalendarInterval struct {
	Hour    *int `yaml:"hour,omitempty"`
	Minute  *int `yaml:"minute,omitempty"`
	Day     *int `yaml:"day,omitempty"`
	Weekday *int `yaml:"weekday,omitempty"`
	Month   *int `yaml:"month,omitempty"`
}

type HealthCheckConfig struct {
	Type             string   `yaml:"type"`
	URL              string   `yaml:"url,omitempty"`
	Port             int      `yaml:"port,omitempty"`
	Command          []string `yaml:"command,omitempty"`
	IntervalSecs     int      `yaml:"interval_secs"`
	TimeoutSecs      int      `yaml:"timeout_secs"`
	FailureThreshold int      `yaml:"failure_threshold"`
	AutoRestart      bool     `yaml:"auto_restart"`
}

type ProxyConfig struct {
	Routes []ProxyRoute `yaml:"routes"`
}

type ProxyRoute struct {
	Path   string `yaml:"path"`
	Target string `yaml:"target"`
	Port   int    `yaml:"port"`
}

// Load reads and parses a skiff.yml file, resolving env vars and applying defaults.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}

	// Load .env file from same directory
	envFile := filepath.Join(filepath.Dir(path), ".env")
	dotenv := loadDotEnv(envFile)

	// Resolve ${VAR} references
	resolved := resolveEnvVars(string(data), dotenv)

	var cfg Config
	if err := yaml.Unmarshal([]byte(resolved), &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	applyDefaults(&cfg)

	if err := validate(&cfg); err != nil {
		return nil, fmt.Errorf("validating config: %w", err)
	}

	return &cfg, nil
}

// Hash returns a SHA-256 hash of the config for change detection.
func Hash(cfg interface{}) string {
	data, _ := yaml.Marshal(cfg)
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x", h[:8])
}

func applyDefaults(cfg *Config) {
	if cfg.Version == 0 {
		cfg.Version = 1
	}
	if cfg.Paths.Base == "" {
		cfg.Paths.Base = "~/platform"
	}
	cfg.Paths.Base = expandHome(cfg.Paths.Base)

	if cfg.Paths.Socket == "" {
		cfg.Paths.Socket = filepath.Join(cfg.Paths.Base, "skiff.sock")
	} else {
		cfg.Paths.Socket = expandHome(cfg.Paths.Socket)
	}
	if cfg.Paths.Logs == "" {
		cfg.Paths.Logs = filepath.Join(cfg.Paths.Base, "logs")
	} else {
		cfg.Paths.Logs = expandHome(cfg.Paths.Logs)
	}
	if cfg.Paths.StateFile == "" {
		cfg.Paths.StateFile = filepath.Join(cfg.Paths.Base, "skiff-state.json")
	} else {
		cfg.Paths.StateFile = expandHome(cfg.Paths.StateFile)
	}

	if cfg.Daemon.StatusPollIntervalSecs == 0 {
		cfg.Daemon.StatusPollIntervalSecs = 5
	}
	if cfg.Daemon.LogBufferLines == 0 {
		cfg.Daemon.LogBufferLines = 500
	}
	if cfg.Daemon.ShutdownTimeoutSecs == 0 {
		cfg.Daemon.ShutdownTimeoutSecs = 30
	}

	if !cfg.DNS.Enabled && cfg.DNS.Port == 0 && cfg.DNS.Domain == "" {
		cfg.DNS.Enabled = true
	}
	if cfg.DNS.Port == 0 {
		cfg.DNS.Port = 15353
	}
	if cfg.DNS.Domain == "" {
		cfg.DNS.Domain = "skiff.local"
	}
	if cfg.DNS.TTL == 0 {
		cfg.DNS.TTL = 5
	}

	for name, svc := range cfg.Services {
		if svc.RestartPolicy == "" {
			svc.RestartPolicy = "always"
		}
		if svc.RestartBackoffSecs == 0 {
			svc.RestartBackoffSecs = 5
		}
		svc.WorkingDir = expandHome(svc.WorkingDir)
		cfg.Services[name] = svc
	}

	for name, c := range cfg.Containers {
		if c.Context == "" && c.Dockerfile != "" {
			c.Context = filepath.Dir(c.Dockerfile)
		}
		cfg.Containers[name] = c
	}

	for name, s := range cfg.Schedules {
		if s.TimeoutSecs == 0 {
			s.TimeoutSecs = 300
		}
		s.WorkingDir = expandHome(s.WorkingDir)
		cfg.Schedules[name] = s
	}
}

func validate(cfg *Config) error {
	if cfg.Version != 1 {
		return fmt.Errorf("unsupported config version: %d (only version 1 is supported)", cfg.Version)
	}

	// Validate names
	allNames := map[string]string{} // name -> type
	for name := range cfg.Services {
		if !nameRegex.MatchString(name) {
			return fmt.Errorf("invalid service name %q: must match %s", name, nameRegex.String())
		}
		allNames[name] = "service"
	}
	for name := range cfg.Containers {
		if !nameRegex.MatchString(name) {
			return fmt.Errorf("invalid container name %q: must match %s", name, nameRegex.String())
		}
		if t, exists := allNames[name]; exists {
			return fmt.Errorf("duplicate name %q: used by both %s and container", name, t)
		}
		allNames[name] = "container"
	}
	for name := range cfg.Schedules {
		if !nameRegex.MatchString(name) {
			return fmt.Errorf("invalid schedule name %q: must match %s", name, nameRegex.String())
		}
		if t, exists := allNames[name]; exists {
			return fmt.Errorf("duplicate name %q: used by both %s and schedule", name, t)
		}
		allNames[name] = "schedule"
	}

	// Validate service configs
	for name, svc := range cfg.Services {
		if len(svc.Command) == 0 {
			return fmt.Errorf("service %q: command is required", name)
		}
		switch svc.RestartPolicy {
		case "always", "on-failure", "never":
		default:
			return fmt.Errorf("service %q: invalid restart_policy %q", name, svc.RestartPolicy)
		}
		if err := validateHealthCheck(name, svc.HealthCheck); err != nil {
			return err
		}
	}

	// Validate container configs
	for name, c := range cfg.Containers {
		if c.Image == "" && c.Dockerfile == "" {
			return fmt.Errorf("container %q: image or dockerfile is required", name)
		}
		for _, v := range c.Volumes {
			parts := strings.SplitN(v, ":", 2)
			if len(parts) == 2 && strings.Contains(parts[0], "..") {
				return fmt.Errorf("container %q: volume source path must not contain '..'", name)
			}
		}
		for k, v := range c.Labels {
			if k == "" || v == "" {
				return fmt.Errorf("container %q: label keys and values must not be empty", name)
			}
			if strings.HasPrefix(k, "skiff.") {
				return fmt.Errorf("container %q: label key %q is reserved (skiff.* prefix)", name, k)
			}
		}
		if c.Network != "" && c.Network != "host" {
			if cfg.Networks == nil {
				return fmt.Errorf("container %q: network %q not defined in networks section", name, c.Network)
			}
			if _, ok := cfg.Networks[c.Network]; !ok {
				return fmt.Errorf("container %q: network %q not defined in networks section", name, c.Network)
			}
		}
		if err := validateHealthCheck(name, c.HealthCheck); err != nil {
			return err
		}
	}

	// Validate schedule configs
	for name, s := range cfg.Schedules {
		if len(s.Command) == 0 {
			return fmt.Errorf("schedule %q: command is required", name)
		}
		if s.IntervalSeconds == 0 && s.Calendar == nil {
			return fmt.Errorf("schedule %q: interval_seconds or calendar is required", name)
		}
	}

	// Validate depends_on references and detect cycles
	dagNodes := map[string][]string{}
	for name, svc := range cfg.Services {
		for _, dep := range svc.DependsOn {
			if _, ok := allNames[dep]; !ok {
				return fmt.Errorf("service %q: depends_on references unknown name %q", name, dep)
			}
		}
		dagNodes[name] = svc.DependsOn
	}
	for name, c := range cfg.Containers {
		for _, dep := range c.DependsOn {
			if _, ok := allNames[dep]; !ok {
				return fmt.Errorf("container %q: depends_on references unknown name %q", name, dep)
			}
		}
		dagNodes[name] = c.DependsOn
	}
	if err := detectCycles(dagNodes); err != nil {
		return err
	}

	// Security validations
	if cfg.Daemon.Listen != "" && cfg.Daemon.AuthToken == "" {
		return fmt.Errorf("daemon.auth_token is required when daemon.listen is set")
	}
	if cfg.Daemon.Listen != "" && !cfg.Daemon.AllowRemote {
		host := cfg.Daemon.Listen
		if idx := strings.LastIndex(host, ":"); idx >= 0 {
			host = host[:idx]
		}
		if host != "127.0.0.1" && host != "localhost" && host != "[::1]" && host != "" {
			return fmt.Errorf("daemon.allow_remote must be true to bind to %q (non-localhost)", host)
		}
	}

	// Validate proxy targets
	if cfg.Proxy != nil {
		for _, r := range cfg.Proxy.Routes {
			if _, ok := allNames[r.Target]; !ok {
				return fmt.Errorf("proxy route %q: target %q not found in config", r.Path, r.Target)
			}
		}
	}

	return nil
}

func validateHealthCheck(name string, hc *HealthCheckConfig) error {
	if hc == nil {
		return nil
	}
	switch hc.Type {
	case "http":
		if hc.URL == "" {
			return fmt.Errorf("%q health_check: url is required for http type", name)
		}
	case "tcp":
		if hc.Port == 0 {
			return fmt.Errorf("%q health_check: port is required for tcp type", name)
		}
	case "command":
		if len(hc.Command) == 0 {
			return fmt.Errorf("%q health_check: command is required for command type", name)
		}
	default:
		return fmt.Errorf("%q health_check: unknown type %q (must be http, tcp, or command)", name, hc.Type)
	}
	if hc.IntervalSecs == 0 {
		hc.IntervalSecs = 30
	}
	if hc.TimeoutSecs == 0 {
		hc.TimeoutSecs = 5
	}
	if hc.FailureThreshold == 0 {
		hc.FailureThreshold = 3
	}
	return nil
}

// DependencyOrder returns resource names in topological order (leaves first).
func DependencyOrder(cfg *Config) ([]string, error) {
	graph := map[string][]string{}
	for name, svc := range cfg.Services {
		graph[name] = svc.DependsOn
	}
	for name, c := range cfg.Containers {
		graph[name] = c.DependsOn
	}

	return topoSort(graph)
}

func topoSort(graph map[string][]string) ([]string, error) {
	var order []string
	state := map[string]int{} // 0=unvisited, 1=visiting, 2=visited

	var visit func(string) error
	visit = func(name string) error {
		if state[name] == 2 {
			return nil
		}
		if state[name] == 1 {
			return fmt.Errorf("dependency cycle detected involving %q", name)
		}
		state[name] = 1
		for _, dep := range graph[name] {
			if err := visit(dep); err != nil {
				return err
			}
		}
		state[name] = 2
		order = append(order, name)
		return nil
	}

	// Sort keys for deterministic order
	names := make([]string, 0, len(graph))
	for name := range graph {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		if err := visit(name); err != nil {
			return nil, err
		}
	}
	return order, nil
}

func detectCycles(graph map[string][]string) error {
	_, err := topoSort(graph)
	return err
}

func resolveEnvVars(input string, dotenv map[string]string) string {
	re := regexp.MustCompile(`\$\{([^}]+)\}`)
	return re.ReplaceAllStringFunc(input, func(match string) string {
		varName := match[2 : len(match)-1]
		if val, ok := os.LookupEnv(varName); ok {
			return val
		}
		if val, ok := dotenv[varName]; ok {
			return val
		}
		return match
	})
}

func loadDotEnv(path string) map[string]string {
	result := map[string]string{}
	f, err := os.Open(path)
	if err != nil {
		return result
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		// Remove surrounding quotes
		if len(val) >= 2 && ((val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'')) {
			val = val[1 : len(val)-1]
		}
		result[key] = val
	}
	return result
}

func expandHome(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return filepath.Join(home, path[2:])
	}
	return path
}

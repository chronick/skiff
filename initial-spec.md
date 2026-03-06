# plat — Container Orchestration for macOS

## Overview

plat is a lightweight container orchestration layer for macOS, built around
Apple Container Runtime. It sits between docker-compose (declarative but
stateless) and Kubernetes (powerful but massive) — offering health-aware
lifecycle management, internal scheduling, service networking, and a
programmatic control plane API, all from a single YAML file and binary.

plat also manages native macOS services via launchd, making it a unified
control plane for everything running on a Mac: containers, daemons, and
scheduled jobs.

### What plat adds over docker-compose

- Health checks with auto-restart and configurable probes
- Internal cron-like scheduler (no external cron/launchd plists needed)
- Control skiff API (unix socket or TCP) for programmatic access
- Embedded DNS for container-to-container service discovery
- Service networking with port exposure and proxy routing
- Startup dependency ordering with health-gated readiness
- Native macOS service management (launchd) alongside containers
- Centralized log aggregation with in-memory ring buffers
- Config drift detection and declarative reconciliation
- Client SDK / CLI for external tools to query and control the platform

### What plat intentionally omits (k8s territory)

- Multi-node clustering and scheduling
- Horizontal scaling / replicas
- Load balancing across hosts
- Custom resource definitions
- RBAC and multi-tenant isolation

plat is for a single machine running many services. It makes that machine
easy to manage, observe, and automate.

## Tech Stack

- **Language**: Go 1.22+
- **HTTP**: `net/http` + `net` (unix socket and TCP)
- **CLI**: `github.com/spf13/cobra`
- **Config**: `gopkg.in/yaml.v3`
- **Logging**: `log/slog` (structured, stdlib)
- **Process management**: `os/exec`
- **Plist**: `howett.net/plist`
- **DNS**: `github.com/miekg/dns` (embedded service discovery)

### External Dependencies (target: 4-6)

| Dependency | Purpose |
|---|---|
| `github.com/spf13/cobra` | CLI subcommands |
| `gopkg.in/yaml.v3` | YAML config parsing |
| `howett.net/plist` | Launchd plist generation |
| `github.com/miekg/dns` | Embedded DNS server for service discovery |
| `github.com/fatih/color` | Terminal colors |
| `github.com/olekukonko/tablewriter` | Status table output |

Everything else is stdlib: `net/http`, `net`, `os/exec`, `encoding/json`,
`time`, `sync`, `context`, `os`, `path/filepath`, `crypto/sha256`, `net/http/httputil`.

## Project Layout

```
cmd/
  plat/
    main.go              — entrypoint, cobra root command
internal/
  config/
    config.go            — plat.yml parsing + validation + env resolution
    config_test.go
  daemon/
    daemon.go            — HTTP server (unix socket + optional TCP)
    routes.go            — control plane API handlers
    proxy.go             — reverse proxy to container-exposed services
    routes_test.go
  dns/
    dns.go               — embedded DNS server for service discovery
    dns_test.go
  service/
    service.go           — launchd native service management
    service_test.go
  container/
    container.go         — Apple Container lifecycle (run/stop/build/exec)
    container_test.go
  plist/
    plist.go             — launchd plist generation
    plist_test.go
  scheduler/
    scheduler.go         — internal schedule runner (goroutines)
    scheduler_test.go
  health/
    health.go            — health check probes (http/tcp/command)
    health_test.go
  status/
    status.go            — SharedState + poller
    status_test.go
  logbuf/
    logbuf.go            — ring buffer log aggregation
    logbuf_test.go
  runner/
    runner.go            — ProcessRunner interface (for mocking exec)
    runner_test.go
config/
  plat.yml               — source of truth
  plat.example.yml       — annotated example
docs/
  specs/spec-go.md       — this file
  tasks/                 — SDD task files
```

## Architecture

```
                    +----------------------------------------------------------+
                    |                plat daemon process                         |
                    |              (com.plat.daemon.plist)                       |
                    |                                                           |
CLI (cobra) ---+   |  +--------------+   +----------------+                    |
               |   |  |  Scheduler   |   | HealthChecker  |                    |
Control API ---+   |  |  goroutines  |   |  goroutines    |                    |
(unix/tcp)     |   |  | interval/cal --> child processes   |  +----------+     |
               v   |  | run history  |   | http/tcp/cmd   |  |LogBuffers|     |
         +--------+|  +------+-------+   +------+---------+  | ring per |     |
         |  http  ||         |                  |             | service  |     |
         | Server ||         v                  v             +----+-----+     |
         +---+----+|  +-------------------------------------------+  |        |
             |     |  |     sync.RWMutex{PlatformState}            |<-+        |
             +---->|  |                                            |           |
             |     |  |  Services    []ServiceStatus               |           |
             |     |  |  Schedules   []ScheduleStatus              |           |
             |     |  |  Containers  []ContainerStatus             |           |
             |     |  +-------------------------------------------+           |
             |     |         ^                                                 |
             |     |  +------+--------+                                        |
             |     |  | StatusPoller  |  every 5s: launchctl + container list  |
             |     |  +--------------+                                         |
             |     +-----------------------------------------------------------+
             |
             +---> launchctl (native services)
             +---> container CLI (Apple Container Runtime)
             +---> Reverse proxy to container-exposed ports
             +---> Embedded DNS (service discovery for containers)
```

### Concurrency Model

Standard Go patterns — no framework needed:

- **HTTP server**: `http.Serve` on unix socket + optional TCP listener
- **StatusPoller**: single goroutine, `time.Ticker`, writes SharedState under `sync.RWMutex`
- **Scheduler**: one goroutine per schedule, `time.Timer` for next run, `exec.CommandContext` for child processes
- **HealthChecker**: one goroutine per probe, `time.Ticker`, `http.Get`/`net.Dial`/`exec.Command`
- **Log tailers**: one goroutine per service with a log file, `os.File` seek+read
- **DNS server**: single goroutine, serves UDP queries, updates records from SharedState
- **Shutdown**: `context.WithCancel` propagated to all goroutines, `signal.NotifyContext` for SIGTERM/SIGINT

SharedState is `sync.RWMutex`-protected — readers (API handlers) take read lock, writers (poller, scheduler, health) take write lock.

## Configuration (plat.yml)

```yaml
version: 1

paths:
  base: ~/platform
  socket: ~/platform/plat.sock
  logs: ~/platform/logs
  state_file: ~/platform/plat-state.json

daemon:
  status_poll_interval_secs: 5
  log_buffer_lines: 500
  config_watch: true
  # Optional TCP listener for remote/programmatic access
  listen: "127.0.0.1:9100"
  # Bearer token required for TCP access (unix socket uses file perms)
  auth_token: "${PLAT_AUTH_TOKEN}"

# --- Service discovery DNS ---
dns:
  enabled: true
  port: 15353
  domain: plat.local
  ttl: 5

# --- Native macOS services (managed via launchd) ---
services:
  task-queue:
    command: [".venv/bin/huey_consumer", "tasks.huey", "-w", "4"]
    working_dir: ~/platform
    env:
      DATABASE_URL: "${DATABASE_URL}"
    keep_alive: true
    log_file: task-queue.log
    health_check:
      type: tcp
      port: 8001
      interval_secs: 30
      failure_threshold: 3

  web-app:
    command: ["npm", "run", "start"]
    working_dir: ~/platform/web
    keep_alive: true
    log_file: web.log
    health_check:
      type: http
      url: "http://localhost:3000/health"
      interval_secs: 15
      timeout_secs: 5
      failure_threshold: 3
      auto_restart: true

# --- Apple Containers ---
containers:
  api-server:
    image: api-server:latest
    dockerfile: containers/Dockerfile.api
    volumes:
      - ~/platform/data:/data
    env:
      # Use service name directly — plat DNS resolves it inside containers
      DATABASE_URL: "postgres://user:pass@db:5432/app"
      API_SECRET: "${API_SECRET}"
    ports:
      - "8080:8080"
    health_check:
      type: http
      url: "http://localhost:8080/health"
      interval_secs: 15
      failure_threshold: 3
      auto_restart: true
    depends_on:
      - task-queue

  worker:
    image: worker:latest
    dockerfile: containers/Dockerfile.worker
    volumes:
      - ~/platform/data:/data
    env:
      DATABASE_URL: "${DATABASE_URL}"

  renderer:
    image: renderer:latest
    dockerfile: containers/Dockerfile.renderer
    volumes:
      - ~/platform/output:/output
    # Ephemeral: started on-demand via `plat run renderer`

# --- Scheduled jobs (run inside plat daemon, not launchd) ---
schedules:
  data-sync:
    command: ["python", "scripts/sync.py"]
    working_dir: ~/platform
    interval_seconds: 3600
    log_file: sync.log
    env:
      SYNC_TOKEN: "${SYNC_TOKEN}"
    timeout_secs: 300

  cleanup:
    command: ["python", "scripts/cleanup.py"]
    working_dir: ~/platform
    calendar:
      hour: 3
      minute: 0
    log_file: cleanup.log
    timeout_secs: 600

  health-report:
    command: ["python", "scripts/health_report.py"]
    working_dir: ~/platform
    interval_seconds: 86400
    log_file: health-report.log
    timeout_secs: 120

# --- Service networking / proxy rules ---
proxy:
  # Expose container services through plat's TCP listener
  routes:
    - path: /api
      target: api-server
      port: 8080
    - path: /app
      target: web-app
      port: 3000
```

### Config Types

```go
type Config struct {
    Version    int                        `yaml:"version"`
    Paths      PathsConfig                `yaml:"paths"`
    Daemon     DaemonConfig               `yaml:"daemon"`
    DNS        DNSConfig                  `yaml:"dns"`
    Services   map[string]ServiceConfig   `yaml:"services"`
    Containers map[string]ContainerConfig `yaml:"containers"`
    Schedules  map[string]ScheduleConfig  `yaml:"schedules"`
    Proxy      *ProxyConfig               `yaml:"proxy,omitempty"`
}

type DNSConfig struct {
    Enabled bool   `yaml:"enabled"` // default: true
    Port    int    `yaml:"port"`    // default: 15353
    Domain  string `yaml:"domain"`  // default: "plat.local"
    TTL     int    `yaml:"ttl"`     // default: 5 seconds
}

type PathsConfig struct {
    Base      string `yaml:"base"`
    Socket    string `yaml:"socket"`
    Logs      string `yaml:"logs"`
    StateFile string `yaml:"state_file"`
}

type DaemonConfig struct {
    StatusPollIntervalSecs int    `yaml:"status_poll_interval_secs"` // default: 5
    LogBufferLines         int    `yaml:"log_buffer_lines"`          // default: 500
    ConfigWatch            bool   `yaml:"config_watch"`
    Listen                 string `yaml:"listen,omitempty"`    // TCP address, e.g. "127.0.0.1:9100"
    AuthToken              string `yaml:"auth_token,omitempty"` // required when listen is set
}

type ServiceConfig struct {
    Command     []string          `yaml:"command"`
    WorkingDir  string            `yaml:"working_dir"`
    Env         map[string]string `yaml:"env"`
    KeepAlive   bool              `yaml:"keep_alive"`
    LogFile     string            `yaml:"log_file"`
    HealthCheck *HealthCheckConfig `yaml:"health_check,omitempty"`
}

type ContainerConfig struct {
    Image      string            `yaml:"image"`
    Dockerfile string            `yaml:"dockerfile"`
    Volumes    []string          `yaml:"volumes"`
    Env        map[string]string `yaml:"env"`
    Ports      []string          `yaml:"ports"`   // "host:container"
    HealthCheck *HealthCheckConfig `yaml:"health_check,omitempty"`
    DependsOn  []string          `yaml:"depends_on,omitempty"`
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
    Type             string   `yaml:"type"` // http | tcp | command
    URL              string   `yaml:"url,omitempty"`
    Port             int      `yaml:"port,omitempty"`
    Command          []string `yaml:"command,omitempty"`
    IntervalSecs     int      `yaml:"interval_secs"`     // default: 30
    TimeoutSecs      int      `yaml:"timeout_secs"`      // default: 5
    FailureThreshold int      `yaml:"failure_threshold"`  // default: 3
    AutoRestart      bool     `yaml:"auto_restart"`       // default: false
}

type ProxyConfig struct {
    Routes []ProxyRoute `yaml:"routes"`
}

type ProxyRoute struct {
    Path   string `yaml:"path"`   // URL path prefix
    Target string `yaml:"target"` // service or container name
    Port   int    `yaml:"port"`   // target port
}
```

## CLI Commands

Binary: `plat`.

| Command | Description |
|---------|-------------|
| `plat up [name...]` | Start all (or named) services and containers. Respects `depends_on` order. |
| `plat down [name...]` | Stop all (or named) services and containers. Reverse dependency order. |
| `plat status [--json]` | Unified status table: services, containers, schedules, health. |
| `plat apply` | Reconcile running state to config. Diff, stop removed, start new, clean orphans. |
| `plat run <name> [-- args...]` | Run an ephemeral container (one-shot worker). |
| `plat build [name...]` | Build container image(s). |
| `plat daemon` | Start control plane daemon (API server, scheduler, health, poller). |
| `plat logs <name> [-f] [-n lines]` | Tail logs for a service/container/schedule. |
| `plat exec <name> -- <cmd>` | Execute a command inside a running container. |
| `plat restart <name>` | Restart a single service or container. |
| `plat install` | Install plat daemon as a launchd agent (auto-start on login). |
| `plat uninstall` | Remove plat daemon from launchd. |
| `plat run-now <name>` | Trigger a scheduled job immediately. |
| `plat init` | Generate a starter `plat.yml` in the current directory. |

### Selective targeting

`plat up`, `plat down`, and `plat build` accept optional names to target specific
services/containers. Without names, they operate on everything in the config.

```bash
plat up api-server worker    # start only these two
plat down web-app            # stop just this one
plat build renderer          # build one image
```

## Control Skiff API

The daemon exposes two listeners:

1. **Unix socket** (`paths.socket`) — local access, file permission auth (0600)
2. **TCP** (`daemon.listen`, optional) — remote/programmatic access, bearer token auth

All routes return JSON.

### Control Routes

| Method | Path | Body | Response |
|--------|------|------|----------|
| GET | `/v1/status` | — | Full platform state |
| GET | `/v1/status/{name}` | — | Single service/container status |
| GET | `/v1/services` | — | All services with health |
| GET | `/v1/containers` | — | All containers with health |
| GET | `/v1/schedules` | — | All schedules with run history |
| GET | `/v1/schedule/{name}` | — | Single schedule detail |
| POST | `/v1/up` | `{ names?: [] }` | Start services/containers |
| POST | `/v1/down` | `{ names?: [] }` | Stop services/containers |
| POST | `/v1/apply` | — | Reconcile config to running state |
| POST | `/v1/restart/{name}` | — | Restart a service or container |
| POST | `/v1/run` | `{ name, args? }` | Run ephemeral container |
| POST | `/v1/build` | `{ names?: [] }` | Build container images |
| POST | `/v1/exec/{name}` | `{ command: [] }` | Exec in running container |
| POST | `/v1/schedule/{name}/run-now` | — | Trigger schedule immediately |
| GET | `/v1/logs/{name}` | `?lines=N&level=error` | Log entries from buffer |

### Authentication

- **Unix socket**: No token needed. Socket file permissions (0600) are the auth boundary.
- **TCP listener**: `Authorization: Bearer <token>` header required on all requests. Token set via `daemon.auth_token` in config (supports `${ENV_VAR}` resolution).

### Versioned API

All routes are prefixed with `/v1/`. This allows future breaking changes without disrupting existing clients.

## Service Networking

plat provides two networking layers: **embedded DNS** for container-to-container
service discovery, and a **reverse proxy** for external access to services.

### Embedded DNS (service discovery)

Containers can reach each other by name, just like docker-compose. plat runs
a lightweight DNS server that resolves service/container names to the host
gateway IP. Combined with port mapping, this gives containers transparent
connectivity to any other managed service.

```
+--container: api-----------+    +--container: frontend------+
|  resolv.conf:             |    |  resolv.conf:             |
|  nameserver 192.168.64.1  |    |  nameserver 192.168.64.1  |
|                           |    |                           |
|  connect to db:5432  -----|--+ |  connect to api:8080 -----|--+
+---------------------------+  | +---------------------------+  |
                               v                                v
+--plat daemon (host)-----------------------------------------------+
|  +-- DNS server (UDP :15353) ---+                                 |
|  |  db         -> 192.168.64.1  |  (host gateway for containers)  |
|  |  api        -> 192.168.64.1  |                                 |
|  |  frontend   -> 192.168.64.1  |                                 |
|  |  task-queue -> 192.168.64.1  |                                 |
|  |  *          -> forward to system DNS                           |
|  +------------------------------+                                 |
|                                                                   |
|  Port map: db:5432, api:8080, frontend:3000                       |
+-------------------------------------------------------------------+
```

#### How it works

1. plat daemon starts an embedded DNS server on a configurable port (default: 15353)
2. On container start, plat injects DNS config pointing to the host gateway:
   - Via `--dns` flag if the `container` CLI supports it
   - Fallback: mount a generated `/etc/resolv.conf` via volumes
3. DNS resolves all configured service/container names to the host gateway IP
4. Non-plat names (e.g., `google.com`) are forwarded to the system DNS resolver
5. Service names resolve under both bare name (`db`) and qualified (`db.plat.local`)

#### DNS config

```yaml
dns:
  enabled: true            # default: true when daemon is running
  port: 15353              # UDP port on host
  domain: plat.local       # search domain
  ttl: 5                   # seconds, low TTL for fast failover
```

#### Implementation

Uses `github.com/miekg/dns` (the library behind CoreDNS / k8s DNS). The server
is ~100 lines:

```go
type ServiceDNS struct {
    gateway  net.IP            // host gateway IP for containers
    records  map[string]net.IP // service name -> gateway (all same IP)
    upstream string            // system DNS for non-plat names
    domain   string            // "plat.local"
    ttl      uint32
}

func (d *ServiceDNS) ServeDNS(w dns.ResponseWriter, r *dns.Msg) {
    msg := new(dns.Msg)
    msg.SetReply(r)

    for _, q := range r.Question {
        // Match "db", "db.plat.local.", or "db.plat.local"
        name := d.extractServiceName(q.Name)
        if ip, ok := d.records[name]; ok && q.Qtype == dns.TypeA {
            msg.Answer = append(msg.Answer, &dns.A{
                Hdr: dns.RR_Header{
                    Name:   q.Name,
                    Rrtype: dns.TypeA,
                    Class:  dns.ClassINET,
                    Ttl:    d.ttl,
                },
                A: ip,
            })
        }
    }

    if len(msg.Answer) == 0 {
        // Forward to system resolver
        resp, err := dns.Exchange(r, d.upstream)
        if err == nil {
            resp.Id = r.Id
            w.WriteMsg(resp)
            return
        }
    }

    w.WriteMsg(msg)
}
```

#### Record updates

When the platform state changes (container starts/stops, `plat apply` adds/removes
services), the DNS server's record map is updated. The low TTL (5s) ensures
containers pick up changes quickly without caching stale records.

#### Gateway detection

plat detects the host gateway IP by inspecting the container runtime's network
configuration. For Apple Containers, this is the VM's gateway address (analogous
to Docker's `host.docker.internal` resolving to `192.168.65.254`). If detection
fails, plat falls back to the IP of the default network interface.

### Reverse Proxy (external access)

plat's TCP listener can also act as a reverse proxy, routing incoming requests
to services and containers based on path prefix. This lets external clients
reach container services through a single authenticated endpoint.

```yaml
proxy:
  routes:
    - path: /api
      target: api-server
      port: 8080
    - path: /app
      target: web-app
      port: 3000
```

#### How it works

1. Request arrives on TCP listener: `GET /api/users`
2. plat matches longest prefix: `/api` -> `api-server:8080`
3. Strips prefix, proxies to `http://localhost:8080/users`
4. Returns response to client

Implementation uses `net/http/httputil.ReverseProxy`. Proxy routes are only
served on the TCP listener (not the unix socket control API).

### Container networking summary

| From | To | Mechanism |
|---|---|---|
| Container -> Container | Embedded DNS resolves name to host gateway, port mapping routes to target |
| Container -> Native service | Same: DNS + host port |
| External client -> Service | TCP listener reverse proxy, or direct host port |
| CLI / local tool -> Control skiff | Unix socket or TCP with bearer token |

## Dependency Ordering

Containers and services can declare `depends_on` to control startup order.

```yaml
containers:
  api-server:
    depends_on: [task-queue, db-migrator]
```

**Startup**: plat builds a DAG from `depends_on`, starts leaves first, waits for
health check to pass (or process to be running if no health check) before starting
dependents. Circular dependencies are a config validation error.

**Shutdown**: reverse order — dependents stop before their dependencies.

`plat apply` respects ordering when restarting changed services.

## Status Model

```go
type ResourceState int

const (
    StateUnknown ResourceState = iota
    StateRunning
    StateStopped
    StateFailed
    StateStarting  // dependency waiting or health not yet passing
)

type ResourceType int

const (
    TypeService ResourceType = iota
    TypeContainer
    TypeSchedule
)

type ResourceStatus struct {
    Name        string        `json:"name"`
    Type        ResourceType  `json:"type"`
    State       ResourceState `json:"state"`
    PID         int           `json:"pid,omitempty"`
    UptimeSecs  int64         `json:"uptime_secs,omitempty"`
    ExitCode    int           `json:"exit_code,omitempty"`
    LastError   string        `json:"last_error,omitempty"`
    ConfigHash  string        `json:"config_hash"`
    Health      *HealthState  `json:"health,omitempty"`
    Ports       []string      `json:"ports,omitempty"`
    DependsOn   []string      `json:"depends_on,omitempty"`
}

type HealthState struct {
    Status           string    `json:"status"` // healthy | unhealthy | unknown
    ConsecutiveFails int       `json:"consecutive_fails"`
    LastCheck        time.Time `json:"last_check"`
    LastError        string    `json:"last_error,omitempty"`
}

type PlatformState struct {
    mu        sync.RWMutex
    Resources []ResourceStatus  `json:"resources"`
    Schedules []ScheduleStatus  `json:"schedules"`
    Timestamp time.Time         `json:"timestamp"`
}

type ScheduleStatus struct {
    Name       string     `json:"name"`
    LastRun    *time.Time `json:"last_run,omitempty"`
    NextRun    time.Time  `json:"next_run"`
    LastResult string     `json:"last_result"` // success | failed | running | pending
    LastError  string     `json:"last_error,omitempty"`
    Duration   int64      `json:"last_duration_ms,omitempty"`
}
```

## Internal Scheduler

One goroutine per schedule:

```go
func (s *Scheduler) runSchedule(ctx context.Context, name string, cfg ScheduleConfig) {
    for {
        nextRun := s.computeNextRun(name, cfg)
        select {
        case <-time.After(time.Until(nextRun)):
            s.execute(ctx, name, cfg)
        case <-s.triggerCh[name]: // manual run-now
            s.execute(ctx, name, cfg)
        case <-ctx.Done():
            return
        }
    }
}

func (s *Scheduler) execute(ctx context.Context, name string, cfg ScheduleConfig) {
    timeout := time.Duration(cfg.TimeoutSecs) * time.Second
    ctx, cancel := context.WithTimeout(ctx, timeout)
    defer cancel()

    start := time.Now()
    cmd := exec.CommandContext(ctx, cfg.Command[0], cfg.Command[1:]...)
    cmd.Dir = cfg.WorkingDir
    cmd.Env = buildEnv(cfg.Env)
    output, err := cmd.CombinedOutput()

    s.state.RecordRun(name, output, err, time.Since(start))
    s.logbuf.Append(name, output)
    s.persistState()
}
```

### State Persistence

`plat-state.json` stores `last_run`, `next_run`, and `last_result` per schedule.
On startup:
- If state file exists, load and compute `next_run` from persisted `last_run`
- If `next_run` is in the past (daemon was down), run immediately
- If no state file, first run for interval schedules = `now + interval`

### Calendar Intervals

Same semantics as launchd `StartCalendarInterval` — all specified fields must match.
Uses stdlib `time` to find the next matching datetime.

## Health Checks

Available for both native services and containers.

### Check Types

| Type | Healthy when | Example |
|---|---|---|
| `http` | GET returns 2xx | `url: "http://localhost:8080/health"` |
| `tcp` | Connect succeeds | `port: 5432` |
| `command` | Exit code 0 | `command: ["pg_isready"]` |

### State Machine

```
Unknown  -> Healthy    (first successful check)
Healthy  -> Unhealthy  (failure_threshold consecutive failures)
Unhealthy -> Healthy   (one success resets counter)
```

### Auto-restart

- `auto_restart: true` + unhealthy -> restart the service/container
- Cooldown: max one restart per 60s per resource
- `keep_alive: true` native services: log warning instead (launchd handles restart)
- All health transitions are logged

### Readiness vs Liveness

Health checks serve double duty:
- **Readiness**: during startup, `depends_on` waits for health to pass before starting dependents
- **Liveness**: at runtime, failed checks trigger auto-restart if configured

## Log Aggregation

```go
type LogBuffer struct {
    mu       sync.Mutex
    entries  []LogEntry // ring buffer
    maxLines int
}

type LogEntry struct {
    Timestamp time.Time `json:"timestamp"`
    Source    string    `json:"source"`
    Level    string    `json:"level"` // info | warn | error | unknown
    Message  string    `json:"message"`
}
```

| Source | How logs enter the buffer |
|---|---|
| Native services | Goroutine tails log file (seek to end, read new lines) |
| Long-running containers | Goroutine tails container log file |
| Scheduled jobs | Capture stdout/stderr from child process |
| Ephemeral containers | Not buffered (use `plat logs` for file-based access) |

Log level detection: prefix heuristic (`ERROR`, `WARN`, `INFO`, `DEBUG`, case-insensitive).

The `/v1/logs/{name}` endpoint supports filtering by level: `?level=error` returns
only error-level entries.

## Container Lifecycle

plat wraps the Apple Container Runtime `container` CLI.

### Long-running containers

Defined in `containers:` with no special flags. Started by `plat up`, stopped by
`plat down`. Restarted on health check failure if `auto_restart: true`.

```bash
# plat translates config to:
container run --name api-server \
  -v ~/platform/data:/data \
  -p 8080:8080 \
  -e DATABASE_URL=... \
  api-server:latest
```

### Ephemeral containers (workers)

Started on-demand via `plat run <name>`. Run to completion, capture output,
report exit code.

```bash
plat run renderer -- --input scene.json --output render.png
```

### Build

```bash
plat build                  # build all images
plat build api-server       # build one
```

Translates to `container build -t <image> -f <dockerfile> <context>`.

### Exec

```bash
plat exec api-server -- sh -c "cat /data/config.json"
```

Translates to `container exec api-server <cmd>`.

## Native Service Management (launchd)

For processes that run directly on the host (not in containers).

### Plist Generation

plat generates launchd plists for native services. Naming: `com.plat.<hostname>.<name>.plist`

Location: `~/Library/LaunchAgents/`

Generated plists include:
- Label, ProgramArguments, WorkingDirectory
- EnvironmentVariables (resolved `${VAR}` from process env)
- StandardOutPath / StandardErrorPath (in logs dir)
- KeepAlive (for `keep_alive: true`)
- ThrottleInterval: 10 (crash loop prevention)
- RunAtLoad: true

### Orphan Cleanup

`plat apply` scans `~/Library/LaunchAgents/` for `com.plat.*.plist` files not
in the current config and removes them (bootout + delete). The `com.plat.` prefix
is the ownership boundary.

## ProcessRunner Interface

All subprocess calls go through an interface for testability:

```go
type ProcessRunner interface {
    Run(ctx context.Context, name string, args []string, opts RunOpts) ([]byte, error)
}

type RunOpts struct {
    Dir    string
    Env    []string
    Stdin  io.Reader
    Stdout io.Writer
    Stderr io.Writer
}

type ExecRunner struct{}

func (r *ExecRunner) Run(ctx context.Context, name string, args []string, opts RunOpts) ([]byte, error) {
    cmd := exec.CommandContext(ctx, name, args...)
    cmd.Dir = opts.Dir
    cmd.Env = opts.Env
    return cmd.CombinedOutput()
}
```

## Security

- **Unix socket**: 0600 file permissions (owner only)
- **TCP listener**: bearer token auth required on all requests
- **Generated plists**: 0600 (may contain resolved secrets)
- **Env var resolution**: `${VAR}` syntax, resolved from process environment at generation time. Unresolved vars produce warnings.
- **No secrets in plat.yml**: always use `${ENV_VAR}` references
- **Name validation**: `^[a-zA-Z0-9_-]+$` on all service/container/schedule names (prevents path traversal)
- **Log endpoint**: validates name against config before serving
- **Socket liveness check**: daemon probes existing socket before replacing (prevents killing a running instance)

## Error Handling

Go idiom: return `error`, wrap with `fmt.Errorf("context: %w", err)`.

| Scenario | Behavior |
|---|---|
| Config parse error | Print error and `os.Exit(1)` (CLI), 400 (API) |
| launchctl failure | Capture stderr, report as `Failed` in status |
| Container failure | Capture exit code + stderr |
| Socket already in use | Report and exit with helpful message |
| Missing config | Report path searched, suggest `plat init` |
| Dependency cycle | Config validation error at parse time |
| Health check timeout | Counted as failure toward threshold |

## Testing

```bash
go test ./...
```

- Unit tests per package: config, plist, status, scheduler, health, dns, logbuf, proxy
- Integration tests: full CLI against dummy configs via `ProcessRunner` mock
- No real launchctl or container CLI in tests
- Table-driven tests (Go convention)
- Dependency DAG cycle detection tests

## Build & Install

```bash
go build -o plat ./cmd/plat    # build
go install ./cmd/plat           # install to $GOPATH/bin
./plat init                     # generate starter config
./plat daemon                   # start control plane
```

Single static binary, no runtime dependencies.

## Implementation Phases

```
Phase 0:  Scaffold + config parsing                          — 1 task
Phase 1:  Native service management (plist, up/down/apply)   — 2 tasks
Phase 2:  Container lifecycle (run/build/stop/exec)           — 2 tasks
Phase 3:  Daemon + control API (unix socket, status routes)   — 2 tasks
Phase 4:  SharedState + status poller                         — 1 task
Phase 5:  Internal scheduler                                  — 2 tasks
Phase 6:  Health checks + dependency ordering                 — 2 tasks
Phase 7:  Embedded DNS + container service discovery           — 2 tasks
Phase 8:  Log aggregation                                     — 1 task
Phase 9:  TCP listener + auth + reverse proxy                  — 2 tasks
Phase 10: Self-management (install/uninstall)                  — 1 task
```

Phases 0-3 deliver a functional CLI (compose-equivalent).
Phases 4-7 add the "more than compose" features (scheduler, health, DNS).
Phases 8-9 add observability and remote access.
Phase 10 enables self-hosting via launchd.

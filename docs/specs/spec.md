# skiff -- Container Orchestration for macOS

## Overview

skiff is a lightweight container orchestration layer for macOS, built around
Apple Container Runtime. It sits between docker-compose (declarative but
stateless) and Kubernetes (powerful but massive) -- offering health-aware
lifecycle management, internal scheduling, service networking, and a
programmatic control plane API, all from a single YAML file and binary.

skiff also manages native macOS services as child processes, making it a unified
control plane for everything running on a Mac: containers, daemons, and
scheduled jobs.

### What skiff adds over docker-compose

- Health checks with auto-restart and configurable probes
- Internal cron-like scheduler (no external cron/launchd plists needed)
- Control skiff API (unix socket or TCP) for programmatic access
- Embedded DNS for container-to-container service discovery
- Service networking with port exposure and proxy routing
- Startup dependency ordering with health-gated readiness
- Native macOS service management alongside containers
- Centralized log aggregation with in-memory ring buffers
- Config drift detection and declarative reconciliation

### What skiff intentionally omits

- Multi-node clustering and scheduling
- Horizontal scaling / replicas
- Load balancing across hosts
- Custom resource definitions
- RBAC and multi-tenant isolation

skiff is for a single machine running many services.

## Tech Stack

- **Language**: Go 1.22+
- **HTTP**: `net/http` + `net` (unix socket and TCP)
- **CLI**: `github.com/spf13/cobra`
- **Config**: `gopkg.in/yaml.v3`
- **Logging**: `log/slog` (structured, stdlib)
- **Process management**: `os/exec` with process groups
- **Plist**: `howett.net/plist` (daemon plist only)
- **DNS**: `github.com/miekg/dns` (embedded service discovery)
- **Terminal**: `github.com/fatih/color`

### External Dependencies

| Dependency | Purpose |
|---|---|
| `github.com/spf13/cobra` | CLI subcommands |
| `gopkg.in/yaml.v3` | YAML config parsing |
| `howett.net/plist` | Daemon plist generation |
| `github.com/miekg/dns` | Embedded DNS server |
| `github.com/fatih/color` | Terminal colors |

5 external dependencies. Tables use stdlib `text/tabwriter`.

## Architecture

### Service Supervision

The daemon owns all native services as child processes via `os/exec`.
There are NO per-service launchd plists. launchd is only used for the
daemon itself (`skiff install`).

If the daemon dies, native services die. launchd restarts daemon (KeepAlive),
daemon restarts services on startup (clean slate recovery).

### Container Runtime

Container operations go through a `ContainerRuntime` interface:

```go
type ContainerRuntime interface {
    Run(ctx, name, cfg) error
    Stop(ctx, name) error
    Build(ctx, name, cfg) error
    Exec(ctx, name, command) ([]byte, error)
    List(ctx) ([]ContainerInfo, error)
    Inspect(ctx, name) (*ContainerInfo, error)
    InjectDNS(cfg, dnsIP, dnsPort) ContainerConfig
    SetLimits(cfg, limits) ContainerConfig
}
```

v1 ships with `AppleRuntime`. Docker support can be added as a second
implementation without changing any other code.

### Concurrency Model

Standard Go patterns:

- HTTP server on unix socket + optional TCP listener
- StatusPoller: single goroutine, time.Ticker
- Scheduler: one goroutine per schedule
- HealthChecker: one goroutine per probe
- DNS server: single goroutine, UDP queries
- Shutdown: context.WithCancel propagated to all goroutines

SharedState is sync.RWMutex-protected.

## Configuration (skiff.yml)

```yaml
version: 1

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
  # listen: "127.0.0.1:9100"     # optional TCP
  # auth_token: "${SKIFF_AUTH_TOKEN}"  # required when listen is set

dns:
  enabled: true
  port: 15353
  domain: skiff.local
  ttl: 5

services:
  task-queue:
    command: [".venv/bin/huey_consumer", "tasks.huey", "-w", "4"]
    working_dir: ~/platform
    restart_policy: always       # always | on-failure | never
    max_restarts: 0              # 0 = unlimited
    restart_backoff_secs: 5      # doubles per crash, caps at 60s
    env:
      DATABASE_URL: "${DATABASE_URL}"
    log_file: task-queue.log
    health_check:
      type: tcp
      port: 8001
      interval_secs: 30
      failure_threshold: 3

containers:
  api-server:
    image: api-server:latest
    dockerfile: containers/Dockerfile.api
    context: containers/          # optional, defaults to dockerfile dir
    volumes:
      - ~/platform/data:/data
    env:
      DATABASE_URL: "postgres://user:pass@db:5432/app"
    ports:
      - "8080:8080"
    cpus: 2.0
    memory: "1g"
    health_check:
      type: http
      url: "http://localhost:8080/health"
      interval_secs: 15
      failure_threshold: 3
      auto_restart: true
    depends_on:
      - task-queue

schedules:
  data-sync:
    command: ["python", "scripts/sync.py"]
    working_dir: ~/platform
    interval_seconds: 3600
    log_file: sync.log
    timeout_secs: 300

proxy:
  routes:
    - path: /api
      target: api-server
      port: 8080
```

### Environment Variables

- `${VAR}` syntax resolved from process environment
- `.env` file in same directory as skiff.yml loaded as fallback
- Process env vars take precedence over .env values
- `.env` is optional (missing file is not an error)

## CLI Commands

| Command | Description |
|---------|-------------|
| `skiff up [name...]` | Idempotent: start missing, restart changed, leave running |
| `skiff up --build [name...]` | Build images first, then start |
| `skiff down [name...]` | Stop + remove containers. Native services just stop |
| `skiff down --volumes` | Also remove container volumes |
| `skiff stop [name...]` | Graceful stop (SIGTERM). Containers preserved |
| `skiff kill [name...]` | Force stop (SIGKILL) |
| `skiff ps [--json]` | Primary status command |
| `skiff status [--json]` | Alias for ps |
| `skiff apply [--dry-run]` | Reconcile: like up + remove orphans |
| `skiff restart <name>` | Restart a single resource |
| `skiff build [name...]` | Build container images |
| `skiff run <name> [-- args...]` | Ephemeral container (synchronous) |
| `skiff exec <name> -- <cmd>` | Exec in running container |
| `skiff logs <name> [-f] [-n N]` | Tail logs |
| `skiff config` | Validate + print resolved config |
| `skiff config --validate-only` | Exit 0 if valid, 1 if invalid |
| `skiff daemon [-d]` | Start control plane |
| `skiff install` | Install daemon as launchd agent |
| `skiff uninstall` | Remove daemon from launchd |
| `skiff run-now <name>` | Trigger scheduled job |
| `skiff init` | Generate starter config |

### Key Semantics

- `up` is idempotent and the primary "make it so" command
- `apply` = `up` + orphan cleanup
- `stop` = graceful stop, resources preserved
- `down` = stop + remove containers (destructive)
- `kill` = SIGKILL (emergency stop)
- `ps` is primary, `status` is alias

## Control Skiff API

Unix socket (file perms auth) + optional TCP (bearer token auth).
All routes return JSON, prefixed with `/v1/`.

### Routes

| Method | Path | Description |
|--------|------|-------------|
| GET | `/v1/status` | Full platform state |
| GET | `/v1/status/{name}` | Single resource status |
| GET | `/v1/services` | All services |
| GET | `/v1/containers` | All containers |
| GET | `/v1/schedules` | All schedules |
| POST | `/v1/up` | Start resources |
| POST | `/v1/down` | Stop resources |
| POST | `/v1/apply` | Reconcile config |
| POST | `/v1/restart/{name}` | Restart resource |
| POST | `/v1/run` | Run ephemeral container |
| POST | `/v1/build` | Build images |
| POST | `/v1/exec/{name}` | Exec in container |
| POST | `/v1/schedule/{name}/run-now` | Trigger schedule |
| GET | `/v1/logs/{name}` | Log entries |
| GET | `/v1/health` | Daemon health |

## Dependency Ordering

Unified DAG across services and containers. Schedules are NOT part of
the DAG. Cycle detection at config validation time.

```yaml
containers:
  api:
    depends_on: [worker]   # worker is a service -- cross-type works
services:
  worker:
    depends_on: [db]       # db is a container -- cross-type works
```

Startup: leaves first, wait for health before starting dependents.
Shutdown: reverse order.

## Health Checks

Types: `http` (GET returns 2xx), `tcp` (connect succeeds), `command` (exit 0).

State machine:
- Unknown -> Healthy (first success)
- Healthy -> Unhealthy (failure_threshold consecutive failures)
- Unhealthy -> Healthy (one success resets)

`auto_restart: true` + unhealthy triggers restart (max 1 per 60s per resource).

## State Recovery

On daemon startup: clean slate.
1. Acquire state file lock (flock)
2. Stop any running containers
3. Load config, validate
4. Start everything from scratch per dependency order
5. Resume schedulers from persisted state

## Security

- Unix socket: 0600 file permissions
- TCP: bearer token required (config error if listen set without auth_token)
- Non-localhost binding requires `allow_remote: true`
- Volume paths must not contain `..`
- Name validation: `^[a-zA-Z0-9_-]+$`
- State file locking prevents concurrent daemons

## Graceful Shutdown

On SIGTERM/SIGINT:
1. Cancel root context
2. SIGTERM to all child process groups
3. Wait up to shutdown_timeout_secs (default: 30)
4. SIGKILL remaining processes
5. Stop containers via runtime
6. Clean up socket file

## Project Layout

```
cmd/skiff/main.go          -- CLI entrypoint (cobra)
internal/
  config/config.go         -- skiff.yml parsing, validation, env resolution
  daemon/daemon.go         -- HTTP server, lifecycle orchestration
  daemon/routes.go         -- API route handlers
  daemon/proxy.go          -- reverse proxy
  dns/dns.go               -- embedded DNS server
  runtime/runtime.go       -- ContainerRuntime interface
  runtime/apple.go         -- Apple Container Runtime implementation
  supervisor/supervisor.go -- native service process management
  plist/plist.go           -- daemon-only launchd plist
  scheduler/scheduler.go   -- internal schedule runner
  health/health.go         -- health check probes
  status/status.go         -- SharedState + status types
  logbuf/logbuf.go         -- ring buffer log aggregation
  runner/runner.go         -- ProcessRunner interface
config/skiff.example.yml   -- annotated example config
docs/specs/spec.md         -- this file
```

## Implementation Phases

```
Phase 0:  Scaffold + config + ContainerRuntime interface       [done]
Phase 1:  Process supervisor (spawn, signal, restart, backoff) [done]
Phase 2:  Container lifecycle via runtime interface             [done]
Phase 3:  Daemon + control API (unix socket, routes, auto-start)[done]
Phase 4:  SharedState + status types + state file locking       [done]
Phase 5:  Internal scheduler + state persistence                [done]
Phase 6:  Health checks + cross-type dependency ordering        [done]
Phase 7:  Embedded DNS + service discovery                      [done]
Phase 8:  Log aggregation                                       [done]
Phase 9:  TCP listener + auth + reverse proxy + security        [done]
Phase 10: Self-management (install/uninstall, daemonize, PID)   [done]
Phase 11: Config watch + apply reconciliation + --dry-run       [done]
```

## Pre-implementation Research Required

**Apple Container Runtime CLI**: Before production use, research:
- Exact binary name and location
- `run` command flags (name, volumes, ports, env, DNS)
- `build` command flags (tag, dockerfile, context)
- Port mapping behavior and network model
- Volume mount syntax and limitations
- Resource limit flags (CPU, memory)

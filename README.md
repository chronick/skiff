# skiff

Container orchestration for macOS. Single binary, single YAML config.

skiff sits between docker-compose and Kubernetes — health-aware lifecycle management, scheduling, service discovery, and a control plane API, built around [Apple Container Runtime](https://github.com/apple/container).

It also manages native macOS services as child processes, making it a unified control plane for containers, daemons, and scheduled jobs on a single Mac.

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/chronick/skiff/main/install.sh | bash
```

Or build from source:

```bash
git clone https://github.com/chronick/skiff.git
cd skiff
make install
```

## Quick Start

```bash
# Generate a starter config
skiff init

# Start everything
skiff up

# Check status
skiff ps

# View logs
skiff logs <name>

# Interactive dashboard
skiff tui
```

## Configuration

skiff uses a single `skiff.yml` file. Config is searched in order:

1. `-c` / `--config` flag
2. `./skiff.yml`
3. `./config/skiff.yml`
4. `~/.config/skiff/config.yml`

```yaml
version: 1

paths:
  base: ~/platform
  socket: ~/platform/skiff.sock
  logs: ~/platform/logs
  state_file: ~/platform/skiff-state.json

daemon:
  config_watch: true
  shutdown_timeout_secs: 30

dns:
  enabled: true
  port: 15353
  domain: skiff.local

services:
  worker:
    command: ["python", "-m", "worker"]
    working_dir: ~/platform
    restart_policy: always
    health_check:
      type: tcp
      port: 8001
      interval_secs: 30

containers:
  api:
    image: api-server:latest
    dockerfile: containers/Dockerfile.api
    volumes:
      - ~/platform/data:/data
    ports:
      - "8080:8080"
    health_check:
      type: http
      url: "http://localhost:8080/health"
      interval_secs: 15
      auto_restart: true
    depends_on:
      - worker

schedules:
  cleanup:
    command: ["python", "scripts/cleanup.py"]
    working_dir: ~/platform
    calendar:
      hour: 3
      minute: 0
```

See [`config/skiff.example.yml`](config/skiff.example.yml) for a fully annotated example.

## Features

**Containers** — Build and run via Apple Container Runtime. Volumes, ports, env vars, CPU/memory limits, networks, labels.

**Native Services** — Manage any process as a child of the daemon. Restart policies (always/on-failure/never), exponential backoff, process group signaling.

**Health Checks** — HTTP, TCP, or command probes with configurable intervals and failure thresholds. Auto-restart on unhealthy.

**Dependency Ordering** — Unified DAG across services and containers. Health-gated startup — dependents wait until dependencies are healthy.

**Scheduler** — Built-in cron-like scheduling with interval or calendar syntax. No external cron or launchd plists needed.

**DNS** — Embedded DNS server for container-to-container service discovery (`<name>.skiff.local`).

**Control API** — Unix socket (default) or TCP with bearer token auth. All operations available programmatically via `/v1/` routes.

**Config Reconciliation** — `skiff apply` detects config drift and reconciles: starts missing resources, restarts changed ones, removes orphans.

**Log Aggregation** — In-memory ring buffers per resource. `skiff logs <name>` tails from the buffer.

**TUI Dashboard** — Interactive terminal UI for monitoring all resources.

**Menu Bar App** — macOS menu bar status indicator.

## CLI

```
skiff up [name...]          Start services and containers
skiff down [name...]        Stop and remove containers
skiff stop [name...]        Graceful stop (SIGTERM)
skiff kill [name...]        Force stop (SIGKILL)
skiff restart <name>        Restart a resource
skiff ps                    Show status of all resources
skiff stats [name]          Container CPU/memory stats
skiff logs <name> [-f]      Tail logs
skiff apply [--dry-run]     Reconcile running state to config
skiff build [name...]       Build container images
skiff run <name>            Run ephemeral container
skiff exec <name> -- <cmd>  Exec in running container
skiff run-now <name>        Trigger scheduled job
skiff config                Validate and print config
skiff daemon [-d]           Start control plane
skiff install               Install as launchd agent
skiff uninstall             Remove from launchd
skiff tui                   Interactive dashboard
skiff init                  Generate starter config
```

## Boot on Login

```bash
# Install daemon + menu bar as launchd agents
skiff install

# Remove
skiff uninstall
```

## Requirements

- macOS with [Apple Container Runtime](https://github.com/apple/container)
- Go 1.22+ (build from source only)

## License

MIT

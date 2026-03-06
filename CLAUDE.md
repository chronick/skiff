# CLAUDE.md

## Project Overview

**skiff** -- Container orchestration for macOS. Single binary, single YAML config.

## Tech Stack

- Go 1.22+, 5 external deps (cobra, yaml.v3, plist, miekg/dns, fatih/color)
- Process supervision via os/exec (NOT per-service launchd plists)
- Apple Container Runtime via abstract ContainerRuntime interface

## Commands

```bash
go build -o skiff ./cmd/skiff    # build
go test ./...                     # test all packages
go build ./...                    # check compilation
./skiff init                      # generate starter config
./skiff daemon                    # start daemon (foreground)
./skiff daemon -d                 # start daemon (background)
./skiff up                        # start all resources
./skiff ps                        # status table
./skiff apply --dry-run           # preview changes
./skiff config --validate-only    # validate config (CI)
```

## Architecture

- `cmd/skiff/main.go` -- cobra CLI, talks to daemon via unix socket
- `internal/config/` -- skiff.yml parsing, validation, env resolution, .env support
- `internal/daemon/` -- HTTP server (unix socket + TCP), API routes, reverse proxy
- `internal/supervisor/` -- native service process management (spawn, signal, restart)
- `internal/runtime/` -- ContainerRuntime interface + Apple implementation
- `internal/scheduler/` -- internal cron-like scheduler
- `internal/health/` -- HTTP/TCP/command health probes
- `internal/status/` -- SharedState (sync.RWMutex-protected)
- `internal/dns/` -- embedded DNS for service discovery
- `internal/logbuf/` -- ring buffer log aggregation
- `internal/plist/` -- daemon-only launchd plist (skiff install/uninstall)
- `internal/runner/` -- ProcessRunner interface for testability

## Key Design Decisions

- Daemon owns all child processes; if daemon dies, services die
- Clean slate recovery on daemon restart
- Cross-type dependency DAG (services can depend on containers and vice versa)
- Schedules are NOT part of the dependency DAG
- State file locking (flock) prevents concurrent daemons
- TCP listener requires auth_token (validation error otherwise)

## Spec

Source of truth: `docs/specs/spec.md`

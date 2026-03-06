# CLAUDE.md

## Project

**skiff** -- Container orchestration for macOS. Single binary, single YAML config.

Go 1.22+, 5 external deps (cobra, yaml.v3, plist, miekg/dns, fatih/color).

## Commands

```bash
go build -o skiff ./cmd/skiff    # build
go test ./...                     # test all
go build ./...                    # check compilation
```

## Layout

```
cmd/skiff/main.go       -- cobra CLI entry point
internal/
  config/               -- skiff.yml parsing, validation, env/.env resolution
  daemon/               -- HTTP server (unix socket + TCP), API routes, reverse proxy
  supervisor/           -- native service process management (os/exec)
  runtime/              -- ContainerRuntime interface + Apple implementation
  scheduler/            -- cron-like scheduler
  health/               -- HTTP/TCP/command health probes
  status/               -- SharedState (sync.RWMutex)
  dns/                  -- embedded DNS for service discovery
  logbuf/               -- ring buffer log aggregation
  plist/                -- launchd plist (daemon install/uninstall only)
  runner/               -- ProcessRunner interface for testability
config/skiff.example.yml
```

## Key Design

- Daemon owns all child processes; daemon dies → services die
- Clean slate recovery on daemon restart
- Cross-type dependency DAG (services ↔ containers); schedules excluded
- State file locking (flock) prevents concurrent daemons
- TCP listener requires auth_token

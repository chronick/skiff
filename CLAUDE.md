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

<!-- br-agent-instructions-v1 -->

---

## Beads Workflow Integration

This project uses [beads_rust](https://github.com/Dicklesworthstone/beads_rust) (`br`/`bd`) for issue tracking. Issues are stored in `.beads/` and tracked in git.

### Essential Commands

```bash
# View ready issues (unblocked, not deferred)
br ready              # or: bd ready

# List and search
br list --status=open # All open issues
br show <id>          # Full issue details with dependencies
br search "keyword"   # Full-text search

# Create and update
br create --title="..." --description="..." --type=task --priority=2
br update <id> --status=in_progress
br close <id> --reason="Completed"
br close <id1> <id2>  # Close multiple issues at once

# Sync with git
br sync --flush-only  # Export DB to JSONL
br sync --status      # Check sync status
```

### Workflow Pattern

1. **Start**: Run `br ready` to find actionable work
2. **Claim**: Use `br update <id> --status=in_progress`
3. **Work**: Implement the task
4. **Complete**: Use `br close <id>`
5. **Sync**: Always run `br sync --flush-only` at session end

### Key Concepts

- **Dependencies**: Issues can block other issues. `br ready` shows only unblocked work.
- **Priority**: P0=critical, P1=high, P2=medium, P3=low, P4=backlog (use numbers 0-4, not words)
- **Types**: task, bug, feature, epic, chore, docs, question
- **Blocking**: `br dep add <issue> <depends-on>` to add dependencies

### Session Protocol

**Before ending any session, run this checklist:**

```bash
git status              # Check what changed
git add <files>         # Stage code changes
br sync --flush-only    # Export beads changes to JSONL
git commit -m "..."     # Commit everything
git push                # Push to remote
```

### Best Practices

- Check `br ready` at session start to find available work
- Update status as you work (in_progress → closed)
- Create new issues with `br create` when you discover tasks
- Use descriptive titles and set appropriate priority/type
- Always sync before ending session

<!-- end-br-agent-instructions -->

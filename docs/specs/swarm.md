# Agent Swarm Orchestration -- Multi-Tool Architecture

## Overview

Running multiple AI coding agents on a single Mac, each in an isolated
Apple container with its own git worktree. The system is composed of
small, independent tools following the flywheel pattern.

### Target Use Cases

- Run 2-8 coding agents against a GitHub repo, each on separate tasks
- Support an OpenClaw installation as the agent runtime inside containers
- Agentic CI: spawn agents to fix failing tests or review code
- Batch refactors: decompose a large change into N parallel agent tasks

## Tool Responsibilities

```
skiff           Container orchestration (lifecycle, health, DNS, scaling)
hoist           Git worktree lifecycle (create, destroy, PR, merge)
bosun           Agent entrypoint/coordinator (claim task, run agent, report)
beads (br)      Task tracking, priority, dependency DAG, ready queue
agent-mail      Inter-agent messaging, file leases, audit trails
ntm             Multi-agent tmux dashboard (optional, for interactive use)
```

Each tool does one thing. They compose via CLI calls and shared conventions.

## Architecture

```
skiff daemon
  |-- container lifecycle, health, DNS, logs, stats
  |-- replicas: N containers from one template
  |
  |  +-----------+  +-----------+  +-----------+
  |  | coder-1   |  | coder-2   |  | coder-3   |
  |  |           |  |           |  |           |
  |  | bosun     |  | bosun     |  | bosun     |
  |  |  -> br    |  |  -> br    |  |  -> br    |
  |  |  -> hoist |  |  -> hoist |  |  -> hoist |
  |  |  -> mail  |  |  -> mail  |  |  -> mail  |
  |  |  -> agent |  |  -> agent |  |  -> agent |
  |  +-----------+  +-----------+  +-----------+
  |       |              |              |
  |  ~/worktrees/   ~/worktrees/  ~/worktrees/
  |    coder-1/      coder-2/      coder-3/

Outside skiff (native services or separate containers):
  agent-mail server (port 8765)
  beads DB (shared .beads/ dir)
```

## What skiff gains (minimal)

Only `replicas` + `{name}` template expansion in existing container config:

```yaml
containers:
  coder:
    image: agent-coder:latest
    replicas: 4
    cpus: 2.0
    memory: "4g"
    volumes:
      - ~/worktrees/{name}:/workspace
    env:
      AGENT_NAME: "{name}"
    health_check:
      type: command
      command: ["pgrep", "-f", "agent"]
      interval_secs: 30
      auto_restart: true
```

At config parse time, `coder` with `replicas: 4` expands to `coder-1`,
`coder-2`, `coder-3`, `coder-4` -- each a regular container with `{name}`
substituted in volumes and env. Everything else (health, DNS, logs, restart,
stats, dependency ordering) works unchanged.

New API:
- `POST /v1/scale` -- `{name: "coder", replicas: N}` adjusts count at runtime
- `GET /v1/replicas` -- returns replica groups with status summary

New CLI:
- `skiff scale <name> <n>` -- adjust replica count
- `skiff ps --group` -- show replicas grouped by template

## hoist -- Git Worktree Manager

Standalone CLI for managing git worktrees in parallel agent workflows.

### Commands

```bash
hoist init <repo-path>                 # register a repo for worktree management
hoist create <branch> [--base=main]    # create worktree + branch
hoist destroy <branch>                 # remove worktree and optionally delete branch
hoist list                             # show active worktrees
hoist reset <branch> [--to=main]       # reset worktree to base branch
hoist pr <branch> --title "..."        # create PR via gh CLI
hoist merge <branch> [--into=main]     # fast-forward merge to target
hoist status                           # show all worktrees with dirty/clean state
```

### Design

- Wraps `git worktree add/remove/list` and `gh pr create`
- No daemon, no config file, no database
- Works with any git repo, not tied to skiff or agents
- Worktrees stored under configurable base dir (default: `.worktrees/`)
- Branch naming convention: configurable prefix (default: `agent/`)
- Go binary, single file, zero dependencies beyond git and gh

### Example Flow

```bash
# Human or bosun sets up worktrees
hoist init ~/repos/myapp
hoist create agent/coder-1
hoist create agent/coder-2

# Agent works in ~/repos/myapp/.worktrees/agent-coder-1/
# ...

# On completion
hoist pr agent/coder-1 --title "Fix auth bug"
hoist destroy agent/coder-1
```

## bosun -- Agent Entrypoint

Thin coordinator that runs inside each agent container on boot.
Sequences the agent lifecycle by calling other tools.

### Lifecycle

```bash
#!/bin/bash
# bosun boot sequence

# 1. Identity
export AGENT_NAME="${AGENT_NAME:-$(hostname)}"
bosun register                    # announce to agent-mail

# 2. Workspace
cd /workspace                     # volume-mounted hoist worktree

# 3. Task loop
while true; do
  TASK=$(bosun claim)             # br ready | pick highest priority
  if [ -z "$TASK" ]; then
    sleep 30                      # no work available
    continue
  fi

  bosun lease "$TASK"             # acquire file lease via agent-mail
  br update "$TASK" --status=in_progress

  # 4. Run the actual agent
  $AGENT_COMMAND /workspace       # claude-code, openclaw, etc.
  EXIT_CODE=$?

  # 5. Report
  if [ $EXIT_CODE -eq 0 ]; then
    br close "$TASK" --reason="Completed by $AGENT_NAME"
    hoist pr "agent/$AGENT_NAME" --title "$(br show $TASK --field=title)"
  else
    br update "$TASK" --status=open  # put it back
  fi

  bosun release "$TASK"           # release file lease
  hoist reset "agent/$AGENT_NAME" # reset worktree to main
done
```

### Design

- Shell script initially, graduate to Go/Rust binary if needed
- Knows how to call: `br` (beads), `hoist`, `agent-mail` CLI
- Does NOT embed any of their logic -- just sequences CLI calls
- Configuration via environment variables:
  - `AGENT_NAME` -- identity
  - `AGENT_COMMAND` -- what agent runtime to invoke
  - `AGENT_MAIL_URL` -- agent-mail server address
  - `BEADS_DB` -- path to beads database
  - `TASK_FILTER` -- optional beads query filter
- Heartbeat: periodic `bosun heartbeat` pings agent-mail
- Graceful shutdown: trap SIGTERM, release leases, update task status

### What bosun is NOT

- Not an agent runtime (that's OpenClaw/Claude Code/Codex)
- Not a task tracker (that's Beads)
- Not a messaging system (that's Agent Mail)
- Not a git manager (that's Hoist)
- Not a container orchestrator (that's Skiff)

## Integration Example

Full `skiff.yml` for an agent swarm:

```yaml
version: 1

dns:
  enabled: true
  domain: skiff.local

services:
  agent-mail:
    command: ["agent-mail-server", "--port", "8765"]
    restart_policy: always
    health_check:
      type: tcp
      port: 8765

containers:
  coder:
    image: agent-coder:latest
    replicas: 3
    cpus: 2.0
    memory: "4g"
    volumes:
      - ~/worktrees/{name}:/workspace
      - ~/.ssh:/root/.ssh:ro
      - ~/repos/myapp/.beads:/workspace/.beads:ro
    env:
      AGENT_NAME: "{name}"
      AGENT_COMMAND: "claude-code"
      AGENT_MAIL_URL: "http://agent-mail.skiff.local:8765"
      ANTHROPIC_API_KEY: "${ANTHROPIC_API_KEY}"
    depends_on:
      - agent-mail
    health_check:
      type: command
      command: ["pgrep", "-f", "bosun"]
      interval_secs: 30
      auto_restart: true
```

Setup:
```bash
# Prepare worktrees
hoist init ~/repos/myapp
hoist create agent/coder-1
hoist create agent/coder-2
hoist create agent/coder-3

# Start everything
skiff up
# agent-mail starts first (dependency)
# coder-1, coder-2, coder-3 start after mail is healthy
# each container runs bosun -> claims tasks -> runs agent
```

## Container-in-Container: How Agents Spawn Containers

### The Problem

OpenClaw (and other agent runtimes) need to spawn sandbox containers for
isolated code execution, browser automation, etc. But Apple containers
are Linux VMs managed by `containermanagerd` via **Mach IPC** -- there is
no unix socket to mount (unlike Docker's `/var/run/docker.sock`). The
`container` CLI is a macOS binary that cannot run inside a Linux guest.

### The Solution: Skiff API as Container Gateway

Agents spawn sibling containers by calling skiff's HTTP API. Skiff already
runs on the host with full access to `containermanagerd`.

```
Agent container (Linux)
  |
  | curl http://skiff-api.skiff.local:9100/v1/containers/run
  |
  v
Skiff daemon (macOS host)
  |
  | container run --name sandbox-coder-1-1709... ...
  |
  v
containermanagerd (Mach IPC)
  |
  v
New sibling container (Linux VM)
```

### Required Skiff Changes

**1. Ad-hoc container creation (`POST /v1/containers/run`)**

Current `/v1/run` requires the container name to exist in `skiff.yml`.
Need a new route that accepts a full container spec inline:

```
POST /v1/containers/run
{
  "image": "ubuntu:22.04",
  "name": "sandbox-coder-1",        // optional, auto-generated if empty
  "command": ["bash", "-c", "..."],
  "volumes": ["/workspace:/workspace"],
  "env": {"FOO": "bar"},
  "cpus": 1.0,
  "memory": "512m",
  "labels": {"skiff.parent": "coder-1"},
  "timeout_secs": 300,              // auto-kill after timeout
  "remove": true                    // auto-remove on exit
}
```

Returns: `{"name": "sandbox-coder-1", "status": "started"}`

**2. Parent-child container tracking**

Spawned containers get `skiff.parent={agent-name}` label automatically.
When a parent agent container stops, all its child containers are killed.
Prevents orphaned sandbox containers.

**3. Container quota per agent**

Optional limit on how many containers an agent can spawn:

```yaml
containers:
  coder:
    replicas: 3
    max_child_containers: 5    # each replica can spawn up to 5 sandboxes
```

**4. Auth scoping**

When TCP listener is enabled, agent containers get a scoped token that
can only create/manage containers with their own name as parent label.
Agents cannot stop other agents' containers.

### OpenClaw Integration

OpenClaw's sandboxing is **hardcoded to Docker/Podman**. It uses:
- `agents.defaults.sandbox.docker.*` configuration
- Docker socket (`OPENCLAW_DOCKER_SOCKET` or `/var/run/docker.sock`)
- Docker CLI/API to spawn sandbox containers

Apple containers use Mach IPC, not a Docker socket. Three deployment options:

#### Option A: Self-Contained Agents, No Docker Sandbox (simplest)

Each agent runs in its own Apple container. The container IS the sandbox.
Set `sandbox.mode: "off"` -- OpenClaw's Docker sandboxing is redundant when
the agent is already isolated in a container.

```yaml
daemon:
  listen: "127.0.0.1:9100"
  auth_token: "${SKIFF_AUTH_TOKEN}"

containers:
  coder:
    image: agent-coder:latest
    replicas: 3
    cpus: 2.0
    memory: "4g"
    volumes:
      - ~/worktrees/{name}:/workspace
      - ~/.ssh:/root/.ssh:ro
    env:
      AGENT_NAME: "{name}"
      SKIFF_API_URL: "http://host.skiff.local:9100"
      SKIFF_AUTH_TOKEN: "${SKIFF_AUTH_TOKEN}"
      ANTHROPIC_API_KEY: "${ANTHROPIC_API_KEY}"
```

OpenClaw config inside the container (`/root/.openclaw/openclaw.json`):
```json5
{
  agents: {
    defaults: {
      sandbox: { mode: "off" },  // container IS the sandbox
      workspace: "/workspace",
    },
  },
  gateway: { mode: "local" },
}
```

If an agent needs a child container (browser automation, isolated exec),
it calls skiff's API:
```bash
curl -X POST http://host.skiff.local:9100/v1/containers/run \
  -H "Authorization: Bearer $SKIFF_AUTH_TOKEN" \
  -d '{"image":"openclaw-sandbox-browser:bookworm-slim","name":"browser-coder-1"}'
```

#### Option B: Gateway on Host + gangway Docker Shim

Run OpenClaw Gateway natively on macOS. For sandbox support, use `gangway`
(a Docker API shim that translates Docker API calls to Apple container CLI).

```yaml
services:
  openclaw:
    command: ["openclaw", "gateway"]
    working_dir: ~/openclaw
    restart_policy: always
    env:
      OPENCLAW_DOCKER_SOCKET: "~/platform/gangway.sock"
    health_check:
      type: tcp
      port: 18789

  gangway:
    command: ["gangway", "--socket", "~/platform/gangway.sock"]
    restart_policy: always
    health_check:
      type: command
      command: ["test", "-S", "~/platform/gangway.sock"]
```

OpenClaw config:
```json5
{
  agents: {
    defaults: {
      sandbox: {
        mode: "non-main",
        scope: "session",
        docker: {
          image: "openclaw-sandbox:bookworm-slim",
          network: "none",
        },
      },
    },
  },
}
```

OpenClaw thinks it's talking to Docker. gangway translates to `container`
CLI calls. Sandbox containers are Apple containers, not Docker containers.

#### Option C: Gateway on Host + Podman

Run Podman on macOS (uses its own Linux VM). OpenClaw's sandbox works
unmodified. Downside: two VM layers (Podman VM + Apple container VMs).

```yaml
services:
  openclaw:
    command: ["openclaw", "gateway"]
    working_dir: ~/openclaw
    restart_policy: always
    env:
      OPENCLAW_DOCKER_SOCKET: "${HOME}/.local/share/containers/podman/machine/podman.sock"
```

Works but wasteful -- Podman's VM duplicates what Apple containers already do.

### Recommended Path

**Start with Option A.** Each agent in its own Apple container with
`sandbox.mode: "off"`. Use skiff API for child containers. Zero new
dependencies.

**Graduate to Option B** if you need OpenClaw's full sandbox tooling
(per-session containers, sandbox browser, workspace scoping). This requires
building `gangway` -- a new tool in the flywheel.

### gangway -- Docker API Shim for Apple Containers (future)

A small Go binary that:
- Listens on a unix socket (mimics `/var/run/docker.sock`)
- Implements the subset of Docker Engine API that OpenClaw uses:
  - `POST /containers/create`
  - `POST /containers/{id}/start`
  - `POST /containers/{id}/stop`
  - `POST /containers/{id}/exec`
  - `GET /containers/{id}/logs`
  - `DELETE /containers/{id}`
  - `GET /containers/json` (list)
  - `GET /_ping`
- Translates each call to Apple `container` CLI commands
- Labels and tracks containers for cleanup

This makes ANY Docker-expecting tool work with Apple containers. Useful
beyond just OpenClaw -- any Node.js dockerode client, CI tools, etc.

Not needed for Phase 1. Build it when Option A hits its limits.

### Container Spawning API (full)

| Method | Path | Description |
|--------|------|-------------|
| POST | `/v1/containers/run` | Spawn ad-hoc container from inline spec |
| GET | `/v1/containers/{name}` | Status of spawned container |
| POST | `/v1/containers/{name}/exec` | Exec in spawned container |
| GET | `/v1/containers/{name}/logs` | Logs from spawned container |
| POST | `/v1/containers/{name}/stop` | Stop spawned container |
| DELETE | `/v1/containers/{name}` | Remove spawned container |
| GET | `/v1/containers?parent={agent}` | List containers spawned by agent |

### Why Not Other Approaches

| Approach | Why it doesn't work |
|----------|-------------------|
| Mount container socket | Apple uses Mach IPC, not unix sockets |
| `container` CLI in Linux guest | macOS binary, won't run in Linux |
| `--virtualization` flag | Exposes hypervisor, not Apple container runtime |
| Docker-in-Docker | No Docker on target platform |
| Nested Apple containers | Not supported by containermanagerd |

## Tool Comparison

| Concern | Tool | Why separate? |
|---------|------|--------------|
| Container lifecycle | skiff | Already exists, does this well |
| Container spawning | skiff (API) | Only tool with host container access |
| Git worktrees | hoist | Useful without containers (human workflows too) |
| Task tracking | beads | Already exists, domain-specific |
| Agent messaging | agent-mail | Already exists, multi-tool |
| Agent sequencing | bosun | Glue logic, changes fastest |
| Agent runtime | openclaw/cc | User's choice, not our code |

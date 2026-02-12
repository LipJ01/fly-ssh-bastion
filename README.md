# Fly SSH Bastion

SSH bastion server on [Fly.io](https://fly.io) that lets you access your machines from anywhere using reverse SSH tunnels. Register a machine once, and SSH into it globally — including from mobile clients like [Blink Shell](https://blink.sh).

## How It Works

```
┌──────────┐       reverse tunnel        ┌─────────────────────┐
│  Machine  │ ──────────────────────────▶ │   Fly SSH Bastion   │
│  (home)   │   port 2222 (sshd)         │                     │
└──────────┘                              │  sshpiper (:2223)   │
                                          │  sshd    (:2222)    │
┌──────────┐       ssh machinename@       │  API     (:8080)    │
│  Laptop   │ ──────────────────────────▶ │                     │
│  (cafe)   │   port 22 (sshpiper)       └─────────────────────┘
└──────────┘
```

1. Your machine establishes a persistent **reverse SSH tunnel** to the bastion server
2. The bastion runs [sshpiper](https://github.com/tg123/sshpiper) to route incoming SSH connections by username
3. When you `ssh machinename@bastion-host`, sshpiper routes the connection through the reverse tunnel to your machine

No inbound firewall rules needed. Machines only make outbound connections.

## Installation

### Client CLI

Requires the [GitHub CLI](https://cli.github.com/) (`gh`) authenticated with access to this repo.

**macOS (Apple Silicon):**

```bash
gh release download --repo LipJ01/fly-ssh-bastion --pattern '*darwin_arm64*' --dir /tmp
tar -xzf /tmp/bastion_*_darwin_arm64.tar.gz -C /tmp
sudo mv /tmp/bastion /usr/local/bin/
```

**macOS (Intel):**

```bash
gh release download --repo LipJ01/fly-ssh-bastion --pattern '*darwin_amd64*' --dir /tmp
tar -xzf /tmp/bastion_*_darwin_amd64.tar.gz -C /tmp
sudo mv /tmp/bastion /usr/local/bin/
```

**Linux (ARM64):**

```bash
gh release download --repo LipJ01/fly-ssh-bastion --pattern '*linux_arm64*' --dir /tmp
tar -xzf /tmp/bastion_*_linux_arm64.tar.gz -C /tmp
sudo mv /tmp/bastion /usr/local/bin/
```

**Or build from source:**

```bash
gh repo clone LipJ01/fly-ssh-bastion
cd fly-ssh-bastion
go build -o bastion ./cmd/bastion
sudo mv bastion /usr/local/bin/
```

### Server

The server is deployed to Fly.io. See [Server Deployment](#server-deployment) below.

## Quick Start

### 1. Initialize

Run `bastion init` to configure the client. This prompts for:
- **Server URL** — the bastion server's HTTPS endpoint
- **API key** — shared secret for authentication

It also generates an Ed25519 SSH keypair at `~/.ssh/bastion-key`.

```bash
bastion init
```

### 2. Register your machine

```bash
bastion register --owner "yourname"
```

This registers your machine with the bastion server, which:
- Assigns a reverse tunnel port (10022–10099)
- Stores your public key for SSH routing
- Adds the server's public key to your `~/.ssh/authorized_keys`
- Installs a launchd service on macOS for auto-reconnect

### 3. Connect

The tunnel starts automatically on macOS via launchd after registration. To run it manually in the foreground:

```bash
bastion connect
```

### 4. SSH from anywhere

From any other device:

```bash
ssh machinename@your-bastion-host
```

## CLI Reference

| Command | Description |
|---------|-------------|
| `bastion init` | Interactive setup — configure server URL, API key, generate SSH keys |
| `bastion register --owner NAME` | Register this machine with the bastion server |
| `bastion connect` | Start the reverse tunnel (foreground, auto-reconnects) |
| `bastion install` | Install macOS launchd service for persistent tunnel |
| `bastion uninstall` | Remove launchd service |
| `bastion status` | Show tunnel config, service status, and server health |
| `bastion list` | List all registered machines |
| `bastion delete [name]` | Delete a machine (defaults to this machine); cleans up launchd if deleting self |
| `bastion rename <new-name>` | Rename this machine on the server and update local config |
| `bastion config list` | List all config values (API key is masked) |
| `bastion config get <key>` | Get a single config value |
| `bastion config set <key> <value>` | Set a config value (server_url, api_key, machine_name, key_path) |

### Register flags

| Flag | Required | Description |
|------|----------|-------------|
| `--owner` | Yes | Owner name for the machine |
| `--local-user` | No | SSH username on this machine (defaults to `$USER`) |

## Example Workflow

```bash
# First-time setup on a new machine
$ bastion init
Server URL: https://ssh.example.com
API key: ********
Generating SSH keypair...
Config saved to ~/.config/bastion/config.json

# Register the machine
$ bastion register --owner bob
Registered "bobs-macbook" on port 10024
Installed launchd service (auto-starts on boot)

To SSH into this machine from another device:
  ssh bobs-macbook@ssh.example.com

# Check status
$ bastion status
Machine:  bobs-macbook
Port:     10024
Server:   ssh.example.com
Service:  running

# List all machines
$ bastion list
NAME             OWNER   PORT    LOCAL USER   LAST SEEN
desktop          alice   10022   alice        2 minutes ago
laptop           alice   10023   alice        5 minutes ago
bobs-macbook     bob     10024   bob          just now
```

## Connecting from Mobile (Blink Shell)

In [Blink Shell](https://blink.sh) on iOS, create a new host:

- **Host**: `your-bastion-host`
- **Port**: `22`
- **User**: `machinename` (the registered machine name)
- **Key**: Import your `~/.ssh/bastion-key` private key

Then connect with: `ssh machinename`

## Server Deployment

### Prerequisites

- [Fly.io](https://fly.io) account with `flyctl` installed
- A persistent volume for data storage

### Deploy

```bash
# Create the app (first time only)
flyctl apps create fly-ssh-bastion

# Create a volume for the database and keys
flyctl volumes create bastion_data --size 1 --region lhr

# Set the API secret
flyctl secrets set API_SECRET_KEY="your-secret-key"

# Deploy
flyctl deploy --remote-only --config deploy/fly.toml
```

### Server architecture

The server runs three services inside a single Fly.io machine:

| Service | Internal Port | External Port | Purpose |
|---------|--------------|---------------|---------|
| API | 8080 | 443 (HTTPS) | REST API for registration and management |
| sshpiper | 2223 | 22 | Routes SSH connections by username |
| sshd | 2222 | 2222 | Accepts reverse tunnel connections |

Reverse tunnel ports 10022–10099 are allocated one per machine (up to 78 machines).

### API Endpoints

**Public:**

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/status` | Health check — returns `{"status":"ok","machine_count":N}` |

**Authenticated** (requires `X-API-Key` header):

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/api/register` | Register a new machine |
| `GET` | `/api/machines` | List all registered machines |
| `DELETE` | `/api/machines/{name}` | Delete a machine |
| `PUT` | `/api/machines/{name}/rename` | Rename a machine |
| `POST` | `/api/heartbeat` | Update machine heartbeat |

### Environment variables

| Variable | Required | Description |
|----------|----------|-------------|
| `API_SECRET_KEY` | Yes | Shared secret for API authentication |
| `SERVER_URL` | Yes | Public hostname for this bastion server |

## Project Structure

```
cmd/
  bastion/          # Client CLI binary
  bastiond/         # Server daemon binary
internal/
  server/           # HTTP API handlers, router, auth middleware
  db/               # SQLite database layer and migrations
  config/           # sshpiper YAML config generator
  tunnel/           # Reverse tunnel with auto-reconnect
deploy/
  Dockerfile        # Multi-stage build for Fly.io
  fly.toml          # Fly.io service configuration
  entrypoint.sh     # Container startup script
```

## License

MIT

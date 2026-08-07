# sklein-convarchive

This project implements the idea described in [Projet 39 — "sklein-convarchive"](https://notes.sklein.xyz/Projet%2039/zen/).

A centralized archiving system for multi-source conversations (Mattermost, Signal, OpenCode, Claude.ai, etc.) into Object Storage, using the open JSONL format, designed for long-term preservation and as a future RAG corpus.

## Features

- Archive all your [Mattermost](https://github.com/mattermost/mattermost) conversations — channels, private groups, and 1-1 direct messages — into Object Storage (S3-compatible)
- Store them in a durable, open format (JSONL) that stays readable forever and is ready for AI/analysis later
- Works with the storage you may already use: AWS S3, Backblaze B2, Scaleway, MinIO, RustFS, etc.
- Choose exactly what to archive: a specific conversation, one month, or a whole year
- Get a clean, readable version of every conversation as Markdown, one file per conversation per month — perfect for browsing or sharing
- Keep your local time: timestamps are shown in your timezone (default Europe/Paris), with automatic handling of daylight saving time
- Test that everything works before archiving: check your Mattermost access and your object storage connection in one command
- Safe by design: nothing is uploaded until you say so (dry-run mode), and an interrupted run never uploads partial data
- Encrypt every object before upload with [age](https://age-encryption.org/) — the object storage never sees plaintext

## Installation

Binaries are published for every release as GitHub Release assets:

- Linux amd64 / arm64 and macOS (darwin) amd64 / arm64 — download the `.tar.gz` from the [releases](https://github.com/stephane-klein/sklein-convarchive/releases) page and verify it against the attached `SHA256SUMS` file

### Install with curl

```bash
$ curl -sSL -o sklein-convarchive.tar.gz \
    https://github.com/stephane-klein/sklein-convarchive/releases/download/v0.1.0/sklein-convarchive_0.1.0_linux_amd64.tar.gz
$ tar xzf sklein-convarchive.tar.gz
$ ./sklein-convarchive --version   # sklein-convarchive version 0.1.0
```

Archives are named `sklein-convarchive_<version>_<os>_<arch>.tar.gz` — adjust `v0.1.0` and `linux_amd64` to the release tag and platform you need (the checksums in the release `SHA256SUMS` file let you verify the download).

### Install with mise

Install with [mise](https://mise.jdx.dev):

```toml
[tools]
"github:stephane-klein/sklein-convarchive" = "0.1.0"

[env]
_.file = ".secret"
MM_SERVER_URL = "https://chat.example.com"
S3_ENDPOINT = "http://localhost:9000"
S3_BUCKET = "conversations"
SKLEIN_CONVARCHIVE_TIMEZONE = "Europe/Paris"
```

```bash
$ mise install
$ sklein-convarchive --version   # sklein-convarchive version 0.1.0
```

Secrets (`MM_TOKEN`, `S3_ACCESS_KEY`, `S3_SECRET_KEY`, `AGE_RECIPIENT`, …) go in a local `.secret` file (loaded by mise via `_.file`), kept out of version control.

A container image is also published (multi-arch `linux/amd64` and `linux/arm64`, Debian-based):

```bash
$ podman pull ghcr.io/stephane-klein/sklein-convarchive:latest
```

### Run from a container

```bash
$ podman run --rm ghcr.io/stephane-klein/sklein-convarchive:latest --version
```

Configuration is passed through environment variables (or a mounted config file):

```bash
$ podman run --rm \
    -e MM_SERVER_URL=https://chat.example.com \
    -e MM_TOKEN=... \
    -e S3_ENDPOINT=http://localhost:9000 \
    -e S3_ACCESS_KEY=... \
    -e S3_SECRET_KEY=... \
    ghcr.io/stephane-klein/sklein-convarchive:latest mattermost archive --dry-run
```

The image ships with the IANA timezone database and CA certificates, so `--timezone` and HTTPS work out of the box.

## AI-Assisted Development

This project was developed using:

- [OpenCode](https://opencode.ai) CLI — coding assistant workflow (not vibe coding)
- Models: DeepSeek v4 Flash (OpenCode Go)

## Tech Stack

- Language: Go
- CLI: [Cobra](https://github.com/spf13/cobra) + [Viper](https://github.com/spf13/viper)
- Mattermost client: minimal HTTP client built on the Go standard library
- Object Storage client: [minio-go](https://github.com/minio/minio-go)
- Encryption: [age](https://age-encryption.org/) (`filippo.io/age`) — client-side encryption of every object before upload
- Local dev object storage: [RustFS](https://github.com/rustfs/rustfs) via Podman Compose (MinIO community edition was archived in February 2026)
- Tooling: [mise](https://mise.jdx.dev)

## Prerequisites

- [mise](https://mise.jdx.dev/getting-started.html) — installs Go and manages environment variables
- [Podman](https://podman.io/) with the compose subcommand

## Getting Started

```bash
$ mise install
$ mise run build   # builds ./sklein-convarchive
$ ./sklein-convarchive --version   # e.g. "sklein-convarchive version 0.1.0"
```

### Start the local object storage (dev)

```bash
$ mise run up      # starts RustFS via podman compose and waits until it is ready
$ mise run down    # stops and removes the RustFS containers (data is kept)
$ mise run teardown  # stops everything and destroys the RustFS data volumes (destructive)
```

RustFS exposes:

- S3 API on `http://localhost:9000`
- Web console on `http://localhost:9001` (login: `rustfsadmin` / `rustfsadmin`)

The `conversations` bucket is created automatically on the first `mattermost archive` run.

### Configure credentials

Copy `.secret.example` to `.secret` and fill in the values. Mise loads `.secret` automatically when present.

```bash
$ cp .secret.example .secret
$ edit .secret
```

For Mattermost, you can authenticate with either:

- a personal access token (`MM_TOKEN`), or
- username/password (`MM_USERNAME` / `MM_PASSWORD`), with optional MFA (`MM_MFA_TOKEN`)

### Encrypt objects before upload (optional)

Objects are encrypted client-side with [age](https://age-encryption.org/) before being sent to the object storage, so the storage provider never sees the plaintext. Each object is an independent age file with its own random file key and can be decrypted with any age tool.

1. Generate a key pair with the age CLI (available from your package manager or via `go install filippo.io/cmd/age/cmd/...@latest`), saving the secret key to a file — it is required to decrypt the archive later:

   ```bash
   $ age-keygen -o age.key
   # public key:  age1...
   # secret key:  written to age.key
   ```

   Keep `age.key` safe and back it up offline.

2. Store the **public** key in `.secret` and enable encryption:

   ```bash
   AGE_RECIPIENT=age1...
   ```

3. Archive with `--encrypt`:

   ```bash
   $ ./sklein-convarchive mattermost archive --encrypt
   ```

   Encryption can also be enabled in the local config (the `[age]` section of `.sklein-convarchive.toml`, see below).

Encrypted objects keep the same path layout with a `.age` suffix, e.g. `jsonl/mattermost/team-nimbus/chan-gamma/2026/2026-08.jsonl.age`.

### Archive Mattermost conversations

```bash
$ ./sklein-convarchive mattermost list-conversations      # list conversations accessible for archiving
$ ./sklein-convarchive mattermost archive --dry-run       # fetch posts and preview what would be uploaded
$ ./sklein-convarchive mattermost archive                 # archive everything to the object storage
```

Target a specific conversation and a period:

```bash
$ ./sklein-convarchive mattermost archive --team dev --conversation general --period 2026-08 # one month of a channel
$ ./sklein-convarchive mattermost archive --period 2026                                  # a whole year
$ ./sklein-convarchive mattermost archive --conversation <id>                           # full history of a channel/DM
```

- `--conversation <id>`: conversation ID (from the `ID` column of `mattermost list-conversations`), or a channel name with `--team`
- `--team <name>`: restrict to a team, or the team of the `--conversation` name
- `--period` accepts `YYYY-MM` (month) or `YYYY` (year)
- `--timezone`: IANA timezone used for timestamps in the render and for day/month boundaries (default `Europe/Paris`; the JSONL stores the local offset, the `raw` field keeps the absolute epoch)

### Browse, decrypt, and delete an archive with rclone

`sklein-convarchive` writes plain S3 objects, so any S3 client can read them back. [rclone](https://rclone.org/) (MIT-licensed, actively maintained) works with RustFS and any S3-compatible endpoint, and pipes naturally into `age` for decryption. (The MinIO client `mc` is archived since late 2025, so rclone is the recommended tool here.)

1. Install rclone through mise (it is declared in `.mise.toml`):

   ```bash
   $ mise install
   ```

The `rustfs` remote is already connected to the object storage through environment variables loaded by mise.

1. List the archived markdown conversations:

   ```bash
   $ rclone lsd rustfs:                                   # all buckets
   $ rclone lsf -R rustfs:conversations/markdown/         # all markdown objects
   # markdown/mattermost/team-nimbus/chan-gamma/2026/2026-08.md.age
   ```

   Team and conversation names are slugified in the object path.

2. Retrieve and decrypt one conversation month:

   ```bash
   $ rclone copy rustfs:conversations/markdown/mattermost/team-nimbus/chan-gamma/2026/2026-08.md.age .
   $ age -d -i age.key 2026-08.md.age > 2026-08.md
   ```

   Or in a single pipe, without writing the encrypted file to disk:

   ```bash
   $ rclone cat rustfs:conversations/markdown/mattermost/team-nimbus/chan-gamma/2026/2026-08.md.age \
     | age -d -i age.key > 2026-08.md
   ```

   `age.key` is the identity file created in the [encryption section](#encrypt-objects-before-upload-optional). The JSONL layer is read and decrypted the same way, e.g. `rclone cat rustfs:conversations/jsonl/mattermost/team-nimbus/chan-gamma/2026/2026-08.jsonl.age | age -d -i age.key`.

### Configuration

A local `.sklein-convarchive.toml` file can also be used (see `.sklein-convarchive.toml.example`):

```toml
[mattermost]
server_url = "https://chat.example.com"
token = "..."

[s3]
endpoint = "http://localhost:9000"
access_key = "..."
secret_key = "..."
bucket = "conversations"

[age]
recipient = "age1..."  # required when encrypt = true
encrypt = true         # optional, encrypt every object with age before upload
```

Priority order (highest to lowest):

1. CLI flag (e.g. `mattermost archive --server-url`)
2. Environment variable (e.g. `MM_SERVER_URL`)
3. Local config (`.sklein-convarchive.toml`)
4. Global config (`~/.config/sklein-convarchive/config.toml`)
5. Default value

Source-specific flags (Mattermost: `--server-url`, `--token`, `--username`, `--password`, `--mfa-token`) are scoped to the `mattermost` command, so they never collide with flags of other future sources.

## Object Storage Layout

```
conversations/
  jsonl/
    mattermost/team-nimbus/chan-gamma/2026/2026-08.jsonl
  markdown/
    mattermost/team-nimbus/chan-gamma/2026/2026-08.md
```

The `jsonl/` layer is the raw, normalized archive (source of truth), one file per conversation per month.
The `markdown/` layer is a human-readable rendering, generated in parallel, one file per conversation per month, in an IRC-like format (aligned username column, text wrapping at 100 chars, messages grouped by day, threads indented under their root).

With `--encrypt`, every object is age-encrypted and uploaded with a `.age` suffix and `application/octet-stream` content type; the path layout is unchanged.

## Common Schema

Each JSONL line is normalized to a common schema shared by all sources:

| Field | Description |
|:------|:------------|
| `source` | Platform of origin |
| `timestamp` | Message date and time (RFC 3339) |
| `author` | Message author |
| `content` | Message content |
| `thread_id` | Thread identifier |
| `metadata` | Source-specific fields |
| `raw` | Optional, original raw data |

Mattermost metadata example:

```json
{ "team": "dev", "channel": "général", "server_url": "https://chat.example.com" }
```

## Releases

To publish a release, tag a commit and push the tag:

```bash
$ git tag -a v0.1.0 -m "Release v0.1.0" && git push origin v0.1.0
```

## Documentation

- [`docs/decisions/`](docs/decisions/) — Architecture Decision Records
  (ADRs) following the MADR convention. To create a new one, ask the
  agent to use the `.opencode/skills/new-decision/` skill.
- [`docs/agents/`](docs/agents/) — operational notes for AI agents (OpenCode, etc.), loaded on demand.

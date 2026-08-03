# sklein-convarchive

This project implements the idea described in [Projet 39 — "sklein-convarchive"](https://notes.sklein.xyz/Projet%2039/zen/).

A centralized archiving system for multi-source conversations (Mattermost, Signal, OpenCode, Claude.ai, etc.) into Object Storage, using the open JSONL format, designed for long-term preservation and as a future RAG corpus.

## Features

- Archive all your [Mattermost](https://github.com/mattermost/mattermost) conversations — channels, private groups, and 1-1 direct messages — into Object Storage (S3-compatible)
- Store them in a durable, open format (JSONL) that stays readable forever and is ready for AI/analysis later
- Works with the storage you may already use: AWS S3, Backblaze B2, Scaleway, MinIO, RustFS, etc.
- Choose exactly what to archive: a specific conversation, one day, one month, or a whole year
- Get a clean, readable version of every conversation as Markdown, one file per conversation per month — perfect for browsing or sharing
- Keep your local time: timestamps are shown in your timezone (default Europe/Paris), with automatic handling of daylight saving time
- Test that everything works before archiving: check your Mattermost access and your object storage connection in one command
- Safe by design: nothing is uploaded until you say so (dry-run mode), and an interrupted run saves what it already fetched

## AI-Assisted Development

This project was developed using:

- [OpenCode](https://opencode.ai) CLI — coding assistant workflow (not vibe coding)
- Models: DeepSeek v4 Flash (OpenCode Go)

## Tech Stack

- Language: Go
- CLI: [Cobra](https://github.com/spf13/cobra) + [Viper](https://github.com/spf13/viper)
- Mattermost client: minimal HTTP client built on the Go standard library
- Object Storage client: [minio-go](https://github.com/minio/minio-go)
- Local dev object storage: [RustFS](https://github.com/rustfs/rustfs) via Podman Compose (MinIO community edition was archived in February 2026)
- Tooling: [mise](https://mise.jdx.dev)

## Prerequisites

- [mise](https://mise.jdx.dev/getting-started.html) — installs Go and manages environment variables
- [Podman](https://podman.io/) with the compose subcommand

## Getting Started

```bash
$ mise install
$ mise run build   # builds ./sklein-convarchive
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

### Archive Mattermost conversations

```bash
$ ./sklein-convarchive mattermost list-conversations      # list conversations accessible for archiving
$ ./sklein-convarchive mattermost archive --dry-run       # fetch posts and preview what would be uploaded
$ ./sklein-convarchive mattermost archive                 # archive everything to the object storage
```

Target a specific conversation and a period:

```bash
$ ./sklein-convarchive mattermost archive --conversation <id> --period 2026-08-03   # one day of a channel/DM
$ ./sklein-convarchive mattermost archive --team dev --conversation general --period 2026-08 # one month of a channel
$ ./sklein-convarchive mattermost archive --period 2026                                  # a whole year
$ ./sklein-convarchive mattermost archive --conversation <id>                           # full history of a channel/DM
```

- `--conversation <id>`: conversation ID (from the `ID` column of `mattermost list-conversations`), or a channel name with `--team`
- `--team <name>`: restrict to a team, or the team of the `--conversation` name
- `--period` accepts `YYYY-MM-DD` (day), `YYYY-MM` (month), or `YYYY` (year)
- `--timezone`: IANA timezone used for timestamps in the render and for day/month boundaries (default `Europe/Paris`; the JSONL stores the local offset, the `raw` field keeps the absolute epoch)

### Configuration

A local `.sklein-convarchive.toml` file can also be used (see `.sklein-convarchive.toml.example`).

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
    mattermost/2026/08/03/2026-08-03.jsonl
  markdown/
    mattermost/dev/town-square/2026/2026-08.md
```

The `jsonl/` layer is the raw, normalized archive (source of truth).
The `markdown/` layer is a human-readable rendering, generated in parallel, one file per conversation per month, in an IRC-like format (aligned username column, text wrapping at 100 chars, messages grouped by day, threads indented under their root).

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

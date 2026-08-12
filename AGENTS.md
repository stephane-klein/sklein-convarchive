# Agent Instructions

## Language Policy

- **All project content must be in English**: source code, comments, commit messages, and documentation.

## Project Context

This project implements the idea described in [Projet 39 — "sklein-convarchive"](https://notes.sklein.xyz/Projet%2039/zen/).

A centralized archiving system for multi-source conversations (Mattermost, Signal, OpenCode, Claude.ai, etc.) into Object Storage, using the open JSONL format, designed for long-term preservation and as a future RAG corpus.

## Architecture

- Mono-repo `sklein-convarchive`, one connector per source.
- Current scope: Mattermost → Object Storage (MVP), plus the import of Claude.ai/ChatGPT exports produced by the `claude-chatgpt-backup-extension` Firefox extension.
- Common normalized schema shared by all sources: `pkg/archive/entry.go`.
- Mattermost client is a minimal HTTP client on the Go standard library (`pkg/mattermost/`), targeting the stable `/api/v4` endpoints — compatible across Mattermost server versions since 4.x.
- Claude/ChatGPT export parsers live in `pkg/claude/` and `pkg/chatgpt/`; they produce one `archive.Thread` per conversation, archived as one JSONL + one Markdown object each (`cmd/claude-chatgpt-backup-extension-export.go`).
- Object Storage client: `minio-go` (`pkg/archive/upload.go`), compatible with any S3 endpoint.

## Config Convention

All CLI flags that can be configured via config file are bound to Viper.

- Persistent flags bound with `viper.BindPFlag()` + `viper.BindEnv()`
- Source-specific flags (Mattermost `--server-url`, `--token`, etc.) are declared on the source command's `PersistentFlags()` (e.g. `mattermostCmd`), never on `rootCmd`, to avoid name collisions between sources
- Env prefix: `MM_*` for Mattermost, `S3_*` for object storage
- Config files: local `.sklein-convarchive.toml` (config name `.sklein-convarchive`), global `~/.config/sklein-convarchive/config.toml`
- Priority: CLI flag > env > local config > global config > default
- `.secret` file is loaded by mise (`_.file = ".secret"`) and holds credentials
- `timezone` (default `Europe/Paris`): IANA zone used to format timestamps in the JSONL (local offset) and to compute day/month boundaries
- `owner` (default `stephane`): identity attributed to `human`/`user` messages of AI conversation exports (global flag, env `SKLEIN_CONVARCHIVE_OWNER`)
- The `claude-chatgpt-backup-extension-export archive` command is an exception to the Viper convention: its import-specific flags (`--file`, `--source`, `--account`, `--period`) are read directly from the Cobra flags and are **not** bound to Viper/env/config — the command is always used explicitly, with one or more `--file` arguments (repeatable)

## Dev Commands

```bash
$ mise install          # install Go via mise
$ mise run build        # build ./sklein-convarchive
$ mise run up           # start local RustFS (S3 API :9000, console :9001)
$ mise run down         # stop RustFS
$ ./sklein-convarchive mattermost archive --dry-run
```

The `conversations` bucket is created automatically on the first `mattermost archive` run (`pkg/archive/upload.go`).

## Object Storage Layout

- Bucket default: `conversations`
- Key layout (raw JSONL): `jsonl/mattermost/<team>/<display-slug>/<year>/<month>.jsonl`
- Key layout (human-readable Markdown): `markdown/mattermost/<team>/<display-slug>/<year>/<month>.md`
- Objects are compressed with zstd by default (suffix `.zst`, before `.age`); disable with `--no-compress`
- One JSONL object per conversation+month, one Markdown object per conversation+month, both uploaded incrementally as each month completes during the fetch (S3 has no native append)
- Posts are fetched in chronological order (oldest first) by iterating the newest-first pages in reverse — a gapless traversal, unlike the `after` cursor which drops posts sharing the anchor's millisecond
- Markdown generation lives in `pkg/archive/markdown.go`, wired in parallel with the JSONL buffer in `cmd/mattermost.go`
- AI export layout (one object per thread, named after the thread creation datetime): `jsonl/claude/<account>/<year>/<yyyy-mm-dd_hhmmss>.jsonl` and `jsonl/chatgpt/<account>/<year>/<yyyy-mm-dd_hhmmss>.jsonl` (Markdown under `markdown/` with the same path). This 5-segment layout (no conversation segment) is handled by `pkg/browse/key.go` alongside the Mattermost 6-segment layout

## Supplementary Documentation

- [`docs/agents/`](docs/agents/) — operational snapshots of subsystems (loaded on demand by the agent)
- [`docs/decisions/`](docs/decisions/) — architecture decision records
- `.opencode/skills/new-decision/` — skill for creating new decision records

## Version Control

This repository uses Jujutsu (`jj`). Use `jj` commands instead of `git`.

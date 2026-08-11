---
status: accepted
date: 2026-08-08
decision-makers: Stéphane Klein
ai-assistants: DeepSeek V4 Flash (OpenCode Go)
---

# ADR 001 — Object granularity mirrors the selection filters (conversation × month)

## Context and Problem Statement

*sklein-convarchive* **wrote** one JSONL object per day, holding the messages of that day from every conversation (channels, direct messages, group messages). The archiver follows a **write-only, idempotent model**: a run computes and writes whole objects, and never reads an object back to merge with new data — so writing replaced any existing object.

**Problem:** somewhat foolishly, without thinking it through, I made the object granularity (one day, all conversations) coarser than the `--conversation` selection filter. Since `--conversation` archives a single conversation, archiving one conversation after a broader run rewrote the shared daily objects with only that conversation's messages, silently deleting the data of the other conversations that had been saved previously.

This data-destroying behavior was an unintended architectural flaw: surprising, error-prone, and unacceptable for an archiving tool. It is the motivation for this redesign.

## Decision Drivers

- **Object granularity equals the smallest selectable granularity**: an archive object is keyed at the finest unit the CLI can select (conversation × month), so a run can never overwrite an object with a subset of its data.
- **Avoid overly small objects**: the minimum temporal unit was raised from one day to one month so that archiving does not generate many tiny objects (per-object minimum billing, listing latency).

## Considered Options

- **A. Day-aggregated object (status quo)** — one object per day containing all conversations.
- **B. Merge-on-upload** — read the existing object, merge new lines by `post_id`, rewrite.
- **C. Per-conversation day objects, keeping a daily `--period`** — `jsonl/.../<conversation>/<year>/<month>/<day>.jsonl`.
- **D. Per-conversation month object, minimum `--period` of a month** — chosen.

## Decision Outcome

Chosen option: **D**, because the object granularity now matches the selection filters 1:1.

Object layout:

```
jsonl/mattermost/<team>/<display-slug>/<year>/<month>.jsonl
markdown/mattermost/<team>/<display-slug>/<year>/<month>.md
```

- Every object corresponds to exactly one (conversation, month) pair — the finest selection filter exposed by the CLI.
- `--period` no longer accepts `YYYY-MM-DD`: a daily filter would write a partial monthly object, breaking the 1:1 invariant and reintroducing the overwrite problem. Month is the smallest accepted temporal unit.
- Months are uploaded incrementally as each one completes during the fetch (which runs oldest first): a month is written only once the traversal has passed below its start, so a partial object can never replace a complete one. The object storage uploader is therefore created before the fetch starts.
- Interrupted runs keep the already-completed months that were uploaded, and never write the partially-fetched month.

### Consequences

- Good, because archiving a team for a month and a single channel for a year no longer collide: each conversation owns its objects.
- Good, because no merge-on-upload machinery is needed (each month is written whole; re-archiving a month is idempotent).
- Good, because the JSONL layer now aligns with the Markdown layer, which was already organized per conversation+month.
- Good, because the number of objects is bounded by conversations × months instead of conversations × days.
- Good, because the incremental upload both bounds the in-memory footprint and makes re-archiving resumable: already-archived months stay archived even if the run is interrupted.
- Bad, because a very active channel produces a large monthly object, rewritten in full on every re-archive.
- Bad, because day-granularity archiving is no longer possible.

## Pros and Cons of the Options

### A. Day-aggregated object (status quo)

- Good, because one object per day is a simple mental model.
- Bad, because the object granularity is coarser than the `--conversation` filter: archiving one conversation rewrites objects holding every conversation, losing the others.

### B. Merge-on-upload

- Good, because it would keep any desired granularity while accumulating data across runs — a fully viable alternative, and the one chosen would not have been forced by S3.
- Bad, because it **complicates the implementation**: the write path becomes a read-merge-write pipeline, with error handling for partial or missing objects.
- Bad, because it requires reading back and merging existing objects on every run (GET + rewrite), and decrypting them in age-encrypted mode (the private key would be needed at archive time).
- Bad, because it adds deduplication logic (`post_id` + newest `update_at`) and per-run latency on large objects.

### C. Per-conversation day objects with a daily `--period`

- Good, because objects would still match a day-level filter 1:1.
- Bad, because it multiplies the object count by ~30 versus option D, for no practical archiving need at day granularity.

### D. Per-conversation month object, minimum `--period` of a month

- Good, because object granularity equals the selection filters (team × conversation × month).
- Good, because it bounds object count and aligns the JSONL and Markdown layers.
- Bad, because day-level archiving is lost; active channels get large monthly objects.

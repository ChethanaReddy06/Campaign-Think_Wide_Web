# NOTES.md

## Part 1 – Read and think

**My interpretation.** Providers push events about messages. We must store them idempotently and serve per-campaign counts. Correctness of the numbers (no double counting, tolerant of late/out-of-order data) matters more than features.

**Assumptions**
- `event_id` alone identifies an event (provider-assigned, global). Retried events are byte-identical.
- Timestamps are UTC RFC3339. Arrival time is a different thing and we store it separately.
- Provider events are trusted enough (no auth in scope).
- Stats are "all time for the campaign", not time-windowed.
- One process, restarts may lose data (see skipped).

**Ambiguities I noticed**
- What is "opened": every open event, or distinct people? I expose both (`total`, `unique`) rather than pick one silently. For marketers, `unique` is the headline; `total` is engagement volume.
- "Same event": by `event_id`. But the seed has 3 IDs reused with *different content*. Not a retry. First write wins, and I report it as a `conflict`.
- Rejected vs. accepted: `OPENED` (uppercase) and `10-08-2026 09:00` (ambiguous dd-mm vs mm-dd) are rejected, not guessed.
- Can opened exist without delivered? Yes, out-of-order. I don't enforce funnel order, so rates can exceed 1 temporarily.
- Unknown campaign: 404 (could be a typo) vs zeros. I chose 404.
- Count by event time or arrival time? Totals don't care; it matters for time-series (not built).
- "Sent" is per message/contact. A contact can legitimately get several messages in a campaign, so `total` and `unique` can differ for sent/delivered too.

**Questions for the PM**
- Is the headline open number unique people or total opens? Do Apple-MPP/bot opens need filtering?
- Do dashboards need time series (per hour/day)? By event time?
- Is data loss on restart acceptable? Retention period?
- Can a provider legitimately correct an event (same id, new content)? Should later win?
- Is the same contact receiving multiple messages in one campaign expected?
- Should rejected events be visible to anyone (dead-letter view/alerts)?

**Priorities (in order):** this file → POST /events correctness (partial batch, dedup, validation) → stats → concurrency + tests → debugging task → memos → list endpoint (done, small).

**Design decisions**
- Storage: in-memory map by event_id + per-campaign incremental counters and distinct-contact sets, behind one RWMutex. Dedup check, insert and counter update are in one critical section, so concurrent retries can't double count. Reads are O(1). I chose it for speed of delivery and zero deps; the cost is no durability.
- POST: `[]json.RawMessage`, each element validated alone. 200 + breakdown when the body is an array; 400 only for non-array. Returning 200 for duplicates/bad items matters: a non-2xx makes providers retry forever.
- Rejected: missing/empty required field, unknown type (strict, case-sensitive), non-RFC3339 timestamp. Accepted: missing metadata, non-string metadata values (stringified), any timestamp offset (normalised to UTC).
- Seed result: 235 received → 210 accepted, 15 duplicates, 3 conflicts, 7 rejected.

## Part 4 – Scale memo (100M events/day ≈ 1,150/s average, maybe 10x peaks)

- **What breaks first:** durability and memory. Everything is in RAM and a restart loses it; the distinct-contact sets grow without bound. Second: the single global lock, and a single instance means no HA.
- **Order of change:** (1) durable store (Postgres first; if aggregation load is high, ClickHouse) with a unique key on event_id and batched inserts; (2) move counters to precomputed rollups per campaign (and per hour) rather than counting on read; (3) only then a queue.
- **Contract/storage:** POST contract can stay. Stats may become "eventually consistent" and should say so (`as_of` already helps). Storage changes: raw events table (append-only, partitioned by day) + rollup tables; unique counts via exact sets at small scale, HyperLogLog beyond it (approximate: a contract change to document).
- **Queue?** Yes, to absorb spikes and keep the webhook fast (ack after enqueue). New problems: at-least-once delivery, consumer lag, ordering only per partition (partition by campaign_id), poison messages, and "accepted" now means "queued", so validation errors can no longer be returned synchronously.
- **New duplicate sources:** consumer retries/rebalances, producer retries on timeout, replays for backfills, two paths (webhook + replay) writing the same event. Dedup must therefore be at the sink (unique event_id key / idempotent upsert), not just at the door.
- **Trustworthy counters:** derive rollups only from deduplicated inserts (increment only when the insert actually inserted); periodically reconcile rollups against `COUNT(DISTINCT event_id)` on raw data; keep raw events so rollups can be rebuilt.
- **Monitor:** ingest rate, p99 latency, 5xx, queue lag, duplicate rate and conflict rate per provider, reject rate by reason, reconciliation drift, store size/growth, time-since-last-event per campaign.
- **Deliberately not solving yet:** exactly-once end-to-end, multi-region, bot-open filtering, per-tenant rate limiting, long-term retention/compaction.

## Part 5 – The angry marketer

Observed: delivered +5,000, opened −2,000, sent and clicked flat.

**Plausible explanations (before blaming code)**
1. Late `delivered` events arrived (expected; providers are late/out of order). Fully explains delivered rising.
2. Opened going *down* means *something is subtracting*. Possible: (a) dashboard shows **unique** opens, and the metric is computed against a different base; (b) opens were deduplicated/compacted/re-bucketed by a background job; (c) provider sent a correction or retraction and we honour it; (d) bot/MPP filtering got retroactively applied; (e) data purged/expired, or a restore from backup; (f) reads served by two replicas/caches with different freshness (10:30 value from a staler node); (g) the dashboard is filtered by event-time window and the window moved (rolling "last 24h"); (h) opens attributed to a different campaign after a campaign_id fix; (i) a deploy changed the definition of "opened"; (j) a genuine bug (e.g., a rollup that is recomputed and a partial rebuild).
3. Delivered/opened can legitimately look inconsistent: both are produced independently by the provider and sent at different delays.

**Check first:** (1) is it stable? Reload several times and compare nodes/caches. (2) ask the dashboard's exact definition and time window. (3) Query raw events for that campaign: count distinct event_id by type *by arrival time*, compare with the two dashboard readings. If raw opens never decreased, the aggregation layer is at fault; if raw rows disappeared, find the purge/correction. (4) check deploys/jobs between 10:00 and 10:30.

**Bug or expected?** In a system that only appends and dedupes, a raw count must never fall. If the displayed number falls and raw data didn't, bug. If the metric is windowed or unique-based and the definition explains it, expected, but then the UI should say so.

**Is delivered rising suspicious?** No. Late delivered events are normal. It would be suspicious only if delivered exceeded sent by a lot for long (data-quality issue; note `delivered/sent` can briefly exceed 1 because of ordering), or if it rose long after the campaign ended without matching raw arrivals.

## What I completed / intentionally skipped
- **Done:** Parts 1–5, POST/GET stats, optional paginated events endpoint, tests (duplicates, conflicts, partial batch, non-array body, out-of-order, concurrent retries, seed file), all four debug fixes.
- **Skipped:** durable storage (in-memory only; restart loses data), auth, time-series stats, timestamp sanity checks (e.g. far future dates are accepted), metrics/logging of rejects, graceful shutdown, structured request limits per provider.

## If I had another day
- SQLite/Postgres with a unique event_id constraint and an append-only raw table; rollups table.
- Hourly/daily buckets by event time, and expose reject/conflict counts per provider.
- Reject/flag far-future and ancient timestamps; configurable normalisation (case-insensitive types).
- Property/fuzz test of ingest; benchmark at 1k req/s.

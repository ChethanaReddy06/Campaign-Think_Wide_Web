# BUGS.md – debugging/main.go

Method: ran `go run -race . events.jsonl`, diffed against `expected_output.txt`, then read the code with the four spec rules next to it. Final diff to `main.go` is ~14 lines.

## 1. Dedup only within a batch
- **What:** `sent`/`delivered`/etc. were over-counted (cmp_A sent 2025 vs expected 1987).
- **Why:** `seen` map was created inside `processBatch`, so it reset every 200 lines. A retry that landed in a different batch counted again. Spec rule 1 says once per file.
- **Fix:** moved it to a package-level `seenIDs` map (touched only from the main goroutine).
- **Verified:** totals now match expected; the diff is empty.

## 2. `unique_opens` keyed by contact only
- **What:** unique_opens too low (549 vs 942 for cmp_A).
- **Why:** `openedBy[ev.ContactID]`: a contact who opened in campaign A was treated as already counted in campaign B. Spec: once *per campaign*.
- **Fix:** key is `CampaignID + "\x00" + ContactID`.
- **Verified:** unique_opens match expected for all 5 campaigns.

## 3. Daily buckets used `Local()` time
- **What:** daily delivered buckets shifted depending on machine timezone (events near midnight land on the wrong day). Invisible if your machine is UTC.
- **Why:** `ev.Timestamp.Local().Format(...)`; spec says UTC date.
- **Fix:** `.UTC()`.
- **Verified:** with the original code, `TZ=Asia/Kolkata` gives different buckets; with the fix, output equals expected under `TZ=Asia/Kolkata`, `TZ=America/Los_Angeles` and default.

## 4. Data race on the counters
- **What:** `go run -race` reports DATA RACE in `apply()`; counters can lose increments, so output may differ between runs (spec rule 4).
- **Why:** 8 worker goroutines do `cs.Sent++` etc. on the same `*CampaignStats` with no synchronisation. The comment "increments are cheap" is wrong: `++` is read-modify-write.
- **Fix:** package-level `sync.Mutex` taken in `apply()`. (Per-campaign mutex or atomics would also work; one mutex is the smallest change. Honestly the worker pool adds no value for this work, but I was told not to restructure.)
- **Verified:** `go run -race` three times in a row: zero race warnings, output identical to expected each time.

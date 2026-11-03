# AI_USAGE.md

**Tool:** Claude (chat, with a code sandbox).

**Used for:** reading the assignment, reading the seed data for anomalies (scripted scan for duplicates/conflicts/bad timestamps), drafting the service, tests, the debugging fixes, and first drafts of the docs. I reviewed and ran everything; `go test -race` and the debugging diff were run in the sandbox.

**Suggestion rejected/changed:** the first suggestion was SQLite with `INSERT OR IGNORE`. I went with an in-memory map + mutex instead because with a 2-hour limit and no third-party dependency it keeps `go run .` working with zero setup. The trade-off (no durability) is written down in NOTES.md.

**Helped me understand:** why `[]json.RawMessage` is needed for partial batches, and why a duplicate `event_id` with *different* content (seen 3 times in the seed) is a separate case from a plain retry.

**Generated then needed debugging:** nothing needed debugging beyond `gofmt` not being available in the sandbox; tests passed first run. (Edit this section to reflect your own experience.)

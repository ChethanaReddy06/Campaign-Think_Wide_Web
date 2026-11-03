# Campaign Events service

Go 1.22+, stdlib only, in-memory storage.

    cd service
    go run .                 # listens on :8080
    go test -race ./...      # tests (incl. concurrency + seed file)

Open http://localhost:8080/ for a small demo dashboard (optional; not part of the required API).

Windows note: `go test -race` needs GCC (cgo); plain `go test ./...` works without it.

Load the seed and query:

    curl -s -X POST localhost:8080/events -H 'Content-Type: application/json' --data-binary @seed/events.json
    curl -s localhost:8080/campaigns/cmp_summer_sale/stats
    curl -s 'localhost:8080/campaigns/cmp_summer_sale/events?limit=10'
    curl -s 'localhost:8080/campaigns/cmp_summer_sale/events?limit=10&after=<next_after from previous page>'

## API

**POST /events** – body must be a JSON array. `400` only if it is not an array; `413` if >5 MiB or >5000 events.
Otherwise `200` and a per-item breakdown:

    {"received":235,"accepted":210,"duplicates":15,
     "conflicts":[{"index":136,"event_id":"evt_00164","reason":"..."}],
     "rejected":[{"index":27,"event_id":"evt_00212","reason":"..."}]}

- `duplicates`: same `event_id` and same content → ignored, harmless (a retry).
- `conflicts`: same `event_id`, different content → first write kept, reported so we can alert the provider.
- `rejected`: invalid event (missing field, unknown type, non-RFC3339 timestamp). Nothing stored.

**GET /campaigns/{id}/stats** – for each of sent/delivered/opened/clicked: `total` (distinct events) and
`unique` (distinct contacts). Plus `rates` (unique-contact based, null if denominator 0, not clamped),
`first_event_at`, `last_event_at`, `last_received_at`, `as_of`. `404` for a campaign we've never seen an event for.

**GET /campaigns/{id}/events?limit=50&after=<seq>** – arrival-order, cursor paginated (`next_after`, 0 = end).

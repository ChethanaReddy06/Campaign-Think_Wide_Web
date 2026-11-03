package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

type campaign struct {
	counts   map[string]int
	contacts map[string]map[string]struct{} // type -> distinct contacts
	events   []*Event                       // arrival order
	firstTS  time.Time
	lastTS   time.Time
	lastRecv time.Time
}

// Store is an in-memory store guarded by one RWMutex. Dedup check + insert + counter
// update happen inside one critical section, so concurrent retries cannot double count.
type Store struct {
	mu        sync.RWMutex
	byID      map[string]*Event
	campaigns map[string]*campaign
	seq       int64
	now       func() time.Time
}

func NewStore() *Store {
	return &Store{byID: map[string]*Event{}, campaigns: map[string]*campaign{}, now: time.Now}
}

// parseEvent validates one raw JSON element. It never panics on odd input.
func parseEvent(raw json.RawMessage) (*Event, string, string) {
	var r rawEvent
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, "", "not a valid event object: " + err.Error()
	}
	get := func(p *string) string {
		if p == nil {
			return ""
		}
		return strings.TrimSpace(*p)
	}
	id := get(r.EventID)
	for name, v := range map[string]string{"event_id": id, "campaign_id": get(r.CampaignID), "contact_id": get(r.ContactID), "type": get(r.Type), "timestamp": get(r.Timestamp)} {
		if v == "" {
			return nil, id, "missing or empty field: " + name
		}
	}
	typ := get(r.Type)
	if !validTypes[typ] {
		return nil, id, fmt.Sprintf("unknown type %q (want sent|delivered|opened|clicked)", typ)
	}
	ts, err := time.Parse(time.RFC3339, get(r.Timestamp))
	if err != nil {
		return nil, id, "timestamp must be RFC3339 (e.g. 2026-08-10T09:15:04Z)"
	}
	var md map[string]string
	if len(r.Metadata) > 0 {
		md = map[string]string{}
		for k, v := range r.Metadata {
			md[k] = fmt.Sprint(v)
		}
	}
	return &Event{EventID: id, CampaignID: get(r.CampaignID), ContactID: get(r.ContactID), Type: typ, Timestamp: ts.UTC(), Metadata: md}, id, ""
}

func sameContent(a, b *Event) bool {
	return a.CampaignID == b.CampaignID && a.ContactID == b.ContactID && a.Type == b.Type && a.Timestamp.Equal(b.Timestamp)
}

// Ingest handles a whole batch. Each element is judged independently.
func (s *Store) Ingest(items []json.RawMessage) IngestResult {
	res := IngestResult{Received: len(items), Conflicts: []Problem{}, Rejected: []Problem{}}
	parsed := make([]*Event, len(items))
	for i, raw := range items { // validate outside the lock
		ev, id, why := parseEvent(raw)
		if why != "" {
			res.Rejected = append(res.Rejected, Problem{Index: i, EventID: id, Reason: why})
			continue
		}
		parsed[i] = ev
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	recv := s.now().UTC()
	for i, ev := range parsed {
		if ev == nil {
			continue
		}
		if old, ok := s.byID[ev.EventID]; ok {
			if sameContent(old, ev) {
				res.Duplicates++
			} else {
				res.Conflicts = append(res.Conflicts, Problem{Index: i, EventID: ev.EventID, Reason: "event_id already stored with different content; first write kept"})
			}
			continue
		}
		s.seq++
		ev.Seq, ev.ReceivedAt = s.seq, recv
		s.byID[ev.EventID] = ev
		c := s.campaigns[ev.CampaignID]
		if c == nil {
			c = &campaign{counts: map[string]int{}, contacts: map[string]map[string]struct{}{}}
			s.campaigns[ev.CampaignID] = c
		}
		c.counts[ev.Type]++
		if c.contacts[ev.Type] == nil {
			c.contacts[ev.Type] = map[string]struct{}{}
		}
		c.contacts[ev.Type][ev.ContactID] = struct{}{}
		c.events = append(c.events, ev)
		if c.firstTS.IsZero() || ev.Timestamp.Before(c.firstTS) {
			c.firstTS = ev.Timestamp
		}
		if ev.Timestamp.After(c.lastTS) {
			c.lastTS = ev.Timestamp
		}
		c.lastRecv = recv
		res.Accepted++
	}
	return res
}

type TypeStat struct {
	Total  int `json:"total"`  // distinct events
	Unique int `json:"unique"` // distinct contacts
}

type Stats struct {
	CampaignID     string              `json:"campaign_id"`
	Sent           TypeStat            `json:"sent"`
	Delivered      TypeStat            `json:"delivered"`
	Opened         TypeStat            `json:"opened"`
	Clicked        TypeStat            `json:"clicked"`
	Rates          map[string]*float64 `json:"rates"` // over unique contacts; null when denominator is 0
	FirstEventAt   time.Time           `json:"first_event_at"`
	LastEventAt    time.Time           `json:"last_event_at"`
	LastReceivedAt time.Time           `json:"last_received_at"`
	AsOf           time.Time           `json:"as_of"`
}

func (s *Store) Stats(id string) (*Stats, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.campaigns[id]
	if !ok {
		return nil, false
	}
	ts := func(t string) TypeStat { return TypeStat{c.counts[t], len(c.contacts[t])} }
	st := &Stats{CampaignID: id, Sent: ts("sent"), Delivered: ts("delivered"), Opened: ts("opened"), Clicked: ts("clicked"),
		FirstEventAt: c.firstTS, LastEventAt: c.lastTS, LastReceivedAt: c.lastRecv, AsOf: s.now().UTC(), Rates: map[string]*float64{}}
	rate := func(n, d int) *float64 {
		if d == 0 {
			return nil
		}
		v := float64(n) / float64(d)
		return &v
	}
	// Computed on unique contacts and NOT clamped: opens can arrive before sent/delivered,
	// so a rate >1 means "data still arriving", not a bug.
	st.Rates["delivered_per_sent"] = rate(st.Delivered.Unique, st.Sent.Unique)
	st.Rates["opened_per_delivered"] = rate(st.Opened.Unique, st.Delivered.Unique)
	st.Rates["clicked_per_delivered"] = rate(st.Clicked.Unique, st.Delivered.Unique)
	return st, true
}

// List returns events in arrival order after cursor `after` (Seq), up to limit.
func (s *Store) List(id string, after int64, limit int) ([]*Event, int64, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.campaigns[id]
	if !ok {
		return nil, 0, false
	}
	i := sort.Search(len(c.events), func(i int) bool { return c.events[i].Seq > after })
	end := i + limit
	if end > len(c.events) {
		end = len(c.events)
	}
	out := append([]*Event(nil), c.events[i:end]...)
	var next int64
	if end < len(c.events) && len(out) > 0 {
		next = out[len(out)-1].Seq
	}
	return out, next, true
}

// Campaigns returns known campaign IDs, sorted. Used only by the demo UI.
func (s *Store) Campaigns() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ids := make([]string, 0, len(s.campaigns))
	for id := range s.campaigns {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

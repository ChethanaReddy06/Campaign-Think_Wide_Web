package main

import "time"

// Event is one validated, normalised webhook event.
type Event struct {
	EventID    string            `json:"event_id"`
	CampaignID string            `json:"campaign_id"`
	ContactID  string            `json:"contact_id"`
	Type       string            `json:"type"` // sent | delivered | opened | clicked
	Timestamp  time.Time         `json:"timestamp"`
	Metadata   map[string]string `json:"metadata,omitempty"`
	Seq        int64             `json:"seq"`         // arrival order, used as list cursor
	ReceivedAt time.Time         `json:"received_at"` // when we got it (vs Timestamp: when it happened)
}

var validTypes = map[string]bool{"sent": true, "delivered": true, "opened": true, "clicked": true}

// rawEvent is the wire shape. Pointers/RawMessage let us tell "missing" from "empty"
// and keep one bad field from failing the whole decode.
type rawEvent struct {
	EventID    *string           `json:"event_id"`
	CampaignID *string           `json:"campaign_id"`
	ContactID  *string           `json:"contact_id"`
	Type       *string           `json:"type"`
	Timestamp  *string           `json:"timestamp"`
	Metadata   map[string]any    `json:"metadata"`
}

type Problem struct {
	Index   int    `json:"index"`
	EventID string `json:"event_id,omitempty"`
	Reason  string `json:"reason"`
}

type IngestResult struct {
	Received   int       `json:"received"`
	Accepted   int       `json:"accepted"`
	Duplicates int       `json:"duplicates"` // identical event_id + identical content: harmless retry
	Conflicts  []Problem `json:"conflicts"`  // same event_id, DIFFERENT content: first write kept
	Rejected   []Problem `json:"rejected"`   // invalid, nothing stored
}

package main

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
)

//go:embed static/index.html
var indexHTML []byte

const (
	maxBodyBytes = 5 << 20 // 5 MiB
	maxBatch     = 5000
)

func main() {
	addr := ":8080"
	fmt.Printf("listening on %s\n", addr)
	log.Fatal(http.ListenAndServe(addr, newMux(NewStore())))
}

func newMux(s *Store) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /events", handlePostEvents(s))
	mux.HandleFunc("GET /campaigns/{campaignID}/stats", handleGetStats(s))
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(indexHTML)
	})
	mux.HandleFunc("GET /campaigns", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"campaigns": s.Campaigns()})
	})
	mux.HandleFunc("GET /campaigns/{campaignID}/events", handleListEvents(s))
	return mux
}

// POST /events: 400 only if the body is not a JSON array at all (or too big/large batch).
// Otherwise 200 with a per-item breakdown, so a provider retry of the same batch is
// always safe and one bad element never costs the other good ones.
func handlePostEvents(s *Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
		if err != nil {
			var mbe *http.MaxBytesError
			if errors.As(err, &mbe) {
				writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "body too large"})
				return
			}
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "cannot read body"})
			return
		}
		var items []json.RawMessage
		if err := json.Unmarshal(body, &items); err != nil || items == nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "body must be a JSON array of events"})
			return
		}
		if len(items) > maxBatch {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": fmt.Sprintf("max %d events per request", maxBatch)})
			return
		}
		writeJSON(w, http.StatusOK, s.Ingest(items))
	}
}

func handleGetStats(s *Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		st, ok := s.Stats(r.PathValue("campaignID"))
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown campaign"})
			return
		}
		writeJSON(w, http.StatusOK, st)
	}
}

func handleListEvents(s *Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit, after := 50, int64(0)
		if v := r.URL.Query().Get("limit"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 || n > 500 {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "limit must be 1..500"})
				return
			}
			limit = n
		}
		if v := r.URL.Query().Get("after"); v != "" {
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil || n < 0 {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad cursor"})
				return
			}
			after = n
		}
		evs, next, ok := s.List(r.PathValue("campaignID"), after, limit)
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown campaign"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"events": evs, "next_after": next})
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("writeJSON: %v", err)
	}
}

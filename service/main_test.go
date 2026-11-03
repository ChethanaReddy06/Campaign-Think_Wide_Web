package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
)

func post(t *testing.T, s *Store, body string) (int, IngestResult) {
	t.Helper()
	rr := httptest.NewRecorder()
	newMux(s).ServeHTTP(rr, httptest.NewRequest("POST", "/events", strings.NewReader(body)))
	var res IngestResult
	json.Unmarshal(rr.Body.Bytes(), &res)
	return rr.Code, res
}

func ev(id, ct, typ string) string {
	return fmt.Sprintf(`{"event_id":%q,"campaign_id":"c1","contact_id":%q,"type":%q,"timestamp":"2026-08-10T09:00:00Z"}`, id, ct, typ)
}

func stats(t *testing.T, s *Store) *Stats {
	st, ok := s.Stats("c1")
	if !ok {
		t.Fatal("no campaign")
	}
	return st
}

func TestDuplicateNotDoubleCounted(t *testing.T) {
	s := NewStore()
	post(t, s, "["+ev("e1", "a", "opened")+"]")
	_, res := post(t, s, "["+ev("e1", "a", "opened")+","+ev("e1", "a", "opened")+"]")
	if res.Duplicates != 2 || res.Accepted != 0 {
		t.Fatalf("%+v", res)
	}
	if st := stats(t, s); st.Opened.Total != 1 {
		t.Fatalf("opened=%d", st.Opened.Total)
	}
}

func TestConflictKeepsFirst(t *testing.T) {
	s := NewStore()
	post(t, s, "["+ev("e1", "a", "sent")+"]")
	_, res := post(t, s, "["+ev("e1", "a", "clicked")+"]")
	if len(res.Conflicts) != 1 || stats(t, s).Clicked.Total != 0 {
		t.Fatalf("%+v", res)
	}
}

func TestPartialBatch(t *testing.T) {
	s := NewStore()
	code, res := post(t, s, "["+ev("e1", "a", "sent")+`,{"event_id":5},"junk",`+ev("e2", "a", "bogus")+","+ev("e3", "b", "sent")+"]")
	if code != 200 || res.Accepted != 2 || len(res.Rejected) != 3 {
		t.Fatalf("%d %+v", code, res)
	}
}

func TestNotAnArray(t *testing.T) {
	for _, b := range []string{`{}`, `null`, `nope`, ``} {
		if code, _ := post(t, NewStore(), b); code != 400 {
			t.Fatalf("%q -> %d", b, code)
		}
	}
}

func TestOutOfOrderAndUniques(t *testing.T) {
	s := NewStore()
	post(t, s, "["+ev("o1", "a", "opened")+","+ev("o2", "a", "opened")+","+ev("o3", "b", "opened")+"]")
	post(t, s, "["+ev("d1", "a", "delivered")+"]") // delivered arrives after opened
	st := stats(t, s)
	if st.Opened.Total != 3 || st.Opened.Unique != 2 || st.Delivered.Total != 1 {
		t.Fatalf("%+v", st)
	}
}

func TestConcurrentRetries(t *testing.T) {
	s := NewStore()
	var wg sync.WaitGroup
	for g := 0; g < 50; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var parts []string
			for i := 0; i < 100; i++ {
				parts = append(parts, ev(fmt.Sprintf("e%d", i), "a", "delivered"))
			}
			post(t, s, "["+strings.Join(parts, ",")+"]")
		}()
	}
	wg.Wait()
	if n := stats(t, s).Delivered.Total; n != 100 {
		t.Fatalf("delivered=%d want 100", n)
	}
}

func TestSeedSurvives(t *testing.T) {
	b, err := os.ReadFile("seed/events.json")
	if err != nil {
		t.Skip("no seed")
	}
	s := NewStore()
	code, res := post(t, s, string(b))
	if code != 200 || res.Received != 235 {
		t.Fatalf("%d %+v", code, res)
	}
	t.Logf("%+v", res)
	rr := httptest.NewRecorder()
	newMux(s).ServeHTTP(rr, httptest.NewRequest("GET", "/campaigns/cmp_summer_sale/stats", nil))
	if rr.Code != http.StatusOK {
		t.Fatal(rr.Code)
	}
}

package study

import (
	"bytes"
	"encoding/json"
	"github.com/3b391433/shanbay-cli/internal/auth"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/3b391433/shanbay-cli/internal/api"
)

func cardTypes(cs []Card) []Type {
	t := make([]Type, len(cs))
	for i, c := range cs {
		t[i] = c.Type
	}
	return t
}

func TestCardsMixing(t *testing.T) {
	s := &Session{
		AItems:  []api.SyncItem{{ItemID: "n1"}, {ItemID: "n2"}},
		CItems:  []api.SyncItem{{ItemID: "r1"}, {ItemID: "r2"}, {ItemID: "r3"}, {ItemID: "r4"}},
		Content: map[string]api.VocabWithSenses{},
	}
	// new-first: all NEW then all REVIEW
	nf := s.Cards(true, false)
	if len(nf) != 6 || nf[0].Type != New || nf[1].Type != New || nf[2].Type != Review {
		t.Fatalf("new-first wrong: %v", cardTypes(nf))
	}
	// mixed: reviews interleaved among new (a REVIEW appears within first 3)
	mx := s.Cards(true, true)
	if len(mx) != 6 || !(mx[1].Type == Review || mx[2].Type == Review) {
		t.Fatalf("mixed not interleaved: %v", cardTypes(mx))
	}
	// new-only ignores review
	if no := s.Cards(false, true); len(no) != 2 {
		t.Fatalf("new-only len = %d, want 2", len(no))
	}
}

// TestBuildSubmitGrades verifies the three grades map to the right submit shape:
// 认识 → known list (schedule=KNOWN, original failed_count); 太简单 → known list
// with failed_count=-1 (mastered); 不认识 → stays in the unfinished list unchanged.
func TestBuildSubmitGrades(t *testing.T) {
	s := &Session{
		Date: "2026-06-30", LearningTime: 100,
		AItems: []api.SyncItem{
			{ItemID: "k", Schedule: 1, FailedCount: 2},   // 认识
			{ItemID: "e", Schedule: 0, FailedCount: 0},   // 太简单
			{ItemID: "u", Schedule: 2, FailedCount: 1.5}, // 不认识 (default)
		},
		CItems: []api.SyncItem{{ItemID: "ck", Schedule: 1, FailedCount: 1}},
	}
	b := s.BuildSubmit(map[string]Grade{"k": Known, "e": TooEasy, "ck": Known}, 200)

	known := map[string]api.SubmitItem{}
	for _, it := range b.AItemsKnown {
		known[it.ItemID] = it
	}
	if got := known["k"]; got.Schedule != knownSchedule || got.FailedCount != 2 {
		t.Fatalf("认识 k: %+v (want schedule=%d failed=2)", got, knownSchedule)
	}
	if got := known["e"]; got.Schedule != knownSchedule || got.FailedCount != -1 {
		t.Fatalf("太简单 e must set failed_count=-1: %+v", got)
	}
	if len(b.AItems) != 1 || b.AItems[0].ItemID != "u" || b.AItems[0].Schedule != 2 {
		t.Fatalf("不认识 should stay unchanged in a_items: %+v", b.AItems)
	}
	if len(b.CItemsKnown) != 1 || b.CItemsKnown[0].Schedule != knownSchedule {
		t.Fatalf("review 认识: %+v", b.CItemsKnown)
	}
	if b.LearningTime != 200 || b.Date != "2026-06-30" {
		t.Fatalf("meta mismatch: time=%d date=%s", b.LearningTime, b.Date)
	}
}

func TestRemainingPreservesFullSourceQueue(t *testing.T) {
	s := &Session{AItems: []api.SyncItem{{ItemID: "a"}, {ItemID: "b"}}, CItems: []api.SyncItem{{ItemID: "c"}}}
	next := s.Remaining(map[string]Grade{"a": Known, "c": TooEasy})
	if len(next.AItems) != 1 || next.AItems[0].ItemID != "b" || len(next.CItems) != 0 {
		t.Fatalf("wrong remaining: %+v", next)
	}
	if len(s.AItems) != 2 || len(s.CItems) != 1 {
		t.Fatal("source queue mutated")
	}
}

func TestSubmitterDoesNotBlockAndSerializesSnapshots(t *testing.T) {
	entered := make(chan string, 2)
	release := make(chan struct{})
	s := &Submitter{submit: func(_ *Session, grades map[string]Grade) error {
		if grades["a"] == Known {
			entered <- "a"
			<-release
		} else {
			entered <- "b"
		}
		return nil
	}}
	grades := map[string]Grade{"a": Known}
	first := s.Enqueue(&Session{}, grades)
	grades["a"] = TooEasy // enqueue must own a snapshot
	second := s.Enqueue(&Session{}, map[string]Grade{"b": Known})
	if got := <-entered; got != "a" {
		t.Fatalf("first snapshot/order: %q", got)
	}
	select {
	case <-second:
		t.Fatal("second write overtook first")
	default:
	}
	close(release)
	if err := s.Wait(); err != nil {
		t.Fatal(err)
	}
	if <-first != nil || <-second != nil {
		t.Fatal("unexpected submit error")
	}
	if got := <-entered; got != "b" {
		t.Fatalf("second order: %q", got)
	}
}

func TestSubmitterFailureStopsFollowingWrites(t *testing.T) {
	calls := 0
	s := &Submitter{submit: func(*Session, map[string]Grade) error { calls++; return api.ErrUnauthorized }}
	first := s.Enqueue(&Session{}, nil)
	second := s.Enqueue(&Session{}, nil)
	if s.Wait() == nil || <-first == nil || <-second == nil {
		t.Fatal("failure must be reported on all drain paths")
	}
	if calls != 1 {
		t.Fatalf("made %d writes after a failure", calls)
	}
}

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSubmitterRebuildsCompleteQueueBetweenGroups(t *testing.T) {
	old := http.DefaultTransport
	defer func() { http.DefaultTransport = old }()
	var mu sync.Mutex
	remaining := []api.SyncItem{{ItemID: "a", UpdatedAt: "original"}, {ItemID: "b", UpdatedAt: "original"}, {ItemID: "c", UpdatedAt: "original"}}
	var bodies []api.SubmitBody
	http.DefaultTransport = transportFunc(func(r *http.Request) (*http.Response, error) {
		mu.Lock()
		defer mu.Unlock()
		var response any = api.BookStatus{Date: "2026-10-16", LearningTime: 99}
		if r.Method == http.MethodPut {
			var body api.SubmitBody
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				return nil, err
			}
			bodies = append(bodies, body)
			known := map[string]bool{}
			for _, it := range body.AItemsKnown {
				known[it.ItemID] = true
			}
			next := []api.SyncItem{}
			for _, it := range remaining {
				if !known[it.ItemID] {
					it.UpdatedAt = "refreshed"
					next = append(next, it)
				}
			}
			remaining = next
			response = struct{}{}
		} else if strings.HasSuffix(r.URL.Path, "items/sync") {
			response = api.SyncItems{ANotFinished: remaining}
		}
		data, _ := json.Marshal(response)
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(data))}, nil
	})
	s := NewSubmitter(api.New(&auth.Credentials{}), "book")
	sess := &Session{Date: "2026-10-16", LearningTime: 10, AItems: append([]api.SyncItem{}, remaining...)}
	s.Enqueue(sess, map[string]Grade{"a": Known})
	s.Enqueue(sess.Remaining(map[string]Grade{"a": Known}), map[string]Grade{"b": TooEasy})
	if err := s.Wait(); err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 2 {
		t.Fatalf("bodies=%d", len(bodies))
	}
	first, second := bodies[0], bodies[1]
	if len(first.AItems) != 2 || len(first.AItemsKnown) != 1 {
		t.Fatalf("first not complete: %+v", first)
	}
	if len(second.AItems) != 1 || second.AItems[0].ItemID != "c" || len(second.AItemsKnown) != 1 || second.AItemsKnown[0].ItemID != "b" || second.AItemsKnown[0].UpdatedAt != "refreshed" || second.AItemsKnown[0].FailedCount != -1 {
		t.Fatalf("second did not use authoritative remaining state: %+v", second)
	}
	if second.LearningTime != 99 {
		t.Fatalf("learning time regressed: %d", second.LearningTime)
	}
}

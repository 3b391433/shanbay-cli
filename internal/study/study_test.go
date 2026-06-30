package study

import (
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

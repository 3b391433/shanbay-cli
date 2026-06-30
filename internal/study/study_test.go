package study

import (
	"testing"

	"shanbay-cli/internal/api"
)

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

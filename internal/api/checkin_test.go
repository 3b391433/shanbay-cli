package api

import (
	"encoding/json"
	"testing"
)

// Real GET /uc/checkin response (trimmed to two tasks), captured 2026-07-01.
// Asserts the model maps the fields auto-checkin depends on: the status gate
// and the streak count.
const checkinSample = `{"date":"2026-07-01","checkin_days_num":70,"status":"NOT_YET_CHECKIN","note":"","tasks":[{"code":"bdc","name":"单词","unit":"个","operation":"背","display":true,"required":false,"finish_status":"FINISHED","used_time":0,"num":60},{"code":"read","name":"阅读","unit":"篇","operation":"读","display":true,"required":false,"finish_status":"UNFINISHED","used_time":0,"num":0}]}`

func TestCheckinUnmarshal(t *testing.T) {
	var ck Checkin
	if err := json.Unmarshal([]byte(checkinSample), &ck); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if ck.CheckinDays != 70 || ck.Date != "2026-07-01" {
		t.Fatalf("meta: days=%d date=%s", ck.CheckinDays, ck.Date)
	}
	if !ck.NotYet() || ck.Done() {
		t.Fatalf("status gate wrong for %q: NotYet=%v Done=%v", ck.Status, ck.NotYet(), ck.Done())
	}
	if len(ck.Tasks) != 2 || ck.Tasks[0].Code != "bdc" || ck.Tasks[0].FinishStatus != "FINISHED" || ck.Tasks[0].Num != 60 {
		t.Fatalf("tasks parsed wrong: %+v", ck.Tasks)
	}
}

func TestCheckinStatusHelpers(t *testing.T) {
	done := &Checkin{Status: CheckinDone}
	if !done.Done() || done.NotYet() {
		t.Fatalf("HAVE_CHECKIN: Done=%v NotYet=%v", done.Done(), done.NotYet())
	}
	notyet := &Checkin{Status: CheckinNotYet}
	if notyet.Done() || !notyet.NotYet() {
		t.Fatalf("NOT_YET_CHECKIN: Done=%v NotYet=%v", notyet.Done(), notyet.NotYet())
	}
	// Any other status (task unfinished) must be neither — auto-checkin skips it.
	forbidden := &Checkin{Status: "FORBIDDEN_CHECKIN_REQUIRED"}
	if forbidden.Done() || forbidden.NotYet() {
		t.Fatalf("FORBIDDEN: Done=%v NotYet=%v", forbidden.Done(), forbidden.NotYet())
	}
}

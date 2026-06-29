package tui

import (
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"shanbay-cli/internal/api"
	"shanbay-cli/internal/study"
)

// TestSnapshot prints the rendered views; run with SNAP=1 to eyeball layout.
func TestSnapshot(t *testing.T) {
	if os.Getenv("SNAP") == "" {
		t.Skip("set SNAP=1 to print the rendered views")
	}
	m := studyingModel()
	t.Logf("UNREVEALED:\n%s", m.View())
	m.revealed = true
	t.Logf("REVEALED:\n%s", m.View())
	d := studyingModel()
	d.phase = phaseDone
	d.doneMsg = "🎉 今日队列已清空。"
	d.totalGraded, d.totalKnown = 12, 9
	d.status = &api.BookStatus{AFinishedCount: 12, ACount: 15, CFinishedCount: 9, CCount: 13, RemainingCount: 3}
	t.Logf("DONE:\n%s", d.View())
}

func studyingModel() Model {
	return Model{
		phase: phaseStudying,
		known: map[string]bool{},
		turn:  1,
		sess:  &study.Session{AItems: []api.SyncItem{{ItemID: "a"}, {ItemID: "b"}}},
		cards: []study.Card{
			{ItemID: "a", Word: "alpha", IPAUS: "ˈælfə", Type: study.New, Defs: []string{"[n.] 第一"}},
			{ItemID: "b", Word: "bravo", Type: study.New},
		},
	}
}

func key(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

func TestRevealAndGradeAdvancesToSubmit(t *testing.T) {
	m := studyingModel()

	m2, _ := m.Update(key(" "))
	if !m2.(Model).revealed {
		t.Fatal("space should reveal the definition")
	}

	m3, _ := m2.(Model).Update(key("k"))
	mm := m3.(Model)
	if !mm.known["a"] {
		t.Fatal("k should mark current card known")
	}
	if mm.idx != 1 || mm.revealed {
		t.Fatalf("after grade: idx=%d revealed=%v, want 1/false", mm.idx, mm.revealed)
	}

	m4, cmd := mm.Update(key("f"))
	mm4 := m4.(Model)
	if mm4.phase != phaseSubmitting {
		t.Fatalf("end of turn should submit, phase=%d", mm4.phase)
	}
	if cmd == nil {
		t.Fatal("expected a submit command at end of turn")
	}
	if mm4.graded != 2 {
		t.Fatalf("graded=%d, want 2", mm4.graded)
	}
}

func TestQuitWithProgressSubmits(t *testing.T) {
	m := studyingModel()
	m2, _ := m.Update(key("k"))
	m3, cmd := m2.(Model).Update(key("q"))
	mm := m3.(Model)
	if !mm.quitting || mm.phase != phaseSubmitting || cmd == nil {
		t.Fatalf("q with progress should submit-then-quit: quitting=%v phase=%d cmd=%v", mm.quitting, mm.phase, cmd != nil)
	}
}

func TestLimitCapsCards(t *testing.T) {
	m := New(Config{Limit: 1})
	sess := &study.Session{AItems: []api.SyncItem{{ItemID: "a"}, {ItemID: "b"}}}
	cards := []study.Card{{ItemID: "a"}, {ItemID: "b"}}
	m2, _ := m.Update(turnLoadedMsg{sess: sess, cards: cards, sig: "x"})
	mm := m2.(Model)
	if mm.phase != phaseStudying || len(mm.cards) != 1 {
		t.Fatalf("limit should cap to 1 card: phase=%d cards=%d", mm.phase, len(mm.cards))
	}
}

func TestEmptyTurnIsDone(t *testing.T) {
	m := New(Config{})
	m2, _ := m.Update(turnEmptyMsg{first: true})
	if m2.(Model).phase != phaseDone {
		t.Fatal("empty turn should reach done")
	}
}

func TestErrMsgSurfaces(t *testing.T) {
	m := New(Config{})
	m2, _ := m.Update(errMsg{err: api.ErrUnauthorized})
	if m2.(Model).Err() == nil {
		t.Fatal("errMsg should set Err()")
	}
}

func TestStudyViewRenders(t *testing.T) {
	m := studyingModel()
	if !strings.Contains(m.View(), "alpha") {
		t.Fatal("study view should show the current word")
	}
	m.revealed = true
	if !strings.Contains(m.View(), "第一") {
		t.Fatal("revealed view should show the definition")
	}
}

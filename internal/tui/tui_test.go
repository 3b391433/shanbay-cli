package tui

import (
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"shanbay-cli/internal/api"
	"shanbay-cli/internal/study"
)

func studyingModel() Model {
	return Model{
		phase:    phaseStudying,
		known:    map[string]bool{},
		examples: map[string][]api.Example{},
		turn:     1,
		sess:     &study.Session{AItems: []api.SyncItem{{ItemID: "a"}, {ItemID: "b"}}},
		cards: []study.Card{
			{ItemID: "a", Word: "alpha", IPAUS: "ˈælfə", Type: study.New, Defs: []string{"[n.] 第一"}},
			{ItemID: "b", Word: "bravo", Type: study.New},
		},
	}
}

func key(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

func TestGradeRevealsThenAdvances(t *testing.T) {
	m := studyingModel()

	// grade card 0 as known -> reveals answer, does NOT advance
	m2, _ := m.Update(key("k"))
	mm := m2.(Model)
	if !mm.curDone || !mm.curKnown || !mm.known["a"] {
		t.Fatalf("k should grade+reveal: curDone=%v curKnown=%v known=%v", mm.curDone, mm.curKnown, mm.known["a"])
	}
	if mm.idx != 0 {
		t.Fatalf("grading must not advance yet, idx=%d", mm.idx)
	}

	// space advances to card 1
	m3, _ := mm.Update(key(" "))
	mm = m3.(Model)
	if mm.idx != 1 || mm.curDone {
		t.Fatalf("space should advance: idx=%d curDone=%v", mm.idx, mm.curDone)
	}

	// grade card 1 as unknown, then advance -> end of turn -> submit
	m4, _ := mm.Update(key("f"))
	mm = m4.(Model)
	if !mm.curDone || mm.curKnown {
		t.Fatalf("f should reveal as unknown: curDone=%v curKnown=%v", mm.curDone, mm.curKnown)
	}
	m5, cmd := mm.Update(key(" "))
	mm = m5.(Model)
	if mm.phase != phaseSubmitting || cmd == nil {
		t.Fatalf("advancing past last card should submit: phase=%d cmd=%v", mm.phase, cmd != nil)
	}
	if mm.graded != 2 {
		t.Fatalf("graded=%d, want 2", mm.graded)
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

func TestExamplesLoaded(t *testing.T) {
	m := New(Config{})
	m2, _ := m.Update(examplesLoadedMsg{id: "a", examples: []api.Example{{ContentEN: "x", ContentCN: "y"}}})
	if got := m2.(Model).examples["a"]; len(got) != 1 || got[0].ContentEN != "x" {
		t.Fatalf("examples not stored: %v", got)
	}
}

func TestStudyViewRenders(t *testing.T) {
	m := studyingModel()
	if !strings.Contains(m.View(), "alpha") {
		t.Fatal("study view should show the current word")
	}
	// after grading, the answer (definition + examples) shows; markup is stripped
	m.curDone = true
	m.examples["a"] = []api.Example{{ContentEN: "He <vocab>rolled</vocab> the ball.", ContentCN: "他把球滚走了。"}}
	v := m.View()
	if !strings.Contains(v, "第一") || !strings.Contains(v, "He rolled the ball.") || !strings.Contains(v, "他把球") {
		t.Fatalf("revealed view should show definition + example:\n%s", v)
	}
	if strings.Contains(v, "<vocab>") {
		t.Fatalf("markup should be stripped:\n%s", v)
	}
}

func TestCleanStripsTags(t *testing.T) {
	if got := clean("you <vocab>roll</vocab> it"); got != "you roll it" {
		t.Fatalf("clean=%q, want %q", got, "you roll it")
	}
}

// TestSnapshot prints the rendered views; run with SNAP=1 to eyeball layout.
func TestSnapshot(t *testing.T) {
	if os.Getenv("SNAP") == "" {
		t.Skip("set SNAP=1 to print the rendered views")
	}
	m := studyingModel()
	t.Logf("ASKING:\n%s", m.View())
	m.curDone, m.curKnown = true, true
	m.examples["a"] = []api.Example{{ContentEN: "He rolled the dice.", ContentCN: "他掷了骰子。"}}
	t.Logf("REVEALED:\n%s", m.View())
}

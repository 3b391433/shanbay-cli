package tui

import (
	"os"
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/3b391433/shanbay-cli/internal/api"
	"github.com/3b391433/shanbay-cli/internal/keymap"
	"github.com/3b391433/shanbay-cli/internal/study"
)

var ansiRe = regexp.MustCompile("\x1b\\[[0-9;]*m")

func noANSI(s string) string { return ansiRe.ReplaceAllString(s, "") }

func studyingModel() Model {
	return Model{
		cfg:         Config{BookName: "日常生活汇总单词", Keys: keymap.Default()},
		phase:       phaseStudying,
		grades:      map[string]study.Grade{},
		knownStreak: map[string]int{},
		seen:        map[string]bool{},
		examples:    map[string][]api.Example{},
		turn:        1,
		sess:        &study.Session{AItems: []api.SyncItem{{ItemID: "a"}, {ItemID: "b"}}},
		cards: []study.Card{
			{ItemID: "a", Word: "alpha", IPAUS: "ˈælfə", Type: study.New, Defs: []string{"[n.] 第一"}},
			{ItemID: "b", Word: "bravo", Type: study.New},
		},
	}
}

func oneCardModel() Model {
	return Model{
		cfg:         Config{Keys: keymap.Default()},
		phase:       phaseStudying,
		grades:      map[string]study.Grade{},
		knownStreak: map[string]int{},
		seen:        map[string]bool{},
		examples:    map[string][]api.Example{},
		turn:        1,
		sess:        &study.Session{AItems: []api.SyncItem{{ItemID: "a"}}},
		cards:       []study.Card{{ItemID: "a", Word: "alpha"}},
	}
}

func key(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

// step grades the current card then advances (空格).
func step(m Model, gradeKey string) Model {
	m2, _ := m.Update(key(gradeKey))
	m3, _ := m2.(Model).Update(key(" "))
	return m3.(Model)
}

func TestFirstSightKnownFinishes(t *testing.T) {
	// 本轮首次出现即认识 → 直接出队 → 组空 → 提交
	m2, _ := oneCardModel().Update(key("k"))
	m3, cmd := m2.(Model).Update(key(" "))
	mm := m3.(Model)
	if mm.phase != phaseSubmitting || cmd == nil || mm.grades["a"] != study.Known {
		t.Fatalf("首见即认识应直接出队提交: phase=%d grade=%v", mm.phase, mm.grades["a"])
	}
}

func TestKnownTwiceAfterUnknown(t *testing.T) {
	// 先不认识(进入巩固路径)→ 此后需连续两次认识才出队
	m := step(oneCardModel(), "f") // 不认识 → 重排队(已出现过)
	if m.phase != phaseStudying || len(m.cards) != 1 {
		t.Fatalf("不认识应重排队: phase=%d len=%d", m.phase, len(m.cards))
	}
	m = step(m, "k") // 第一次认识(非首见)→ streak 1,仍重排队
	if m.phase != phaseStudying || m.knownStreak["a"] != 1 || m.grades["a"] == study.Known {
		t.Fatalf("不认识后第一次认识应重排队: phase=%d streak=%d", m.phase, m.knownStreak["a"])
	}
	// 第二次认识 → 连续两次,出队 → 组空 → 提交
	m2, _ := m.Update(key("k"))
	m3, cmd := m2.(Model).Update(key(" "))
	mm := m3.(Model)
	if mm.phase != phaseSubmitting || cmd == nil || mm.grades["a"] != study.Known {
		t.Fatalf("连续两次认识应出队提交: phase=%d grade=%v", mm.phase, mm.grades["a"])
	}
}

func TestUnknownResetsKnownStreak(t *testing.T) {
	m := step(oneCardModel(), "f") // 不认识 → 已出现过,streak 0
	m = step(m, "k")               // 认识(非首见)→ streak 1
	if m.knownStreak["a"] != 1 {
		t.Fatalf("认识应累计连续计数, streak=%d", m.knownStreak["a"])
	}
	m = step(m, "f") // 不认识 → 清零
	if m.knownStreak["a"] != 0 {
		t.Fatalf("不认识应清零连续计数, streak=%d", m.knownStreak["a"])
	}
	m = step(m, "k") // 又认识一次,但只算 1(非连续 2)→ 不出队
	if m.phase != phaseStudying || m.grades["a"] == study.Known {
		t.Fatalf("中断后单次认识不应出队: phase=%d", m.phase)
	}
}

func TestTooEasyFinishesOnce(t *testing.T) {
	m2, _ := oneCardModel().Update(key("e"))
	m3, cmd := m2.(Model).Update(key(" "))
	mm := m3.(Model)
	if mm.phase != phaseSubmitting || cmd == nil || mm.grades["a"] != study.TooEasy {
		t.Fatalf("太简单应一次出队提交: phase=%d grade=%v", mm.phase, mm.grades["a"])
	}
}

func TestTooEasyGrade(t *testing.T) {
	m2, _ := studyingModel().Update(key("e"))
	mm := m2.(Model)
	if mm.curResult != study.TooEasy || !mm.curDone {
		t.Fatalf("e should grade 太简单: result=%v done=%v", mm.curResult, mm.curDone)
	}
}

func TestNumberKeysGrade(t *testing.T) {
	for _, tc := range []struct {
		key  string
		want study.Grade
	}{{"1", study.Known}, {"2", study.Unknown}, {"3", study.TooEasy}} {
		m2, _ := studyingModel().Update(key(tc.key))
		if got := m2.(Model).curResult; got != tc.want || !m2.(Model).curDone {
			t.Fatalf("key %q → %v (done=%v), want %v", tc.key, got, m2.(Model).curDone, tc.want)
		}
	}
}

func TestEscQuits(t *testing.T) {
	_, cmd := studyingModel().Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("esc should quit")
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
	if !strings.Contains(m.View(), "日常生活汇总单词") {
		t.Fatal("study view should show the current word book name")
	}
	// after grading, the answer (definition + examples) shows; markup is stripped
	m.curDone, m.curResult = true, study.Known
	m.grades["a"] = study.Known
	m.examples["a"] = []api.Example{{ContentEN: "He <vocab>rolled</vocab> the ball.", ContentCN: "他把球滚走了。"}}
	v := noANSI(m.View())
	if !strings.Contains(v, "第一") || !strings.Contains(v, "He rolled the ball.") || !strings.Contains(v, "他把球") {
		t.Fatalf("revealed view should show definition + example:\n%s", v)
	}
	if strings.Contains(v, "<vocab>") {
		t.Fatalf("markup should be stripped:\n%s", v)
	}
}

func TestRenderENKeepsTextHighlightsVocab(t *testing.T) {
	// plain text (ANSI stripped) must keep words/spaces and drop the tags
	if got := noANSI(renderEN("you <vocab>roll</vocab> it")); got != "you roll it" {
		t.Fatalf("renderEN plain=%q, want %q", got, "you roll it")
	}
	// the vocab word should be wrapped in styling (differs from plain enStyle)
	if renderEN("a <vocab>b</vocab> c") == noANSI(renderEN("a <vocab>b</vocab> c")) {
		t.Skip("no ANSI in this env; styling not observable")
	}
}

func TestCleanStripsTags(t *testing.T) {
	if got := clean("you <vocab>roll</vocab> it"); got != "you roll it" {
		t.Fatalf("clean=%q, want %q", got, "you roll it")
	}
}

func TestRegradeAfterReveal(t *testing.T) {
	m := studyingModel()
	// 认识 → 揭晓
	m2, _ := m.Update(key("k"))
	mm := m2.(Model)
	if mm.curResult != study.Known || !mm.curDone {
		t.Fatalf("initial: result=%v done=%v", mm.curResult, mm.curDone)
	}
	// 看完释义后改判为不认识:停留本卡(未前进、未出队)
	m3, _ := mm.Update(key("f"))
	mm = m3.(Model)
	if mm.curResult != study.Unknown || !mm.curDone || len(mm.cards) != 2 || mm.cards[0].ItemID != "a" {
		t.Fatalf("re-grade 应停留本卡: result=%v cards0=%s", mm.curResult, mm.cards[0].ItemID)
	}
	// 下一词:不认识 → a 轮到队尾
	m4, _ := mm.Update(key(" "))
	mm = m4.(Model)
	if len(mm.cards) != 2 || mm.cards[0].ItemID != "b" {
		t.Fatalf("不认识应重排到队尾: cards0=%s len=%d", mm.cards[0].ItemID, len(mm.cards))
	}
}

func TestEnterAsKnownAndNext(t *testing.T) {
	m := studyingModel()
	m.cfg.Keys = keymap.Keymap{
		Known: []string{"enter"}, Unknown: []string{"2"}, TooEasy: []string{"3"},
		Next: []string{"enter"}, Audio: []string{"0"}, Quit: []string{"esc"},
	}
	// 提问态:enter = 认识(揭晓,不前进)
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm := m2.(Model)
	if mm.curResult != study.Known || !mm.curDone || len(mm.cards) != 2 {
		t.Fatalf("asking enter 应=认识+揭晓: result=%v done=%v", mm.curResult, mm.curDone)
	}
	// 揭晓态:enter = 下一词(a 认识一次→轮到队尾,前进到 b)
	m3, _ := mm.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm = m3.(Model)
	if mm.cards[0].ItemID != "b" {
		t.Fatalf("revealed enter 应前进到下一词, cards0=%s", mm.cards[0].ItemID)
	}
}

func TestNextTurnFlow(t *testing.T) {
	// 本轮空但可再来一组 → phaseMore(不结束)
	m2, _ := New(Config{}).Update(turnEmptyMsg{canNext: true})
	if m2.(Model).phase != phaseMore {
		t.Fatalf("canNext empty 应进 phaseMore, got %d", m2.(Model).phase)
	}
	// phaseMore 按 next(空格)→ 触发再来一组(loading + cmd)
	m3, cmd := m2.(Model).Update(key(" "))
	if m3.(Model).phase != phaseLoading || cmd == nil {
		t.Fatalf("phaseMore next 应重新加载: phase=%d cmd=%v", m3.(Model).phase, cmd != nil)
	}
	// phaseMore 按 quit(esc)→ done
	if m4, _ := m2.(Model).Update(tea.KeyMsg{Type: tea.KeyEsc}); m4.(Model).phase != phaseDone {
		t.Fatalf("phaseMore quit 应 done, got %d", m4.(Model).phase)
	}
	// 不能再来一组 → 直接 done
	if m5, _ := New(Config{}).Update(turnEmptyMsg{canNext: false}); m5.(Model).phase != phaseDone {
		t.Fatalf("无 canNext 应 done, got %d", m5.(Model).phase)
	}
}

// TestSnapshot prints the rendered views; run with SNAP=1 to eyeball layout.
func TestSnapshot(t *testing.T) {
	if os.Getenv("SNAP") == "" {
		t.Skip("set SNAP=1 to print the rendered views")
	}
	m := studyingModel()
	t.Logf("ASKING:\n%s", m.View())
	m.curDone, m.curResult = true, study.TooEasy
	m.examples["a"] = []api.Example{{ContentEN: "He <vocab>rolled</vocab> the dice.", ContentCN: "他掷了骰子。"}}
	t.Logf("REVEALED(太简单):\n%s", m.View())
}

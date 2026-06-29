// Package tui is a bubbletea front-end for the study loop: one card at a time,
// single-key grading, then it reveals the definition + examples before advancing,
// auto-playing pronunciation. It loops through turns until the day's queue is done.
package tui

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"shanbay-cli/internal/api"
	"shanbay-cli/internal/audio"
	"shanbay-cli/internal/study"
)

// tagRe strips inline markup like <vocab>word</vocab> from example sentences.
var tagRe = regexp.MustCompile("<[^>]+>")

func clean(s string) string { return strings.TrimSpace(tagRe.ReplaceAllString(s, "")) }

type phase int

const (
	phaseLoading phase = iota
	phaseStudying
	phaseSubmitting
	phaseDone
	phaseError
)

// Config parameterizes a study session.
type Config struct {
	Client   *api.Client
	MBID     string
	BookName string
	Review   bool
	Audio    bool
	Limit    int
}

// Model is the bubbletea model (exported so callers can read Err after Run).
type Model struct {
	cfg Config

	phase    phase
	sess     *study.Session
	cards    []study.Card
	idx      int
	graded   int             // graded in the current turn
	curDone  bool            // current card graded → showing answer (def+examples)
	curKnown bool            // the grade chosen for the current card
	known    map[string]bool // known marks for the current turn
	examples map[string][]api.Example
	turn     int
	prevSig  string
	quitting bool

	totalGraded, totalKnown int
	status                  *api.BookStatus
	doneMsg                 string
	err                     error
}

// messages
type turnLoadedMsg struct {
	sess  *study.Session
	cards []study.Card
	sig   string
}
type turnEmptyMsg struct{ first bool }
type submittedMsg struct {
	status        *api.BookStatus
	graded, known int
}
type examplesLoadedMsg struct {
	id       string
	examples []api.Example
}
type errMsg struct{ err error }

// New builds the initial model.
func New(cfg Config) Model {
	return Model{cfg: cfg, phase: phaseLoading, known: map[string]bool{}, examples: map[string][]api.Example{}}
}

// Err returns a terminal error (e.g. auth failure) so the caller can re-login.
func (m Model) Err() error { return m.err }

func (m Model) Init() tea.Cmd { return m.loadTurnCmd() }

func (m Model) loadTurnCmd() tea.Cmd {
	cfg, first, prevSig := m.cfg, m.turn == 0, m.prevSig
	return func() tea.Msg {
		sess, err := study.Load(cfg.Client, cfg.MBID, cfg.Review)
		if err != nil {
			return errMsg{err}
		}
		cards := sess.Cards(cfg.Review)
		if len(cards) == 0 {
			return turnEmptyMsg{first: first}
		}
		sig := sigOf(sess)
		if sig == prevSig {
			return turnEmptyMsg{first: false} // no progress since last submit
		}
		return turnLoadedMsg{sess: sess, cards: cards, sig: sig}
	}
}

func (m Model) submitCmd() tea.Cmd {
	cfg, sess, known := m.cfg, m.sess, m.known
	graded, nk := m.graded, countTrue(m.known)
	return func() tea.Msg {
		body := sess.BuildSubmit(known, sess.LearningTime)
		if err := cfg.Client.SubmitItems(cfg.MBID, body); err != nil {
			return errMsg{err}
		}
		st, _ := cfg.Client.BookStatus(cfg.MBID)
		return submittedMsg{status: st, graded: graded, known: nk}
	}
}

// onCardShown plays pronunciation and prefetches examples for the current card.
func (m Model) onCardShown() tea.Cmd {
	if m.idx >= len(m.cards) {
		return nil
	}
	return tea.Batch(m.audioCmd(), m.examplesCmd(m.cards[m.idx].ItemID))
}

func (m Model) audioCmd() tea.Cmd {
	if !m.cfg.Audio || m.idx >= len(m.cards) {
		return nil
	}
	url := m.cards[m.idx].AudioUS
	if url == "" {
		return nil
	}
	return func() tea.Msg { _ = audio.Play(url); return nil }
}

func (m Model) examplesCmd(id string) tea.Cmd {
	if _, ok := m.examples[id]; ok || id == "" {
		return nil // already loaded
	}
	client := m.cfg.Client
	return func() tea.Msg {
		ex, err := client.GetExamples(id)
		if err != nil {
			ex = nil // best-effort
		}
		return examplesLoadedMsg{id: id, examples: ex}
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case errMsg:
		m.phase, m.err = phaseError, msg.err
		return m, tea.Quit
	case turnEmptyMsg:
		m.phase = phaseDone
		if msg.first {
			m.doneMsg = "队列为空 — 今天没有待学/待复习的词(或已全部完成)。"
		} else {
			m.doneMsg = "🎉 今日队列已清空。"
		}
		return m, tea.Quit
	case turnLoadedMsg:
		cards := msg.cards
		if m.cfg.Limit > 0 {
			rem := m.cfg.Limit - m.totalGraded
			if rem <= 0 {
				m.phase, m.doneMsg = phaseDone, "已达本次上限。"
				return m, tea.Quit
			}
			if rem < len(cards) {
				cards = cards[:rem]
			}
		}
		m.sess, m.cards, m.idx = msg.sess, cards, 0
		m.curDone, m.curKnown = false, false
		m.known, m.graded, m.prevSig = map[string]bool{}, 0, msg.sig
		m.turn++
		m.phase = phaseStudying
		return m, m.onCardShown()
	case examplesLoadedMsg:
		if msg.examples == nil {
			msg.examples = []api.Example{}
		}
		m.examples[msg.id] = msg.examples
		return m, nil
	case submittedMsg:
		m.totalGraded += msg.graded
		m.totalKnown += msg.known
		m.status = msg.status
		if m.quitting {
			m.phase = phaseDone
			return m, tea.Quit
		}
		m.phase = phaseLoading
		return m, m.loadTurnCmd()
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Type == tea.KeyCtrlC {
		return m, tea.Quit
	}
	if m.phase != phaseStudying {
		if msg.String() == "q" {
			return m, tea.Quit
		}
		return m, nil
	}
	switch msg.String() {
	case "q":
		m.quitting = true
		if m.graded == 0 {
			m.phase = phaseDone
			return m, tea.Quit
		}
		m.phase = phaseSubmitting
		return m, m.submitCmd()
	case "p":
		return m, m.audioCmd()
	}

	if !m.curDone {
		// asking: grade the card, then show the answer
		switch msg.String() {
		case "k":
			m.known[m.cards[m.idx].ItemID] = true
			m.curKnown = true
			m.curDone = true
			m.graded++
		case "f", "j":
			m.curDone = true
			m.graded++
		}
		return m, nil
	}

	// answer shown: advance on space/enter/n/→
	switch msg.String() {
	case " ", "space", "enter", "n", "right":
		return m.advance()
	}
	return m, nil
}

func (m Model) advance() (tea.Model, tea.Cmd) {
	m.idx++
	m.curDone, m.curKnown = false, false
	if m.idx >= len(m.cards) {
		m.phase = phaseSubmitting
		return m, m.submitCmd()
	}
	return m, m.onCardShown()
}

// ---- view ----

var (
	wordStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	ipaStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	dimStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	defStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("15"))
	enStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("14"))
	tagNew    = lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Render("● 新词")
	tagReview = lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Render("● 复习")
	cardStyle = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(1, 3).Width(58)
	helpStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	errStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	okStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
)

func (m Model) View() string {
	switch m.phase {
	case phaseLoading:
		return "\n  加载中…\n"
	case phaseSubmitting:
		return "\n  提交中…\n"
	case phaseError:
		return "\n" + errStyle.Render("错误:"+m.err.Error()) + "\n"
	case phaseDone:
		return m.doneView()
	case phaseStudying:
		return m.studyView()
	}
	return ""
}

func (m Model) studyView() string {
	card := m.cards[m.idx]
	tag := tagNew
	if card.Type == study.Review {
		tag = tagReview
	}
	header := fmt.Sprintf("第 %d 组   %d/%d   %s   队列 新%d/复习%d",
		m.turn, m.idx+1, len(m.cards), tag, len(m.sess.AItems), len(m.sess.CItems))

	var b strings.Builder
	b.WriteString(wordStyle.Render(card.Word))
	if card.IPAUS != "" {
		b.WriteString("   ")
		b.WriteString(ipaStyle.Render("/" + card.IPAUS + "/"))
	}
	if m.curDone {
		if m.curKnown {
			b.WriteString("   ")
			b.WriteString(okStyle.Render("✓ 认识"))
		} else {
			b.WriteString("   ")
			b.WriteString(errStyle.Render("✗ 不认识"))
		}
	}
	b.WriteString("\n")

	if !m.curDone {
		b.WriteString("\n")
		b.WriteString(dimStyle.Render("你认识吗?"))
	} else {
		b.WriteString("\n")
		if len(card.Defs) == 0 {
			b.WriteString(dimStyle.Render("(无释义)"))
			b.WriteString("\n")
		}
		for _, d := range card.Defs {
			b.WriteString(defStyle.Render(d))
			b.WriteString("\n")
		}
		b.WriteString(m.examplesBlock(card.ItemID))
	}

	help := "k 认识    f 不认识    p 发音    q 退出"
	if m.curDone {
		help = "↵/空格 下一个    p 发音    q 退出并保存"
	}
	return "\n" + dimStyle.Render(header) + "\n" + cardStyle.Render(b.String()) + "\n" + helpStyle.Render(help) + "\n"
}

func (m Model) examplesBlock(id string) string {
	ex, ok := m.examples[id]
	if !ok {
		return "\n" + dimStyle.Render("例句加载中…")
	}
	if len(ex) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(dimStyle.Render("例句:"))
	b.WriteString("\n")
	n := min(len(ex), 2)
	for _, e := range ex[:n] {
		b.WriteString(enStyle.Render("• " + clean(e.ContentEN)))
		b.WriteString("\n")
		if cn := clean(e.ContentCN); cn != "" {
			b.WriteString("  ")
			b.WriteString(dimStyle.Render(cn))
			b.WriteString("\n")
		}
	}
	return b.String()
}

func (m Model) doneView() string {
	var b strings.Builder
	b.WriteString("\n")
	if m.doneMsg != "" {
		b.WriteString(m.doneMsg)
		b.WriteString("\n")
	}
	if m.totalGraded > 0 {
		b.WriteString(okStyle.Render(fmt.Sprintf("本次共评分 %d 词(认识 %d)。", m.totalGraded, m.totalKnown)))
		b.WriteString("\n")
	}
	if m.status != nil {
		b.WriteString(dimStyle.Render(fmt.Sprintf("当前进度:新词 %d/%d,复习 %d/%d,剩余 %d",
			m.status.AFinishedCount, m.status.ACount, m.status.CFinishedCount, m.status.CCount, m.status.RemainingCount)))
		b.WriteString("\n")
	}
	return b.String()
}

func sigOf(s *study.Session) string {
	ids := make([]string, 0, len(s.AItems)+len(s.CItems))
	for _, it := range s.AItems {
		ids = append(ids, "a:"+it.ItemID)
	}
	for _, it := range s.CItems {
		ids = append(ids, "c:"+it.ItemID)
	}
	sort.Strings(ids)
	return strings.Join(ids, ",")
}

func countTrue(m map[string]bool) int {
	n := 0
	for _, v := range m {
		if v {
			n++
		}
	}
	return n
}

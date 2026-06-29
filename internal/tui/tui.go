// Package tui is a bubbletea front-end for the study loop: one card at a time,
// single-key grading, auto-advancing through turns until the day's queue is done.
package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"shanbay-cli/internal/api"
	"shanbay-cli/internal/audio"
	"shanbay-cli/internal/study"
)

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
	revealed bool
	known    map[string]bool
	graded   int // graded in the current turn
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
type errMsg struct{ err error }

// New builds the initial model.
func New(cfg Config) Model {
	return Model{cfg: cfg, phase: phaseLoading, known: map[string]bool{}}
}

// Err returns a terminal error encountered during the run (e.g. auth failure),
// so the caller can react (re-login + retry).
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
		m.sess, m.cards, m.idx, m.revealed = msg.sess, cards, 0, false
		m.known, m.graded, m.prevSig = map[string]bool{}, 0, msg.sig
		m.turn++
		m.phase = phaseStudying
		return m, m.audioCmd()
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
		m.quitting = true
		m.phase = phaseDone
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
	case " ", "space", "enter":
		m.revealed = true
		return m, nil
	case "p":
		return m, m.audioCmd()
	case "k": // 认识
		m.known[m.cards[m.idx].ItemID] = true
		m.graded++
		return m.advance()
	case "f", "j": // 不认识
		m.graded++
		return m.advance()
	}
	return m, nil
}

func (m Model) advance() (tea.Model, tea.Cmd) {
	m.idx++
	m.revealed = false
	if m.idx >= len(m.cards) {
		m.phase = phaseSubmitting
		return m, m.submitCmd()
	}
	return m, m.audioCmd()
}

// ---- view ----

var (
	wordStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	ipaStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	dimStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	defStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("15"))
	tagNew    = lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Render("● 新词")
	tagReview = lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Render("● 复习")
	cardStyle = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(1, 3).Width(52)
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

	var inner strings.Builder
	inner.WriteString(wordStyle.Render(card.Word))
	if card.IPAUS != "" {
		inner.WriteString("   ")
		inner.WriteString(ipaStyle.Render("/" + card.IPAUS + "/"))
	}
	inner.WriteString("\n\n")
	if m.revealed {
		if len(card.Defs) == 0 {
			inner.WriteString(dimStyle.Render("(无释义)"))
		}
		for _, d := range card.Defs {
			inner.WriteString(defStyle.Render(d))
			inner.WriteString("\n")
		}
	} else {
		inner.WriteString(dimStyle.Render("〔空格 显示释义〕"))
	}

	help := helpStyle.Render("空格/↵ 释义    k 认识    f 不认识    p 发音    q 退出并保存")
	return "\n" + dimStyle.Render(header) + "\n" + cardStyle.Render(inner.String()) + "\n" + help + "\n"
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

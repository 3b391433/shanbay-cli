// Package tui is a bubbletea front-end for the study loop. A group's words form
// a FIFO queue: 认识/太简单 removes a word, 不认识 rotates it to the back so it
// comes around again — the group ends (and submits) only when every word is
// known, or the user quits. It auto-plays pronunciation, reveals the definition
// and examples after grading, and offers 再来一组 when the daily plan is done.
package tui

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/3b391433/shanbay-cli/internal/api"
	"github.com/3b391433/shanbay-cli/internal/audio"
	"github.com/3b391433/shanbay-cli/internal/keymap"
	"github.com/3b391433/shanbay-cli/internal/study"
)

// tagRe matches inline markup like <vocab>word</vocab> in example sentences.
var tagRe = regexp.MustCompile("<[^>]+>")

func stripTags(s string) string { return tagRe.ReplaceAllString(s, "") }
func clean(s string) string     { return strings.TrimSpace(stripTags(s)) }

// renderEN renders an English example, emphasizing the target word/variants
// (the parts the API wraps in <vocab>…</vocab>).
func renderEN(s string) string {
	var b strings.Builder
	emit := func(text string, style lipgloss.Style) {
		if t := stripTags(text); t != "" {
			b.WriteString(style.Render(t))
		}
	}
	for {
		before, after, found := strings.Cut(s, "<vocab>")
		emit(before, enStyle)
		if !found {
			return b.String()
		}
		word, rest, closed := strings.Cut(after, "</vocab>")
		emit(word, vocabStyle)
		if !closed {
			return b.String()
		}
		s = rest
	}
}

type phase int

const (
	phaseLoading phase = iota
	phaseStudying
	phaseSubmitting
	phaseMore    // 本轮完成,询问是否「再来一组」
	phaseCheckin // 今日完成,正在自动打卡
	phaseDone
	phaseError
	phaseBoss // 老板键:显示伪装的 apt update 输出,再按任意键返回
)

// Config parameterizes a study session.
type Config struct {
	Client   *api.Client
	MBID     string
	BookName string
	Review   bool
	Audio    bool
	Limit    int           // 本次总上限(0=不限)
	Group    int           // 每组单词数(0=整队列)
	Mixed    bool          // 新词与复习混合穿插
	Keys     keymap.Keymap // 按键绑定
	Checkin  bool          // 完成今日任务后自动打卡(等同网页「去打卡」)
}

// Model is the bubbletea model (exported so callers can read Err after Run).
type Model struct {
	cfg Config

	phase       phase
	sess        *study.Session
	content     study.Content          // 当天单词内容池(日内稳定,跨组复用;NextTurn 后失效重拉)
	cards       []study.Card           // 当前组工作队列(FIFO,cards[0]=当前)
	curDone     bool                   // 当前词已评分,正在展示答案
	curResult   study.Grade            // 当前词的判定
	grades      map[string]study.Grade // 已判定为 认识/太简单 的词(提交用)
	knownStreak map[string]int         // 各词连续「认识」次数(达标才出队)
	seen        map[string]bool        // 本组内已出现过的词(首见即认识直接出队)
	touched     bool                   // 本轮是否评过分
	groupTotal  int                    // 本组初始词数
	groupDone   int                    // 本组已学会数
	examples    map[string][]api.Example
	turn        int
	prevSig     string
	quitting    bool
	bossReturn  phase // 老板键:进入伪装界面前的 phase,退出老板键时恢复

	totalKnown int // 本次累计学会(认识+太简单)
	status     *api.BookStatus
	doneMsg    string
	err        error

	loadingHint string // phaseLoading 期间的进度文案(由 412 轮询回调回传)

	checkinTried bool         // 已尝试过自动打卡(每次会话仅一次)
	checkin      *api.Checkin // 打卡后的状态(nil=未打卡)
	checkinJust  bool         // 本次刚打的卡(区分「已打过」)
	checkinErr   error        // 打卡出错(非致命,仅提示)
}

// messages
type turnLoadedMsg struct {
	sess    *study.Session
	cards   []study.Card
	sig     string
	content study.Content // 复用/新拉到的内容池,回存以供下一组复用
}
type turnEmptyMsg struct{ first, canNext bool }
type reloadMsg struct{}
type submittedMsg struct {
	status  *api.BookStatus
	learned int
}
type examplesLoadedMsg struct {
	id       string
	examples []api.Example
}
type checkinResultMsg struct {
	state *api.Checkin
	just  bool
	err   error
}

// tickMsg 由 phaseLoading 期间的 500ms ticker 发出;hint 指向 loadTurnCmd 闭包
// 内的 atomic 计数器——retryNotReady 的 OnWait 回调把"已等秒数"写进去,
// tick 读它刷新 loadingHint。hint 沿 tick 链传递以持续 tick。
type tickMsg struct{ hint *atomic.Int64 }

type errMsg struct{ err error }

// New builds the initial model.
func New(cfg Config) Model {
	if cfg.Keys.Empty() {
		cfg.Keys = keymap.Default()
	}
	return Model{cfg: cfg, phase: phaseLoading, grades: map[string]study.Grade{}, knownStreak: map[string]int{}, seen: map[string]bool{}, examples: map[string][]api.Example{}}
}

// Err returns a terminal error (e.g. auth failure) so the caller can re-login.
func (m Model) Err() error { return m.err }

func (m Model) Init() tea.Cmd { return m.loadTurnCmd() }

// loadTurnCmd loads the next group. Today's content pool is day-stable, so it's
// fetched once and reused (m.content); only status+sync are refreshed each group
// (concurrently, in LoadQueue). m.content is nil on the first turn and is reset
// after 再来一组 (NextTurn may add new words), forcing a content refresh then.
func (m Model) loadTurnCmd() tea.Cmd {
	cfg, first, prevSig, content := m.cfg, m.turn == 0, m.prevSig, m.content
	hint := &atomic.Int64{}
	load := func() tea.Msg {
		// 注入进度回调:retryNotReady 在 412 退避前把"已等秒数"写进 hint
		// (取 max 抵消多路并发各自计时起点的抖动),TUI ticker 读它刷新
		// loadingHint;用完清空,避免串到 submit/nextTurn 等无关路径。
		cfg.Client.OnWait = func(elapsed time.Duration) {
			if s := int64(elapsed.Seconds()); s > hint.Load() {
				hint.Store(s)
			}
		}
		defer func() { cfg.Client.OnWait = nil }()
		// 首组(content==nil)用 LoadTurn 三路并发拉 content+status+sync,
		// 跨零点 412 久等时后端被多路同时戳、ready 更快;后续组复用
		// m.content,只走 LoadQueue 两路并发刷 status+sync。
		var sess *study.Session
		var err error
		if content == nil {
			sess, err = study.LoadTurn(cfg.Client, cfg.MBID, cfg.Review)
		} else {
			sess, err = study.LoadQueue(cfg.Client, cfg.MBID, content)
		}
		if err != nil {
			return errMsg{err}
		}
		content = sess.Content
		cards := sess.Cards(cfg.Review, cfg.Mixed)
		if len(cards) == 0 {
			return turnEmptyMsg{first: first, canNext: sess.CanNextTurn}
		}
		if sig := sigOf(sess); sig == prevSig {
			// 理论上不该发生(每组学完才提交,not_finished 必然变化);兜底退出
			return turnEmptyMsg{canNext: sess.CanNextTurn}
		}
		return turnLoadedMsg{sess: sess, cards: cards, sig: sigOf(sess), content: content}
	}
	tick := tea.Tick(500*time.Millisecond, func(time.Time) tea.Msg {
		return tickMsg{hint: hint}
	})
	return tea.Batch(load, tick)
}

func (m Model) submitCmd() tea.Cmd {
	cfg, sess, grades := m.cfg, m.sess, m.grades
	learnedN := learned(grades)
	return func() tea.Msg {
		if err := cfg.Client.SubmitItems(cfg.MBID, sess.BuildSubmit(grades, sess.LearningTime)); err != nil {
			return errMsg{err}
		}
		st, _ := cfg.Client.BookStatus(cfg.MBID)
		return submittedMsg{status: st, learned: learnedN}
	}
}

// nextTurnCmd requests another group ("再来一组") then reloads.
func (m Model) nextTurnCmd() tea.Cmd {
	cfg := m.cfg
	return func() tea.Msg {
		if err := cfg.Client.NextTurn(cfg.MBID); err != nil {
			return errMsg{err}
		}
		return reloadMsg{}
	}
}

// checkinCmd performs the daily check-in (no-op if not eligible / already done).
func (m Model) checkinCmd() tea.Cmd {
	client := m.cfg.Client
	return func() tea.Msg {
		state, just, err := study.Checkin(client)
		return checkinResultMsg{state: state, just: just, err: err}
	}
}

// finish ends the session. With auto-checkin on it first runs one check-in
// (phaseCheckin, then quits on checkinResultMsg); otherwise it quits straight
// away. Reaching here means the day's task is done or the user is leaving — the
// check-in itself is gated server-side, so it's a no-op unless truly eligible.
func (m Model) finish() (tea.Model, tea.Cmd) {
	if m.cfg.Checkin && !m.checkinTried {
		m.checkinTried = true
		m.phase = phaseCheckin
		return m, m.checkinCmd()
	}
	m.phase = phaseDone
	return m, tea.Quit
}

// onCardShown plays pronunciation and prefetches examples for the current card.
func (m Model) onCardShown() tea.Cmd {
	if len(m.cards) == 0 {
		return nil
	}
	return tea.Batch(m.audioCmd(), m.examplesCmd(m.cards[0].ItemID))
}

func (m Model) audioCmd() tea.Cmd {
	if !m.cfg.Audio || len(m.cards) == 0 || m.cards[0].AudioUS == "" {
		return nil
	}
	url := m.cards[0].AudioUS
	return func() tea.Msg { _ = audio.Play(url); return nil }
}

// exampleAudioCmd plays the current card's first example sentence audio.
func (m Model) exampleAudioCmd() tea.Cmd {
	if !m.cfg.Audio || len(m.cards) == 0 {
		return nil
	}
	for _, e := range m.examples[m.cards[0].ItemID] {
		if u := e.AudioURL(); u != "" {
			return func() tea.Msg { _ = audio.Play(u); return nil }
		}
	}
	return nil
}

func (m Model) examplesCmd(id string) tea.Cmd {
	if _, ok := m.examples[id]; ok || id == "" {
		return nil
	}
	client := m.cfg.Client
	return func() tea.Msg {
		ex, err := client.GetExamples(id)
		if err != nil {
			ex = nil
		}
		return examplesLoadedMsg{id: id, examples: ex}
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tickMsg:
		// 离开 loading 就停 tick,避免在 studying/submitting 阶段空转刷新。
		if m.phase != phaseLoading {
			return m, nil
		}
		if msg.hint != nil {
			if s := msg.hint.Load(); s > 0 {
				m.loadingHint = fmt.Sprintf("扇贝后端在准备今日数据…已等 %ds", s)
			} else {
				m.loadingHint = "" // 新一轮 hint 从 0 起,清掉上一轮残留文案
			}
		}
		return m, tea.Tick(500*time.Millisecond, func(time.Time) tea.Msg {
			return tickMsg{hint: msg.hint}
		})
	case errMsg:
		m.phase, m.err = phaseError, msg.err
		return m, tea.Quit
	case turnEmptyMsg:
		if msg.canNext {
			m.phase = phaseMore // 可「再来一组」,等用户选择
			return m, nil
		}
		if msg.first {
			m.doneMsg = "队列为空 — 今天没有待学/待复习的词(或已全部完成)。"
		} else {
			m.doneMsg = "🎉 今日队列已清空。"
		}
		return m.finish()
	case reloadMsg:
		m.phase = phaseLoading
		m.prevSig = ""  // 新一组,允许重新加载
		m.content = nil // 再来一组可能加入新词,失效内容缓存重新拉取
		return m, m.loadTurnCmd()
	case turnLoadedMsg:
		cards := msg.cards
		if m.cfg.Group > 0 && m.cfg.Group < len(cards) {
			cards = cards[:m.cfg.Group] // 每组只呈现 Group 个
		}
		if m.cfg.Limit > 0 {
			rem := m.cfg.Limit - m.totalKnown
			if rem <= 0 {
				m.doneMsg = "已达本次上限。"
				return m.finish()
			}
			if rem < len(cards) {
				cards = cards[:rem]
			}
		}
		m.sess, m.cards, m.content = msg.sess, cards, msg.content
		m.groupTotal, m.groupDone = len(cards), 0
		m.curDone, m.curResult = false, study.Unknown
		m.grades, m.knownStreak, m.seen = map[string]study.Grade{}, map[string]int{}, map[string]bool{}
		m.touched, m.prevSig = false, msg.sig
		m.turn++
		m.phase = phaseStudying
		return m, m.onCardShown()
	case examplesLoadedMsg:
		if msg.examples == nil {
			msg.examples = []api.Example{}
		}
		m.examples[msg.id] = msg.examples
		if m.curDone && len(m.cards) > 0 && m.cards[0].ItemID == msg.id {
			return m, m.exampleAudioCmd()
		}
		return m, nil
	case submittedMsg:
		m.totalKnown += msg.learned
		m.status = msg.status
		if m.quitting {
			return m.finish()
		}
		m.phase = phaseLoading
		return m, m.loadTurnCmd()
	case checkinResultMsg:
		m.checkin, m.checkinJust, m.checkinErr = msg.state, msg.just, msg.err
		m.phase = phaseDone
		return m, tea.Quit
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Type == tea.KeyCtrlC {
		return m, tea.Quit
	}
	s := msg.String()
	k := m.cfg.Keys

	// 老板键:处于伪装界面时只有 boss 键才能返回,其它键忽略以免露馅
	if m.phase == phaseBoss {
		if keymap.Has(k.Boss, s) {
			m.phase = m.bossReturn
		}
		return m, nil
	}
	if keymap.Has(k.Boss, s) {
		m.bossReturn = m.phase
		m.phase = phaseBoss
		return m, nil
	}

	if m.phase == phaseMore {
		switch {
		case keymap.Has(k.Quit, s):
			return m.finish()
		case keymap.Has(k.Next, s):
			m.phase = phaseLoading
			return m, m.nextTurnCmd() // 再来一组
		}
		return m, nil
	}
	if m.phase != phaseStudying {
		if keymap.Has(k.Quit, s) {
			return m, tea.Quit
		}
		return m, nil
	}

	switch {
	case keymap.Has(k.Quit, s):
		m.quitting = true
		// 把当前已判定的「认识/太简单」也记入提交
		if m.curDone && m.curResult != study.Unknown && len(m.cards) > 0 {
			m.grades[m.cards[0].ItemID] = m.curResult
		}
		if !m.touched {
			return m.finish()
		}
		m.phase = phaseSubmitting
		return m, m.submitCmd()
	case keymap.Has(k.Audio, s):
		if m.curDone {
			return m, m.exampleAudioCmd()
		}
		return m, m.audioCmd()
	}

	if !m.curDone {
		if g, ok := m.gradeForKey(s); ok {
			m.grade(g)
			return m, m.exampleAudioCmd() // 揭晓后自动朗读例句
		}
		return m, nil
	}

	// 揭晓后:next 优先于改判(同一键如 enter 可既作"认识"又作"下一词")
	if keymap.Has(k.Next, s) {
		return m.advance()
	}
	if g, ok := m.gradeForKey(s); ok {
		m.setGrade(g)
		return m, nil
	}
	return m, nil
}

// gradeForKey maps a key to a grade via the configured bindings.
func (m Model) gradeForKey(s string) (study.Grade, bool) {
	k := m.cfg.Keys
	switch {
	case keymap.Has(k.Known, s):
		return study.Known, true
	case keymap.Has(k.Unknown, s):
		return study.Unknown, true
	case keymap.Has(k.TooEasy, s):
		return study.TooEasy, true
	}
	return 0, false
}

func (m *Model) setGrade(g study.Grade) { m.curResult = g; m.touched = true }
func (m *Model) grade(g study.Grade)    { m.setGrade(g); m.curDone = true }

// advance applies the current grade: 认识/太简单 finish the word (leave queue),
// 不认识 rotates it to the back to study again. Submits when the queue empties.
//
// 认识出队规则:本轮首次出现就认识 → 直接出队(初见即会,无需巩固);一旦判过
// 不认识而重排队,则回到「连续两次认识」的巩固逻辑(ConsecutiveKnown)。
func (m Model) advance() (tea.Model, tea.Cmd) {
	id := m.cards[0].ItemID
	firstSight := !m.seen[id]
	m.seen[id] = true
	finish := func() { m.grades[id] = m.curResult; m.groupDone++; m.cards = m.cards[1:] }
	requeue := func() { m.cards = append(append([]study.Card{}, m.cards[1:]...), m.cards[0]) }

	switch m.curResult {
	case study.TooEasy:
		finish() // 太简单:一次即出队
	case study.Known:
		if firstSight {
			finish() // 首见即会,直接出队
			break
		}
		m.knownStreak[id]++
		if m.knownStreak[id] >= study.ConsecutiveKnown {
			finish() // 连续认识达标,出队
		} else {
			requeue() // 还需再认识一次巩固
		}
	default: // Unknown:清零连续计数,轮到队尾
		m.knownStreak[id] = 0
		requeue()
	}
	m.curDone, m.curResult = false, study.Unknown
	if len(m.cards) == 0 {
		m.phase = phaseSubmitting
		return m, m.submitCmd()
	}
	return m, m.onCardShown()
}

// ---- view ----

var (
	wordStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	ipaStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	dimStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	defStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("15"))
	enStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("14"))
	vocabStyle = lipgloss.NewStyle().Bold(true).Underline(true).Foreground(lipgloss.Color("11"))
	tagNew     = lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Render("● 新词")
	tagReview  = lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Render("● 复习")
	cardStyle  = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(1, 3).Width(58)
	helpStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	errStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	okStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	bookStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("13"))
)

func (m Model) bookTitle() string {
	if m.cfg.BookName == "" {
		return ""
	}
	return bookStyle.Render("《"+m.cfg.BookName+"》") + "\n"
}

func (m Model) View() string {
	switch m.phase {
	case phaseLoading:
		if m.loadingHint != "" {
			return "\n  " + m.loadingHint + "\n"
		}
		return "\n  加载中…\n"
	case phaseSubmitting:
		return "\n  提交中…\n"
	case phaseCheckin:
		return "\n  打卡中…\n"
	case phaseMore:
		return m.moreView()
	case phaseError:
		return "\n" + errStyle.Render("错误:"+m.err.Error()) + "\n"
	case phaseDone:
		return m.doneView()
	case phaseStudying:
		return m.studyView()
	case phaseBoss:
		return bossView()
	}
	return ""
}

// bossView 渲染伪装的 `sudo apt update` 输出,用于混过路过的老板/同事。
// 内容纯静态,按任意键返回原界面。
func bossView() string {
	return "$ sudo apt update\n" +
		"Hit:1 http://archive.ubuntu.com/ubuntu jammy InRelease\n" +
		"Get:2 http://security.ubuntu.com/ubuntu jammy-security InRelease [110 kB]\n" +
		"Get:3 http://archive.ubuntu.com/ubuntu jammy-updates InRelease [119 kB]\n" +
		"Get:4 http://archive.ubuntu.com/ubuntu jammy-backports InRelease [108 kB]\n" +
		"Get:5 http://security.ubuntu.com/ubuntu jammy-security/main amd64 Packages [1,842 kB]\n" +
		"Get:6 http://archive.ubuntu.com/ubuntu jammy-updates/main amd64 Packages [1,516 kB]\n" +
		"Get:7 http://security.ubuntu.com/ubuntu jammy-security/main Translation-en [284 kB]\n" +
		"Get:8 http://archive.ubuntu.com/ubuntu jammy-updates/main Translation-en [312 kB]\n" +
		"Get:9 http://security.ubuntu.com/ubuntu jammy-security/universe amd64 Packages [947 kB]\n" +
		"Get:10 http://archive.ubuntu.com/ubuntu jammy-updates/universe amd64 Packages [1,224 kB]\n" +
		"Get:11 http://archive.ubuntu.com/ubuntu jammy-updates/universe Translation-en [278 kB]\n" +
		"Get:12 http://security.ubuntu.com/ubuntu jammy-security/restricted amd64 Packages [2,105 kB]\n" +
		"Get:13 http://archive.ubuntu.com/ubuntu jammy-updates/restricted amd64 Packages [2,187 kB]\n" +
		"Get:14 http://archive.ubuntu.com/ubuntu jammy-updates/multiverse amd64 Packages [51.8 kB]\n" +
		"Fetched 11.1 MB in 3s (3,682 kB/s)\n" +
		"Reading package lists... Done\n" +
		"Building dependency tree... Done\n" +
		"Reading state information... Done\n" +
		"42 packages can be upgraded. Run 'apt list --upgradable' to see them.\n" +
		"$ "
}

func (m Model) studyView() string {
	card := m.cards[0]
	tag := tagNew
	if card.Type == study.Review {
		tag = tagReview
	}
	header := fmt.Sprintf("第 %d 组   已会 %d/%d   %s   队列 新%d/复习%d",
		m.turn, m.groupDone, m.groupTotal, tag, len(m.sess.AItems), len(m.sess.CItems))

	var b strings.Builder
	b.WriteString(wordStyle.Render(card.Word))
	if card.IPAUS != "" {
		b.WriteString("   ")
		b.WriteString(ipaStyle.Render("/" + card.IPAUS + "/"))
	}
	if m.curDone {
		b.WriteString("   ")
		b.WriteString(m.resultLabel())
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

	k := m.cfg.Keys
	help := fmt.Sprintf("%s 认识   %s 不认识   %s 太简单   %s 发音   %s 老板键   %s 退出",
		keymap.First(k.Known), keymap.First(k.Unknown), keymap.First(k.TooEasy), keymap.First(k.Audio), keymap.First(k.Boss), keymap.First(k.Quit))
	if m.curDone {
		help = fmt.Sprintf("%s 下一词   %s/%s/%s 改判   %s 发音   %s 老板键   %s 退出",
			keymap.First(k.Next), keymap.First(k.Known), keymap.First(k.Unknown), keymap.First(k.TooEasy), keymap.First(k.Audio), keymap.First(k.Boss), keymap.First(k.Quit))
	}
	return "\n" + m.bookTitle() + dimStyle.Render(header) + "\n" + cardStyle.Render(b.String()) + "\n" + helpStyle.Render(help) + "\n"
}

func (m Model) resultLabel() string {
	switch m.curResult {
	case study.TooEasy:
		return okStyle.Render("⏭ 太简单·已掌握(不再复习)")
	case study.Known:
		id := m.cards[0].ItemID
		// 首见即会,或(被不认识过后)连续认识达标 → 出队
		if !m.seen[id] || m.knownStreak[id]+1 >= study.ConsecutiveKnown {
			return okStyle.Render("✓ 认识 ✓ 已掌握")
		}
		return okStyle.Render("✓ 认识(再巩固一次)")
	default:
		return errStyle.Render("✗ 不认识(稍后再来)")
	}
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
		b.WriteString(enStyle.Render("• "))
		b.WriteString(renderEN(e.ContentEN))
		b.WriteString("\n")
		if cn := clean(e.ContentCN); cn != "" {
			b.WriteString("  ")
			b.WriteString(dimStyle.Render(cn))
			b.WriteString("\n")
		}
	}
	return b.String()
}

func (m Model) moreView() string {
	k := m.cfg.Keys
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(m.bookTitle())
	b.WriteString(okStyle.Render("🎉 本轮完成"))
	if m.totalKnown > 0 {
		b.WriteString(dimStyle.Render(fmt.Sprintf("   本次学会 %d 词", m.totalKnown)))
	}
	b.WriteString("\n")
	if m.status != nil {
		b.WriteString(dimStyle.Render(fmt.Sprintf("进度:新词 %d/%d,复习 %d/%d",
			m.status.AFinishedCount, m.status.ACount, m.status.CFinishedCount, m.status.CCount)))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "再来一组?   %s 再来一组    %s 老板键    %s 结束   (每日最多 3 组)\n",
		keymap.First(k.Next), keymap.First(k.Boss), keymap.First(k.Quit))
	return b.String()
}

func (m Model) doneView() string {
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(m.bookTitle())
	if m.doneMsg != "" {
		b.WriteString(m.doneMsg)
		b.WriteString("\n")
	}
	if m.totalKnown > 0 {
		b.WriteString(okStyle.Render(fmt.Sprintf("本次学会 %d 词。", m.totalKnown)))
		b.WriteString("\n")
	}
	if m.status != nil {
		b.WriteString(dimStyle.Render(fmt.Sprintf("当前进度:新词 %d/%d,复习 %d/%d,剩余 %d",
			m.status.AFinishedCount, m.status.ACount, m.status.CFinishedCount, m.status.CCount, m.status.RemainingCount)))
		b.WriteString("\n")
	}
	b.WriteString(m.checkinLine())
	return b.String()
}

// checkinLine renders the auto-checkin outcome (empty when off / not eligible).
func (m Model) checkinLine() string {
	switch {
	case m.checkinErr != nil:
		return dimStyle.Render(fmt.Sprintf("(打卡跳过:%v)", m.checkinErr)) + "\n"
	case m.checkin == nil:
		return ""
	case m.checkinJust:
		return okStyle.Render(fmt.Sprintf("✅ 已自动打卡,累计 %d 天。", m.checkin.CheckinDays)) + "\n"
	case m.checkin.Done():
		return dimStyle.Render(fmt.Sprintf("今日已打卡,累计 %d 天。", m.checkin.CheckinDays)) + "\n"
	}
	return "" // 未满足打卡条件:静默
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

// learned counts cards judged 认识 or 太简单 (both leave the queue as known).
func learned(grades map[string]study.Grade) int {
	n := 0
	for _, g := range grades {
		if g == study.Known || g == study.TooEasy {
			n++
		}
	}
	return n
}

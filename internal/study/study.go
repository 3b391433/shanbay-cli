// Package study assembles the book-scoped learning loop: it joins the working
// queue (items/sync: item_id+schedule+failed_count) with word content
// (today_learning_items: vocab_with_senses), keyed by item_id == vocab id.
//
// Submit semantics (verified live): the PUT must carry the COMPLETE not-finished
// set (a = new, c = review); a partial set is rejected with HTTP 400. Items the
// user marks 认识 go to the *_items_known lists with schedule=KNOWN(3) and count
// as finished; everything else stays in the *_items lists unchanged.
package study

import (
	"fmt"
	"sync"

	"github.com/3b391433/shanbay-cli/internal/api"
)

type Type string

const (
	New    Type = "NEW"
	Review Type = "REVIEW"
)

const knownSchedule = 3 // api Nn.KNOWN

// ConsecutiveKnown is how many consecutive 认识 a word needs before it leaves
// the queue (一次不认识清零重来;太简单不受此限,一次即出队)。
const ConsecutiveKnown = 2

// Grade is the user's judgment for a card.
type Grade int

const (
	Unknown Grade = iota // 不认识
	Known                // 认识(算完成,进入复习循环)
	TooEasy              // 太简单 — 已掌握:schedule=KNOWN 且 failed_count=-1,不再学习/复习
)

// Card is one word presented for grading.
type Card struct {
	ItemID      string
	Word        string
	IPAUS       string
	IPAUK       string
	Defs        []string
	AudioUS     string
	Type        Type
	Schedule    int
	FailedCount float64
}

// Session is the working set for one run. AItems/CItems hold the full
// not-finished sets (required intact by the submit), Content maps item_id→word.
type Session struct {
	MBID         string
	Date         string
	LearningTime int
	CanNextTurn  bool // 本轮做完后可「再来一组」(每日上限 3 组)
	AItems       []api.SyncItem
	CItems       []api.SyncItem
	Content      map[string]api.VocabWithSenses
}

// Content is today's day-stable word pool (item_id → vocab). It only grows when
// 再来一组 (NextTurn) adds new words, so it can be loaded once (LoadContent) and
// reused across groups within a session (LoadQueue) — the expensive, decoded
// today_learning_items fetch then happens once per session instead of per group.
type Content = map[string]api.VocabWithSenses

// LoadContent pages through today_learning_items to build the word pool: NEW
// always, REVIEW when withReview. This is the heavy, decoded fetch — load it
// once and pass it to LoadQueue for each group.
//
// ☠ 不要在这里(或任何自动路径上)调用 BookReinit ☠
// 接口名很有迷惑性,实测它会清空**整本词书**的历史学习进度(KNOWN/
// LEARNING/FORGOT/MASTERED 全部归零),不是"重新初始化今日"。跨天首次
// 412 由 retryNotReady 的 120s 被动轮询兜底就够了。详见 BookReinit 头
// 部血泪注释。
func LoadContent(c *api.Client, mbid string, withReview bool) (Content, error) {
	// nudge: 跨天首次 today_learning_items 会 412(lazy init)。先打一次
	// items/all(纯读、非破坏性、不受 lazy init 影响,返回当日 item id 桶),
	// 网页版就是靠它秒开——这次调用似乎会顺带触发服务端准备今日数据。
	// 忽略结果:就算失败,后续 BookTodayItems 的 retryNotReady 仍会兜底轮询。
	_ = c.BookItemsAll(mbid)

	content := Content{}
	const ipp = 50 // server caps ipp at range(1,50)
	loadType := func(typeOf string) error {
		for page := 1; ; page++ {
			t, err := c.BookTodayItems(mbid, typeOf, page, ipp)
			if err != nil {
				return err
			}
			for _, o := range t.Objects {
				content[o.Vocab.ID] = o.Vocab
			}
			if len(t.Objects) < ipp {
				return nil
			}
		}
	}
	if err := loadType("NEW"); err != nil {
		return nil, fmt.Errorf("today NEW: %w", err)
	}
	if withReview {
		if err := loadType("REVIEW"); err != nil {
			return nil, fmt.Errorf("today REVIEW: %w", err)
		}
	}
	return content, nil
}

// LoadQueue fetches the working queue (statuses + items/sync) and pairs it with
// already-loaded content. Both are plain-JSON GETs and run concurrently — this
// is the cheap per-group refresh that runs between groups.
func LoadQueue(c *api.Client, mbid string, content Content) (*Session, error) {
	var (
		status    *api.BookStatus
		queue     *api.SyncItems
		statusErr error
		queueErr  error
		wg        sync.WaitGroup
	)
	wg.Add(2)
	go func() { defer wg.Done(); status, statusErr = c.BookStatus(mbid) }()
	go func() { defer wg.Done(); queue, queueErr = c.BookSync(mbid) }()
	wg.Wait()
	if statusErr != nil {
		return nil, fmt.Errorf("status: %w", statusErr)
	}
	if queueErr != nil {
		return nil, fmt.Errorf("sync: %w", queueErr)
	}
	return &Session{
		MBID: mbid, Date: status.Date, LearningTime: status.LearningTime,
		CanNextTurn: status.CanInitNextTurn,
		AItems:      queue.ANotFinished, CItems: queue.CNotFinished, Content: content,
	}, nil
}

// Load does a full load (content + queue) in one call. Kept for the first turn
// and the line-mode fallback; hot paths reuse content via LoadContent+LoadQueue.
func Load(c *api.Client, mbid string, withReviewContent bool) (*Session, error) {
	content, err := LoadContent(c, mbid, withReviewContent)
	if err != nil {
		return nil, err
	}
	return LoadQueue(c, mbid, content)
}

// Cards returns the words to present: NEW always, REVIEW when includeReview.
// mixed interleaves new and review words (else new-first then review).
func (s *Session) Cards(includeReview, mixed bool) []Card {
	newC := make([]Card, 0, len(s.AItems))
	for _, it := range s.AItems {
		newC = append(newC, s.card(it, New))
	}
	if !includeReview {
		return newC
	}
	revC := make([]Card, 0, len(s.CItems))
	for _, it := range s.CItems {
		revC = append(revC, s.card(it, Review))
	}
	if !mixed {
		return append(newC, revC...)
	}
	return interleave(newC, revC)
}

// interleave evenly spreads two slices (proportional merge), preserving order.
func interleave(a, b []Card) []Card {
	if len(a) == 0 {
		return b
	}
	if len(b) == 0 {
		return a
	}
	out := make([]Card, 0, len(a)+len(b))
	ai, bi := 0, 0
	for ai < len(a) || bi < len(b) {
		if bi >= len(b) || (ai < len(a) && float64(ai)/float64(len(a)) <= float64(bi)/float64(len(b))) {
			out = append(out, a[ai])
			ai++
		} else {
			out = append(out, b[bi])
			bi++
		}
	}
	return out
}

func (s *Session) card(it api.SyncItem, typ Type) Card {
	v := s.Content[it.ItemID]
	return Card{
		ItemID: it.ItemID, Word: v.Word, IPAUS: v.Sound.IPAUS, IPAUK: v.Sound.IPAUK,
		Defs: defsOf(v), AudioUS: firstURL(v.Sound.AudioUSURLs),
		Type: typ, Schedule: it.Schedule, FailedCount: it.FailedCount,
	}
}

// BuildSubmit assembles the PUT body over the COMPLETE not-finished set.
// grades[itemID]: Known/TooEasy → *_items_known (schedule=KNOWN; TooEasy also
// sets failed_count=-1 = mastered/不再学习); otherwise → *_items unchanged.
func (s *Session) BuildSubmit(grades map[string]Grade, learningTime int) api.SubmitBody {
	b := api.SubmitBody{
		Date: s.Date, LearningTime: learningTime,
		AItems: []api.SubmitItem{}, AItemsKnown: []api.SubmitItem{},
		CItems: []api.SubmitItem{}, CItemsKnown: []api.SubmitItem{},
	}
	partition(s.AItems, grades, &b.AItems, &b.AItemsKnown)
	partition(s.CItems, grades, &b.CItems, &b.CItemsKnown)
	return b
}

func partition(items []api.SyncItem, grades map[string]Grade, unk, kn *[]api.SubmitItem) {
	for _, it := range items {
		si := api.SubmitItem{ItemID: it.ItemID, Schedule: it.Schedule, FailedCount: it.FailedCount, UpdatedAt: it.UpdatedAt}
		switch grades[it.ItemID] {
		case TooEasy:
			si.Schedule, si.FailedCount = knownSchedule, -1
			*kn = append(*kn, si)
		case Known:
			si.Schedule = knownSchedule
			*kn = append(*kn, si)
		default: // Unknown — stays in queue unchanged
			*unk = append(*unk, si)
		}
	}
}

func defsOf(v api.VocabWithSenses) []string {
	var out []string
	for _, se := range v.Senses {
		d := se.DefinitionCN
		if d == "" {
			d = se.DefinitionEN
		}
		if se.POS != "" {
			d = "[" + se.POS + "] " + d
		}
		out = append(out, d)
	}
	return out
}

func firstURL(u []string) string {
	if len(u) > 0 {
		return u[0]
	}
	return ""
}

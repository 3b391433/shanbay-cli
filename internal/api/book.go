package api

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/mattn/go-isatty"
)

// Book-scoped learning endpoints under /wordsapp/user_material_books/{materialbookId}/...
// This is the family the current account actually uses (the global
// /wordscollection/learning/* family returns empty for it).

// BookStatus is GET .../learning/statuses (plain JSON). a_* = new, c_* = review.
type BookStatus struct {
	ACount          int    `json:"a_count"`
	AFinishedCount  int    `json:"a_finished_count"`
	CCount          int    `json:"c_count"`
	CFinishedCount  int    `json:"c_finished_count"`
	RemainingCount  int    `json:"remaining_count"`
	LearningTime    int    `json:"learning_time"`
	TurnCount       int    `json:"turn_count"`
	CanInitNextTurn bool   `json:"can_init_next_turn"`
	IsFinished      bool   `json:"is_finished"`
	Date            string `json:"date"`
}

// SyncItem is one item in the working queue (item_id == vocab id).
type SyncItem struct {
	ItemID       string  `json:"item_id"`
	Schedule     int     `json:"schedule"`
	FailedCount  float64 `json:"failed_count"`
	UnknownCount int     `json:"unknown_count"`
	UpdatedAt    string  `json:"updated_at,omitempty"`
}

// SyncItems is GET .../learning/items/sync (plain JSON).
type SyncItems struct {
	ANotFinished []SyncItem `json:"a_not_finished_items"`
	CNotFinished []SyncItem `json:"c_not_finished_items"`
	Date         string     `json:"date"`
	LearningTime int        `json:"learning_time"`
}

// TodayItems is the decoded GET .../learning/words/today_learning_items.
type TodayItems struct {
	Total   int         `json:"total"`
	Page    int         `json:"page"`
	IPP     int         `json:"ipp"`
	Objects []TodayItem `json:"objects"`
}

type TodayItem struct {
	TypeOf       string          `json:"type_of"`
	Unit         string          `json:"unit"`
	UnknownCount int             `json:"unknown_count"`
	Vocab        VocabWithSenses `json:"vocab_with_senses"`
}

type VocabWithSenses struct {
	ID     string  `json:"id"`
	Word   string  `json:"word"`
	Sound  Sound   `json:"sound"`
	Senses []Sense `json:"senses"`
}

// SubmitItem is one graded item in the PUT body ({failed_count,item_id,schedule}).
type SubmitItem struct {
	ItemID      string  `json:"item_id"`
	Schedule    int     `json:"schedule"`
	FailedCount float64 `json:"failed_count"`
	UpdatedAt   string  `json:"updated_at,omitempty"`
}

// SubmitBody is PUT .../learning/items/sync. a_* = new, c_* = review.
type SubmitBody struct {
	AItems       []SubmitItem `json:"a_items"`       // new, unknown (still in queue)
	AItemsKnown  []SubmitItem `json:"a_items_known"` // new, known (finished)
	CItems       []SubmitItem `json:"c_items"`       // review, unknown
	CItemsKnown  []SubmitItem `json:"c_items_known"` // review, known
	Date         string       `json:"date"`
	LearningTime int          `json:"learning_time"`
}

func bookPath(mbid, suffix string) string {
	return fmt.Sprintf("/wordsapp/user_material_books/%s/%s", mbid, suffix)
}

// retryNotReady polls fn while it returns ErrDataNotReady. On a new day the
// learning data is computed lazily server-side: reinit only queues the job,
// content prep itself can take 60s~几分钟. We poll up to 150×800ms (~120s)
// and, on a TTY, refresh a single-line "已等 Ns" progress hint on stderr so
// the user knows the wait is real, not a hang.
func (c *Client) retryNotReady(fn func() error) error {
	const attempts = 150
	const delay = 800 * time.Millisecond
	isTTY := isatty.IsTerminal(os.Stderr.Fd())
	start := time.Now()
	var err error
	for i := range attempts {
		if err = fn(); !errors.Is(err, ErrDataNotReady) {
			if isTTY {
				fmt.Fprint(os.Stderr, "\r\033[K")
			}
			return err
		}
		if isTTY {
			fmt.Fprintf(os.Stderr, "\r\033[K扇贝后端在准备今日数据…已等 %ds", int(time.Since(start).Seconds()))
		}
		if i < attempts-1 {
			time.Sleep(delay)
		}
	}
	if isTTY {
		fmt.Fprint(os.Stderr, "\r\033[K")
	}
	return err
}

func (c *Client) BookStatus(mbid string) (*BookStatus, error) {
	var s BookStatus
	err := c.retryNotReady(func() error { return c.getJSON(bookPath(mbid, "learning/statuses"), &s) })
	return &s, err
}

func (c *Client) BookSync(mbid string) (*SyncItems, error) {
	var s SyncItems
	err := c.retryNotReady(func() error { return c.getJSON(bookPath(mbid, "learning/items/sync"), &s) })
	return &s, err
}

// BookItemsAll returns today's item id buckets (a_item_ids: NEW, c_item_ids:
// REVIEW). Unlike today_learning_items / items/sync / statuses this endpoint
// does NOT 412 during the lazy-init window — it answers immediately and, as a
// side effect, appears to kick the server into preparing the day's session.
// Used purely as a nudge in LoadContent; ONE-SHOT, no retryNotReady wrapping.
// Verified non-destructive (finish_count/finished_count/new_count/review_count
// unchanged before/after call).
func (c *Client) BookItemsAll(mbid string) error {
	var s struct {
		AItemIDs []string `json:"a_item_ids"`
		CItemIDs []string `json:"c_item_ids"`
	}
	return c.getJSON(bookPath(mbid, "learning/items/all"), &s)
}

// BookTodayItems returns today's NEW/REVIEW words (decoded). It polls past the
// transient ErrDataNotReady that occurs before the day's session is initialized.
func (c *Client) BookTodayItems(mbid, typeOf string, page, ipp int) (*TodayItems, error) {
	var t TodayItems
	p := bookPath(mbid, fmt.Sprintf("learning/words/today_learning_items?type_of=%s&page=%d&ipp=%d", typeOf, page, ipp))
	err := c.retryNotReady(func() error { return c.getEncoded(p, &t) })
	return &t, err
}

// SubmitItems sends graded results (PUT). This mutates real learning progress.
func (c *Client) SubmitItems(mbid string, body SubmitBody) error {
	return c.putJSON(bookPath(mbid, "learning/items/sync"), body, nil)
}

// BookReinit ☠☠☠ 高危破坏性接口 ☠☠☠
// POST .../reinit 会把**整本词书**的学习进度清空 —— 不是"重置今日",而是把
// KNOWN/LEARNING/FORGOT/MASTERED 所有历史状态桶全部归零,finished_count → 0。
// 实测代价:一次误调把用户 71 天学习进度全部抹掉,客户端无缓存、服务端无
// 客户可见 undo,只能找扇贝客服要账号回滚。别再在自动流程里碰它。
// 保留仅为将来做显式"清空本词书重新开始"命令时的接入点,且必须由用户手动
// 二次确认。
func (c *Client) BookReinit(mbid string) error {
	p := bookPath(mbid, "reinit")
	body, status, err := c.do(http.MethodPost, p, nil)
	if err != nil {
		return err
	}
	return statusErr(p, status, body)
}

// NextTurn asks the server for another group of words ("再来一组"). Allowed only
// when BookStatus.CanInitNextTurn is true; the server caps it at 3 extra groups/day.
func (c *Client) NextTurn(mbid string) error {
	p := bookPath(mbid, "learning/next_turn")
	body, status, err := c.do(http.MethodPost, p, nil)
	if err != nil {
		return err
	}
	return statusErr(p, status, body)
}

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

// retryNotReady polls fn while it returns ErrDataNotReady with **exponential
// backoff**: 300ms → 450ms → 675ms → … capped at 5s, with a 5-minute total
// budget. On a new day learning data is computed lazily server-side; init
// time is unpredictable(几秒 ~ 几分钟)—早期高频探测抓住 backend 一 ready 就
// 返回,后期拉长间隔避免刷屏还刷不动。
//
// 首次 412 时会补一次 Warmup(),模仿网页首页那批并发预取,把后端下游服务
// 都唤醒一下——观测:网页"秒进"就是靠这批预热。
//
// 进度报告二选一:若 Client.OnWait 非 nil 则回调它(TUI 用此把"已等 Ns"
// 刷进 loading 视图,不碰 stderr——bubbletea 会打乱 stderr);否则在 TTY
// 上原地刷新 stderr 进度条,提示这是真等待不是 hang。
func (c *Client) retryNotReady(fn func() error) error {
	const (
		initialDelay = 300 * time.Millisecond
		maxDelay     = 5 * time.Second
		maxWait      = 5 * time.Minute
	)
	isTTY := isatty.IsTerminal(os.Stderr.Fd())
	stderrProgress := isTTY && c.OnWait == nil
	clear := func() {
		if stderrProgress {
			fmt.Fprint(os.Stderr, "\r\033[K")
		}
	}
	start := time.Now()
	deadline := start.Add(maxWait)
	delay := initialDelay
	var wu warmupOnce
	var err error
	for {
		if err = fn(); !errors.Is(err, ErrDataNotReady) {
			clear()
			return err
		}
		// 第一次 412 才补预热——避免每次调用都重复戳
		wu.fire(c)
		now := time.Now()
		if !now.Before(deadline) {
			break
		}
		elapsed := now.Sub(start)
		if c.OnWait != nil {
			c.OnWait(elapsed)
		} else if stderrProgress {
			fmt.Fprintf(os.Stderr, "\r\033[K扇贝后端在准备今日数据…已等 %ds", int(elapsed.Seconds()))
		}
		sleep := delay
		if now.Add(sleep).After(deadline) {
			sleep = deadline.Sub(now)
		}
		time.Sleep(sleep)
		if delay = delay * 3 / 2; delay > maxDelay {
			delay = maxDelay
		}
	}
	clear()
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

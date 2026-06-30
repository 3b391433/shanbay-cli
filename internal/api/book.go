package api

import (
	"errors"
	"fmt"
	"time"
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
// learning data is computed lazily server-side: the first request 412s, then it
// becomes ready within a few seconds (the web client polls 20×500ms the same way).
func (c *Client) retryNotReady(fn func() error) error {
	const attempts = 20
	const delay = 500 * time.Millisecond
	var err error
	for i := range attempts {
		if err = fn(); !errors.Is(err, ErrDataNotReady) {
			return err
		}
		if i < attempts-1 {
			time.Sleep(delay)
		}
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

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

	"shanbay-cli/internal/api"
)

type Type string

const (
	New    Type = "NEW"
	Review Type = "REVIEW"
)

const knownSchedule = 3 // api Nn.KNOWN

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
	AItems       []api.SyncItem
	CItems       []api.SyncItem
	Content      map[string]api.VocabWithSenses
}

// Load fetches the queue (items/sync) and word content (today_learning_items).
// withReviewContent also pulls REVIEW word text (needed only to display reviews).
func Load(c *api.Client, mbid string, withReviewContent bool) (*Session, error) {
	status, err := c.BookStatus(mbid)
	if err != nil {
		return nil, fmt.Errorf("status: %w", err)
	}
	sync, err := c.BookSync(mbid)
	if err != nil {
		return nil, fmt.Errorf("sync: %w", err)
	}

	content := map[string]api.VocabWithSenses{}
	const ipp = 50 // server caps ipp at range(1,50)
	loadContent := func(typeOf string) error {
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
	if err := loadContent("NEW"); err != nil {
		return nil, fmt.Errorf("today NEW: %w", err)
	}
	if withReviewContent {
		if err := loadContent("REVIEW"); err != nil {
			return nil, fmt.Errorf("today REVIEW: %w", err)
		}
	}
	return &Session{
		MBID: mbid, Date: status.Date, LearningTime: status.LearningTime,
		AItems: sync.ANotFinished, CItems: sync.CNotFinished, Content: content,
	}, nil
}

// Cards returns the words to present: NEW always, REVIEW when includeReview.
func (s *Session) Cards(includeReview bool) []Card {
	var cards []Card
	for _, it := range s.AItems {
		cards = append(cards, s.card(it, New))
	}
	if includeReview {
		for _, it := range s.CItems {
			cards = append(cards, s.card(it, Review))
		}
	}
	return cards
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
// known[itemID]==true means 认识 (→ *_items_known, schedule=KNOWN).
func (s *Session) BuildSubmit(known map[string]bool, learningTime int) api.SubmitBody {
	b := api.SubmitBody{
		Date: s.Date, LearningTime: learningTime,
		AItems: []api.SubmitItem{}, AItemsKnown: []api.SubmitItem{},
		CItems: []api.SubmitItem{}, CItemsKnown: []api.SubmitItem{},
	}
	partition(s.AItems, known, &b.AItems, &b.AItemsKnown)
	partition(s.CItems, known, &b.CItems, &b.CItemsKnown)
	return b
}

func partition(items []api.SyncItem, known map[string]bool, unk, kn *[]api.SubmitItem) {
	for _, it := range items {
		si := api.SubmitItem{ItemID: it.ItemID, FailedCount: it.FailedCount, UpdatedAt: it.UpdatedAt}
		if known[it.ItemID] {
			si.Schedule = knownSchedule
			*kn = append(*kn, si)
		} else {
			si.Schedule = it.Schedule
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

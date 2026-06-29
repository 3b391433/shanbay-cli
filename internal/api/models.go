package api

// Book is GET /wordsapp/user_material_books/current (plain JSON).
type Book struct {
	ID             string       `json:"id"` // user_material_book id
	LearningTaskID string       `json:"learning_task_id"`
	MaterialbookID string       `json:"materialbook_id"` // id used by book-scoped endpoints
	Materialbook   Materialbook `json:"materialbook"`
	NewCount       int          `json:"new_count"`
	ReviewCount    int          `json:"review_count"`
	FinishedCount  int          `json:"finished_count"`
	TotalCount     int          `json:"total_count"`
	UserID         string       `json:"user_id"`
}

type Materialbook struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	DictionaryID string `json:"dictionary_id"`
	TotalCount   int    `json:"total_count"`
}

// Count is GET /wordscollection/learning/count?type_of=NEW|REVIEW.
type Count struct {
	Value int `json:"value"`
}

// CountChoices is GET /wordscollection/learning/count_choices.
type CountChoices struct {
	CountChoices []int `json:"count_choices"`
}

// LearningItems is the decoded payload of the *_items endpoints (RootObject).
type LearningItems struct {
	IPP     int    `json:"ipp"`
	Page    int    `json:"page"`
	Total   int    `json:"total"`
	Objects []Item `json:"objects"`
}

type Item struct {
	TypeOf     string     `json:"type_of"`
	Vocabulary Vocabulary `json:"vocabulary"`
}

type Vocabulary struct {
	ID           string  `json:"id"`
	Word         string  `json:"word"`
	VocabularyID string  `json:"vocabulary_id"`
	RefID        string  `json:"ref_id"`
	Comment      string  `json:"comment"`
	Senses       []Sense `json:"senses"`
	Sound        Sound   `json:"sound"`
}

type Sense struct {
	DefinitionCN string  `json:"definition_cn"`
	DefinitionEN string  `json:"definition_en"`
	POS          string  `json:"pos"`
	Sequence     float64 `json:"sequence"`
}

type Sound struct {
	IPAUK       string   `json:"ipa_uk"`
	IPAUS       string   `json:"ipa_us"`
	AudioUKURLs []string `json:"audio_uk_urls"`
	AudioUSURLs []string `json:"audio_us_urls"`
}

// SessionSync is GET /wordscollection/learning/items/sync (plain JSON):
// today's in-progress items. ca_* = new words, cc_* = review words.
type SessionSync struct {
	Date           string `json:"date"`
	LearningTime   int    `json:"learning_time"`
	NewNotFinished []any  `json:"ca_not_finished_items"`
	OldNotFinished []any  `json:"cc_not_finished_items"`
}

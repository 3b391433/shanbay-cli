package api

import "net/url"

// Example is one example sentence for a vocabulary (plain JSON from the abc API).
type Example struct {
	ContentEN string  `json:"content_en"`
	ContentCN string  `json:"content_cn"`
	Audio     exAudio `json:"audio"`
}

type exAudio struct {
	US exVoice `json:"us"`
	UK exVoice `json:"uk"`
}

type exVoice struct {
	URLs []string `json:"urls"`
}

// AudioURL returns the example's pronunciation URL (US preferred), or "".
func (e Example) AudioURL() string {
	if len(e.Audio.US.URLs) > 0 {
		return e.Audio.US.URLs[0]
	}
	if len(e.Audio.UK.URLs) > 0 {
		return e.Audio.UK.URLs[0]
	}
	return ""
}

// GetExamples returns example sentences for a vocabulary id.
// Endpoint: GET /abc/words/vocabularies/{id}/examples (plain JSON array).
func (c *Client) GetExamples(vocabID string) ([]Example, error) {
	var ex []Example
	err := c.getJSON("/abc/words/vocabularies/"+url.PathEscape(vocabID)+"/examples", &ex)
	return ex, err
}

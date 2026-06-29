package api

import "net/url"

// Example is one example sentence for a vocabulary (plain JSON from the abc API).
type Example struct {
	ContentEN string `json:"content_en"`
	ContentCN string `json:"content_cn"`
}

// GetExamples returns example sentences for a vocabulary id.
// Endpoint: GET /abc/words/vocabularies/{id}/examples (plain JSON array).
func (c *Client) GetExamples(vocabID string) ([]Example, error) {
	var ex []Example
	err := c.getJSON("/abc/words/vocabularies/"+url.PathEscape(vocabID)+"/examples", &ex)
	return ex, err
}

// Package api is a thin client for shanbay's private apiv3 endpoints.
//
// Auth is the browser cookie + X-CSRFToken (see package auth). Some endpoints
// return plain JSON; the learning/word endpoints wrap their payload as
// {"data":"<enc>"} which must pass through package decode first.
package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/3b391433/shanbay-cli/internal/auth"
	"github.com/3b391433/shanbay-cli/internal/decode"
)

const BaseURL = "https://apiv3.shanbay.com"

// ErrDataNotReady maps the 412 "learning data not ready" precondition. The web
// client polls (≤20×, 500ms) on this; callers can retry or trigger reinit.
var ErrDataNotReady = errors.New("shanbay: learning data not ready (HTTP 412)")

// ErrUnauthorized maps 401/403 — the login cookie is missing or no longer valid.
var ErrUnauthorized = errors.New("登录态失效")

type Client struct {
	creds *auth.Credentials
	http  *http.Client
}

func New(creds *auth.Credentials) *Client {
	return &Client{creds: creds, http: &http.Client{Timeout: 30 * time.Second}}
}

func (c *Client) do(method, path string, reqBody []byte) (body []byte, status int, err error) {
	var rdr io.Reader
	if reqBody != nil {
		rdr = bytes.NewReader(reqBody)
	}
	req, err := http.NewRequest(method, BaseURL+path, rdr)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64; rv:152.0) Gecko/20100101 Firefox/152.0")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("X-CSRFToken", c.creds.CSRF)
	req.Header.Set("Origin", "https://web.shanbay.com")
	req.Header.Set("Referer", "https://web.shanbay.com/")
	req.Header.Set("Cookie", c.creds.Cookie)
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err = io.ReadAll(resp.Body)
	return body, resp.StatusCode, err
}

func (c *Client) get(path string) ([]byte, int, error) { return c.do(http.MethodGet, path, nil) }

func statusErr(path string, status int, body []byte) error {
	switch {
	case status == http.StatusPreconditionFailed:
		return ErrDataNotReady
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return fmt.Errorf("%w (HTTP %d on %s)", ErrUnauthorized, status, path)
	case status >= 400:
		return fmt.Errorf("%s: HTTP %d: %s", path, status, truncate(body, 200))
	}
	return nil
}

func (c *Client) getJSON(path string, v any) error {
	body, status, err := c.get(path)
	if err != nil {
		return err
	}
	if err := statusErr(path, status, body); err != nil {
		return err
	}
	return json.Unmarshal(body, v)
}

func (c *Client) getEncoded(path string, v any) error {
	body, status, err := c.get(path)
	if err != nil {
		return err
	}
	if err := statusErr(path, status, body); err != nil {
		return err
	}
	var env struct {
		Data string `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return fmt.Errorf("%s: unmarshal envelope: %w", path, err)
	}
	plain, err := decode.Decode(env.Data)
	if err != nil {
		return fmt.Errorf("%s: decode: %w", path, err)
	}
	return json.Unmarshal([]byte(plain), v)
}

func (c *Client) putJSON(path string, reqBody, out any) error {
	return c.writeJSON(http.MethodPut, path, reqBody, out)
}

func (c *Client) postJSON(path string, reqBody, out any) error {
	return c.writeJSON(http.MethodPost, path, reqBody, out)
}

func (c *Client) writeJSON(method, path string, reqBody, out any) error {
	data, err := json.Marshal(reqBody)
	if err != nil {
		return err
	}
	body, status, err := c.do(method, path, data)
	if err != nil {
		return err
	}
	if err := statusErr(path, status, body); err != nil {
		return err
	}
	if out != nil {
		return json.Unmarshal(body, out)
	}
	return nil
}

func truncate(b []byte, n int) string {
	if len(b) > n {
		return string(b[:n]) + "…"
	}
	return string(b)
}

// --- general endpoints ---

// CurrentBook returns the user's active word book (plain JSON).
func (c *Client) CurrentBook() (*Book, error) {
	var b Book
	return &b, c.getJSON("/wordsapp/user_material_books/current", &b)
}

// LearningCount returns the count for type_of "NEW" or "REVIEW".
func (c *Client) LearningCount(typeOf string) (int, error) {
	var cnt Count
	err := c.getJSON("/wordscollection/learning/count?type_of="+url.QueryEscape(typeOf), &cnt)
	return cnt.Value, err
}

// CountChoices returns the selectable daily-goal values.
func (c *Client) CountChoices() ([]int, error) {
	var cc CountChoices
	return cc.CountChoices, c.getJSON("/wordscollection/learning/count_choices", &cc)
}

// SetDailyGoal sets the daily NEW-word goal (should be one of CountChoices).
func (c *Client) SetDailyGoal(n int) error {
	return c.putJSON("/wordscollection/learning/count", map[string]int{"value": n}, nil)
}

// LookupVocab returns dictionary data for a word (decoded).
func (c *Client) LookupVocab(word string) (*Vocabulary, error) {
	var v Vocabulary
	return &v, c.getEncoded("/wordsapp/words/vocab?word="+url.QueryEscape(word), &v)
}

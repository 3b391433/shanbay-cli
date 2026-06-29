// Package auth loads shanbay credentials from a browser-exported cookie string.
//
// There is no OAuth/token issuance endpoint, so the CLI reuses a logged-in
// browser session: the user exports the Cookie header from web.shanbay.com once.
// The relevant pieces are the csrftoken (echoed back as X-CSRFToken) and the
// auth_token JWT (whose exp tells us when to prompt for a refresh).
package auth

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Credentials struct {
	Cookie   string
	CSRF     string
	UserID   int64
	Username string
	Expires  time.Time
}

// DefaultCookiePath resolves SHANBAY_COOKIE_FILE, else ~/.config/shanbay-cli/cookie.
func DefaultCookiePath() string {
	if p := os.Getenv("SHANBAY_COOKIE_FILE"); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "shanbay-cli", "cookie")
}

// Load reads and parses the cookie file. An empty path uses DefaultCookiePath.
func Load(path string) (*Credentials, error) {
	if path == "" {
		path = DefaultCookiePath()
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read cookie file %s: %w", path, err)
	}
	cookie := strings.TrimSpace(string(b))
	if cookie == "" {
		return nil, errors.New("cookie file is empty")
	}
	c := &Credentials{Cookie: cookie}
	c.CSRF = cookieValue(cookie, "csrftoken")
	if c.CSRF == "" {
		return nil, errors.New("csrftoken not found in cookie")
	}
	if tok := cookieValue(cookie, "auth_token"); tok != "" {
		if claims, err := parseJWT(tok); err == nil {
			c.UserID, c.Username = claims.ID, claims.Username
			if claims.Exp > 0 {
				c.Expires = time.Unix(claims.Exp, 0)
			}
		}
	}
	return c, nil
}

// Expired reports whether the auth_token JWT is past its exp (zero exp = unknown).
func (c *Credentials) Expired() bool {
	return !c.Expires.IsZero() && time.Now().After(c.Expires)
}

func cookieValue(cookie, name string) string {
	for part := range strings.SplitSeq(cookie, ";") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(part), name+"="); ok {
			return v
		}
	}
	return ""
}

type jwtClaims struct {
	ID       int64  `json:"id"`
	Exp      int64  `json:"exp"`
	Username string `json:"username"`
}

func parseJWT(token string) (*jwtClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errors.New("malformed jwt")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}
	var c jwtClaims
	if err := json.Unmarshal(payload, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

// ParseCookieFromCurl extracts the Cookie header value from a pasted curl
// command (as produced by a browser's "Copy as cURL"). It also accepts a raw
// cookie string pasted directly. The value must contain csrftoken and auth_token.
func ParseCookieFromCurl(curl string) (string, error) {
	s := strings.ReplaceAll(curl, "\\\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")

	var rest string
	if i := strings.Index(strings.ToLower(s), "cookie:"); i >= 0 {
		rest = strings.TrimLeft(s[i+len("cookie:"):], " ")
	} else if k := strings.Index(s, "csrftoken="); k >= 0 {
		rest = s[k:] // fallback: raw cookie value pasted directly
	} else {
		return "", errors.New("未找到 Cookie 头(也没有 csrftoken=)")
	}

	// curl wraps the header value in quotes; cut at the first quote. A raw paste
	// has no quote, so we keep everything (cookie values contain no quotes).
	end := len(rest)
	for j := 0; j < len(rest); j++ {
		if rest[j] == '\'' || rest[j] == '"' {
			end = j
			break
		}
	}
	cookie := strings.TrimSpace(rest[:end])

	if !strings.Contains(cookie, "csrftoken=") {
		return "", errors.New("Cookie 中缺少 csrftoken")
	}
	if !strings.Contains(cookie, "auth_token=") {
		return "", errors.New("Cookie 中缺少 auth_token(请用登录后的请求)")
	}
	return cookie, nil
}

// Save writes the cookie to path (DefaultCookiePath if empty) with 0600 perms,
// creating the parent directory. Returns the path written.
func Save(path, cookie string) (string, error) {
	if path == "" {
		path = DefaultCookiePath()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(strings.TrimSpace(cookie)+"\n"), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

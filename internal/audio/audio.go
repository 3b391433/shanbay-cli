// Package audio plays word pronunciation (mp3 URLs) best-effort via whatever
// command-line player is available. It degrades silently when none is found.
package audio

import (
	"context"
	"crypto/sha1"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// candidate players, each entry is {binary, args...}; the file path is appended.
// All exit on end-of-stream so a play call returns promptly.
var candidates = [][]string{
	{"mpg123", "-q"},
	{"ffplay", "-nodisp", "-autoexit", "-loglevel", "quiet"},
	{"mpv", "--no-video", "--really-quiet"},
	{"gst-play-1.0"},
}

var player = findPlayer()

func findPlayer() []string {
	for _, c := range candidates {
		if p, err := exec.LookPath(c[0]); err == nil {
			out := append([]string{p}, c[1:]...)
			return out
		}
	}
	return nil
}

// Available reports whether a usable player was found.
func Available() bool { return player != nil }

var cache = map[string]string{}

// Play downloads (cached) and plays the audio at url. Best-effort: any error is
// returned but callers typically ignore it. No-op if no player or empty url.
func Play(url string) error {
	if player == nil || url == "" {
		return nil
	}
	path, err := fetch(url)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	args := append(append([]string{}, player[1:]...), path)
	cmd := exec.CommandContext(ctx, player[0], args...)
	return cmd.Run()
}

func fetch(url string) (string, error) {
	if p, ok := cache[url]; ok {
		return p, nil
	}
	dir := filepath.Join(os.TempDir(), "shanbay-cli-audio")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	ext := filepath.Ext(url)
	if ext == "" || len(ext) > 5 {
		ext = ".mp3"
	}
	path := filepath.Join(dir, fmt.Sprintf("%x%s", sha1.Sum([]byte(url)), ext))
	if _, err := os.Stat(path); err == nil {
		cache[url] = path
		return path, nil
	}
	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	tmp := path + ".part"
	out, err := os.Create(tmp)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(out, resp.Body); err != nil {
		out.Close()
		return "", err
	}
	out.Close()
	if err := os.Rename(tmp, path); err != nil {
		return "", err
	}
	cache[url] = path
	return path, nil
}

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
	"runtime"
	"sync"
	"time"
)

// candidate players, each entry is {binary, args...}; the file path is appended.
// All exit on end-of-stream so a play call returns promptly. Ordered so that
// players able to decode everything Shanbay serves (word MP3 + example AAC) win;
// mpg123 is MP3-only and would turn AAC into noise, so it sits last as a
// best-effort fallback.
var candidates = [][]string{
	{"ffplay", "-nodisp", "-autoexit", "-loglevel", "quiet"},
	{"mpv", "--no-video", "--really-quiet"},
	{"gst-play-1.0"},
	{"mpg123", "-q"},
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

// InstallHint returns a command the user can run to install a supported player
// on this system — e.g. "sudo apt install mpg123". It only builds the string;
// it never installs anything. The command is picked by OS and, on Linux, by
// which package manager is present, with a generic fallback when none matches.
func InstallHint() string {
	return installHint(runtime.GOOS, func(bin string) bool {
		_, err := exec.LookPath(bin)
		return err == nil
	})
}

// installHint is the testable core of InstallHint: goos is a runtime.GOOS value
// and has reports whether a binary is on PATH.
func installHint(goos string, has func(bin string) bool) string {
	switch goos {
	case "darwin":
		return "brew install mpg123"
	case "linux":
		// Probe package managers in rough order of prevalence; first match wins.
		for _, pm := range []struct{ bin, cmd string }{
			{"apt-get", "sudo apt install mpg123"},
			{"dnf", "sudo dnf install mpg123"},
			{"pacman", "sudo pacman -S mpg123"},
			{"zypper", "sudo zypper install mpg123"},
			{"apk", "sudo apk add mpg123"},
			{"emerge", "sudo emerge media-sound/mpg123"},
		} {
			if has(pm.bin) {
				return pm.cmd
			}
		}
		return "用你的包管理器安装 mpg123、ffmpeg 或 mpv 之一"
	default:
		return "安装 mpg123、ffmpeg(ffplay)或 mpv 之一"
	}
}

var cache = map[string]string{}

// Only one clip plays at a time: starting a new Play stops the previous one.
// mu guards current, the process Play is currently waiting on.
var (
	mu      sync.Mutex
	current *exec.Cmd
)

// Play downloads (cached) and plays the audio at url, first stopping whatever is
// already playing. Best-effort: any error is returned but callers typically
// ignore it. No-op if no player or empty url.
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

	// Stop the previous clip, then register ourselves as the current one.
	mu.Lock()
	stopLocked()
	if err := cmd.Start(); err != nil {
		mu.Unlock()
		return err
	}
	current = cmd
	mu.Unlock()

	err = cmd.Wait()

	// Deregister, unless a later Play already replaced (and killed) us.
	mu.Lock()
	if current == cmd {
		current = nil
	}
	mu.Unlock()
	return err
}

// Stop halts any clip currently playing. Safe to call when nothing is playing.
func Stop() {
	mu.Lock()
	stopLocked()
	mu.Unlock()
}

// stopLocked kills the currently playing clip. The caller must hold mu.
func stopLocked() {
	if current != nil && current.Process != nil {
		_ = current.Process.Kill()
		current = nil
	}
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

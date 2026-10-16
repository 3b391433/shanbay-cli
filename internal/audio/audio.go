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

// mp3Only lists players that decode MP3 but not AAC. Some Shanbay clips (certain
// example sentences) are AAC, so these players can't play everything.
var mp3Only = map[string]bool{"mpg123": true}

// PlayerName returns the base name of the selected player (e.g. "ffplay"), or ""
// when none was found.
func PlayerName() string {
	if player == nil {
		return ""
	}
	return filepath.Base(player[0])
}

// FullFormat reports whether the selected player can decode everything Shanbay
// serves (MP3 + AAC). It is false when the only player found is MP3-only
// (mpg123), letting callers nudge the user toward ffplay/mpv.
func FullFormat() bool { return fullFormat(PlayerName()) }

func fullFormat(name string) bool { return name != "" && !mp3Only[name] }

// InstallHint returns an OS-appropriate command to install a recommended
// full-format player (ffmpeg, which provides ffplay). It only builds the string;
// it never installs anything. Falls back to a generic message when the platform
// or package manager isn't recognized.
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
		return "brew install ffmpeg"
	case "linux":
		// Probe package managers in rough order of prevalence; first match wins.
		for _, pm := range []struct{ bin, cmd string }{
			{"apt-get", "sudo apt install ffmpeg"},
			{"dnf", "sudo dnf install ffmpeg"},
			{"pacman", "sudo pacman -S ffmpeg"},
			{"zypper", "sudo zypper install ffmpeg"},
			{"apk", "sudo apk add ffmpeg"},
			{"emerge", "sudo emerge media-video/ffmpeg"},
		} {
			if has(pm.bin) {
				return pm.cmd
			}
		}
		return "用你的包管理器安装 ffmpeg 或 mpv"
	default:
		return "安装 ffmpeg 或 mpv"
	}
}

var cache = map[string]string{}
var cacheMu sync.Mutex

// Prepare reserves playback synchronously. Even if its returned task is scheduled
// late, Stop or a newer Prepare cancels it before any download/player can start.
var (
	mu            sync.Mutex
	current       *exec.Cmd
	cancelCurrent context.CancelFunc
)

func Prepare(url string) func() error {
	mu.Lock()
	stopLocked()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	cancelCurrent = cancel
	mu.Unlock()
	return func() error {
		defer cancel()
		if player == nil || url == "" {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		path, err := fetch(ctx, url)
		if err != nil {
			return err
		}
		args := append(append([]string{}, player[1:]...), path)
		cmd := exec.CommandContext(ctx, player[0], args...)
		mu.Lock()
		if err := ctx.Err(); err != nil {
			mu.Unlock()
			return err
		}
		if err := cmd.Start(); err != nil {
			mu.Unlock()
			return err
		}
		current = cmd
		mu.Unlock()
		err = cmd.Wait()
		mu.Lock()
		if current == cmd {
			current = nil
		}
		mu.Unlock()
		return err
	}
}

func Play(url string) error { return Prepare(url)() }

// Stop cancels pending downloads/tasks and stops the currently playing clip.
func Stop() { mu.Lock(); stopLocked(); mu.Unlock() }

func stopLocked() {
	if cancelCurrent != nil {
		cancelCurrent()
		cancelCurrent = nil
	}
	if current != nil && current.Process != nil {
		_ = current.Process.Kill()
		current = nil
	}
}

func fetch(ctx context.Context, url string) (string, error) {
	cacheMu.Lock()
	p, ok := cache[url]
	cacheMu.Unlock()
	if ok {
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
		cacheMu.Lock()
		cache[url] = path
		cacheMu.Unlock()
		return path, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("audio: HTTP %d", resp.StatusCode)
	}
	out, err := os.CreateTemp(dir, ".audio-*.part")
	if err != nil {
		return "", err
	}
	tmp := out.Name()
	defer os.Remove(tmp)
	if _, err := io.Copy(out, resp.Body); err != nil {
		out.Close()
		return "", err
	}
	out.Close()
	if err := os.Rename(tmp, path); err != nil {
		return "", err
	}
	cacheMu.Lock()
	cache[url] = path
	cacheMu.Unlock()
	return path, nil
}

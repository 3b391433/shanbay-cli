package audio

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestInstallHint(t *testing.T) {
	none := func(string) bool { return false }
	only := func(name string) func(string) bool {
		return func(bin string) bool { return bin == name }
	}

	tests := []struct {
		name string
		goos string
		has  func(string) bool
		want string
	}{
		{"macOS uses brew", "darwin", none, "brew install ffmpeg"},
		{"debian/ubuntu apt", "linux", only("apt-get"), "sudo apt install ffmpeg"},
		{"fedora dnf", "linux", only("dnf"), "sudo dnf install ffmpeg"},
		{"arch pacman", "linux", only("pacman"), "sudo pacman -S ffmpeg"},
		{"suse zypper", "linux", only("zypper"), "sudo zypper install ffmpeg"},
		{"alpine apk", "linux", only("apk"), "sudo apk add ffmpeg"},
		{"gentoo emerge", "linux", only("emerge"), "sudo emerge media-video/ffmpeg"},
		{"linux, no known pm", "linux", none, "用你的包管理器安装 ffmpeg 或 mpv"},
		{"other os", "windows", none, "安装 ffmpeg 或 mpv"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := installHint(tt.goos, tt.has); got != tt.want {
				t.Errorf("installHint(%q) = %q, want %q", tt.goos, got, tt.want)
			}
		})
	}
}

// When several package managers are present, the first candidate (apt) wins.
func TestInstallHintPrefersAptWhenMultiplePresent(t *testing.T) {
	all := func(string) bool { return true }
	if got := installHint("linux", all); got != "sudo apt install ffmpeg" {
		t.Errorf("with all managers present, got %q, want apt", got)
	}
}

func TestFullFormat(t *testing.T) {
	cases := map[string]bool{
		"ffplay":       true,
		"mpv":          true,
		"gst-play-1.0": true,
		"mpg123":       false, // MP3-only
		"":             false, // no player
	}
	for name, want := range cases {
		if got := fullFormat(name); got != want {
			t.Errorf("fullFormat(%q) = %v, want %v", name, got, want)
		}
	}
}

// Reserving a task before dispatch is essential: Bubble Tea may dispatch the old
// card's command after it has already shown a newer card.
func TestStopCancelsNotYetDispatchedTask(t *testing.T) {
	old := player
	player = []string{"unused-player"}
	defer func() { Stop(); player = old }()
	play := Prepare("http://127.0.0.1:1/never-fetch")
	Stop()
	if err := play(); !errors.Is(err, context.Canceled) {
		t.Fatalf("late task = %v", err)
	}
}

func TestNewTaskCancelsSlowDownloadAndCannotPlayAfterward(t *testing.T) {
	old := player
	marker := filepath.Join(t.TempDir(), "played")
	script := filepath.Join(t.TempDir(), "player")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ntouch '"+marker+"'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	player = []string{script}
	defer func() { Stop(); player = old }()
	entered := make(chan struct{})
	canceled := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-r.Context().Done()
		close(canceled)
	}))
	defer server.Close()
	oldTask := Prepare(server.URL + "/old.mp3")
	done := make(chan error, 1)
	go func() { done <- oldTask() }()
	<-entered
	// A new word without audio must also cancel the previous clip/download.
	Stop()
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("download was not canceled")
	}
	if err := <-done; err == nil {
		t.Fatal("old task succeeded after cancellation")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("stale download started the player")
	}
}

func TestNewPrepareCancelsOlderTask(t *testing.T) {
	old := player
	player = []string{"unused-player"}
	defer func() { Stop(); player = old }()
	first := Prepare("http://127.0.0.1:1/old")
	_ = Prepare("")
	if err := first(); !errors.Is(err, context.Canceled) {
		t.Fatalf("replaced task = %v", err)
	}
}

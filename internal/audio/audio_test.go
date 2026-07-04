package audio

import "testing"

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

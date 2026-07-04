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
		{"macOS uses brew", "darwin", none, "brew install mpg123"},
		{"debian/ubuntu apt", "linux", only("apt-get"), "sudo apt install mpg123"},
		{"fedora dnf", "linux", only("dnf"), "sudo dnf install mpg123"},
		{"arch pacman", "linux", only("pacman"), "sudo pacman -S mpg123"},
		{"suse zypper", "linux", only("zypper"), "sudo zypper install mpg123"},
		{"alpine apk", "linux", only("apk"), "sudo apk add mpg123"},
		{"gentoo emerge", "linux", only("emerge"), "sudo emerge media-sound/mpg123"},
		{"linux, no known pm", "linux", none, "用你的包管理器安装 mpg123、ffmpeg 或 mpv 之一"},
		{"other os", "windows", none, "安装 mpg123、ffmpeg(ffplay)或 mpv 之一"},
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
	if got := installHint("linux", all); got != "sudo apt install mpg123" {
		t.Errorf("with all managers present, got %q, want apt", got)
	}
}

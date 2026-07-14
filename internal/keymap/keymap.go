// Package keymap holds the user-configurable key bindings for study actions.
//
// It is loaded from ~/.config/shanbay-cli/keys.json (SHANBAY_KEYS_FILE overrides).
// Each action maps to a list of keys; a key is matched against bubbletea's
// KeyMsg.String() — letters/digits as-is, plus "enter", " " (space), "esc",
// "up"/"down"/"left"/"right", "tab", etc. The same key may serve different
// actions in different states (e.g. "enter" = 认识 while asking, = 下一词 after
// the answer is revealed), since the study UI resolves by state.
package keymap

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
)

type Keymap struct {
	Known   []string `json:"known"`    // 认识
	Unknown []string `json:"unknown"`  // 不认识
	TooEasy []string `json:"too_easy"` // 太简单(已掌握)
	Next    []string `json:"next"`     // 揭晓后进入下一词
	Audio   []string `json:"audio"`    // 重新发音
	Quit    []string `json:"quit"`     // 结束并提交
	Boss    []string `json:"boss"`     // 老板键:切换伪装的 apt 输出界面
}

// Default is the built-in binding (number keys are Chinese-IME friendly).
func Default() Keymap {
	return Keymap{
		Known:   []string{"1", "k"},
		Unknown: []string{"2", "f"},
		TooEasy: []string{"3", "e"},
		Next:    []string{"enter", " ", "space"},
		Audio:   []string{"0", "p"},
		Quit:    []string{"esc", "q"},
		Boss:    []string{"b"},
	}
}

// Path is the keymap file location.
func Path() string {
	if p := os.Getenv("SHANBAY_KEYS_FILE"); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "shanbay-cli", "keys.json")
}

// Load returns the keymap from disk merged over defaults. If the file is
// missing it writes the default (so the user can discover and edit it).
func Load() Keymap {
	km := Default()
	b, err := os.ReadFile(Path())
	if err != nil {
		if data, e := json.MarshalIndent(km, "", "  "); e == nil {
			_ = os.MkdirAll(filepath.Dir(Path()), 0o755)
			_ = os.WriteFile(Path(), data, 0o644)
		}
		return km
	}
	var u Keymap
	if json.Unmarshal(b, &u) == nil {
		mergeNonEmpty(&km.Known, u.Known)
		mergeNonEmpty(&km.Unknown, u.Unknown)
		mergeNonEmpty(&km.TooEasy, u.TooEasy)
		mergeNonEmpty(&km.Next, u.Next)
		mergeNonEmpty(&km.Audio, u.Audio)
		mergeNonEmpty(&km.Quit, u.Quit)
		mergeNonEmpty(&km.Boss, u.Boss)
	}
	return km
}

func mergeNonEmpty(dst *[]string, v []string) {
	if len(v) > 0 {
		*dst = v
	}
}

// Has reports whether key is bound in list.
func Has(list []string, key string) bool { return slices.Contains(list, key) }

// Empty reports a zero keymap (no bindings) — used to fall back to Default.
func (k Keymap) Empty() bool {
	return len(k.Known) == 0 && len(k.Unknown) == 0 && len(k.TooEasy) == 0 &&
		len(k.Next) == 0 && len(k.Audio) == 0 && len(k.Quit) == 0 && len(k.Boss) == 0
}

// Label returns a friendly display name for a key (空格/↵/esc/…).
func Label(key string) string {
	switch key {
	case " ", "space":
		return "空格"
	case "enter":
		return "↵"
	default:
		return key
	}
}

// First returns a display label for the first bound key (or "?").
func First(list []string) string {
	if len(list) > 0 {
		return Label(list[0])
	}
	return "?"
}

package api

import "testing"

func TestExampleAudioURL(t *testing.T) {
	var us Example
	us.Audio.US.URLs = []string{"https://x/us.aac"}
	us.Audio.UK.URLs = []string{"https://x/uk.aac"}
	if got := us.AudioURL(); got != "https://x/us.aac" {
		t.Fatalf("US preferred, got %q", got)
	}

	var uk Example
	uk.Audio.UK.URLs = []string{"https://x/uk.aac"}
	if got := uk.AudioURL(); got != "https://x/uk.aac" {
		t.Fatalf("UK fallback, got %q", got)
	}

	if got := (Example{}).AudioURL(); got != "" {
		t.Fatalf("empty expected, got %q", got)
	}
}

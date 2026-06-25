package youtube

import "testing"

func TestParseRecordMode(t *testing.T) {
	tests := map[string]RecordMode{
		"":              ModeVideoAudio,
		"video_audio":   ModeVideoAudio,
		"video/audio":   ModeVideoAudio,
		"video+mp3":     ModeVideoAudio,
		"audio":         ModeAudio,
		"mp3":           ModeAudio,
		"audio-only":    ModeAudio,
		"none":          ModeNone,
		"off":           ModeNone,
		"disabled":      ModeNone,
		"unknown-value": ModeVideoAudio,
	}
	for in, want := range tests {
		if got := ParseRecordMode(in); got != want {
			t.Errorf("ParseRecordMode(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRecordModeCycle(t *testing.T) {
	if got := ModeVideoAudio.next(); got != ModeAudio {
		t.Fatalf("VideoAudio next = %q, want %q", got, ModeAudio)
	}
	if got := ModeAudio.next(); got != ModeNone {
		t.Fatalf("Audio next = %q, want %q", got, ModeNone)
	}
	if got := ModeNone.next(); got != ModeVideoAudio {
		t.Fatalf("None next = %q, want %q", got, ModeVideoAudio)
	}
}

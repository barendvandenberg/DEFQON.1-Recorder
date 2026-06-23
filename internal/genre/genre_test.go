package genre

import "testing"

func TestForStageMapped(t *testing.T) {
	cases := map[string]string{
		"UV":      "Euphoric Hardstyle",
		"uv":      "Euphoric Hardstyle",  // case-insensitive
		"  blue ": "Rawstyle",             // trimmed
		"YELLOW":  "Uptempo Hardcore",
		"BLACK":   "Hardcore",
		"PINK":    "Drum & Bass",
	}
	for stage, want := range cases {
		if got := ForStage(stage, "ignored"); got != want {
			t.Errorf("ForStage(%q) = %q, want %q", stage, got, want)
		}
	}
}

func TestForStageFallback(t *testing.T) {
	if got := ForStage("Orocash Music", "Hardstyle"); got != "Hardstyle" {
		t.Errorf("unmapped stage should use fallback, got %q", got)
	}
	if got := ForStage("Whatever", ""); got != "Hardstyle" {
		t.Errorf("empty fallback should default to Hardstyle, got %q", got)
	}
}
